package tests

import "testing"

// --- TDD-00207: a Promise rejection reason carries the real thrown value
// (string / number / object / Error), not an Error wrapper — the async twin of a
// throw, riding the same caught-value channel as try/catch. ---

func TestE2EPromiseRejectStringValue(t *testing.T) {
	// Promise.reject("x").then(_, onR) — the handler sees the raw string, not "".
	assertOutput(t, `
Promise.reject("boom").then(() => {}, e => console.log("rej:", e))
`, "rej: boom")
}

func TestE2EPromiseRejectNumberValue(t *testing.T) {
	assertOutput(t, `
Promise.reject(42).catch(e => console.log("n:", e))
`, "n: 42")
}

func TestE2EPromiseRejectErrorMessage(t *testing.T) {
	// A real Error reason keeps message/instanceof through the reject channel.
	assertOutput(t, `
Promise.reject(new Error("m")).catch((e: Error) =>
  console.log("msg:", e.message, "isErr:", e instanceof Error))
`, "msg: m isErr: true")
}

func TestE2ENewPromiseRejectValueTypes(t *testing.T) {
	// The executor's reject() carries arbitrary values (previously invalid IR for
	// a numeric reason).
	assertOutput(t, `
new Promise((_, rej) => rej(7)).then(v => console.log("ok", v), e => console.log("num", e))
new Promise((_, rej) => rej("s")).then(v => console.log("ok", v), e => console.log("str", e))
`, "num 7\nstr s")
}

func TestE2EPromiseRejectPropagatesThroughThen(t *testing.T) {
	// A rejection with no onR propagates to the next .catch with its reason intact.
	assertOutput(t, `
Promise.reject("p").then(v => v).catch(e => console.log("caught:", e))
`, "caught: p")
}

func TestE2EPromiseRejectFinallyPassThrough(t *testing.T) {
	assertOutput(t, `
Promise.reject("f").finally(() => console.log("fin")).catch(e => console.log("after:", e))
`, "fin\nafter: f")
}

func TestE2EAwaitRejectedNonErrorRethrows(t *testing.T) {
	// await of a Promise.reject(nonError) re-throws the real value into catch.
	assertOutput(t, `
async function run(): Promise<void> {
  try { await Promise.reject("R") } catch (e) { console.log("caught:", e, typeof e) }
}
run()
`, "caught: R string")
}

func TestE2EAsyncThrowNonErrorRejectsWithValue(t *testing.T) {
	// An async fn that throws a non-Error rejects with that value verbatim.
	assertOutput(t, `
async function f(): Promise<void> { throw "async-throw" }
f().catch(e => console.log("caught:", e))
`, "caught: async-throw")
}

// --- the async-return-type inference fix TDD-00207 required: an UNANNOTATED
// async function still returns a Promise, so .then/.catch/await work on its
// result (previously "a number has no method 'then'"). ---

func TestE2EUnannotatedAsyncReturnThen(t *testing.T) {
	assertOutput(t, `
async function h() { return 5 }
h().then(v => console.log("got", v))
`, "got 5")
}

func TestE2EUnannotatedAsyncThrowCatch(t *testing.T) {
	assertOutput(t, `
async function g() { throw new Error("E") }
g().catch((e: Error) => console.log("g msg:", e.message))
`, "g msg: E")
}
