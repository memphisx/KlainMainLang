package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// A module with a top-level `await` runs as a coroutine (TDD-00224): the
// continuation of `await p` is a reaction on p, so it runs among p's other
// reactions in registration order — after the `.then` registered before the
// await, before the jobs that reaction queued, and before a `.then` registered
// after it. The main-stack wait ran it ahead of every reaction.
func TestE2EModuleTaskAwaitContinuationIsAReaction(t *testing.T) {
	assertOutput(t, `
const log: string[] = [];
const p = new Promise<number>((resolve) => setTimeout(() => resolve(7), 5));
p.then(() => { log.push("r1"); Promise.resolve().then(() => log.push("r1-child")); });
const v = await p;
log.push("top-" + v);
p.then(() => log.push("r2"));
await null;
log.push("end");
setTimeout(() => console.log(log.join(",")), 0);
`, "r1,top-7,r1-child,r2,end\n")
}

// Settled operands, a caught rejection and `for await` at top level keep their
// order on the task path.
func TestE2EModuleTaskSettledRejectedAndForAwait(t *testing.T) {
	assertOutput(t, `
const log: string[] = [];
Promise.resolve().then(() => log.push("m1"));
queueMicrotask(() => log.push("m2"));
await 1;
log.push("after-await");
Promise.resolve().then(() => log.push("m3"));
await Promise.resolve(2);
log.push("end");
try { await Promise.reject(new Error("rej")); } catch (e) { log.push("caught " + (e as Error).message); }
for await (const v of [Promise.resolve(1), Promise.resolve(2)]) log.push("fa" + v);
console.log(log.join(","));
`, "m1,m2,after-await,m3,end,caught rej,fa1,fa2\n")
}

// An uncaught throw after a top-level await is an uncaught exception — message
// and exit code 1 — not a silently rejected module promise.
func TestE2EModuleTaskThrowAfterAwaitIsUncaught(t *testing.T) {
	bin := buildBinaryImports(t, `
console.log("a");
await new Promise<void>((r) => setTimeout(r, 5));
console.log("b");
throw new Error("boom");
`)
	cmd := exec.Command(bin)
	raw, _ := cmd.CombinedOutput()
	out := string(raw)
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "a\nb\n") || !strings.Contains(out, "boom") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

// An unsettled top-level await: the warning, then `process.on('exit')`
// listeners with code 13 (and process.exitCode 13), then exit code 13.
func TestE2EModuleTaskUnsettledRunsExitListenersWith13(t *testing.T) {
	bin := buildBinaryImports(t, `
process.on("exit", (c) => console.log("exit", c, process.exitCode));
console.log("a");
await new Promise<void>(() => {});
console.log("never");
`)
	cmd := exec.Command(bin)
	raw, _ := cmd.CombinedOutput()
	out := string(raw)
	if code := cmd.ProcessState.ExitCode(); code != 13 {
		t.Errorf("exit code = %d, want 13\n%s", code, out)
	}
	if !strings.Contains(out, "exit 13 13") || strings.Contains(out, "never") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if !strings.Contains(out, "unsettled top-level await") {
		t.Errorf("missing the unsettled-await warning:\n%s", out)
	}
}

// process.argv is read from the module task exactly as from main().
func TestE2EModuleTaskReadsArgv(t *testing.T) {
	bin := buildBinaryImports(t, `
await null;
console.log(process.argv.slice(2).join("|"));
const x = await Promise.resolve(41);
console.log(x + 1);
`)
	raw, err := exec.Command(bin, "A", "b c").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, raw)
	}
	compareLines(t, string(raw), "A|b c\n42\n")
}

// Closures over top-level bindings keep working after the module body — and
// with it the module task's stack — has ended.
func TestE2EModuleTaskClosuresOutliveTheBody(t *testing.T) {
	src := `
let count = 0;
const names: string[] = ["a"];
const bump = () => { count++; names.push("n" + count); };
await new Promise<void>((r) => setTimeout(r, 2));
setTimeout(() => { bump(); bump(); }, 5);
setTimeout(() => { bump(); console.log(count, names.join(",")); }, 40);
console.log("body-end");
`
	want := "body-end\n3 a,n1,n2,n3\n"
	assertOutput(t, src, want)
	out, err := exec.Command(buildBinaryGC(t, src)).CombinedOutput()
	if err != nil {
		t.Fatalf("gc run: %v\n%s", err, out)
	}
	compareLines(t, string(out), want)
}

