package llvm

import (
	_ "embed"
	"fmt"
	"runtime"
	"strings"
)

// CSource is one embedded C runtime file that must be compiled and linked
// alongside the emitted LLVM IR when the program uses the corresponding
// feature. Name is a short suffix (e.g. "dtoa") used to build a per-program
// temp filename; Content is the C source; CFlags/Libs are the extra clang
// flags and `-l` libraries that file needs.
type CSource struct {
	Name    string
	Content string
	CFlags  []string
	Libs    []string
	// Ext is the source-file extension clang compiles this member as, without
	// the leading dot. Empty means "c" (the overwhelming default); the one
	// C++14 member — the webview binding — sets "cc" so clang's driver picks
	// the C++ frontend. SrcExt() resolves the default.
	Ext string
}

// Standalone is the source for a clang command that compiles several
// sources together, where a -D would reach every one of them: its own
// defines open its text, and the flags left are the rest of CFlags.
func (cs CSource) Standalone() (content string, flags []string) {
	var defs strings.Builder
	for _, f := range cs.CFlags {
		if d, ok := strings.CutPrefix(f, "-D"); ok {
			name, val, hasVal := strings.Cut(d, "=")
			if !hasVal {
				val = "1"
			}
			defs.WriteString("#define " + name + " " + val + "\n")
			continue
		}
		flags = append(flags, f)
	}
	return defs.String() + cs.Content, flags
}

// NeedsMoc reports whether this source must have Qt's moc run over it (producing
// the webview_sailfish.moc it #includes) before clang compiles it — true only for
// the Sailfish webview shim (TDD-00146 Stage 3). Every writer of CSources
// (main.go's two loops, the test build path) consults this so the moc pass can
// never be forgotten in one place. Keyed off the well-known member name + the C++
// extension so it stays a pure function of the CSource, without widening the
// struct's many positional literals.
func (c CSource) NeedsMoc() bool { return c.Name == "webview_sailfish" }

// SrcExt returns the source-file extension for this member (without the dot),
// defaulting to "c" when unset — the single point every writer (main.go, the
// conformance runner, the test build path) consults so a .cc member can never
// be written as .c by accident.
func (c CSource) SrcExt() string {
	if c.Ext == "" {
		return "c"
	}
	return c.Ext
}

// EmbeddedCSources returns every embedded C runtime file this program's emitted
// IR depends on, based on which features it actually used — the single source
// of truth shared by the CLI driver (main.go) and the conformance runner, so
// the two can never drift on which .c files get linked (a drift that silently
// under-counted conformance: any test needing dtoa/bigint/JSON/etc. failed to
// link in the runner even though the CLI compiled it fine). Order matches the
// CLI's historical order. Returns an error only for an unknown backend name.
func (e *Emitter) EmbeddedCSources() ([]CSource, error) {
	if !e.cSourcesDone {
		e.cSources, e.cSourcesErr = e.embeddedCSources()
		e.cSourcesDone = true
	}
	return e.cSources, e.cSourcesErr
}

