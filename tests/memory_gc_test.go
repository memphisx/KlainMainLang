package tests

import (
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// --- -mm=gc (Boehm GC mode) ---
//
// See docs/adr/ADR-00071.md and docs/tdd/TDD-00001.md. Unlike the
// Memory.free(x) tests in memory_test.go (a single, small, deterministic
// free), what actually needs proving here is that *real collections*
// happen under load and don't corrupt anything — a program that merely
// compiles and runs under -mm=gc without ever allocating enough to trigger
// a collection wouldn't catch a broken shim, a wrong calloc doubling, or a
// mis-set GC_stackbottom. gcChurnProgram below allocates ~216MB of
// throwaway string buffers over 2,000,000 iterations, keeping only the
// last one reachable at any time, specifically to force several real
// collection cycles.

const gcChurnProgram = `
let total = 0;
for (let i = 0; i < 2000000; i++) {
  let s: string = "abcdefghijklmnopqrstuvwxyz0123456789" + "abcdefghijklmnopqrstuvwxyz0123456789" + "abcdefghijklmnopqrstuvwxyz0123456789";
  total = total + s.length;
}
console.log(total);
`

// 2,000,000 iterations * 108 (3 concatenated 36-byte segments).
const gcChurnWant = "216000000"

func TestE2EGCModeCorrectnessUnderChurn(t *testing.T) {
	binFile := buildBinaryGC(t, gcChurnProgram)
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimRight(string(out), "\n")
	if got != gcChurnWant {
		t.Errorf("got %q, want %q", got, gcChurnWant)
	}
}

// TestE2EGCModeClassChurn is a regression for the frozen-set collection crash:
// under -mm=gc, allocating class instances in a loop segfaulted once Boehm's
// first collection ran (~50k allocations). The object-field-write frozen-set
// check (Object.freeze enforcement) held its map only through a thread_local
// pointer, which Boehm does not scan as a root, so the map was collected
// mid-use. Fixed by allocating the frozen-set header GC_malloc_uncollectable
// (ADR-00350). Churns 200,000 class instances — well past the crash threshold.
func TestE2EGCModeClassChurn(t *testing.T) {
	const src = `
class Box { n: number; constructor(n: number) { this.n = n; } }
let sink = 0;
for (let i = 0; i < 200000; i = i + 1) { const t = new Box(i); sink = sink + t.n; }
console.log(sink);
`
	binFile := buildBinaryGC(t, src)
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// sum of 0..199999 = 199999*200000/2 = 19999900000
	got := strings.TrimRight(string(out), "\n")
	if got != "19999900000" {
		t.Errorf("got %q, want %q", got, "19999900000")
	}
}

// TestE2EGCModeWeakCollected proves the -mm=gc weak path is genuinely weak
// (TDD-00112): a WeakRef/WeakMap referent that becomes unreachable is dropped
// after a forced collection, while a still-reachable one survives. The referent
// is created in a separate function so its pointer leaves the caller's stack
// frame (conservative scanning would otherwise keep it), and gc() forces a full
// Boehm collection. Object literals (not class instances) are used as referents
// deliberately — see the class-allocation gc crash noted on the status page.
func TestE2EGCModeWeakCollected(t *testing.T) {
	const src = `
interface Box { v: number }
function makeOrphanRef(): WeakRef<Box> {
  const tmp: Box = { v: 99 };
  return new WeakRef(tmp);
}
const wm = new WeakMap<Box, string>();
function orphanEntry(): void {
  const tmp: Box = { v: 1 };
  wm.set(tmp, "gone");
}
const kept: Box = { v: 7 };
const keptRef = new WeakRef(kept);
wm.set(kept, "stays");
const orphanRef = makeOrphanRef();
orphanEntry();
gc();
gc();
console.log(keptRef.deref() === null);
console.log(orphanRef.deref() === null);
console.log(wm.has(kept));
console.log(wm.get(kept));
`
	binFile := buildBinaryGC(t, src)
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimRight(string(out), "\n")
	// kept survives (false), orphan collected (true), map key kept (true, "stays").
	want := "false\ntrue\ntrue\nstays"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestE2EGCModeBoundsMemory checks that peak RSS stays far below the ~216MB
// actually churned by gcChurnProgram — proof that collections actually
// happened, not just that the program didn't crash. /usr/bin/time's report
// format differs between macOS (BSD, -l) and Linux (GNU, -v), so this
// parses whichever applies; if the binary isn't there or the output can't
// be parsed, the RSS assertion is skipped rather than failing the suite,
// since TestE2EGCModeCorrectnessUnderChurn above is the required check.
func TestE2EGCModeBoundsMemory(t *testing.T) {
	binFile := buildBinaryGC(t, gcChurnProgram)

	if _, err := exec.LookPath("/usr/bin/time"); err != nil {
		t.Skip("/usr/bin/time not found")
	}

	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"-l", binFile}
	case "linux":
		args = []string{"-v", binFile}
	default:
		t.Skipf("no known /usr/bin/time RSS format for %s", runtime.GOOS)
	}

	out, err := exec.Command("/usr/bin/time", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("run under /usr/bin/time: %v\n%s", err, out)
	}

	peakBytes, ok := parsePeakRSS(string(out))
	if !ok {
		t.Skipf("could not parse peak RSS from /usr/bin/time output:\n%s", out)
	}

	const churnedBytes = 216_000_000
	const boundBytes = 100_000_000 // well below churnedBytes; generous vs. Boehm's own heap growth
	if peakBytes > boundBytes {
		t.Errorf("peak RSS %d bytes exceeds %d bytes bound (total churn was ~%d bytes) — collections may not be happening", peakBytes, boundBytes, churnedBytes)
	}
}

