// runtime_fswatch.go — fs.watch (TDD-00181): a native, event-driven file
// watcher folded into the select() event loop via the standard hook trio
// (__kml_fswatch_keepalive / _fdset_add / _dispatch), matching worker/cp/
// readline. Stage 1 is the Linux inotify backend — the inotify instance is a
// readable fd, so it drops straight into the loop's read fd_set with no
// thread. Windows (ReadDirectoryChangesW + a wakeup socket) and macOS
// (kqueue) are Stages 2/3; until then their __kml_fs_watch throws and the
// hooks are no-ops.
package llvm

import (
	"fmt"
)

// fsWatcherStructIR: { i64 wd, ptr changeCb, ptr renameCb, i64 open, ptr name }.
const fsWatcherStructIR = "{ i64, ptr, ptr, i64, ptr }"

func (e *Emitter) ensureFsWatchRuntime() {
	if e.usedFsWatchRuntime {
		return
	}
	e.usedFsWatchRuntime = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureStrHeaderRuntime()

	// Shared globals + the FSWatcher object accessors, platform-independent.
	e.emitGlobal(`
@__kml_fswatch_ifd = internal global i32 -1, align 4
@__kml_fswatch_reg_data = internal global ptr null, align 8
@__kml_fswatch_reg_len = internal global i64 0, align 8
@__kml_fswatch_reg_cap = internal global i64 0, align 8
@__kml_fswatch_open = internal global i64 0, align 8`)

	if targetGOOS() == "windows" {
		e.emitFsWatchWindows()
		return
	}
	if targetGOOS() == "darwin" {
		e.emitFsWatchDarwin()
		return
	}
	if targetGOOS() != "linux" {
		// Other BSDs etc.: no backend — watch throws, hooks inert.
		notYet := e.internString("cannot watch path")
		e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_fs_watch(ptr %%path) {
entry:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
}
define i1 @__kml_fswatch_keepalive() {
entry:
  ret i1 0
}
define i1 @__kml_fswatch_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  ret i1 0
}
define void @__kml_fswatch_dispatch() {
entry:
  ret void
}
define void @__kml_fswatch_on(ptr %%w, i64 %%isRename, ptr %%cb) {
entry:
  ret void
}
define void @__kml_fswatch_close(ptr %%w) {
entry:
  ret void
}`, notYet, e.internString("watch")))
		return
	}

	// ---- Linux (inotify) ----------------------------------------------------
	e.emitGlobal("declare i32 @inotify_init1(i32 noundef)")
	e.emitGlobal("declare i32 @inotify_add_watch(i32 noundef, ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @inotify_rm_watch(i32 noundef, i32 noundef)")
	e.ensureReadDecl()
	watchDesc := e.internString("cannot watch path")
	renameStr := e.internString("rename")
	changeStr := e.internString("change")
	sw := fsWatcherStructIR

	// __kml_fs_watch(path): lazily create the inotify instance, add a watch
	// (IN_MODIFY|IN_ATTRIB|IN_CREATE|IN_DELETE|IN_MOVED_FROM|IN_MOVED_TO|
	// IN_MOVE_SELF|IN_DELETE_SELF = 0xFC6), and register the FSWatcher.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_fs_watch(ptr %%path) {
entry:
  %%ifd0 = load i32, ptr @__kml_fswatch_ifd, align 4
  %%needinit = icmp slt i32 %%ifd0, 0
  br i1 %%needinit, label %%doinit, label %%haveifd
doinit:
  ; IN_NONBLOCK|IN_CLOEXEC = 0x80800
  %%newifd = call i32 @inotify_init1(i32 526336)
  store i32 %%newifd, ptr @__kml_fswatch_ifd, align 4
  br label %%haveifd
haveifd:
  %%ifd = load i32, ptr @__kml_fswatch_ifd, align 4
  %%ifdbad = icmp slt i32 %%ifd, 0
  br i1 %%ifdbad, label %%fail, label %%addw
addw:
  %%wd = call i32 @inotify_add_watch(i32 %%ifd, ptr %%path, i32 4038)
  %%wdbad = icmp slt i32 %%wd, 0
  br i1 %%wdbad, label %%fail, label %%mk
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
mk:
  %%w = call ptr @malloc(i64 40)
  %%wd_p = getelementptr %s, ptr %%w, i32 0, i32 0
  %%wd64 = sext i32 %%wd to i64
  store i64 %%wd64, ptr %%wd_p, align 8
  %%chg_p = getelementptr %s, ptr %%w, i32 0, i32 1
  store ptr null, ptr %%chg_p, align 8
  %%rnm_p = getelementptr %s, ptr %%w, i32 0, i32 2
  store ptr null, ptr %%rnm_p, align 8
  %%open_p = getelementptr %s, ptr %%w, i32 0, i32 3
  store i64 1, ptr %%open_p, align 8
  %%name_p = getelementptr %s, ptr %%w, i32 0, i32 4
  store ptr %%path, ptr %%name_p, align 8
  ; register
  %%rlen = load i64, ptr @__kml_fswatch_reg_len, align 8
  %%rcap = load i64, ptr @__kml_fswatch_reg_cap, align 8
  %%rdata = load ptr, ptr @__kml_fswatch_reg_data, align 8
  %%needp1 = add i64 %%rlen, 1
  %%needgrow = icmp sgt i64 %%needp1, %%rcap
  br i1 %%needgrow, label %%grow, label %%store
grow:
  %%cap2 = mul i64 %%rcap, 2
  %%atleast8 = icmp sgt i64 %%cap2, 8
  %%newcap = select i1 %%atleast8, i64 %%cap2, i64 8
  %%newbytes = mul i64 %%newcap, 8
  %%newdata = call ptr @realloc(ptr %%rdata, i64 %%newbytes)
  store ptr %%newdata, ptr @__kml_fswatch_reg_data, align 8
  store i64 %%newcap, ptr @__kml_fswatch_reg_cap, align 8
  br label %%store
store:
  %%rdata2 = load ptr, ptr @__kml_fswatch_reg_data, align 8
  %%slot = getelementptr ptr, ptr %%rdata2, i64 %%rlen
  store ptr %%w, ptr %%slot, align 8
  %%newlen = add i64 %%rlen, 1
  store i64 %%newlen, ptr @__kml_fswatch_reg_len, align 8
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%oc1 = add i64 %%oc, 1
  store i64 %%oc1, ptr @__kml_fswatch_open, align 8
  ret ptr %%w
}`, watchDesc, e.internString("watch"), sw, sw, sw, sw, sw))

	// keepalive / fdset_add / on / close
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_fswatch_keepalive() {
entry:
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%alive = icmp sgt i64 %%oc, 0
  ret i1 %%alive
}
define i1 @__kml_fswatch_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%any = icmp sgt i64 %%oc, 0
  %%ifd = load i32, ptr @__kml_fswatch_ifd, align 4
  %%ok = icmp sge i32 %%ifd, 0
  %%go = and i1 %%any, %%ok
  br i1 %%go, label %%add, label %%ret