// Top-level recursion after an await has the depth main() had: the module
// task's stack is a main-thread stack, not a coroutine's 256 KiB.
func TestE2EModuleTaskRecursionDepth(t *testing.T) {
	assertOutput(t, `
const seen: number[] = [];
function d(n: number): number { if (n === 0) return 0; seen.push(n); const r = d(n - 1); seen.push(r); return r + 1; }
await null;
console.log(d(15000));
`, "15000\n")
}

// A blocking http.listen after a top-level await: the event loop runs on the
// main stack (never on the module coroutine's), the server serves, and the
// statements after listen — including another await — run once it closes.
func TestE2EHTTPListenAfterTopLevelAwait(t *testing.T) {
	assertOutputImports(t, `
import http from 'klain:http'

interface Res { status: number; body: string }

const greeting = await new Promise<string>((r) => setTimeout(() => r("hi"), 5));
console.log("config loaded:", greeting);

setTimeout(async () => {
  const r = await fetch("http://127.0.0.1:18431/x");
  console.log("client got:", await r.text());
  http.close();
}, 50);

http.listen(18431, (req: HttpRequest): Res => {
  return { status: 200, body: greeting + " " + req.path };
});

console.log("server closed");
const tail = await Promise.resolve("tail");
console.log(tail);
`, "config loaded: hi\nclient got: hi /x\nserver closed\ntail\n")
}

// A coroutine parked on a promise nothing can settle does not hold the process
// open — a pending promise keeps nothing alive.
func TestE2EParkedTaskDoesNotHoldTheLoop(t *testing.T) {
	assertOutput(t, `
async function f(): Promise<void> {
  console.log("in");
  await new Promise<void>(() => {});
  console.log("never");
}
f();
process.on("exit", (c) => console.log("exit", c));
console.log("sync-end");
`, "in\nsync-end\nexit 0\n")
}

// finished(readable) follows the stream's 'end': every 'data' and every 'end'
// listener registered before it has run by the time the await continues — at
// top level and inside an async function alike.
func TestE2EStreamFinishedFollowsEnd(t *testing.T) {
	want := "word: kalimera\nword: kosme\nall words delivered\nafter finished\ntail\n"
	assertOutputImports(t, `
import { Readable } from 'stream';
import { finished } from 'stream/promises';
const words = Readable.from(["kalimera", "kosme"]);
words.on("data", (w) => { console.log("word:", w); });
words.once("end", () => { console.log("all words delivered"); });
await finished(words);
console.log("after finished");
await null;
console.log("tail");
`, want)
	assertOutputImports(t, `
import { Readable } from 'stream';
import { finished } from 'stream/promises';
async function main(): Promise<void> {
  const words = Readable.from(["kalimera", "kosme"]);
  words.on("data", (w) => { console.log("word:", w); });
  words.once("end", () => { console.log("all words delivered"); });
  await finished(words);
  console.log("after finished");
  await null;
  console.log("tail");
}
main();
`, want)
}

// An error inside a nested function's body is a compile error, not a compiler
// crash: the function emitter left its own (shorter) scope stack in place on the
// error path, and the enclosing try/finally emitters then popped past its bottom
// (Test262 language/statements/async-function/try-return-finally-return.js).
func TestE2ENestedFunctionErrorInsideTryFinallyIsNotAPanic(t *testing.T) {
	for _, fn := range []string{
		`function(resolve, reject) { resolve("override"); }`,
		`(resolve, reject) => { resolve("override"); }`,
	} {
		assertCodegenError(t, `
async function f() {
  try {
    return "early-return";
  } finally {
    return await new Promise(`+fn+`);
  }
}
f().then(function(value) { console.log(value); });
`, "incompatible with the parameter's declared type")
	}
}
