package llvm

// ensureRuntimeDeps links the gated runtime files (those with libraries of
// their own, or Windows-only) another linked runtime file calls (TDD-00251
// A); the rest are linked by the symbols they name (runtime_resolve.go). A
// runtime object may not name a symbol its program does not define: a COFF
// link resolves every object's references before it collects sections, so
// the unreached call still fails there. It runs until nothing changes,
// since a dependency may bring its own.
func (e *Emitter) ensureRuntimeDeps() {
	for {
		changed := false
		need := func(user bool, linked bool, ensure func()) {
			if user && !linked {
				ensure()
				changed = true
			}
		}
		// fserror.c on Windows resolves the paths it reports with
		// path_win32.c (C only; nothing to declare in the IR).
		need(e.usedFsThrow && e.opts.Target.OS() == "windows", e.usesPathWin32, func() { e.usesPathWin32 = true })
		// And the pool's recursive mkdir on Windows returns its first
		// directory namespaced through it.
		need(e.usedThreadPool && e.opts.Target.OS() == "windows", e.usesPathWin32, func() { e.usesPathWin32 = true })
		// fetch.c holds the async path too, which reads its multi handle,
		// the abort signal, the fiber globals and the event loop's
		// wait-outside abort (ensureFetchAsync).
		need(e.usedFetch, e.usedFetchAsync, e.ensureFetchAsync)
		if !changed {
			return
		}
	}
}