add:
  %%div8 = sdiv i32 %%ifd, 8
  %%mod8 = srem i32 %%ifd, 8
  %%div8_64 = sext i32 %%div8 to i64
  %%byteptr = getelementptr i8, ptr %%fdset, i64 %%div8_64
  %%bit8 = trunc i32 %%mod8 to i8
  %%mask = shl i8 1, %%bit8
  %%old = load i8, ptr %%byteptr, align 1
  %%new = or i8 %%old, %%mask
  store i8 %%new, ptr %%byteptr, align 1
  %%curmax = load i32, ptr %%maxfd, align 4
  %%bigger = icmp sgt i32 %%ifd, %%curmax
  br i1 %%bigger, label %%upd, label %%ret
upd:
  store i32 %%ifd, ptr %%maxfd, align 4
  br label %%ret
ret:
  ret i1 0
}
define void @__kml_fswatch_on(ptr %%w, i64 %%isRename, ptr %%cb) {
entry:
  %%rn = icmp ne i64 %%isRename, 0
  br i1 %%rn, label %%setr, label %%setc
setr:
  %%rnm_p = getelementptr %s, ptr %%w, i32 0, i32 2
  store ptr %%cb, ptr %%rnm_p, align 8
  ret void
setc:
  %%chg_p = getelementptr %s, ptr %%w, i32 0, i32 1
  store ptr %%cb, ptr %%chg_p, align 8
  ret void
}
define void @__kml_fswatch_close(ptr %%w) {
entry:
  %%open_p = getelementptr %s, ptr %%w, i32 0, i32 3
  %%isopen = load i64, ptr %%open_p, align 8
  %%wasopen = icmp ne i64 %%isopen, 0
  br i1 %%wasopen, label %%doclose, label %%ret
doclose:
  %%wd_p = getelementptr %s, ptr %%w, i32 0, i32 0
  %%wd64 = load i64, ptr %%wd_p, align 8
  %%wd = trunc i64 %%wd64 to i32
  %%ifd = load i32, ptr @__kml_fswatch_ifd, align 4
  call i32 @inotify_rm_watch(i32 %%ifd, i32 %%wd)
  store i64 0, ptr %%open_p, align 8
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%oc1 = sub i64 %%oc, 1
  store i64 %%oc1, ptr @__kml_fswatch_open, align 8
  ret void
ret:
  ret void
}`, sw, sw, sw, sw))

	// __kml_fswatch_dispatch: read all pending inotify events and fire the
	// matching watcher's listener. struct inotify_event = { i32 wd, u32 mask,
	// u32 cookie, u32 len, char name[len] }; name starts at offset 16.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fswatch_dispatch() {
entry:
  %%ifd = load i32, ptr @__kml_fswatch_ifd, align 4
  %%bad = icmp slt i32 %%ifd, 0
  br i1 %%bad, label %%ret, label %%readloop
readloop:
  %%buf = alloca [4096 x i8], align 8
  %%bufp = getelementptr [4096 x i8], ptr %%buf, i32 0, i32 0
  %%n = call i64 @read(i32 %%ifd, ptr %%bufp, i64 4096)
  %%hasdata = icmp sgt i64 %%n, 0
  br i1 %%hasdata, label %%parse, label %%ret
parse:
  %%offslot = alloca i64, align 8
  store i64 0, ptr %%offslot, align 8
  br label %%evloop
evloop:
  %%off = load i64, ptr %%offslot, align 8
  %%more = icmp slt i64 %%off, %%n
  br i1 %%more, label %%evbody, label %%readloop
evbody:
  %%evp = getelementptr i8, ptr %%bufp, i64 %%off
  %%wd = load i32, ptr %%evp, align 1
  %%mask_p = getelementptr i8, ptr %%evp, i64 4
  %%mask = load i32, ptr %%mask_p, align 1
  %%len_p = getelementptr i8, ptr %%evp, i64 12
  %%elen32 = load i32, ptr %%len_p, align 1
  %%elen = zext i32 %%elen32 to i64
  ; kind: rename if mask & 0xFC0, else change if mask & 0x6
  %%rmask = and i32 %%mask, 4032
  %%isrename = icmp ne i32 %%rmask, 0
  %%cmask = and i32 %%mask, 6
  %%ischange = icmp ne i32 %%cmask, 0
  %%fireable = or i1 %%isrename, %%ischange
  br i1 %%fireable, label %%find, label %%evnext
find:
  ; linear scan of the registry for an open watcher with this wd
  %%wd64 = sext i32 %%wd to i64
  %%rlen = load i64, ptr @__kml_fswatch_reg_len, align 8
  %%rdata = load ptr, ptr @__kml_fswatch_reg_data, align 8
  %%islot = alloca i64, align 8
  store i64 0, ptr %%islot, align 8
  br label %%scan
scan:
  %%i = load i64, ptr %%islot, align 8
  %%iin = icmp slt i64 %%i, %%rlen
  br i1 %%iin, label %%scanbody, label %%evnext
scanbody:
  %%wslotp = getelementptr ptr, ptr %%rdata, i64 %%i
  %%wp = load ptr, ptr %%wslotp, align 8
  %%wwd_p = getelementptr %s, ptr %%wp, i32 0, i32 0
  %%wwd = load i64, ptr %%wwd_p, align 8
  %%wopen_p = getelementptr %s, ptr %%wp, i32 0, i32 3
  %%wopen = load i64, ptr %%wopen_p, align 8
  %%wmatch = icmp eq i64 %%wwd, %%wd64
  %%wisopen = icmp ne i64 %%wopen, 0
  %%hit = and i1 %%wmatch, %%wisopen
  br i1 %%hit, label %%fire, label %%scannext
scannext:
  %%i1 = add i64 %%i, 1
  store i64 %%i1, ptr %%islot, align 8
  br label %%scan
fire:
  ; choose listener + event-type string
  %%cbslot = getelementptr %s, ptr %%wp, i32 0, i32 2
  %%cbslotc = getelementptr %s, ptr %%wp, i32 0, i32 1
  %%cbr = load ptr, ptr %%cbslot, align 8
  %%cbc = load ptr, ptr %%cbslotc, align 8
  %%cb = select i1 %%isrename, ptr %%cbr, ptr %%cbc
  %%evstr = select i1 %%isrename, ptr %s, ptr %s
  %%hascb = icmp ne ptr %%cb, null
  br i1 %%hascb, label %%docall, label %%evnext
docall:
  ; filename: the event's own name if present, else the watcher's path
  %%haslen = icmp sgt i64 %%elen, 0
  br i1 %%haslen, label %%evname, label %%selfname
evname:
  %%namep = getelementptr i8, ptr %%evp, i64 16
  %%fnfromev = call ptr @__kml_str_from_cstr(ptr %%namep)
  br label %%callit
selfname:
  %%wname_p = getelementptr %s, ptr %%wp, i32 0, i32 4
  %%fnself = load ptr, ptr %%wname_p, align 8
  br label %%callit
callit:
  %%fn = phi ptr [ %%fnfromev, %%evname ], [ %%fnself, %%selfname ]
  %%fp_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 0
  %%fp = load ptr, ptr %%fp_p, align 8
  %%ep_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 1
  %%ep = load ptr, ptr %%ep_p, align 8
  call void %%fp(ptr %%ep, ptr %%evstr, ptr %%fn)
  br label %%evnext
evnext:
  %%off2 = load i64, ptr %%offslot, align 8
  %%elen2_p = getelementptr i8, ptr %%evp, i64 12
  %%elen2_32 = load i32, ptr %%elen2_p, align 1
  %%elen2 = zext i32 %%elen2_32 to i64
  %%rec = add i64 16, %%elen2
  %%off3 = add i64 %%off2, %%rec
  store i64 %%off3, ptr %%offslot, align 8
  br label %%evloop
ret:
  ret void
}`, sw, sw, sw, sw, renameStr, changeStr, sw))
}

