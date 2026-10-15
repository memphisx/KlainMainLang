package llvm

// ensureRuntimeDeps links the runtime files another linked runtime file
// calls (TDD-00251 A). A runtime object may not name a symbol its program
// does not define: a COFF link resolves every object's references before it
// collects sections, so the unreached call still fails there. It runs until
// nothing changes, since a dependency may bring its own.
func (e *Emitter) ensureRuntimeDeps() {
	for {
		changed := false
		need := func(user bool, linked bool, ensure func()) {
			if user && !linked {
				ensure()
				changed = true
			}
		}
		// process.c's memory query reads the heap shim (ProcMemSource).
		need(e.UsesProcessRuntime(), e.usedHeapStats, e.ensureNativeProcessMemory)
		// exceptions.c built for Workers (KML_WORKERS) reports through the
		// Worker runtime's @__kml_worker_uncaught.
		need(e.usedExceptionHelpers && e.hasWorkers, e.usedWorkerRuntime, e.ensureWorkerRuntime)
		// fserror.c on Windows resolves the paths it reports with
		// path_win32.c (C only; nothing to declare in the IR).
		need(e.usedFsThrow && e.opts.Target.OS() == "windows", e.usesPathWin32, func() { e.usesPathWin32 = true })
		// And the pool's recursive mkdir on Windows returns its first
		// directory namespaced through it.
		need(e.usedThreadPool && e.opts.Target.OS() == "windows", e.usesPathWin32, func() { e.usesPathWin32 = true })
		// native.c holds __kml_native_fs_error beside the errno helpers; it
		// builds the error through the fs runtime's __kml_fs_error_new.
		need(e.UsesNative(), e.usedFsThrow, e.ensureFsThrow)
		// shape.c __kml_own_symbols_checked reads dynarr.c arrays.
		need(e.usedShapeRuntime, e.usedDynArr, e.ensureDynArr)
		// The pool's fs natives (klainfs.c) take statfs from osinfo.c.
		need(e.usedThreadPool, e.usedOSInfo, e.ensureOSInfo)
		// fetch.c holds the async path too, which reads its multi handle,
		// the abort signal, the fiber globals and the event loop's
		// wait-outside abort (ensureFetchAsync), and the monotonic clock
		// (timers.c).
		need(e.usedFetch, e.usedFetchAsync, e.ensureFetchAsync)
		need(e.UsesFetch(), e.usedTimers, e.ensureTimerRuntime)
		// cryptorand.c reads typed-array views through dynjson.c.
		need(e.usedCryptoRandomBytes, e.usedDynJSONC, e.ensureDynJSONC)
		if !changed {
			return
		}
	}
}
