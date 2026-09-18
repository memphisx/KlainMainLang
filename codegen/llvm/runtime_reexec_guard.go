package llvm

// ADR-00972: shared self-spawn detection used by both child_process spawn paths
// (blocking spawnSync and async spawn/fork). A compiled binary handed
// process.execPath as a spawn target has no interpreter mode, so re-running its
// own program body would re-hit the same spawn — an unbounded self-fork chain.
// __kml_mark_child_if_self_spawn, called in the forked child just before execvp,
// detects a target that resolves to this very executable and sets
// KML_KLAIN_REEXEC in the environment; the startup guard
// (@__kml_reject_node_interp_flags, ensureNodeInterpFlagGuard) then refuses to
// run the child's body, breaking the recursion at the first child.

// UsesReexecGuard reports whether the shared self-spawn helper C source must be
// linked (any child_process spawn path that can re-exec this binary).
func (e *Emitter) UsesReexecGuard() bool { return e.usedReexecGuard }

// ensureReexecGuardHelper declares the shared helper once (IR side) and flags the
// C source for linking. Callers also arrange for the startup reject-guard to run
// (ensureNodeInterpFlagGuard), which reads the marker this helper sets.
func (e *Emitter) ensureReexecGuardHelper() {
	if e.usedReexecGuard {
		return
	}
	e.usedReexecGuard = true
	e.emitGlobal("declare void @__kml_mark_child_if_self_spawn(ptr)")
	// The reader side of the marker.
	e.ensureNodeInterpFlagGuard()
}

// ReexecGuardSource is the embedded C implementation of the shared helper.
func ReexecGuardSource() string {
	return `#include <stdlib.h>
#include <string.h>
#ifndef _WIN32
#include <limits.h>
#include <unistd.h>
#if defined(__APPLE__)
#include <mach-o/dyld.h>
#endif
#endif

/* Sets KML_KLAIN_REEXEC in the current environment when file resolves to this
   running executable — call it in the forked child immediately before execvp so
   the re-executed copy's startup guard refuses to run its program body. A no-op
   on Windows (the fork+exec self-spawn chain is a POSIX construct here). */
void __kml_mark_child_if_self_spawn(const char *file) {
#ifndef _WIN32
  if (!file) return;
  char self[4096];
#if defined(__APPLE__)
  unsigned int sz = (unsigned int)sizeof(self);
  if (_NSGetExecutablePath(self, &sz) != 0) return;
#else
  ssize_t n = readlink("/proc/self/exe", self, sizeof(self) - 1);
  if (n < 0) return;
  self[n] = 0;
#endif
  char rself[4096], rfile[4096];
  const char *sr = realpath(self, rself) ? rself : self;
  const char *fr = realpath(file, rfile) ? rfile : file;
  if (strcmp(sr, fr) == 0) setenv("KML_KLAIN_REEXEC", "1", 1);
#else
  (void)file;
#endif
}
`
}
