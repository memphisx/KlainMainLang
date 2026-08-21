package tests

import (
	"fmt"
	"testing"
)

// TDD-00084 Part C: Promise.any over raw fetches settles to the first
// transport-successful response, skipping members whose fetch fails at the
// transport level (an unreachable host); when every fetch fails at the transport
// level it throws an AggregateError.
func TestE2EPromiseAnyOverFetchesSkipsFailure(t *testing.T) {
	srv := newFetchTestServer(t)
	// One unreachable member (connection refused) + one good one → the good wins.
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
  const r: Response = await Promise.any([fetch("http://127.0.0.1:1/x"), fetch("%s/flat")])
  console.log(r.status)
}
main2()
`, srv.URL)
	assertOutput(t, src, "200")
}

func TestE2EPromiseAnyOverFetchesAllFailAggregateError(t *testing.T) {
	src := `
async function main2(): Promise<void> {
  try {
    const r: Response = await Promise.any([fetch("http://127.0.0.1:1/x"), fetch("http://127.0.0.1:2/y")])
    console.log("no throw " + r.status)
  } catch (e) {
    console.log(e.name)
    console.log(e.errors.length)
  }
}
main2()
`
	assertOutput(t, src, "AggregateError\n2")
}

// TDD-00084 Part C: a genuinely-suspending async fn (awaits fetch) can take a
// nullable-scalar parameter and a destructured (object/array) parameter — they
// are marshalled through the coroutine task's args bundle.
func TestE2ESuspendingNullableParam(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function f(x: number | null): Promise<number> {
  const r = await fetch("%s/flat")
  return (x ?? 0) + r.status
}
async function run(): Promise<void> {
  console.log(await f(null))
  console.log(await f(10))
}
run()
`, srv.URL)
	assertOutput(t, src, "200\n210")
}

func TestE2ESuspendingDestructuredObjectParam(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function f({ a, b }: { a: number; b: number }): Promise<number> {
  const r = await fetch("%s/flat")
  return a + b + r.status
}
async function run(): Promise<void> {
  console.log(await f({ a: 1, b: 2 }))
}
run()
`, srv.URL)
	assertOutput(t, src, "203")
}

func TestE2ESuspendingDestructuredArrayParam(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function f([a, b]: number[]): Promise<number> {
  const r = await fetch("%s/flat")
  return a * b + r.status
}
async function run(): Promise<void> {
  const xs: number[] = [3, 4]
  console.log(await f(xs))
}
run()
`, srv.URL)
	assertOutput(t, src, "212")
}

// A genuinely-suspending async fn can take a rest parameter — the trailing args
// collect into an array that is marshalled through the coroutine task's args
// bundle like any other array param (former TDD-00085 caveat).
func TestE2ESuspendingRestParam(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function sum(...ns: number[]): Promise<number> {
  const r = await fetch("%s/flat")
  let total = 0
  for (const n of ns) { total = total + n }
  return total + r.status
}
async function run(): Promise<void> {
  console.log(await sum(1, 2, 3))
  console.log(await sum())
}
run()
`, srv.URL)
	assertOutput(t, src, "206\n200")
}

// Promise.race over may-suspend task promises resolves to the first to settle
// (the fast fetch beats the slow timer) via the event-driven waiter list, not a
// busy poll (ADR-00266).
func TestE2ERaceTaskPromisesWaiterList(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function viaFetch(): Promise<number> {
  const r = await fetch("%s/flat")
  return r.status
}
async function viaTimer(): Promise<number> {
  await new Promise<void>((resolve) => { setTimeout(() => { resolve() }, 400) })
  return 7
}
async function main2(): Promise<void> {
  const w: number = await Promise.race([viaFetch(), viaTimer()])
  console.log(w)
}
main2()
`, srv.URL)
	assertOutput(t, src, "200")
}
