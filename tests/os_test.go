package tests

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"testing"
)

// --- os module (TDD-00024) ---
//
// Most of this module's values are environment-dependent (hostname, home
// directory, memory size, core count) — these tests assert invariants/
// self-consistency rather than exact values, and where a value IS knowable
// from this test binary's own Go runtime (GOOS, NumCPU), that's asserted
// directly so the same test source is portable across whatever machine
// `go test` actually runs on (Linux here; Darwin once tested there per
// docs/tdd/TDD-00024.md/docs/adr/ADR-00090.md).

func TestE2EOSPlatform(t *testing.T) {
	want := runtime.GOOS
	if want == "windows" {
		want = "win32"
	}
	assertOutputImports(t, `import os from 'os'
console.log(os.platform())`, want)
}

func TestE2EOSEOL(t *testing.T) {
	if runtime.GOOS == "windows" {
		assertOutputImports(t, "import os from 'os'\nconsole.log(os.EOL === \"\\r\\n\")", "true")
		return
	}
	assertOutputImports(t, `import os from 'os'
console.log(os.EOL === "\n")`, "true")
}

func TestE2EOSHomedirMatchesEnvHOME(t *testing.T) {
	// Node: HOME on POSIX, USERPROFILE on Windows. (A PowerShell-launched
	// `go test` has no HOME at all; comparing against a missing env value
	// once crashed — ADR-00724.)
	homeVar := "HOME"
	if runtime.GOOS == "windows" {
		homeVar = "USERPROFILE"
	}
	assertOutputImports(t, `
import os from 'os'
console.log(os.homedir() === process.env.`+homeVar+`)
`, "true")
}

// os.homedir() POSIX fidelity (ADR-00790): mirrors libuv's uv_os_homedir — a
// set HOME wins (an empty one included, Node returns ""), only an unset HOME
// falls back to the passwd database (getpwuid) instead of throwing.

func TestE2EOSHomedirEmptyHomeStaysEmpty(t *testing.T) {
	skipPOSIXToolsOnWindows(t, "HOME/passwd semantics (Windows uses USERPROFILE)")
	// HOME set but empty: Node returns "" (it is NOT treated as unset), so the
	// passwd fallback must not fire.
	assertOutputImportsEnv(t, `
import os from 'os'
console.log("[" + os.homedir() + "]")
`, "[]", "HOME=")
}

func TestE2EOSHomedirUnsetFallsBackToPasswd(t *testing.T) {
	skipPOSIXToolsOnWindows(t, "HOME/passwd semantics (Windows uses USERPROFILE)")
	// HOME unset entirely: os.homedir() must fall back to getpwuid's pw_dir
	// (real Node behaviour) rather than throwing. The expected value is the
	// current user's passwd home, which Go's os/user reads the same way.
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		t.Skipf("no passwd home to compare against: %v", err)
	}
	binFile := buildBinaryImports(t, `
import os from 'os'
console.log(os.homedir())
`)
	env := os.Environ()
	kept := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "HOME=") {
			kept = append(kept, e)
		}
	}
	cmd := exec.Command(binFile)
	cmd.Env = kept
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), u.HomeDir)
}

func TestE2EOSTmpdirNonEmpty(t *testing.T) {
	assertOutputImports(t, `
import os from 'os'
const t = os.tmpdir()
console.log(t.length > 0)
`, "true")
}

// os.tmpdir() POSIX fidelity (ADR-00777): `process.env.TMPDIR || '/tmp'`, then
// strip a single trailing '/' unless the path is just "/".

func TestE2EOSTmpdirTrailingSlashStripped(t *testing.T) {
	skipPOSIXToolsOnWindows(t, "TMPDIR/POSIX-tmpdir semantics (Windows uses TEMP/TMP)")
	assertOutputImportsEnv(t, `
import os from 'os'
console.log(os.tmpdir())
`, "/foo/bar", "TMPDIR=/foo/bar/")
}

func TestE2EOSTmpdirNoTrailingSlashUnchanged(t *testing.T) {
	skipPOSIXToolsOnWindows(t, "TMPDIR/POSIX-tmpdir semantics (Windows uses TEMP/TMP)")
	assertOutputImportsEnv(t, `
import os from 'os'
console.log(os.tmpdir())
`, "/foo/bar", "TMPDIR=/foo/bar")
}

func TestE2EOSTmpdirRootNotStripped(t *testing.T) {
	skipPOSIXToolsOnWindows(t, "TMPDIR/POSIX-tmpdir semantics (Windows uses TEMP/TMP)")
	// A lone "/" is length 1, so the trailing-slash strip must not touch it.
	assertOutputImportsEnv(t, `
import os from 'os'
console.log(os.tmpdir())
`, "/", "TMPDIR=/")
}

func TestE2EOSTmpdirEmptyFallsBackToTmp(t *testing.T) {
	skipPOSIXToolsOnWindows(t, "TMPDIR/POSIX-tmpdir semantics (Windows uses TEMP/TMP)")
	// An empty TMPDIR is falsy in Node's `|| '/tmp'`, so it falls back.
	assertOutputImportsEnv(t, `
import os from 'os'
console.log(os.tmpdir())
`, "/tmp", "TMPDIR=")
}

func TestE2EOSHostnameNonEmpty(t *testing.T) {
	assertOutputImports(t, `
import os from 'os'
const h = os.hostname()
console.log(h.length > 0)
`, "true")
}

func TestE2EOSTotalmemFreememPositive(t *testing.T) {
	assertOutputImports(t, `
import os from 'os'
const total = os.totalmem()
const free = os.freemem()
console.log(total > 0)
console.log(free > 0)
console.log(free <= total)
`, "true\ntrue\ntrue")
}

func TestE2EOSCpusCountMatchesRuntime(t *testing.T) {
	want := fmt.Sprintf("%d", runtime.NumCPU())
	assertOutputImports(t, `import os from 'os'
console.log(os.cpus().length)`, want)
}

func TestE2EOSCpusFieldsWellFormed(t *testing.T) {
	assertOutputImports(t, `
import os from 'os'
const cpus = os.cpus()
let allOk = true
for (let i = 0; i < cpus.length; i = i + 1) {
  if (cpus[i].model.length === 0) { allOk = false }
  // speed: on macOS always a positive nominal (ADR-00569: real value on
  // Intel, the 2400 MHz libuv fallback on Apple Silicon); on Linux it is
  // cpufreq's scaling_max_freq, which VMs and containers do not expose, so 0
  // is what Node itself reports there (ADR-00733) — never negative.
  if (cpus[i].speed < 0) { allOk = false }
  if (process.platform === 'darwin' && cpus[i].speed <= 0) { allOk = false }
  if (cpus[i].times.user < 0) { allOk = false }
  if (cpus[i].times.nice < 0) { allOk = false }
  if (cpus[i].times.sys < 0) { allOk = false }
  if (cpus[i].times.idle < 0) { allOk = false }
  if (cpus[i].times.irq < 0) { allOk = false }
}
console.log(allOk)
`, "true")
}

func TestE2EOSCpusSameModelAcrossCores(t *testing.T) {
	// Not a real Node guarantee in general (heterogeneous cores exist), but
	// true for every machine this test suite actually runs on today — a
	// cheap cross-core consistency check.
	assertOutputImports(t, `
import os from 'os'
const cpus = os.cpus()
console.log(cpus[0].model === cpus[cpus.length - 1].model)
`, "true")
}
