// runtime_chan.go — the shared channel-endpoint runtime behind
// BroadcastChannel and MessageChannel/MessagePort (TDD-00099). Every
// endpoint owns one pipe; delivery is the same 8-byte envelope-pointer
// write the Worker channel uses, so messages ride each thread's existing
// select()-based event loop through a hook trio (__kml_chan_keepalive /
// _fdset_add / _dispatch) mirroring the worker trio, with no-op stubs when
// the program never uses channels.
//
// Ownership: an endpoint is drained by whichever thread registered its
// message listener (__kml_chan_listen appends it to that thread's
// thread_local "mine" list) — construction alone never registers for
// draining, which is what lets a MessagePort cross to a worker and be
// listened to there. BroadcastChannel endpoints additionally live in one
// process-wide, mutex-guarded registry that postMessage fans out over;
// ports skip the registry entirely (each half holds its peer directly).
package llvm

import "fmt"

// chanEpIR is the endpoint block. Field indices:
//
//	0 name (BC channel name; null for a port)
//	1 pipe read fd   2 pipe write fd
//	3 alive flag (atomic i64 — read cross-thread without the registry mutex)
//	4 cb (uniform adapter closure, void(ptr env, i64 w0, i64 w1))
//	5 hold flag (keeps the owning thread's loop alive while a listener is registered)
//	6 kind (0 BroadcastChannel, 1 MessagePort)
//	7 peer (the other half, ports only)
const chanEpIR = "{ ptr, i32, i32, i64, ptr, i64, i64, ptr }"

const chanEpBytes = 56

// ensurePipeDecl declares pipe(2) exactly once (also used by the worker
// runtime).
func (e *Emitter) ensurePipeDecl() {
	if e.usedPipeDecl {
		return
	}
	e.usedPipeDecl = true
	e.emitGlobal("declare i32 @pipe(ptr noundef)")
}

