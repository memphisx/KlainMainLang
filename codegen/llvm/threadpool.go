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
//go:embed threadpoolsrc/kml_pool.h
var poolHeader string

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
// always compiled into the pool; the link drops them when unused.
//
//go:embed threadpoolsrc/klainfs.c
var fsNativeSource string

// node:sqlite's natives (libsqlite3 behind handle ids), compiled in only
// with -DKLAINPOOL_SQLITE (ThreadPoolCFlags).
//
//go:embed threadpoolsrc/klainsqlite.c
var sqliteNativeSource string

// klain:http's cluster natives (the fork, the shared close flags).
//
//go:embed threadpoolsrc/klainkhttp.c
var khttpNativeSource string

// ThreadPoolSource returns the embedded pool C runtime, linked (with -pthread)
// whenever the program routes an fs op through the pool.
func ThreadPoolSource() string {
	return "#define KML_POOL_SELF 1\n" + loopUser + poolHeader + threadPoolSource + "\n" + httpParserSource + "\n" + dnsNativeSource + "\n" + fsNativeSource + "\n" + khttpNativeSource
}

// UsesThreadPool reports whether any pooled async fs op was emitted, so the CLI
// driver / conformance runner know to compile and link klainpool.c.

// ThreadPoolCFlags returns the clang flags klainpool.c is compiled with — the
// single source of truth shared by EmbeddedCSources (the CLI / conformance
// runner) and the test build harness so the two can't drift. GC_allow_register_
// threads is call-once and shared across concurrency subsystems: main's
// prologue calls it for a program with Worker modules and klain:sync's C
// calls it when linked, so the pool owns the enable (KLAINPOOL_GC_ENABLE)
// only when neither of those does.
func (e *Emitter) ThreadPoolCFlags() []string {
	// The process handle is libuv's uv_process_t (TDD-00244 Stage 4): its
	// unit locates libuv's headers.
	cflags := []string{"-pthread"}
	cflags = append(cflags, DeadStripFlags(e.exportsRuntime())...)
	if e.isGCMode() && !e.hasWorkers && !e.UsesSync() {
		cflags = append(cflags, "-DKLAINPOOL_GC_ENABLE=1")
	}
	return cflags
}

// ensureCryptoNatives compiles the crypto backend (its node:crypto natives)
// with the pool, which runs their pooled forms.
func (e *Emitter) ensureCryptoNatives() {
	e.usesCrypto = true
}

// poolUnits are the pool's natives over a library of their own, each a unit
// linked by the symbols the program names: zlib (with Brotli and Zstd),
// sqlite, and WebAssembly over wasmtime.
func (e *Emitter) poolUnits() []libUnit {
	os := e.opts.Target.OS()
	unit := func(name, src string, cflags []string) CSource {
		return CSource{name, poolHeader + src, append([]string{"-pthread"}, cflags...), nil, ""}
	}
	zcflags, zlinks := []string{"-DKLAINPOOL_ZLIB=1"}, []string{"z"}
	if winSelfContained(os) {
		// A self-contained build links what is otherwise loaded at run time
		// (TDD-00253); the encoder and decoder before their common part.
		zcflags = append(zcflags, "-DKZ_STATIC=1")
		zlinks = append(zlinks, "brotlienc", "brotlidec", "brotlicommon", "zstd")
	}
	slinks := []string{"sqlite3"}
	if os == "linux" {
		zlinks = append(zlinks, "dl") // libbrotli and libzstd load with dlopen
		slinks = append(slinks, "dl") // the optional entry points are looked up with dlsym
	}
	return []libUnit{
		{src: unit("zlibnat", zlibNativeSource, zcflags), links: zlinks},
		{src: unit("sqlite", sqliteNativeSource, []string{"-DKLAINPOOL_SQLITE=1"}), links: slinks},
		{src: unit("wasm", wasmBackendSource+"\n"+wasmNativeSource, []string{"-DKLAINPOOL_WASM=1"}), locate: func() ([]string, []string, error) {
			cflags, libs := LocateWasm()
			return cflags, libs, nil
		}},
	}
}
