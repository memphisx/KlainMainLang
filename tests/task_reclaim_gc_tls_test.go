package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// A finished coroutine gives its stack back (ADR-01048). Before, every call of a
// suspending async function kept its 256 KiB fiber stack, context and jmpbuf
// stack for the life of the process: 20,000 sequential awaits grew RSS from
// 9 MB to 363 MB (Node stays flat), and the scheduler scanned every task the
// process had ever run. The program checks its own RSS so the assertion is the
// same on every platform. 5,000 iterations (each timer is clamped to 1 ms,
// ADR-01071, so 20,000 would take 20 s as in Node): the leak was ~18 KB resident
// per call, so a leaking build lands near 100 MB against a fixed one's ~17 MB.
const taskReclaimProgram = `
async function tick(i: number): Promise<number> {
  await new Promise<void>((r) => setTimeout(r, 0));
  return i;
}
async function run(): Promise<void> {
  let s = 0;
  for (let i = 0; i < 5000; i++) {
    s += await tick(i);
  }
  const mb = Math.round(process.memoryUsage().rss / 1048576);
  console.log(s);
  console.log(mb < 60 ? "rss ok" : "rss " + mb + " MB");
}
run();
`

const taskReclaimWant = "12497500\nrss ok"

func TestE2ETaskStackReclaimed(t *testing.T) {
	assertOutput(t, taskReclaimProgram, taskReclaimWant)
}

// The same program under -mm=gc. It used to segfault inside an allocation on
// Windows and Linux alike: a coroutine's frames were not scanned while it ran
// (Windows: the collector's per-thread stack base still named the thread's own
// stack, not the fiber's), and the runtime's thread_local roots — task array,
// microtask queue, timer list — were never scanned on either platform
// (ADR-01049).
func TestE2ETaskStackReclaimedGC(t *testing.T) {
	binFile := buildBinaryGC(t, taskReclaimProgram)
	out, err := exec.Command(binFile).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), taskReclaimWant)
}

// Timers only, no coroutine anywhere: the timer list is reachable solely from a
// thread_local global, so under -mm=gc the churn below collected it mid-use
// (5 crashes in 5 runs on Linux x86-64 before ADR-01049).
func TestE2EGCModeThreadLocalRootsSurviveChurn(t *testing.T) {
	const src = `
let n = 0;
function step(): void {
  n++;
  const junk: string[] = [];
  for (let k = 0; k < 50; k++) junk.push("x" + k + n);
  if (n < 3000) setTimeout(step, 0); else console.log("done", n, junk.length);
}
step();
`
	binFile := buildBinaryGC(t, src)
	out, err := exec.Command(binFile).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), "done 3000 50")
}

// Microtask-only coroutines under -mm=gc: no timer, no fd — every resume is a
// reaction job, so the only roots keeping the chain alive are the parked
// fibers' stacks and the thread_local microtask queue.
func TestE2EGCModeCoroutineChainSurvivesChurn(t *testing.T) {
	const src = `
async function tick(i: number): Promise<number> {
  await Promise.resolve();
  return i;
}
async function run(): Promise<void> {
  let s = 0;
  for (let i = 0; i < 3000; i++) { s += await tick(i); }
  console.log("done", s);
}
run();
`
	binFile := buildBinaryGC(t, src)
	out, err := exec.Command(binFile).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), "done 4498500")
}

// A generator driven from the module task of a top-level-await entry
// (ADR-01050): each .next() returns to a coroutine stack, and the stack bottom
// the collector is given there must be that stack's, not the process stack's.
// Restoring the process stack left the collector scanning from the module
// task's SP to the process stack base (examples/classes/classes.ts segfaulted
// under -mm=gc on Linux).
func TestE2EGCModeGeneratorFromModuleTask(t *testing.T) {
	const src = `
await Promise.resolve();
function* count(lo: number, hi: number) {
  for (let i = lo; i < hi; i++) {
    const junk: string[] = [];
    for (let k = 0; k < 200; k++) junk.push("x" + k + i);
    yield i + junk.length;
  }
}
let s = 0;
for (const v of count(0, 2000)) s += v;
const it = count(5, 7);
console.log("done", s, it.next().value, it.next().value, it.next().done);
`
	binFile := buildBinaryGC(t, src)
	out, err := exec.Command(binFile).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), "done 2399000 205 206 true")
}

// Threads the collector does not know allocate through the shim too: libcurl
// resolves a host name on its own thread (getaddrinfo), so a lookup under
// allocation churn put GC_malloc calls there — and a collection it started
// aborted the process ("Collecting from unknown thread",
// examples/async/promise_all.ts under -mm=gc on Linux, whose .invalid fetch is
// such a lookup). Those threads now get libc's allocator (gcshim.c, ADR-01088).
// A `.invalid` name never resolves, so this needs no network. Ten rounds fail
// every run without that fix; at 25 a separate, older collector crash appears
// (BACKLOG §0 item 20), so the size is deliberate, not a mask.
func TestE2EGCModeFetchFromResolverThread(t *testing.T) {
	const src = `
let failed = 0
for (let round = 0; round < 10; round++) {
  const ps: Promise<Response>[] = []
  for (let k = 0; k < 3; k++) ps.push(fetch("http://kml-gc-" + round + "-" + k + ".invalid/"))
  const junk: string[] = []
  for (let j = 0; j < 2000; j++) junk.push("x" + j + round)
  const rs = await Promise.allSettled(ps)
  for (const r of rs) if (r.status === "rejected") failed++
}
console.log("done", failed)
`
	binFile := buildBinaryGCImports(t, src)
	out, err := exec.Command(binFile).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), "done 30")
}