func (e *Emitter) ensureChanRuntime() {
	if e.usedChanRuntime {
		return
	}
	e.usedChanRuntime = true
	e.ensureMalloc()
	e.ensureCalloc()
	e.ensureFree()
	e.ensureStrcmp()
	e.ensurePipeDecl()
	e.ensureWorkerFdSetbit()
	e.ensurePthreadMutexDecls()
	// The delivery loop itself (select/fdset/dispatch), plus read/write/
	// close/fcntl decls.
	e.ensureHTTPRuntime()

	rawAlloc := "@malloc"
	epAlloc := fmt.Sprintf("call ptr @calloc(i64 1, i64 %d)", chanEpBytes)
	if e.isGCMode() {
		// Same reasoning as the worker runtime: envelopes live only in the
		// kernel pipe buffer while in flight, and endpoint blocks are
		// referenced from other threads — both invisible to a Boehm scan.
		e.ensureGCUncollectable()
		rawAlloc = "@GC_malloc_uncollectable"
		epAlloc = fmt.Sprintf("call ptr @GC_malloc_uncollectable(i64 %d)", chanEpBytes)
	}

	// Process-wide BroadcastChannel registry + its mutex (lazily initialized
	// through a cmpxchg guard — zeroed static storage is not a valid mutex
	// on Darwin, see runtime_atomics.go).
	e.emitGlobal("@__kml_chan_mtx = internal global [64 x i8] zeroinitializer, align 8")
	e.emitGlobal("@__kml_chan_state = internal global i32 0, align 4")
	e.emitGlobal("@__kml_chan_bcs_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_chan_bcs_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_chan_bcs_cap = internal global i64 0, align 8")
	// Per-thread drain list: the endpoints THIS thread listens on.
	e.emitGlobal("@__kml_chan_mine_data = internal thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_chan_mine_len = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_chan_mine_cap = internal thread_local global i64 0, align 8")

	e.emitGlobal(`define void @__kml_chan_init() {
entry:
  %pair = cmpxchg ptr @__kml_chan_state, i32 0, i32 1 seq_cst seq_cst
  %won = extractvalue { i32, i1 } %pair, 1
  br i1 %won, label %doinit, label %waitready

doinit:
  call i32 @pthread_mutex_init(ptr @__kml_chan_mtx, ptr null)
  store atomic i32 2, ptr @__kml_chan_state seq_cst, align 4
  ret void

waitready:
  %st = load atomic i32, ptr @__kml_chan_state seq_cst, align 4
  %ready = icmp eq i32 %st, 2
  br i1 %ready, label %done, label %waitready

done:
  ret void
}`)

	// __kml_chan_new(name, kind): allocate an endpoint with its pipe (read
	// end non-blocking), mark it alive. BroadcastChannel endpoints (kind 0)
	// are appended to the process-wide registry under the mutex.
	e.emitGlobal(fmt.Sprintf(`define ptr @__kml_chan_new(ptr %%name, i64 %%kind) {
entry:
  call void @__kml_chan_init()
  %%pipes = alloca [2 x i32], align 4
  %%ep = %s
  call i32 @pipe(ptr %%pipes)
  %%r_p = getelementptr [2 x i32], ptr %%pipes, i32 0, i32 0
  %%w_p = getelementptr [2 x i32], ptr %%pipes, i32 0, i32 1
  %%r = load i32, ptr %%r_p, align 4
  %%w = load i32, ptr %%w_p, align 4
  %%fl = call i32 (i32, i32, ...) @fcntl(i32 %%r, i32 3)
  %%fln = or i32 %%fl, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%r, i32 4, i32 %%fln)
  %%f0 = getelementptr %s, ptr %%ep, i32 0, i32 0
  store ptr %%name, ptr %%f0, align 8
  %%f1 = getelementptr %s, ptr %%ep, i32 0, i32 1
  store i32 %%r, ptr %%f1, align 4
  %%f2 = getelementptr %s, ptr %%ep, i32 0, i32 2
  store i32 %%w, ptr %%f2, align 4
  %%f3 = getelementptr %s, ptr %%ep, i32 0, i32 3
  store atomic i64 1, ptr %%f3 seq_cst, align 8
  %%f6 = getelementptr %s, ptr %%ep, i32 0, i32 6
  store i64 %%kind, ptr %%f6, align 8
  %%isbc = icmp eq i64 %%kind, 0
  br i1 %%isbc, label %%reg, label %%done

reg:
  call i32 @pthread_mutex_lock(ptr @__kml_chan_mtx)
  call void @__kml_chan_append(ptr @__kml_chan_bcs_data, ptr @__kml_chan_bcs_len, ptr @__kml_chan_bcs_cap, ptr %%ep)
  call i32 @pthread_mutex_unlock(ptr @__kml_chan_mtx)
  br label %%done

done:
  ret ptr %%ep
}`, epAlloc, httpNonblockFlag(), chanEpIR, chanEpIR, chanEpIR, chanEpIR, chanEpIR))

	// __kml_chan_append: generic doubling append of ep into the (data, len,
	// cap) triple at the given global slots. Caller synchronizes.
	e.emitGlobal(fmt.Sprintf(`define void @__kml_chan_append(ptr %%data_g, ptr %%len_g, ptr %%cap_g, ptr %%ep) {
entry:
  %%len = load i64, ptr %%len_g, align 8
  %%cap = load i64, ptr %%cap_g, align 8
  %%needgrow = icmp sge i64 %%len, %%cap
  br i1 %%needgrow, label %%grow, label %%putslot

grow:
  %%cap2 = mul i64 %%cap, 2
  %%atleast4 = icmp sge i64 %%cap2, 4
  %%newcap = select i1 %%atleast4, i64 %%cap2, i64 4
  %%newbytes = mul i64 %%newcap, 8
  %%newdata = call ptr %s(i64 %%newbytes)
  %%olddata = load ptr, ptr %%data_g, align 8
  %%oldbytes = mul i64 %%len, 8
  %%hasold = icmp ne ptr %%olddata, null
  br i1 %%hasold, label %%copyold, label %%aftercopy

copyold:
  call ptr @memcpy(ptr %%newdata, ptr %%olddata, i64 %%oldbytes)
  call void @free(ptr %%olddata)
  br label %%aftercopy

aftercopy:
  store ptr %%newdata, ptr %%data_g, align 8
  store i64 %%newcap, ptr %%cap_g, align 8
  br label %%putslot

putslot:
  %%data = load ptr, ptr %%data_g, align 8
  %%slot = getelementptr ptr, ptr %%data, i64 %%len
  store ptr %%ep, ptr %%slot, align 8
  %%newlen = add i64 %%len, 1
  store i64 %%newlen, ptr %%len_g, align 8
  ret void
}`, rawAlloc))

	// __kml_chan_send_env: envelope {0, w0, w1} into fd (same atomic 8-byte
	// pointer write the worker channel relies on).
	e.emitGlobal(fmt.Sprintf(`define void @__kml_chan_send_env(i32 %%fd, i64 %%w0, i64 %%w1) {
entry:
  %%env = call ptr %s(i64 24)
  %%k_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 0
  store i64 0, ptr %%k_p, align 8
  %%w0_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 1
  store i64 %%w0, ptr %%w0_p, align 8
  %%w1_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 2
  store i64 %%w1, ptr %%w1_p, align 8
  %%slot = alloca ptr, align 8
  store ptr %%env, ptr %%slot, align 8
  call i64 @write(i32 %%fd, ptr %%slot, i64 8)
  ret void
}`, rawAlloc))

	// __kml_chan_post_bc: fan out to every OTHER live same-name endpoint,
	// calling clonefn once per subscriber so each owns a private copy.
	e.emitGlobal(fmt.Sprintf(`define void @__kml_chan_post_bc(ptr %%self, i64 %%w0, i64 %%w1, ptr %%clonefn) {
entry:
  call i32 @pthread_mutex_lock(ptr @__kml_chan_mtx)
  %%myname_p = getelementptr %s, ptr %%self, i32 0, i32 0
  %%myname = load ptr, ptr %%myname_p, align 8
  %%len = load i64, ptr @__kml_chan_bcs_len, align 8
  %%data = load ptr, ptr @__kml_chan_bcs_data, align 8
  br label %%loop

loop:
  %%i = phi i64 [ 0, %%entry ], [ %%inext, %%next ]
  %%inb = icmp slt i64 %%i, %%len
  br i1 %%inb, label %%body, label %%done

body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%ep = load ptr, ptr %%slot, align 8
  %%isself = icmp eq ptr %%ep, %%self
  br i1 %%isself, label %%next, label %%checkalive

checkalive:
  %%al_p = getelementptr %s, ptr %%ep, i32 0, i32 3
  %%al = load atomic i64, ptr %%al_p seq_cst, align 8
  %%live = icmp ne i64 %%al, 0
  br i1 %%live, label %%checkname, label %%next

checkname:
  %%nm_p = getelementptr %s, ptr %%ep, i32 0, i32 0
  %%nm = load ptr, ptr %%nm_p, align 8
  %%c = call i32 @strcmp(ptr %%nm, ptr %%myname)
  %%same = icmp eq i32 %%c, 0
  br i1 %%same, label %%deliver, label %%next

deliver:
  %%pair = call { i64, i64 } %%clonefn(i64 %%w0, i64 %%w1)
  %%cw0 = extractvalue { i64, i64 } %%pair, 0
  %%cw1 = extractvalue { i64, i64 } %%pair, 1
  %%wfd_p = getelementptr %s, ptr %%ep, i32 0, i32 2
  %%wfd = load i32, ptr %%wfd_p, align 4
  call void @__kml_chan_send_env(i32 %%wfd, i64 %%cw0, i64 %%cw1)
  br label %%next

next:
  %%inext = add i64 %%i, 1
  br label %%loop

done:
  call i32 @pthread_mutex_unlock(ptr @__kml_chan_mtx)
  ret void
}`, chanEpIR, chanEpIR, chanEpIR, chanEpIR))

	// __kml_chan_post_port: single-receiver delivery into the peer half's
	// pipe (payload already cloned by the caller). Dropped if the peer is
	// closed, matching a closed MessagePort's silent discard.
	e.emitGlobal(fmt.Sprintf(`define void @__kml_chan_post_port(ptr %%port, i64 %%w0, i64 %%w1) {
entry:
  %%peer_p = getelementptr %s, ptr %%port, i32 0, i32 7
  %%peer = load ptr, ptr %%peer_p, align 8
  %%al_p = getelementptr %s, ptr %%peer, i32 0, i32 3
  %%al = load atomic i64, ptr %%al_p seq_cst, align 8
  %%live = icmp ne i64 %%al, 0
  br i1 %%live, label %%deliver, label %%done

deliver:
  %%wfd_p = getelementptr %s, ptr %%peer, i32 0, i32 2
  %%wfd = load i32, ptr %%wfd_p, align 4
  call void @__kml_chan_send_env(i32 %%wfd, i64 %%w0, i64 %%w1)
  br label %%done

done:
  ret void
}`, chanEpIR, chanEpIR, chanEpIR))

	// __kml_chan_listen: store the adapter closure, set the loop hold, and
	// claim the endpoint for THIS thread's drain list.
	e.emitGlobal(fmt.Sprintf(`define void @__kml_chan_listen(ptr %%ep, ptr %%cb) {
entry:
  %%cb_p = getelementptr %s, ptr %%ep, i32 0, i32 4
  store ptr %%cb, ptr %%cb_p, align 8
  %%h_p = getelementptr %s, ptr %%ep, i32 0, i32 5
  store i64 1, ptr %%h_p, align 8
  call void @__kml_chan_append(ptr @__kml_chan_mine_data, ptr @__kml_chan_mine_len, ptr @__kml_chan_mine_cap, ptr %%ep)
  ret void
}`, chanEpIR, chanEpIR))

	// __kml_chan_close: mark dead + release the loop hold. The pipe fds are
	// deliberately left open — a concurrent poster that loaded the alive
	// flag just before the close may still write; leaking two fds per
	// closed channel is the safe trade.
	e.emitGlobal(fmt.Sprintf(`define void @__kml_chan_close(ptr %%ep) {
entry:
  %%al_p = getelementptr %s, ptr %%ep, i32 0, i32 3
  store atomic i64 0, ptr %%al_p seq_cst, align 8
  %%h_p = getelementptr %s, ptr %%ep, i32 0, i32 5
  store i64 0, ptr %%h_p, align 8
  ret void
}`, chanEpIR, chanEpIR))

	// Event-loop hook trio (mirrors the worker trio; stubs in
	// emitLoopTaskStubs when unused).
	e.emitGlobal(fmt.Sprintf(`define i1 @__kml_chan_keepalive() {
entry:
  %%len = load i64, ptr @__kml_chan_mine_len, align 8
  %%data = load ptr, ptr @__kml_chan_mine_data, align 8
  br label %%loop

loop:
  %%i = phi i64 [ 0, %%entry ], [ %%inext, %%next ]
  %%inb = icmp slt i64 %%i, %%len
  br i1 %%inb, label %%body, label %%none

body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%ep = load ptr, ptr %%slot, align 8
  %%al_p = getelementptr %s, ptr %%ep, i32 0, i32 3
  %%al = load atomic i64, ptr %%al_p seq_cst, align 8
  %%h_p = getelementptr %s, ptr %%ep, i32 0, i32 5
  %%h = load i64, ptr %%h_p, align 8
  %%live = icmp ne i64 %%al, 0
  %%held = icmp ne i64 %%h, 0
  %%keeps = and i1 %%live, %%held
  br i1 %%keeps, label %%alive, label %%next

next:
  %%inext = add i64 %%i, 1
  br label %%loop

alive:
  ret i1 1

none:
  ret i1 0
}`, chanEpIR, chanEpIR))

	e.emitGlobal(fmt.Sprintf(`define i1 @__kml_chan_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  %%len = load i64, ptr @__kml_chan_mine_len, align 8
  %%data = load ptr, ptr @__kml_chan_mine_data, align 8
  br label %%loop

loop:
  %%i = phi i64 [ 0, %%entry ], [ %%inext, %%next ]
  %%inb = icmp slt i64 %%i, %%len
  br i1 %%inb, label %%body, label %%done

body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%ep = load ptr, ptr %%slot, align 8
  %%al_p = getelementptr %s, ptr %%ep, i32 0, i32 3
  %%al = load atomic i64, ptr %%al_p seq_cst, align 8
  %%live = icmp ne i64 %%al, 0
  br i1 %%live, label %%addfd, label %%next

addfd:
  %%r_p = getelementptr %s, ptr %%ep, i32 0, i32 1
  %%r = load i32, ptr %%r_p, align 4
  call void @__kml_worker_fd_setbit(i32 %%r, ptr %%fdset, ptr %%maxfd)
  br label %%next

next:
  %%inext = add i64 %%i, 1
  br label %%loop

done:
  ret i1 0
}`, chanEpIR, chanEpIR))

	e.emitGlobal(fmt.Sprintf(`define void @__kml_chan_dispatch() {
entry:
  %%slotbuf = alloca ptr, align 8
  %%len = load i64, ptr @__kml_chan_mine_len, align 8
  %%data = load ptr, ptr @__kml_chan_mine_data, align 8
  br label %%loop

loop:
  %%i = phi i64 [ 0, %%entry ], [ %%inext, %%next ]
  %%inb = icmp slt i64 %%i, %%len
  br i1 %%inb, label %%body, label %%done

body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%ep = load ptr, ptr %%slot, align 8
  %%al_p = getelementptr %s, ptr %%ep, i32 0, i32 3
  %%al = load atomic i64, ptr %%al_p seq_cst, align 8
  %%live = icmp ne i64 %%al, 0
  br i1 %%live, label %%drain, label %%next

drain:
  %%r_p = getelementptr %s, ptr %%ep, i32 0, i32 1
  %%r = load i32, ptr %%r_p, align 4
  br label %%readone

readone:
  %%n = call i64 @read(i32 %%r, ptr %%slotbuf, i64 8)
  %%got = icmp eq i64 %%n, 8
  br i1 %%got, label %%gotenv, label %%next

gotenv:
  %%env = load ptr, ptr %%slotbuf, align 8
  %%w0_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 1
  %%w0 = load i64, ptr %%w0_p, align 8
  %%w1_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 2
  %%w1 = load i64, ptr %%w1_p, align 8
  call void @free(ptr %%env)
  %%cb_p = getelementptr %s, ptr %%ep, i32 0, i32 4
  %%cb = load ptr, ptr %%cb_p, align 8
  %%hascb = icmp ne ptr %%cb, null
  br i1 %%hascb, label %%callcb, label %%readone

callcb:
  %%fp_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 0
  %%ep_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 1
  %%fp = load ptr, ptr %%fp_p, align 8
  %%envp = load ptr, ptr %%ep_p, align 8
  call void %%fp(ptr %%envp, i64 %%w0, i64 %%w1)
  br label %%readone

next:
  %%inext = add i64 %%i, 1
  br label %%loop

done:
  ret void
}`, chanEpIR, chanEpIR, chanEpIR))
}
