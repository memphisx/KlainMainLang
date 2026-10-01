package llvm

import (
	_ "embed"
)

// threadpool.go — TDD-00185 Stages 1-2: a libuv-style blocking-work thread pool
// that makes fs async I/O genuinely non-blocking. The threading (pthreads,
// condvar, atomics, the socketpair wakeup) lives in the embedded C runtime
// (threadpoolsrc/klainpool.c); this file owns only the two IR thunks that
// carry the Promise/exception layout the C side is deliberately kept ignorant
// of, plus the Uses*/ensure* plumbing.
//
//go:embed threadpoolsrc/klainpool.c
var threadPoolSource string

// The HTTP/1 parser the http module's TypeScript drives, compiled into the
// same unit (it uses the pool's string helpers).
//
//go:embed threadpoolsrc/klainhttp.c
var httpParserSource string

// The dns module's natives (getaddrinfo/getnameinfo and the DNS client),
// compiled into the same unit (they use the pool's items).
//
//go:embed threadpoolsrc/klaindns.c
var dnsNativeSource string

// The zlib module's natives (node_zlib.cc's contexts over libz), compiled in
// only with -DKLAINPOOL_ZLIB (ThreadPoolCFlags).
//
//go:embed threadpoolsrc/klainzlib.c
var zlibNativeSource string

// The fs module's natives (libuv's uv_fs_* requests, sync or pooled),
// compiled in only with -DKLAINPOOL_FS (ThreadPoolCFlags).
//
//go:embed threadpoolsrc/klainfs.c
var fsNativeSource string

// node:sqlite's natives (libsqlite3 behind handle ids), compiled in only
// with -DKLAINPOOL_SQLITE (ThreadPoolCFlags).
//
//go:embed threadpoolsrc/klainsqlite.c
var sqliteNativeSource string

// ThreadPoolSource returns the embedded pool C runtime, linked (with -pthread)
// whenever the program routes an fs op through the pool.
func ThreadPoolSource() string {
	return threadPoolSource + "\n" + httpParserSource + "\n" + dnsNativeSource + "\n" + zlibNativeSource + "\n" + fsNativeSource + "\n" + sqliteNativeSource
}

// UsesThreadPool reports whether any pooled async fs op was emitted, so the CLI
// driver / conformance runner know to compile and link klainpool.c.
func (e *Emitter) UsesThreadPool() bool { return e.usedThreadPool }

// ThreadPoolCFlags returns the clang flags klainpool.c is compiled with — the
// single source of truth shared by EmbeddedCSources (the CLI / conformance
// runner) and the test build harness so the two can't drift. GC_allow_register_
// threads is call-once and shared across concurrency subsystems: main's
// prologue calls it for a program with Worker modules and klain:sync's C
// calls it when linked, so the pool owns the enable (KLAINPOOL_GC_ENABLE)
// only when neither of those does.
func (e *Emitter) ThreadPoolCFlags() []string {
	cflags := []string{"-pthread"}
	if e.usedZlibNatives {
		cflags = append(cflags, "-DKLAINPOOL_ZLIB=1")
	}
	if e.usedFsNatives {
		cflags = append(cflags, "-DKLAINPOOL_FS=1")
	}
	if e.usedSqliteNatives {
		cflags = append(cflags, "-DKLAINPOOL_SQLITE=1")
	}
	if e.isGCMode() {
		cflags = append(cflags, "-DKLAINPOOL_GC=1")
		if !e.hasWorkers && !e.UsesSync() {
			cflags = append(cflags, "-DKLAINPOOL_GC_ENABLE=1")
		}
	}
	return cflags
}

// ensureThreadPool declares the pool's loop hooks exactly once. Marks
// usedThreadPool so the C source gets linked and the reactor's pool hooks
// bind to the real C definitions rather than the no-op stubs
// (runtime_task.go).
func (e *Emitter) ensureThreadPool() {
	if e.usedThreadPool {
		return
	}
	e.usedThreadPool = true

	e.ensureMicrotasks() // __kml_drain_microtasks — run after a native callback

	// klainpool.c provides these — declare them so the emitted reactor's calls
	// resolve at the IR level (the no-op stubs in runtime_task.go are emitted
	// only when the pool is unused).
	e.emitGlobal("declare i1 @__kml_pool_keepalive()")
	e.emitGlobal("declare i1 @__kml_pool_fdset_add(ptr noundef, ptr noundef)")
	e.emitGlobal("declare zeroext i1 @__kml_pool_dispatch()")
	e.emitGlobal("declare i1 @__kml_tcp_keepalive()")
	e.emitGlobal("declare i1 @__kml_tcp_fdset_add(ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare zeroext i1 @__kml_tcp_dispatch()")
}

// ensureZlibNatives compiles the zlib natives into the pool's unit and links
// libz: the `@link zlib` of lib/native.d.ts.
func (e *Emitter) ensureZlibNatives() {
	e.ensureNativePool()
	if e.usedZlibNatives {
		return
	}
	e.usedZlibNatives = true
	e.requireLink("z")
	if e.opts.Target.OS() == "linux" {
		e.requireLink("dl") // libbrotli and libzstd load with dlopen
	}
}

// ensureSqliteNatives compiles klainsqlite.c into the pool's unit and links
// libsqlite3: the `@link sqlite` of lib/native.d.ts.
func (e *Emitter) ensureSqliteNatives() {
	e.ensureNativePool()
	if e.usedSqliteNatives {
		return
	}
	e.usedSqliteNatives = true
	e.requireLink("sqlite3")
	if e.opts.Target.OS() == "linux" {
		e.requireLink("dl") // the optional entry points are looked up with dlsym
	}
}

// ensureFsNatives compiles klainfs.c into the pool (its statfs is osinfo's).
func (e *Emitter) ensureFsNatives() {
	e.ensureNativePool()
	if e.usedFsNatives {
		return
	}
	e.usedFsNatives = true
	e.ensureOSInfo()
}

// ensureCryptoNatives compiles the crypto backend (its node:crypto natives)
// with the pool, which runs their pooled forms.
func (e *Emitter) ensureCryptoNatives() {
	e.ensureNativePool()
	e.usesCrypto = true
}
