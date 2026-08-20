package tests

import (
	"fmt"
	"testing"
)

// TDD-00084 Part B: a program mixing coroutine tasks (a may-suspend async fn that
// awaits fetch) with a timer must drive both under one unified, task-aware event
// loop. Before Part B, task_run_all completed the task and exited without ever
// firing a pending timer (the task drive and the timer drive were separate,
// mutually-exclusive exit loops). The timer is set after the await so the order
// is deterministic — it fires at loop exit, once the task is done.
func TestE2ETaskPlusTimerFires(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function grab(u: string): Promise<number> { const r = await fetch(u); return r.status }
async function main2(): Promise<void> {
  const s = await grab("%s/flat")
  console.log("fetched " + s)
  setTimeout(() => { console.log("timer fired") }, 1)
}
main2()
`, srv.URL)
	assertOutput(t, src, "fetched 200\ntimer fired")
}
