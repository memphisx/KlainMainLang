package tests

import (
	"fmt"
	"runtime"
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
	assertOutputImports(t, `import os from 'os'
console.log(os.EOL === "\n")`, "true")
}

func TestE2EOSHomedirMatchesEnvHOME(t *testing.T) {
	assertOutputImports(t, `
import os from 'os'
console.log(os.homedir() === process.env.HOME)
`, "true")
}

func TestE2EOSTmpdirNonEmpty(t *testing.T) {
	assertOutputImports(t, `
import os from 'os'
const t = os.tmpdir()
console.log(t.length > 0)
`, "true")
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
  // speed is always a positive nominal (ADR-00569): real value on Intel, the
  // 2400 MHz libuv fallback on Apple Silicon — never 0, matching real Node.
  if (cpus[i].speed <= 0) { allOk = false }
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
