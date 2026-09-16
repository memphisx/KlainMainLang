//go:build !windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestReaperEndToEndDetachedWorker is the full-path regression for the
// conformance memory blowup. It reproduces the *exact* leak shape: a test that
// spawns a DETACHED child (setsid → a new session that escapes kill(-pgid))
// which re-execs the compiled binary (so its command line carries the workdir
// path), then the parent exits 0 — a clean run, no timeout, nothing the
// existing killableCommand group-kill can catch. The child loops forever,
// re-parented to init. This is precisely what accumulated to ~880 processes /
// ~42GB. The test drives the reproducer through the real harness exec path
// (killableCommand + runTracked) with a context that is never cancelled during
// the assertions, so killableCommand's own group-kill provably cannot be what
// clears the worker — only the reaper's sweepOnce may.
//
// Skips unless the repo's klainmain compiler is present, since it must compile a
// real reproducer.
func TestReaperEndToEndDetachedWorker(t *testing.T) {
	klain := repoKlainmain(t)
	if klain == "" {
		t.Skip("klainmain binary not built at repo root; run `go build -o klainmain .` first")
	}

	workDir := t.TempDir()
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		t.Fatal(err)
	}

	// A primary that spawns a DETACHED child re-execing this same binary (via
	// process.execPath, so the child's command line carries the workdir path),
	// then exits 0 without waiting. The child (KML_ROLE=worker) loops forever,
	// setsid-detached and re-parented to init — escaping the primary's process
	// group entirely.
	src := filepath.Join(workDir, "detachedleak.ts")
	prog := `import { spawn } from 'child_process';
const role = process.env.KML_ROLE ?? '';
if (role === 'worker') {
  let i = 0;
  while (true) { i = i + 1; }
} else {
  const c = spawn(process.execPath, [], { detached: true, stdio: 'ignore', env: { KML_ROLE: 'worker' } });
  c.unref();
  console.log('primary spawned detached worker, exiting');
}
`
	if err := os.WriteFile(src, []byte(prog), 0644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(absWorkDir, "node0.bin")
	compile := exec.Command(klain, "-o", bin, src)
	if out, err := compile.CombinedOutput(); err != nil {
		t.Skipf("could not compile cluster reproducer (feature/platform gap, not a reaper failure): %v\n%s", err, out)
	}

	// Run the primary exactly as the harness runs a test binary. A generous
	// context that we do NOT cancel until the very end, so killableCommand's own
	// timeout group-kill can never be what clears the worker — only the reaper's
	// sweepOnce below may. runTracked returns as soon as the primary exits 0.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := killableCommand(ctx, bin)
	_ = runTracked(cmd) // primary exits 0; its worker is now orphaned & looping

	r := newReaper(absWorkDir, 0)

	// Phase 1 — the leak is real: an orphaned worker (re-exec'd node0.bin, so its
	// command line carries absWorkDir) must materialize. If it never does, the
	// reproducer didn't leak and the test would prove nothing — fail loudly.
	leaked := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if procs, _ := r.scanWorkdirProcs(); len(procs) > 0 {
			leaked = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !leaked {
		t.Fatal("no orphaned workdir worker appeared — reproducer did not leak, test proves nothing")
	}

	// Phase 2 — the reaper's own sweep clears it. This is the assertion under
	// test: sweepOnce must SIGKILL the orphaned worker.
	r.sweepOnce()

	cleared := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if procs, _ := r.scanWorkdirProcs(); len(procs) == 0 {
			cleared = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !cleared {
		procs, _ := r.scanWorkdirProcs()
		for _, p := range procs { // clean up so the host isn't left leaking
			_ = syscall.Kill(-p.pid, syscall.SIGKILL)
			_ = syscall.Kill(p.pid, syscall.SIGKILL)
		}
		t.Fatalf("reaper sweepOnce left %d workdir process(es) alive: %+v", len(procs), procs)
	}
}

// repoKlainmain returns the path to a built klainmain at the repo root, or "".
func repoKlainmain(t *testing.T) string {
	t.Helper()
	// tools/conformance → repo root is two levels up.
	for _, rel := range []string{"../../klainmain", "../../klainmain.exe"} {
		if abs, err := filepath.Abs(rel); err == nil {
			if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
				return abs
			}
		}
	}
	return ""
}