func (e *Emitter) embeddedCSources() ([]CSource, error) {
	// What the runtime reads from and calls into the program (TDD-00251).
	out := []CSource{e.hooksCSource()}
	// The files without libraries of their own: linked when the program
	// or a linked file names a symbol one defines (runtime_resolve.go).
	var pool []CSource
	var libPool []libUnit
	// The BigInt and crypto backends, each the one the options select.
	if backend := e.BigIntBackend(); true {
		src, ok := BigIntBackendSource(backend)
		if !ok {
			return nil, fmt.Errorf("bigint: unknown backend %q", backend)
		}
		libPool = append(libPool, libUnit{src: CSource{"bigint", src, nil, nil, ""}, locate: func() ([]string, []string, error) {
			cflags, libs := LocateBigInt(backend)
			return cflags, libs, nil
		}})
	}
	if backend := e.CryptoBackend(); true {
		src, ok := CryptoBackendSource(backend)
		if !ok {
			return nil, fmt.Errorf("crypto: unknown backend %q", backend)
		}
		libPool = append(libPool, libUnit{src: CSource{"crypto", src, nil, nil, ""}, locate: func() ([]string, []string, error) {
			cflags, libs := LocateCrypto(backend)
			return cflags, libs, nil
		}})
	}
	libPool = append(libPool, libUnit{src: CSource{"tlshandle", TLSHandleSource(), nil, nil, ""}, locate: func() ([]string, []string, error) {
		if b := e.CryptoBackend(); b == "commoncrypto" || b == "cng" {
			return nil, nil, fmt.Errorf("tls: the `tls` module requires the OpenSSL crypto backend")
		}
		cflags, libs := LocateTLS()
		return cflags, libs, nil
	}})
	libPool = append(libPool, libUnit{src: CSource{"h2node", H2NodeSource(), nil, nil, ""}, locate: func() ([]string, []string, error) {
		cflags, libs := LocateHTTP2()
		return cflags, libs, nil
	}})
	if e.UsesFFIDl() && e.opts.Target.OS() == "windows" {
		// Windows has no libdl; supply dlopen/dlsym/dlclose/dlerror over
		// LoadLibrary/GetProcAddress so node:ffi links (runtime_ffi.go).
		out = append(out, CSource{"ffiwindl", FFIWinDlShimSource(), nil, nil, ""})
	}
	if e.UsesFFIRegistry() {
		// node:ffi's runtime library registry + its std::unordered_map-ordered
		// tables (TDD-00229).
		out = append(out, e.FFIRegistrySources()...)
	}
	pool = append(pool, CSource{"ipc", IPCSource(), nil, nil, ""})
	pool = append(pool, CSource{"bufcodecs", BufferCodecsSource(), nil, nil, ""})
	if e.usesDynamicImport && (e.opts.DynamicImport == "isolated" || e.opts.DynamicImport == "lazy") {
		// The dlopen loader (TDD-00056, TDD-00238 Stage 5). Linux resolves dlopen from
		// libdl (-ldl); macOS ships it in libSystem, so no extra lib there.
		var libs []string
		if e.opts.Target.OS() != "darwin" {
			libs = []string{"-ldl"}
			if e.opts.Target.OS() == "windows" {
				libs = nil // LoadLibrary lives in kernel32; there is no libdl
			}
		}
		out = append(out, CSource{"dynimport", DynImportShimSource(), nil, libs, ""})
	}
	pool = append(pool, CSource{"jsontree", JSONParseTreeSource(), nil, nil, ""})
	if e.UsesPathWin32() {
		out = append(out, CSource{"pathwin32", PathWin32Source(), nil, nil, ""})
	}
	pool = append(pool, CSource{"dynjson", DynJSONSource(), []string{fmt.Sprintf("-DKML_ARRAY_HDR_WORD=%dLL", e.arrayHeaderWord()), fmt.Sprintf("-DKML_ARGS_HDR_WORD=%dLL", e.argumentsHeaderWord())}, nil, ""})
	// Every runtime unit that switches fibers or unwinds keeps the shadow
	// call stack's thread-locals (kml_heap.h), so it always links.
	out = append(out, CSource{"frames", FramesSource(), nil, nil, ""})
	pool = append(pool, CSource{"jmpstack", JmpStackSource(), nil, nil, ""})
	pool = append(pool, CSource{"stackguard", StackGuardSource(), nil, nil, ""})
	pool = append(pool, CSource{"microtask", MicrotaskSource(), nil, nil, ""})
	pool = append(pool, CSource{"promise", PromiseSource(), nil, nil, ""})
	pool = append(pool, CSource{"task", TaskSource(), nil, nil, ""})
	pool = append(pool, CSource{"core", CoreSource(), nil, LibmLibs(), ""})
	pool = append(pool, CSource{"jsonrt", JSONRuntimeSource(), nil, nil, ""})
	pool = append(pool, CSource{"strheader", StrHeaderSource(), nil, nil, ""})
	pool = append(pool, CSource{"request", RequestSource(), nil, nil, ""})
	pool = append(pool, CSource{"strings", StringsCSource(), nil, nil, ""})
	pool = append(pool, CSource{"regexp", RegexpCSource(), nil, nil, ""})
	pool = append(pool, CSource{"dynobj", DynObjSource(), nil, nil, ""})
	pool = append(pool, CSource{"dynarr", DynArrSource(), nil, nil, ""})
	pool = append(pool, CSource{"dynentries", DynEntriesSource(), nil, nil, ""})
	pool = append(pool, CSource{"dynjsonnode", DynJSONNodeSource(), nil, nil, ""})
	pool = append(pool, CSource{"date", DateSource(), nil, LibmLibs(), ""})
	pool = append(pool, CSource{"encoding", EncodingSource(), nil, nil, ""})
	pool = append(pool, CSource{"path", PathSource(), nil, nil, ""})
	pool = append(pool, CSource{"weak", e.WeakSource(), nil, nil, ""})
	pool = append(pool, CSource{"finalization", e.FinalizationSource(), nil, nil, ""})
	pool = append(pool, CSource{"atomics", AtomicsSource(), nil, nil, ""})
	pool = append(pool, CSource{"process", ProcessSource(), e.ProcessCFlags(), nil, ""})
	pool = append(pool, CSource{"asynchooks", AsyncHooksSource(), nil, nil, ""})
	pool = append(pool, CSource{"views", e.ViewsSource(), nil, nil, ""})
	pool = append(pool, CSource{"cryptorand", CryptoRandSource(), e.CryptoRandCFlags(), nil, ""})
	pool = append(pool, CSource{"errno", ErrnoSource(), nil, nil, ""})
	pool = append(pool, CSource{"fserror", FsErrorSource(), nil, nil, ""})
	pool = append(pool, CSource{"fswatch", FsWatchSource(), nil, nil, ""})
	pool = append(pool, CSource{"streams", StreamsSource(), nil, nil, ""})
	libPool = append(libPool, libUnit{src: CSource{"zlibstream", ZlibStreamSource(), nil, nil, ""}, links: []string{"z"}})
	// libcurl: the link brings -lcurl.
	libPool = append(libPool, libUnit{src: CSource{"fetch", FetchSource(), e.FetchCFlags(), nil, ""}, links: []string{"curl"}})
	pool = append(pool, CSource{"promise_adopt", PromiseAdoptSource(), nil, nil, ""})
	pool = append(pool, CSource{"fetchslot", FetchSlotSource(), nil, nil, ""})
	pool = append(pool, CSource{"exceptions", ExceptionsSource(), e.ExceptionsCFlags(), nil, ""})
	pool = append(pool, CSource{"worker", WorkerSource(), nil, nil, ""})
	pool = append(pool, CSource{"ipcc", IPCChildSource(), nil, nil, ""})
	// Timers are uv_timer_t handles and the loop is libuv's: both locate it.
	uvTarget, uvCross := e.opts.Target.OS(), e.opts.Target.Triple != ""
	libPool = append(libPool, libUnit{src: CSource{"timers", TimersSource(), nil, nil, ""}, locate: func() ([]string, []string, error) {
		cflags, _ := LocateUV(uvTarget, uvCross)
		return cflags, nil, nil
	}})
	libPool = append(libPool, libUnit{src: CSource{"loop", LoopSource(), e.loopCFlags, nil, ""}, locate: func() ([]string, []string, error) {
		cflags, libs := LocateUV(uvTarget, uvCross)
		return cflags, libs, nil
	}})
	pool = append(pool, CSource{"native", NativeSource(), e.NativeCFlags(), nil, ""})
	pool = append(pool, CSource{"entrypath", entryPathSource, nil, nil, ""})
	pool = append(pool, CSource{"dynwhen", DynWhenSource(), nil, nil, ""})
	if e.usedIslandLoop {
		// The host reaches an island's entry points through dlsym: nothing
		// in the island names them.
		out = append(out, CSource{"island", islandSource, nil, nil, ""})
	}
	pool = append(pool, CSource{"dynimportwatch", DynImportWatchSource(), nil, nil, ""})
	pool = append(pool, CSource{"promise_settle", PromiseSettleSource(), nil, nil, ""})
	pool = append(pool, CSource{"promise_defer", PromiseDeferSource(), nil, nil, ""})
	pool = append(pool, CSource{"promise_finally", PromiseFinallySource(), nil, nil, ""})
	pool = append(pool, CSource{"anyprom", AnyPromSource(), nil, nil, ""})
	pool = append(pool, CSource{"fetchdrive", FetchDriveSource(), nil, nil, ""})
	pool = append(pool, CSource{"fetchbodyprom", FetchBodyPromSource(), nil, nil, ""})
	pool = append(pool, CSource{"module_task", ModuleTaskSource(), nil, nil, ""})
	pool = append(pool, CSource{"bigintbox", BigIntBoxSource(), nil, nil, ""})
	pool = append(pool, CSource{"bigintrel", BigIntRelSource(), nil, nil, ""})
	pool = append(pool, CSource{"toprim", ToPrimitiveSource(), nil, nil, ""})
	pool = append(pool, CSource{"stricteq", StrictEqSource(), nil, nil, ""})
	pool = append(pool, CSource{"looseeq", LooseEqSource(), nil, nil, ""})
	pool = append(pool, CSource{"hostbox", HostBoxSource(), nil, nil, ""})
	pool = append(pool, CSource{"dynconstruct", DynConstructSource(), nil, nil, ""})
	pool = append(pool, CSource{"errkeys", ErrKeysSource(), nil, nil, ""})
	pool = append(pool, CSource{"arrayguard", ArrayGuardSource(), nil, nil, ""})
	pool = append(pool, CSource{"collections", CollectionsSource(), nil, nil, ""})
	pool = append(pool, CSource{"collections_any", CollectionsAnySource(), nil, nil, ""})
	pool = append(pool, CSource{"unitreg", UnitRegSource(), nil, nil, ""})
	pool = append(pool, CSource{"shape", ShapeSource(), e.ShapeCFlags(), nil, ""})
	// Linked only when the program names __kml_obj_extra_on: in place of
	// hooks.c's defaults, which a library unit never replaces on its own.
	libPool = append(libPool, libUnit{src: CSource{"objextra", ObjExtraSource(), nil, nil, ""}})
	libPool = append(libPool, libUnit{src: CSource{"promisebox", PromiseBoxSource(), nil, nil, ""}})
	// Unicode case mapping for toUpperCase/toLowerCase. Tables + code,
	// libc only.
	pool = append(pool, CSource{"casemap", CasemapSource(), nil, LibmLibs(), ""})
	pool = append(pool, CSource{"collate", CollateSource(), nil, nil, ""})
	pool = append(pool, CSource{"string", StringSource(), []string{fmt.Sprintf("-DKML_ARRAY_HDR_WORD=%dLL", e.arrayHeaderWord())}, LibmLibs(), ""})
	pool = append(pool, CSource{"number", NumberSource(), nil, LibmLibs(), ""})
	pool = append(pool, CSource{"fnmeta", FnMetaSource(), e.FnMetaCFlags(), nil, ""})
	pool = append(pool, CSource{"inspect", InspectReduceSource(), nil, nil, ""})
	// os.type/release/version/machine/uptime/loadavg/userInfo/
	// availableParallelism/networkInterfaces + process.env enumeration.
	libPool = append(libPool, libUnit{src: CSource{"osinfo", OSInfoSource(), nil, e.OSInfoLibs(), ""}})
	pool = append(pool, CSource{"osinfobags", OSInfoBagsSource(), nil, nil, ""})
	pool = append(pool, CSource{"dtoa", DtoaSource(), nil, nil, ""})
	if e.UsesWebview() {
		cflags, libs, err := e.LocateWebview(e.WebviewBackend())
		if err != nil {
			return nil, err
		}
		if runtime.GOOS == "windows" && staticLinkMode {
			// --static: precompile the amalgamation with g++ (COMDAT-compatible with the
			// static libstdc++.a) and link the object, so the desktop .exe is
			// self-contained — no libstdc++/libwinpthread DLLs beside it (ADR-00772).
			obj, oerr := windowsWebviewObject()
			if oerr != nil {
				return nil, oerr
			}
			winLibs := append([]string{obj}, libs...)
			out = append(out, CSource{"webview", "// webview linked as a prebuilt g++ object (see webview_win32.go).\n", nil, winLibs, "cc"})
		} else {
			// Default / POSIX: the amalgamation compiles on the shared clang line and
			// links the dynamic C++ runtime. The Sailfish backend is a Qt source
			// under a distinct member name so CSource.NeedsMoc() flags the moc pass
			// the shim needs before clang (TDD-00146 Stage 3).
			name := "webview"
			if e.WebviewBackend() == "sailfish" {
				name = "webview_sailfish"
			}
			out = append(out, CSource{name, WebviewSource(e.WebviewBackend()), cflags, libs, "cc"})
		}
		out = append(out, CSource{"wvnatives", webviewNatives, nil, nil, ""})
	}
	// process.memoryUsage() heapTotal/heapUsed. Under -mm=gc it reads the
	// collector's heap through the GC interface. No extra libs —
	// malloc.h/malloc/malloc.h live in libc.
	pool = append(pool, CSource{"procmem", ProcMemSource(), nil, nil, ""})
	// TDD-00031: termios/ioctl/raw-read shim. No extra libs — termios and
	// ioctl live in libc on both platforms.
	pool = append(pool, CSource{"tty", TTYShimSource(), nil, nil, ""})
	if e.UsesTui() {
		// TDD-00150 Stage 1: the klain:tui painter runtime (tui.c) plus the
		// vendored Yoga flexbox engine, which is pre-compiled to objects and
		// linked in — see yogaCSources for why (C++20 can't share the C line).
		tui, err := yogaCSources(e.Toolchain())
		if err != nil {
			return nil, err
		}
		out = append(out, tui...)
	}
	if e.UsesSync() {
		// TDD-00143: the klain:sync GMP goroutine runtime. Needs pthread; the
		// macOS ucontext calls are deprecated-but-functional (silence the
		// warning). Under -mm=gc the runtime registers each M thread and each
		// goroutine stack with the collector (KML_GC); the -lgc link is
		// already added globally in gc mode (LocateGC).
		cflags := []string{"-Wno-deprecated-declarations"}
		var libs []string
		if runtime.GOOS == "windows" && staticLinkMode {
			// --static: -pthread would link libwinpthread-1.dll dynamically (it rides
			// the shared compile+link clang line); link the static archive instead so a
			// goroutine program is self-contained (ADR-00772). mingw's pthread.h is
			// thread-safe without -pthread, so compiling without it is fine.
			libs = WorkerPthreadLinkFlags()
		} else {
			cflags = append(cflags, "-pthread") // dynamic winpthread (default / POSIX)
		}
		out = append(out, CSource{"klainsync", SyncSource(), cflags, libs, ""})
	}
	if e.UsesThreadPool() {
		// TDD-00185: the blocking-work thread pool for real async fs I/O. Needs
		// pthread; under -mm=gc each worker registers with Boehm before its first
		// allocation (KML_GC), matching the Worker/klain:sync threads.
		// --static on Windows links the static winpthread archive instead of
		// -pthread's DLL, as klain:sync does above (ADR-00772).
		if runtime.GOOS == "windows" && staticLinkMode {
			var cflags []string
			for _, f := range e.ThreadPoolCFlags() {
				if f != "-pthread" {
					cflags = append(cflags, f)
				}
			}
			out = append(out, CSource{"threadpool", ThreadPoolSource(), cflags, WorkerPthreadLinkFlags(), ""})
		} else {
			var poolLibs []string
			if e.usedWasmNatives {
				_, poolLibs = LocateWasm()
			}
			out = append(out, CSource{"threadpool", ThreadPoolSource(), e.ThreadPoolCFlags(), poolLibs, ""})
		}
	}
	var caresLinks []string
	if e.opts.Target.OS() == "windows" {
		caresLinks = []string{"cares", "iphlpapi", "ws2_32"}
	}
	libPool = append(libPool, libUnit{src: CSource{"cares", CaresSource(), nil, nil, ""}, locate: func() ([]string, []string, error) {
		cflags, libs := e.CaresFlags()
		return cflags, libs, nil
	}, links: caresLinks})
	libPool = append(libPool, libUnit{src: CSource{"h2o", H2OSource(), nil, nil, ""}, locate: func() ([]string, []string, error) {
		if _, _, ok := LocateH2O(); !ok && e.opts.Target.Triple == "" {
			return nil, nil, fmt.Errorf("h2o: libh2o is not installed")
		}
		cflags, libs := e.H2OFlags()
		return cflags, libs, nil
	}})
	if e.UsesEmbeddedAssets() {
		// The embedded static server needs pthread; -pthread is already added
		// by the CLI when workers are used, but a serve-only program needs it too.
		if runtime.GOOS == "windows" && staticLinkMode {
			out = append(out, CSource{"embedassets", EmbedAssetsSource(), nil, WorkerPthreadLinkFlags(), ""})
		} else {
			out = append(out, CSource{"embedassets", EmbedAssetsSource(), []string{"-pthread"}, nil, ""})
		}
	}
	pool = append(pool, CSource{"fdlibm", fdlibmSource, nil, LibmLibs(), ""})
	pool = append(pool, CSource{"buffill", bufFillSource, nil, LibmLibs(), ""})
	pool = append(pool, CSource{"keyorder", keyOrderSource, nil, nil, ""})
	pool = append(pool, CSource{"propattr", propAttrSource, nil, nil, ""})
	pool = append(pool, CSource{"holes", holesSource, nil, nil, ""})
	pool = append(pool, CSource{"wrapper", wrapperSource, nil, nil, ""})
	// Every member allocates program values through the managed-heap
	// interface (TDD-00230 P3.3a), reads a header word's object state
	// through the same constants, and sees -mm=gc as KML_GC.
	prelude := kmlHeapHeader + hdrStateDefines()
	if e.isGCMode() {
		prelude = "#define KML_GC 1\n" + prelude
	}
	for i := range out {
		out[i].Content = prelude + out[i].Content
	}
	for i := range pool {
		pool[i].Content = prelude + pool[i].Content
	}
	for i := range libPool {
		libPool[i].src.Content = prelude + libPool[i].src.Content
	}
	picked, libs, err := resolveRuntime(e.Toolchain(), e.finalIR, out, pool, libPool)
	if err != nil {
		return nil, err
	}
	for _, u := range libs {
		for _, lib := range u.links {
			e.requireLink(lib)
		}
	}
	return append(out, picked...), nil
}

//go:embed kmlheapsrc/kml_heap.h
var kmlHeapHeader string

// LibmLibs is the link flag a C runtime file calling libm (trunc, floor, …)
// needs: glibc and mingw keep the math functions in libm; macOS has them in
// libSystem, which is always linked.
func LibmLibs() []string {
	if runtime.GOOS == "darwin" {
		return nil
	}
	return []string{"-lm"}
}