// TestE2EHTTPListenGCModeConcurrentChurn is the fiber/GC_stackbottom
// correctness test: http.listen's concurrent connections each run on their
// own ucontext_t fiber with a separately malloc'd stack (see
// docs/adr/ADR-00071.md). If a collection fires while a fiber is actively
// running and GC_stackbottom hasn't been repointed at that fiber's own
// stack, Boehm's root-stack scan walks from the live SP to the *original*
// process stack's address — an unrelated, likely-unmapped range — which
// would very likely segfault the whole server, not just return a wrong
// answer. Firing several concurrent, allocation-heavy requests is
// specifically designed to make that crash happen if the fix is missing or
// wrong; correct, distinct responses across all of them is the evidence
// the fix works.
func TestE2EHTTPListenGCModeConcurrentChurn(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8952, (req: HttpRequest): Res => {
  let total = 0;
  for (let i = 0; i < 200000; i++) {
    let s: string = "abcdefghijklmnopqrstuvwxyz0123456789" + "abcdefghijklmnopqrstuvwxyz0123456789";
    total = total + s.length;
  }
  return { status: 200, body: req.path + ":" + total };
})
`
	startHTTPServerGC(t, src, 8952)

	const n = 6
	// 200,000 iterations * 72 (two concatenated 36-byte segments).
	const wantTotal = "14400000"

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := fmt.Sprintf("/req%d", i)
			resp, err := http.Get("http://127.0.0.1:8952" + path)
			if err != nil {
				errs[i] = fmt.Errorf("GET %s: %w", path, err)
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			want := path + ":" + wantTotal
			if string(body) != want {
				errs[i] = fmt.Errorf("GET %s: got %q, want %q", path, string(body), want)
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d: %v", i, err)
		}
	}
}

var (
	macPeakFootprintRE = regexp.MustCompile(`(\d+)\s+peak memory footprint`)
	linuxMaxRSSRE      = regexp.MustCompile(`Maximum resident set size \(kbytes\):\s*(\d+)`)
)

// parsePeakRSS extracts peak resident set size, in bytes, from /usr/bin/time
// output — macOS's BSD `-l` ("<n> peak memory footprint", already bytes) or
// Linux's GNU `-v` ("Maximum resident set size (kbytes): <n>").
func parsePeakRSS(out string) (int64, bool) {
	if m := macPeakFootprintRE.FindStringSubmatch(out); m != nil {
		v, err := strconv.ParseInt(m[1], 10, 64)
		return v, err == nil
	}
	if m := linuxMaxRSSRE.FindStringSubmatch(out); m != nil {
		kb, err := strconv.ParseInt(m[1], 10, 64)
		return kb * 1024, err == nil
	}
	return 0, false
}
