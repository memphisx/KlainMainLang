package tests

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// FFI differential oracle. `node:ffi` is experimental and lands only in Node
// v26.1.0+ behind `--experimental-ffi`, so it is a *separate* oracle from the
// pinned general Node (assertSameAsNode's 24.19.0 has no node:ffi). The binary
// is named by KML_FFI_NODE (an absolute path to a Node >= 26.1.0); CI installs
// it alongside the general oracle. When it is absent the differential check is
// skipped — the hard-coded `want` (which is Node's own output, structural where
// a value would be a non-deterministic pointer) still runs against our binary.
// This keeps node:ffi strictly Node-conformant (TDD-00164/TDD-00190): the same
// program compiled here and run under real node:ffi must print the same thing.

var ffiNodeOnce sync.Once
var ffiNodePath string
var ffiNodeProbeErr string

// ffiNodeBin returns a Node that supports `node:ffi`, or skips the test. It
// probes KML_FFI_NODE first, then a plain `node` on PATH (a dev box whose
// default Node already satisfies 26.1.0+), verifying each can actually load the
// module under --experimental-ffi before trusting it.
func ffiNodeBin(t *testing.T) string {
	t.Helper()
	ffiNodeOnce.Do(func() {
		for _, cand := range []string{os.Getenv("KML_FFI_NODE"), "node"} {
			if cand == "" {
				continue
			}
			bin, err := exec.LookPath(cand)
			if err != nil {
				ffiNodeProbeErr += "; " + cand + ": " + err.Error()
				continue
			}
			out, err := exec.Command(bin, "--experimental-ffi", "--no-warnings",
				"-e", "require('node:ffi');console.log(process.versions.node)").CombinedOutput()
			if err == nil && ffiNodeVersionOK(strings.TrimSpace(string(out))) {
				ffiNodePath = bin
				return
			}
			ffiNodeProbeErr += "; " + bin + ": " + strings.TrimSpace(string(out))
			if err != nil {
				ffiNodeProbeErr += " (" + err.Error() + ")"
			}
		}
	})
	if ffiNodePath == "" {
		t.Skip("no node:ffi oracle found (set KML_FFI_NODE to a Node >= " + ffiNodeMinVersion + ") — skipping the node:ffi differential check")
	}
	return ffiNodePath
}

// ffiNodeMinVersion is the node:ffi oracle the expectations are pinned to
// (CI installs exactly this): earlier 26.x builds differ observably — 26.3.0
// accepts -0 for an int32 argument and hands out a fresh callable per
// getFunction — so an older ffi-capable Node on PATH must not be trusted.
const ffiNodeMinVersion = "26.10.0"

// ffiNodeVersionOK reports whether a `process.versions.node` string is at
// least ffiNodeMinVersion.
func ffiNodeVersionOK(v string) bool {
	parse := func(s string) [3]int {
		var out [3]int
		for i, part := range strings.SplitN(s, ".", 3) {
			n := 0
			for _, c := range part {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int(c-'0')
			}
			out[i] = n
		}
		return out
	}
	got, want := parse(v), parse(ffiNodeMinVersion)
	for i := 0; i < 3; i++ {
		if got[i] != want[i] {
			return got[i] > want[i]
		}
	}
	return true
}

// runNodeFFI runs src (TypeScript, types stripped) under a node:ffi-capable
// Node with --experimental-ffi and returns its stdout. A non-zero exit is a
// failure showing Node's stderr — the program is meant to be a valid,
// terminating node:ffi program.
func runNodeFFI(t *testing.T, src string) string {
	t.Helper()
	bin := ffiNodeBin(t)
	dir := tempDir(t)
	file := dir + "/prog.ts"
	writeFile(t, file, src)
	cmd := exec.Command(bin, "--experimental-ffi", "--experimental-strip-types", "--no-warnings", file)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node:ffi run failed: %v\n%s", err, stderr.String())
	}
	return strings.TrimRight(string(raw), "\n")
}

// assertSameAsNodeFFI is assertOutputImports plus the node:ffi differential
// check: our binary's output must equal want, and — when a node:ffi-capable
// Node is available — real node:ffi's output for the identical program must too.
// want must therefore be deterministic: print structural facts (typeof, > 0n,
// equalities) rather than a raw pointer bigint, whose value differs per process.
func assertSameAsNodeFFI(t *testing.T, src, want string) {
	t.Helper()
	compareLines(t, compileAndRunImports(t, src), want)
	compareLines(t, runNodeFFI(t, src), want)
}

// assertMatchesNodeFFI runs src under our compiler and under real node:ffi
// and requires identical output — for behaviour whose exact text differs per
// platform by design (the std::unordered_map enumeration order Node exposes,
// dlerror texts), so no single hard-coded `want` exists. Skips without a
// node:ffi-capable Node.
func assertMatchesNodeFFI(t *testing.T, src string) {
	t.Helper()
	node := runNodeFFI(t, src)
	compareLines(t, compileAndRunImports(t, src), node)
}
