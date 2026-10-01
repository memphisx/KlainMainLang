package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// Unhandled promise rejections (ADR-01192), as Node's default treats them:
// reported after the microtask checkpoint — to process.on('unhandledRejection')
// when registered, else as an uncaught exception (exit 1) — and never for a
// rejection something consumes (catch, then's onRejected, await, a
// combinator, finally's chain). Node is the oracle for the handled shapes and
// the listener; the default report is this compiler's uncaught print.

func TestE2EUnhandledRejectionHandledShapes(t *testing.T) {
	assertSameAsNode(t, `
async function f(): Promise<number> { await null; throw new Error("y") }
const p = Promise.reject(new Error("x"))
p.catch((e: Error) => console.log("c1", e.message))
try { await f() } catch (e) { console.log("c2") }
f().catch(() => console.log("c3"))
try { await Promise.all([f(), Promise.resolve(1)]) } catch (e) { console.log("c4") }
const r5 = await Promise.allSettled([f()])
console.log("c5", r5[0].status)
const p2 = new Promise<number>((_, rej) => rej(new Error("z")))
p2.then(() => {}, () => console.log("c6"))
async function g(): Promise<void> { await f() }
g().catch(() => console.log("c8"))
await new Promise<void>((r) => setTimeout(r, 30))
console.log("end")
`)
}

func TestE2EUnhandledRejectionListener(t *testing.T) {
	assertSameAsNode(t, `
process.on('unhandledRejection', (reason: any, promise: any) => {
  console.log('unhandled:', reason instanceof Error ? reason.message : reason, typeof promise)
})
async function f(): Promise<void> { throw new Error("boom") }
f()
Promise.reject(42)
setTimeout(() => console.log('still running'), 20)
`)
}

func TestE2EUnhandledRejectionExitsNonZero(t *testing.T) {
	bin := buildBinary(t, `
async function f(): Promise<void> { await null; throw new Error("boom") }
f()
setTimeout(() => console.log("timer ran"), 3000)
`)
	out, err := exec.Command(bin).CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 1 {
		t.Fatalf("want exit 1, got %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Uncaught: boom") || strings.Contains(string(out), "timer ran") {
		t.Fatalf("want the uncaught report before the timer, got:\n%s", out)
	}
}