// emitFsWatchDarwin emits the macOS fs.watch backend IR (TDD-00181 Stage 3):
// kqueue + EVFILT_VNODE. Like inotify, the kqueue is a readable fd that drops
// into the loop's fd_set with no thread; unlike inotify, it watches an open
// fd (not a path), so each watcher keeps its `open(O_EVTONLY)` fd and stores
// the FSWatcher pointer in the kevent's udata — dispatch reads udata back and
// needs no registry scan. kqueue cannot name a changed directory entry, so
// `filename` is the watched path (a documented divergence from Node's FSEvents
// naming). struct kevent (64-bit): ident@0 i64, filter@8 i16, flags@10 i16,
// fflags@12 i32, data@16 i64, udata@24 ptr (32 bytes).
func (e *Emitter) emitFsWatchDarwin() {
	e.ensureOpenDecl()
	watchDesc := e.internString("cannot watch path")
	renameStr := e.internString("rename")
	changeStr := e.internString("change")
	sw := fsWatcherStructIR
	e.emitGlobal("@__kml_fswatch_kq = internal global i32 -1, align 4")
	e.emitGlobal("declare i32 @kqueue()")
	e.emitGlobal("declare i32 @kevent(i32 noundef, ptr noundef, i32 noundef, ptr noundef, i32 noundef, ptr noundef)")

	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_fs_watch(ptr %%path) {
entry:
  %%kq0 = load i32, ptr @__kml_fswatch_kq, align 4
  %%needinit = icmp slt i32 %%kq0, 0
  br i1 %%needinit, label %%doinit, label %%haveq
doinit:
  %%newkq = call i32 @kqueue()
  store i32 %%newkq, ptr @__kml_fswatch_kq, align 4
  br label %%haveq
haveq:
  %%kq = load i32, ptr @__kml_fswatch_kq, align 4
  %%qbad = icmp slt i32 %%kq, 0
  br i1 %%qbad, label %%fail, label %%openf
openf:
  ; O_EVTONLY = 0x8000
  %%fd = call i32 (ptr, i32, ...) @open(ptr %%path, i32 32768)
  %%fdbad = icmp slt i32 %%fd, 0
  br i1 %%fdbad, label %%fail, label %%mk
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
mk:
  %%w = call ptr @malloc(i64 40)
  %%fd_p = getelementptr %s, ptr %%w, i32 0, i32 0
  %%fd64 = sext i32 %%fd to i64
  store i64 %%fd64, ptr %%fd_p, align 8
  %%chg_p = getelementptr %s, ptr %%w, i32 0, i32 1
  store ptr null, ptr %%chg_p, align 8
  %%rnm_p = getelementptr %s, ptr %%w, i32 0, i32 2
  store ptr null, ptr %%rnm_p, align 8
  %%open_p = getelementptr %s, ptr %%w, i32 0, i32 3
  store i64 1, ptr %%open_p, align 8
  %%name_p = getelementptr %s, ptr %%w, i32 0, i32 4
  store ptr %%path, ptr %%name_p, align 8
  ; build a struct kevent on the stack: EV_SET(fd, EVFILT_VNODE, EV_ADD|EV_CLEAR, 0x7f, 0, w)
  %%kev = alloca [4 x i64], align 8
  %%kev_ident = getelementptr i8, ptr %%kev, i64 0
  store i64 %%fd64, ptr %%kev_ident, align 8
  %%kev_filter = getelementptr i8, ptr %%kev, i64 8
  store i16 -4, ptr %%kev_filter, align 2
  %%kev_flags = getelementptr i8, ptr %%kev, i64 10
  store i16 33, ptr %%kev_flags, align 2
  %%kev_fflags = getelementptr i8, ptr %%kev, i64 12
  store i32 127, ptr %%kev_fflags, align 4
  %%kev_data = getelementptr i8, ptr %%kev, i64 16
  store i64 0, ptr %%kev_data, align 8
  %%kev_udata = getelementptr i8, ptr %%kev, i64 24
  store ptr %%w, ptr %%kev_udata, align 8
  call i32 @kevent(i32 %%kq, ptr %%kev, i32 1, ptr null, i32 0, ptr null)
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%oc1 = add i64 %%oc, 1
  store i64 %%oc1, ptr @__kml_fswatch_open, align 8
  ret ptr %%w
}
define i1 @__kml_fswatch_keepalive() {
entry:
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%alive = icmp sgt i64 %%oc, 0
  ret i1 %%alive
}
define i1 @__kml_fswatch_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%any = icmp sgt i64 %%oc, 0
  %%kq = load i32, ptr @__kml_fswatch_kq, align 4
  %%ok = icmp sge i32 %%kq, 0
  %%go = and i1 %%any, %%ok
  br i1 %%go, label %%add, label %%ret
add:
  %%div8 = sdiv i32 %%kq, 8
  %%mod8 = srem i32 %%kq, 8
  %%div8_64 = sext i32 %%div8 to i64
  %%byteptr = getelementptr i8, ptr %%fdset, i64 %%div8_64
  %%bit8 = trunc i32 %%mod8 to i8
  %%mask = shl i8 1, %%bit8
  %%old = load i8, ptr %%byteptr, align 1
  %%new = or i8 %%old, %%mask
  store i8 %%new, ptr %%byteptr, align 1
  %%curmax = load i32, ptr %%maxfd, align 4
  %%bigger = icmp sgt i32 %%kq, %%curmax
  br i1 %%bigger, label %%upd, label %%ret
upd:
  store i32 %%kq, ptr %%maxfd, align 4
  br label %%ret
ret:
  ret i1 0
}
define void @__kml_fswatch_on(ptr %%w, i64 %%isRename, ptr %%cb) {
entry:
  %%rn = icmp ne i64 %%isRename, 0
  br i1 %%rn, label %%setr, label %%setc
setr:
  %%rnm_p = getelementptr %s, ptr %%w, i32 0, i32 2
  store ptr %%cb, ptr %%rnm_p, align 8
  ret void
setc:
  %%chg_p = getelementptr %s, ptr %%w, i32 0, i32 1
  store ptr %%cb, ptr %%chg_p, align 8
  ret void
}
define void @__kml_fswatch_close(ptr %%w) {
entry:
  %%open_p = getelementptr %s, ptr %%w, i32 0, i32 3
  %%isopen = load i64, ptr %%open_p, align 8
  %%wasopen = icmp ne i64 %%isopen, 0
  br i1 %%wasopen, label %%doclose, label %%ret
doclose:
  %%fd_p = getelementptr %s, ptr %%w, i32 0, i32 0
  %%fd64 = load i64, ptr %%fd_p, align 8
  %%fd = trunc i64 %%fd64 to i32
  ; closing the fd auto-removes its kevent from the kqueue
  call i32 @close(i32 %%fd)
  store i64 0, ptr %%open_p, align 8
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%oc1 = sub i64 %%oc, 1
  store i64 %%oc1, ptr @__kml_fswatch_open, align 8
  ret void
ret:
  ret void
}
define void @__kml_fswatch_dispatch() {
entry:
  %%kq = load i32, ptr @__kml_fswatch_kq, align 4
  %%bad = icmp slt i32 %%kq, 0
  br i1 %%bad, label %%ret, label %%pull
pull:
  %%evs = alloca [64 x i64], align 8
  %%ts = alloca [2 x i64], align 8
  %%ts0 = getelementptr [2 x i64], ptr %%ts, i32 0, i32 0
  store i64 0, ptr %%ts0, align 8
  %%ts1 = getelementptr [2 x i64], ptr %%ts, i32 0, i32 1
  store i64 0, ptr %%ts1, align 8
  %%n = call i32 @kevent(i32 %%kq, ptr null, i32 0, ptr %%evs, i32 16, ptr %%ts)
  %%islot = alloca i64, align 8
  store i64 0, ptr %%islot, align 8
  br label %%loop
loop:
  %%i = load i64, ptr %%islot, align 8
  %%n64 = sext i32 %%n to i64
  %%more = icmp slt i64 %%i, %%n64
  br i1 %%more, label %%body, label %%ret
body:
  %%evoff = mul i64 %%i, 32
  %%ev = getelementptr i8, ptr %%evs, i64 %%evoff
  %%ud_p = getelementptr i8, ptr %%ev, i64 24
  %%wp = load ptr, ptr %%ud_p, align 8
  %%ff_p = getelementptr i8, ptr %%ev, i64 12
  %%fflags = load i32, ptr %%ff_p, align 4
  %%rmask = and i32 %%fflags, 97
  %%isrename = icmp ne i32 %%rmask, 0
  %%cmask = and i32 %%fflags, 30
  %%ischange = icmp ne i32 %%cmask, 0
  %%fireable = or i1 %%isrename, %%ischange
  br i1 %%fireable, label %%chkw, label %%next
chkw:
  %%wnull2 = icmp eq ptr %%wp, null
  br i1 %%wnull2, label %%next, label %%fire
fire:
  %%wopen_p = getelementptr %s, ptr %%wp, i32 0, i32 3
  %%wopen = load i64, ptr %%wopen_p, align 8
  %%wisopen = icmp ne i64 %%wopen, 0
  br i1 %%wisopen, label %%dofire, label %%next
dofire:
  %%cbr_p = getelementptr %s, ptr %%wp, i32 0, i32 2
  %%cbc_p = getelementptr %s, ptr %%wp, i32 0, i32 1
  %%cbr = load ptr, ptr %%cbr_p, align 8
  %%cbc = load ptr, ptr %%cbc_p, align 8
  %%cb = select i1 %%isrename, ptr %%cbr, ptr %%cbc
  %%evstr = select i1 %%isrename, ptr %s, ptr %s
  %%hascb = icmp ne ptr %%cb, null
  br i1 %%hascb, label %%docall, label %%next
docall:
  %%name_p = getelementptr %s, ptr %%wp, i32 0, i32 4
  %%fn = load ptr, ptr %%name_p, align 8
  %%fp_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 0
  %%fp = load ptr, ptr %%fp_p, align 8
  %%ep_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 1
  %%ep = load ptr, ptr %%ep_p, align 8
  call void %%fp(ptr %%ep, ptr %%evstr, ptr %%fn)
  br label %%next
next:
  %%i2 = load i64, ptr %%islot, align 8
  %%i3 = add i64 %%i2, 1
  store i64 %%i3, ptr %%islot, align 8
  br label %%loop
ret:
  ret void
}`, watchDesc, e.internString("watch"),
		// 12 FSWatcher-struct GEPs precede the event-name select; the docall
		// `name_p` GEP is the one that follows it. renameStr/changeStr fill the
		// select — they must sit between the 12th and 13th `sw`, not after both
		// (the earlier all-`sw`-then-strings order put fsWatcherStructIR into the
		// select and produced invalid `select … ptr { … }` IR on macOS).
		sw, sw, sw, sw, sw, sw, sw, sw, sw, sw, sw, sw, renameStr, changeStr, sw))
}

// emitFsWatchWindows emits the Windows fs.watch backend IR (TDD-00181 Stage 2)
// on top of the win32fswatch.c helpers: __kml_fs_watch starts a watcher
// thread, the hooks add the loopback wakeup socket to the loop and drain the
// C event queue. Shares the FSWatcher struct/registry globals with Linux;
// field 0 holds the C watcher context pointer (as i64).
func (e *Emitter) emitFsWatchWindows() {
	watchDesc := e.internString("cannot watch path")
	renameStr := e.internString("rename")
	changeStr := e.internString("change")
	sw := fsWatcherStructIR
	e.emitGlobal("declare ptr @__kml_fswatch_win_start(ptr noundef)")
	e.emitGlobal("declare i32 @__kml_fswatch_win_wakefd()")
	e.emitGlobal("declare i32 @__kml_fswatch_win_next(ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare void @__kml_fswatch_win_stop(ptr noundef)")

	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_fs_watch(ptr %%path) {
entry:
  %%ctx = call ptr @__kml_fswatch_win_start(ptr %%path)
  %%bad = icmp eq ptr %%ctx, null
  br i1 %%bad, label %%fail, label %%mk
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
mk:
  %%w = call ptr @malloc(i64 40)
  %%wd_p = getelementptr %s, ptr %%w, i32 0, i32 0
  %%ctxi = ptrtoint ptr %%ctx to i64
  store i64 %%ctxi, ptr %%wd_p, align 8
  %%chg_p = getelementptr %s, ptr %%w, i32 0, i32 1
  store ptr null, ptr %%chg_p, align 8
  %%rnm_p = getelementptr %s, ptr %%w, i32 0, i32 2
  store ptr null, ptr %%rnm_p, align 8
  %%open_p = getelementptr %s, ptr %%w, i32 0, i32 3
  store i64 1, ptr %%open_p, align 8
  %%name_p = getelementptr %s, ptr %%w, i32 0, i32 4
  store ptr %%path, ptr %%name_p, align 8
  %%rlen = load i64, ptr @__kml_fswatch_reg_len, align 8
  %%rcap = load i64, ptr @__kml_fswatch_reg_cap, align 8
  %%rdata = load ptr, ptr @__kml_fswatch_reg_data, align 8
  %%needp1 = add i64 %%rlen, 1
  %%needgrow = icmp sgt i64 %%needp1, %%rcap
  br i1 %%needgrow, label %%grow, label %%store
grow:
  %%cap2 = mul i64 %%rcap, 2
  %%atleast8 = icmp sgt i64 %%cap2, 8
  %%newcap = select i1 %%atleast8, i64 %%cap2, i64 8
  %%newbytes = mul i64 %%newcap, 8
  %%newdata = call ptr @realloc(ptr %%rdata, i64 %%newbytes)
  store ptr %%newdata, ptr @__kml_fswatch_reg_data, align 8
  store i64 %%newcap, ptr @__kml_fswatch_reg_cap, align 8
  br label %%store
store:
  %%rdata2 = load ptr, ptr @__kml_fswatch_reg_data, align 8
  %%slot = getelementptr ptr, ptr %%rdata2, i64 %%rlen
  store ptr %%w, ptr %%slot, align 8
  %%newlen = add i64 %%rlen, 1
  store i64 %%newlen, ptr @__kml_fswatch_reg_len, align 8
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%oc1 = add i64 %%oc, 1
  store i64 %%oc1, ptr @__kml_fswatch_open, align 8
  ret ptr %%w
}
define i1 @__kml_fswatch_keepalive() {
entry:
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%alive = icmp sgt i64 %%oc, 0
  ret i1 %%alive
}
define i1 @__kml_fswatch_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%any = icmp sgt i64 %%oc, 0
  br i1 %%any, label %%chk, label %%ret
chk:
  %%wfd = call i32 @__kml_fswatch_win_wakefd()
  %%ok = icmp sge i32 %%wfd, 0
  br i1 %%ok, label %%add, label %%ret
add:
  %%div8 = sdiv i32 %%wfd, 8
  %%mod8 = srem i32 %%wfd, 8
  %%div8_64 = sext i32 %%div8 to i64
  %%byteptr = getelementptr i8, ptr %%fdset, i64 %%div8_64
  %%bit8 = trunc i32 %%mod8 to i8
  %%mask = shl i8 1, %%bit8
  %%old = load i8, ptr %%byteptr, align 1
  %%new = or i8 %%old, %%mask
  store i8 %%new, ptr %%byteptr, align 1
  %%curmax = load i32, ptr %%maxfd, align 4
  %%bigger = icmp sgt i32 %%wfd, %%curmax
  br i1 %%bigger, label %%upd, label %%ret
upd:
  store i32 %%wfd, ptr %%maxfd, align 4
  br label %%ret
ret:
  ret i1 0
}
define void @__kml_fswatch_on(ptr %%w, i64 %%isRename, ptr %%cb) {
entry:
  %%rn = icmp ne i64 %%isRename, 0
  br i1 %%rn, label %%setr, label %%setc
setr:
  %%rnm_p = getelementptr %s, ptr %%w, i32 0, i32 2
  store ptr %%cb, ptr %%rnm_p, align 8
  ret void
setc:
  %%chg_p = getelementptr %s, ptr %%w, i32 0, i32 1
  store ptr %%cb, ptr %%chg_p, align 8
  ret void
}
define void @__kml_fswatch_close(ptr %%w) {
entry:
  %%open_p = getelementptr %s, ptr %%w, i32 0, i32 3
  %%isopen = load i64, ptr %%open_p, align 8
  %%wasopen = icmp ne i64 %%isopen, 0
  br i1 %%wasopen, label %%doclose, label %%ret
doclose:
  %%wd_p = getelementptr %s, ptr %%w, i32 0, i32 0
  %%ctxi = load i64, ptr %%wd_p, align 8
  %%ctx = inttoptr i64 %%ctxi to ptr
  call void @__kml_fswatch_win_stop(ptr %%ctx)
  store i64 0, ptr %%open_p, align 8
  %%oc = load i64, ptr @__kml_fswatch_open, align 8
  %%oc1 = sub i64 %%oc, 1
  store i64 %%oc1, ptr @__kml_fswatch_open, align 8
  ret void
ret:
  ret void
}
define void @__kml_fswatch_dispatch() {
entry:
  %%ctxslot = alloca ptr, align 8
  %%rnslot = alloca i32, align 4
  %%namebuf = alloca [520 x i8], align 8
  %%namep = getelementptr [520 x i8], ptr %%namebuf, i32 0, i32 0
  br label %%loop
loop:
  %%got = call i32 @__kml_fswatch_win_next(ptr %%ctxslot, ptr %%rnslot, ptr %%namep)
  %%has = icmp ne i32 %%got, 0
  br i1 %%has, label %%body, label %%ret
body:
  %%ctx = load ptr, ptr %%ctxslot, align 8
  %%ctxi = ptrtoint ptr %%ctx to i64
  %%rn32 = load i32, ptr %%rnslot, align 4
  %%isrename = icmp ne i32 %%rn32, 0
  %%rlen = load i64, ptr @__kml_fswatch_reg_len, align 8
  %%rdata = load ptr, ptr @__kml_fswatch_reg_data, align 8
  %%islot = alloca i64, align 8
  store i64 0, ptr %%islot, align 8
  br label %%scan
scan:
  %%i = load i64, ptr %%islot, align 8
  %%iin = icmp slt i64 %%i, %%rlen
  br i1 %%iin, label %%scanbody, label %%loop
scanbody:
  %%wslotp = getelementptr ptr, ptr %%rdata, i64 %%i
  %%wp = load ptr, ptr %%wslotp, align 8
  %%wwd_p = getelementptr %s, ptr %%wp, i32 0, i32 0
  %%wwd = load i64, ptr %%wwd_p, align 8
  %%wopen_p = getelementptr %s, ptr %%wp, i32 0, i32 3
  %%wopen = load i64, ptr %%wopen_p, align 8
  %%wmatch = icmp eq i64 %%wwd, %%ctxi
  %%wisopen = icmp ne i64 %%wopen, 0
  %%hit = and i1 %%wmatch, %%wisopen
  br i1 %%hit, label %%fire, label %%scannext
scannext:
  %%i1 = add i64 %%i, 1
  store i64 %%i1, ptr %%islot, align 8
  br label %%scan
fire:
  %%cbr_p = getelementptr %s, ptr %%wp, i32 0, i32 2
  %%cbc_p = getelementptr %s, ptr %%wp, i32 0, i32 1
  %%cbr = load ptr, ptr %%cbr_p, align 8
  %%cbc = load ptr, ptr %%cbc_p, align 8
  %%cb = select i1 %%isrename, ptr %%cbr, ptr %%cbc
  %%evstr = select i1 %%isrename, ptr %s, ptr %s
  %%hascb = icmp ne ptr %%cb, null
  br i1 %%hascb, label %%docall, label %%loop
docall:
  %%fn = call ptr @__kml_str_from_cstr(ptr %%namep)
  %%fp_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 0
  %%fp = load ptr, ptr %%fp_p, align 8
  %%ep_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 1
  %%ep = load ptr, ptr %%ep_p, align 8
  call void %%fp(ptr %%ep, ptr %%evstr, ptr %%fn)
  br label %%loop
ret:
  ret void
}`, watchDesc, e.internString("watch"), sw, sw, sw, sw, sw, sw, sw, sw, sw, sw, sw, sw, sw, renameStr, changeStr))
}
