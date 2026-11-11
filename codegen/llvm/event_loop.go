package llvm

// ensureEventLoop links the event loop (loopsrc/loop.c, on libuv, TDD-00244)
// and what it calls into: the timers and the task scheduler's stubs. Its
// libcurl step is hooks.c's no-op unless the program fetches.
func (e *Emitter) ensureEventLoop() {
	if e.usedEventLoop {
		return
	}
	e.usedEventLoop = true
	e.ensureTimerRuntime()
	// The loop is thread-affine (its state is thread-local): run inside a
	// klain:sync goroutine, it locks the goroutine to its OS thread so a
	// migration cannot hand it another thread's loop.
	if e.usesSyncProgram {
		e.loopCFlags = append(e.loopCFlags, "-DKML_SYNC_LOCK=1")
	}
	e.ensureLoopTurn()
}
