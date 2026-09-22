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
// same on every platform.
const taskReclaimProgram = `
async function tick(i: number): Promise<number> {
  await new Promise<void>((r) => setTimeout(r, 0));
  return i;
}
async function run(): Promise<void> {
  let s = 0;
  for (let i = 0; i < 20000; i++) {
    s += await tick(i);
  }
  const mb = Math.round(process.memoryUsage().rss / 1048576);
  console.log(s);
  console.log(mb < 120 ? "rss ok" : "rss " + mb + " MB");
}
run();
`

const taskReclaimWant = "199990000\nrss ok"

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
