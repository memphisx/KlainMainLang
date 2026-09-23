package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// ADR-01073: a finished coroutine's task struct is freed once nothing names it.
// The holders that used to outlive a task are the waiter registrations a
// Promise.race / Promise.any leaves on its losing members; they are undone on
// resume, and the losers settling later must not write through freed memory.
// ADR-01074: under -mm=gc the same program runs with task stacks outside the
// collected heap.
const taskStructFreeProgram = `
async function tick(i: number): Promise<number> {
  await new Promise<void>((r) => setTimeout(r, 0));
  return i;
}
async function slow(i: number, ms: number): Promise<number> {
  await new Promise<void>((r) => setTimeout(r, ms));
  return i;
}
async function run(): Promise<void> {
  let s = 0;
  for (let i = 0; i < 1000; i++) s += await tick(i);
  let w = 0;
  for (let i = 0; i < 100; i++) w += await Promise.race([slow(1, 1), slow(2, 3)]);
  await new Promise<void>((r) => setTimeout(r, 10));
  const a = await Promise.any([slow(5, 2), slow(6, 1)]);
  await new Promise<void>((r) => setTimeout(r, 10));
  const mb = Math.round(process.memoryUsage().rss / 1048576);
  console.log(s, w, a, mb < 60 ? "rss ok" : "rss " + mb + " MB");
}
run();
`

const taskStructFreeWant = "499500 100 6 rss ok"

func TestE2ETaskStructFreedAfterRaceLoser(t *testing.T) {
	assertOutput(t, taskStructFreeProgram, taskStructFreeWant)
}

func TestE2ETaskStructFreedAfterRaceLoserGC(t *testing.T) {
	binFile := buildBinaryGC(t, taskStructFreeProgram)
	out, err := exec.Command(binFile).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), taskStructFreeWant)
}
