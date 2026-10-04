package llvm

import (
	"fmt"
	"runtime"
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
	var out []CSource
	if e.UsesBigInt() {
		backend := e.BigIntBackend()
		src, ok := BigIntBackendSource(backend)
		if !ok {
			return nil, fmt.Errorf("bigint: unknown backend %q", backend)
		}
		cflags, libs := LocateBigInt(backend)
		out = append(out, CSource{"bigint", src, cflags, libs, ""})
	}
	if e.UsesCrypto() {
		backend := e.CryptoBackend()
		src, ok := CryptoBackendSource(backend)
		if !ok {
			return nil, fmt.Errorf("crypto: unknown backend %q", backend)
		}
		cflags, libs := LocateCrypto(backend)
		out = append(out, CSource{"crypto", src, cflags, libs, ""})
	}
	if e.UsesTLS() {
		if e.CryptoBackend() == "commoncrypto" {
			return nil, fmt.Errorf("tls: the `tls` module requires the OpenSSL crypto backend")
		}
		cflags, libs := LocateTLS()
		out = append(out, CSource{"tls", TLSClientSource(), cflags, libs, ""})
	}
	if e.UsesTLSHandles() {
		if e.CryptoBackend() == "commoncrypto" {
			return nil, fmt.Errorf("tls: the `tls` module requires the OpenSSL crypto backend")
		}
		cflags, libs := LocateTLS()
		out = append(out, CSource{"tlshandle", TLSHandleSource(), cflags, libs, ""})
	}
	if e.UsesHTTP2() {
		cflags, libs := LocateHTTP2()
		out = append(out, CSource{"http2", HTTP2ServerSource(), cflags, libs, ""})
	}
	if e.UsesH2Node() {
		cflags, libs := LocateHTTP2()
		out = append(out, CSource{"h2node", H2NodeSource(), cflags, libs, ""})
	}
	if e.UsesSpawnSync() {
		out = append(out, CSource{"spawnsync", SpawnSyncSource(), nil, nil, ""})
	}
	if e.UsesReexecGuard() {
		out = append(out, CSource{"reexecguard", ReexecGuardSource(), nil, nil, ""})
	}
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
	if e.UsesIPC() {
		out = append(out, CSource{"ipc", IPCSource(), nil, nil, ""})
	}
	if e.UsesBufferCodecs() {
		out = append(out, CSource{"bufcodecs", BufferCodecsSource(), nil, nil, ""})
	}
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
	if e.UsesJSONParse() {
		out = append(out, CSource{"jsontree", JSONParseTreeSource(), nil, nil, ""})
	}
	if e.UsesPathWin32() {
		out = append(out, CSource{"pathwin32", PathWin32Source(), nil, nil, ""})
	}
	if e.UsesDynJSON() {
		out = append(out, CSource{"dynjson", DynJSONSource(), e.dynJSONViewFlags(), nil, ""})
	}
	if e.usedJmpStack {
		out = append(out, CSource{"jmpstack", JmpStackSource(), nil, nil, ""})
	}
	if e.UsesMicrotasks() {
		out = append(out, CSource{"microtask", MicrotaskSource(), nil, nil, ""})
	}
	if e.usedPromiseRuntime {
		out = append(out, CSource{"promise", PromiseSource(), nil, nil, ""})
	}
	if e.usedTaskRuntime {
		out = append(out, CSource{"task", TaskSource(), e.TaskCFlags(), nil, ""})
	}
	if e.UsesCoreC() {
		out = append(out, CSource{"core", CoreSource(), nil, LibmLibs(), ""})
	}
	if e.UsesJSONRuntime() {
		out = append(out, CSource{"jsonrt", JSONRuntimeSource(), nil, nil, ""})
	}
	if e.UsesStrHeaderC() {
		out = append(out, CSource{"strheader", StrHeaderSource(), nil, nil, ""})
	}
	if e.UsesStringsC() {
		out = append(out, CSource{"strings", StringsCSource(), nil, nil, ""})
	}
	if e.UsesRegexpC() {
		out = append(out, CSource{"regexp", RegexpCSource(), nil, nil, ""})
	}
	if e.UsesDynObj() {
		out = append(out, CSource{"dynobj", DynObjSource(), nil, nil, ""})
	}
	if e.UsesDynArr() {
		out = append(out, CSource{"dynarr", DynArrSource(), nil, nil, ""})
	}
	if e.UsesDynJSONNode() {
		out = append(out, CSource{"dynjsonnode", DynJSONNodeSource(), nil, nil, ""})
	}
	if e.UsesDate() {
		out = append(out, CSource{"date", DateSource(), nil, nil, ""})
	}
	if e.UsesEncoding() {
		out = append(out, CSource{"encoding", EncodingSource(), nil, nil, ""})
	}
	if e.UsesPathNormalize() {
		out = append(out, CSource{"path", PathSource(), nil, nil, ""})
	}
	if e.UsesWeakHelpers() {
		out = append(out, CSource{"weak", e.WeakSource(), nil, nil, ""})
	}
	if e.UsesFinRegHelpers() {
		out = append(out, CSource{"finalization", e.FinalizationSource(), nil, nil, ""})
	}
	if e.UsesAtomicsRuntime() {
		out = append(out, CSource{"atomics", AtomicsSource(), nil, nil, ""})
	}
	if e.UsesProcessRuntime() {
		out = append(out, CSource{"process", ProcessSource(), e.ProcessCFlags(), nil, ""})
	}
	if e.UsesAsyncHooks() {
		out = append(out, CSource{"asynchooks", AsyncHooksSource(), e.AsyncHooksCFlags(), nil, ""})
	}
	if e.UsesViews() {
		out = append(out, CSource{"views", e.ViewsSource(), e.dynJSONViewFlags(), nil, ""})
	}
	if e.UsesCryptoRand() {
		out = append(out, CSource{"cryptorand", CryptoRandSource(), e.CryptoRandCFlags(), nil, ""})
	}
	if e.UsesErrno() {
		out = append(out, CSource{"errno", ErrnoSource(), nil, nil, ""})
	}
	if e.UsesFsError() {
		out = append(out, CSource{"fserror", FsErrorSource(), nil, nil, ""})
	}
	if e.UsesFsWatch() {
		out = append(out, CSource{"fswatch", FsWatchSource(), nil, nil, ""})
	}
	if e.UsesStreams() {
		out = append(out, CSource{"streams", StreamsSource(), nil, nil, ""})
	}
	if e.UsesZlibStream() {
		out = append(out, CSource{"zlibstream", ZlibStreamSource(), nil, nil, ""})
	}
	if e.UsesChildProcRuntime() {
		out = append(out, CSource{"childproc", ChildProcSource(), nil, nil, ""})
	}
	if e.UsesFetch() {
		out = append(out, CSource{"fetch", FetchSource(), e.FetchCFlags(), nil, ""})
	}
	if e.UsesPromiseAdopt() {
		out = append(out, CSource{"promise_adopt", PromiseAdoptSource(), nil, nil, ""})
	}
	if e.UsesFetchSlotToPromise() {
		out = append(out, CSource{"fetchslot", FetchSlotSource(), nil, nil, ""})
	}
	if e.UsesExceptions() {
		out = append(out, CSource{"exceptions", ExceptionsSource(), e.ExceptionsCFlags(), nil, ""})
	}
	if e.UsesWorkers() {
		out = append(out, CSource{"worker", WorkerSource(), e.WorkerCFlags(), nil, ""})
	}
	if e.UsesCPIPC() {
		out = append(out, CSource{"cpipc", CPIPCSource(), nil, nil, ""})
	}
	if e.UsesIPCChild() {
		out = append(out, CSource{"ipcc", IPCChildSource(), nil, nil, ""})
	}
	if e.UsesNetSockIO() {
		out = append(out, CSource{"netsock", NetSockSource(), nil, nil, ""})
	}
	if e.UsesNet() {
		out = append(out, CSource{"net", NetSource(), nil, nil, ""})
	}
	if e.UsesTimers() {
		out = append(out, CSource{"timers", TimersSource(), nil, nil, ""})
	}
	if e.UsesNative() {
		out = append(out, CSource{"native", NativeSource(), e.NativeCFlags(), nil, ""})
	}
	if e.UsesDynWhen() {
		out = append(out, CSource{"dynwhen", DynWhenSource(), nil, nil, ""})
	}
	if e.UsesDynImportWatch() {
		out = append(out, CSource{"dynimportwatch", DynImportWatchSource(), nil, nil, ""})
	}
	if e.UsesPromiseSettle() {
		out = append(out, CSource{"promise_settle", PromiseSettleSource(), nil, nil, ""})
	}
	if e.UsesPromiseDefer() {
		out = append(out, CSource{"promise_defer", PromiseDeferSource(), nil, nil, ""})
	}
	if e.UsesPromiseFinally() {
		out = append(out, CSource{"promise_finally", PromiseFinallySource(), nil, nil, ""})
	}
	if e.UsesAnyProm() {
		out = append(out, CSource{"anyprom", AnyPromSource(), nil, nil, ""})
	}
	if e.UsesFetchDrive() {
		out = append(out, CSource{"fetchdrive", FetchDriveSource(), nil, nil, ""})
	}
	if e.UsesFetchBodyProm() {
		out = append(out, CSource{"fetchbodyprom", FetchBodyPromSource(), nil, nil, ""})
	}
	if e.UsesModuleTask() {
		out = append(out, CSource{"module_task", ModuleTaskSource(), e.ModuleTaskCFlags(), nil, ""})
	}
	if e.UsesBigIntBoxC() {
		out = append(out, CSource{"bigintbox", BigIntBoxSource(), nil, nil, ""})
	}
	if e.UsesBigIntRelC() {
		out = append(out, CSource{"bigintrel", BigIntRelSource(), nil, nil, ""})
	}
	if e.UsesToPrimitiveC() {
		out = append(out, CSource{"toprim", ToPrimitiveSource(), nil, nil, ""})
	}
	if e.UsesLooseEqC() {
		out = append(out, CSource{"looseeq", LooseEqSource(), nil, nil, ""})
	}
	if e.UsesHostBoxC() {
		out = append(out, CSource{"hostbox", HostBoxSource(), nil, nil, ""})
	}
	if e.UsesErrKeysC() {
		out = append(out, CSource{"errkeys", ErrKeysSource(), nil, nil, ""})
	}
	if e.UsesArrayGuard() {
		out = append(out, CSource{"arrayguard", ArrayGuardSource(), nil, nil, ""})
	}
	if e.UsesCollections() {
		out = append(out, CSource{"collections", CollectionsSource(e.isGCMode()), nil, nil, ""})
	}
	if e.UsesCollectionsAny() {
		out = append(out, CSource{"collections_any", CollectionsAnySource(), nil, nil, ""})
	}
	if e.UsesUnitReg() {
		out = append(out, CSource{"unitreg", UnitRegSource(), nil, nil, ""})
	}
	if e.UsesShapes() {
		out = append(out, CSource{"shape", ShapeSource(), e.ShapeCFlags(), nil, ""})
	}
	if e.UsesCasemap() {
		// Unicode case mapping for toUpperCase/toLowerCase. Tables + code,
		// libc only.
		out = append(out, CSource{"casemap", CasemapSource(), nil, LibmLibs(), ""})
	}
	if e.UsesStringC() {
		out = append(out, CSource{"string", StringSource(), nil, LibmLibs(), ""})
	}
	if e.UsesNumberC() {
		out = append(out, CSource{"number", NumberSource(), nil, LibmLibs(), ""})
	}
	if e.UsesFnMeta() {
		out = append(out, CSource{"fnmeta", FnMetaSource(), e.FnMetaCFlags(), nil, ""})
	}
	if e.UsesInspectReduce() {
		out = append(out, CSource{"inspect", InspectReduceSource(), nil, nil, ""})
	}
	if e.UsesOSInfo() {
		// os.type/release/version/machine/uptime/loadavg/userInfo/
		// availableParallelism/networkInterfaces + process.env enumeration.
		out = append(out, CSource{"osinfo", OSInfoSource(), nil, e.OSInfoLibs(), ""})
	}
	if e.UsesFloatFmt() {
		out = append(out, CSource{"dtoa", DtoaSource(), nil, nil, ""})
	}
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
	}
	if e.UsesHeapStats() {
		// process.memoryUsage() heapTotal/heapUsed. Under -mm=gc it reads Boehm's
		// heap (gc.h, on the global include path LocateGC adds); -DKLAIN_GC selects
		// that branch. No extra libs — malloc.h/malloc/malloc.h live in libc, and
		// -lgc is already linked globally in gc mode.
		var cflags []string
		if e.isGCMode() {
			cflags = []string{"-DKLAIN_GC=1"}
		}
		out = append(out, CSource{"procmem", ProcMemSource(), cflags, nil, ""})
	}
	if e.UsesTtyShim() {
		// TDD-00031: termios/ioctl/raw-read shim. No extra libs — termios and
		// ioctl live in libc on both platforms.
		out = append(out, CSource{"tty", TTYShimSource(), nil, nil, ""})
	}
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
		// goroutine stack with Boehm — gated by KLAINSYNC_GC; the -lgc link is
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
		if e.isGCMode() {
			cflags = append(cflags, "-DKLAINSYNC_GC=1")
		}
		out = append(out, CSource{"klainsync", SyncSource(), cflags, libs, ""})
	}
	if e.UsesThreadPool() {
		// TDD-00185: the blocking-work thread pool for real async fs I/O. Needs
		// pthread; under -mm=gc each worker registers with Boehm before its first
		// allocation (KLAINPOOL_GC), matching the Worker/klain:sync threads.
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
			out = append(out, CSource{"threadpool", ThreadPoolSource(), e.ThreadPoolCFlags(), nil, ""})
		}
	}
	if e.UsesEmbeddedAssets() {
		// The embedded static server needs pthread; -pthread is already added
		// by the CLI when workers are used, but a serve-only program needs it too.
		if runtime.GOOS == "windows" && staticLinkMode {
			out = append(out, CSource{"embedassets", EmbedAssetsSource(), nil, WorkerPthreadLinkFlags(), ""})
		} else {
			out = append(out, CSource{"embedassets", EmbedAssetsSource(), []string{"-pthread"}, nil, ""})
		}
	}
	return out, nil
}

// LibmLibs is the link flag a C runtime file calling libm (trunc, floor, …)
// needs: glibc and mingw keep the math functions in libm; macOS has them in
// libSystem, which is always linked.
func LibmLibs() []string {
	if runtime.GOOS == "darwin" {
		return nil
	}
	return []string{"-lm"}
}
