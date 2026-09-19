package tests

import "testing"

// ADR-01002/ADR-01003 (TDD-00169): a Promise.allSettled rejected `reason` is a
// NaN-boxed `any` carrying the original rejected value with full fidelity — an
// Error reason recovers its shape via the field-0 type-id (TDD-00222), and a
// non-Error reason keeps its true type. Byte-exact to Node, no segfault.

// A non-Error reject value keeps its true type through `reason: any`:
// `typeof` is the value's own type and JSON renders the raw value, not a
// quoted/stringified Error (TDD-00169).
func TestE2EAllSettledNonErrorReasonFidelity(t *testing.T) {
	assertOutput(t, `
async function run() {
    const r = await Promise.allSettled([Promise.reject(42), Promise.reject("boom")])
    const a = r[0]
    if (a.status === "rejected") console.log(typeof a.reason, a.reason)
    const b = r[1]
    if (b.status === "rejected") console.log(typeof b.reason, b.reason)
    console.log(JSON.stringify(r))
}
run()
`, "number 42\nstring boom\n"+`[{"status":"rejected","reason":42},{"status":"rejected","reason":"boom"}]`)
}

// An Error reason serializes as Node's `{}` (no enumerable own properties),
// recovered at the JSON site via the boxed-object type-id (TDD-00222).
func TestE2EAllSettledErrorReasonJSONEmptyObject(t *testing.T) {
	assertOutput(t, `
async function run() {
    const r = await Promise.allSettled([Promise.resolve(1), Promise.reject(new Error("neg"))])
    console.log(JSON.stringify(r))
}
run()
`, `[{"status":"fulfilled","value":1},{"status":"rejected","reason":{}}]`)
}

// `instanceof Error` on a boxed value is now precise: a boxed Error matches, a
// boxed plain object does not (was unconditionally true before the type-id).
func TestE2EBoxedErrorInstanceOfPrecision(t *testing.T) {
	assertOutput(t, `
const e: any = new Error("x")
const o: any = { a: 1 }
const te: any = new TypeError("t")
console.log(e instanceof Error, o instanceof Error)
console.log(e instanceof TypeError, te instanceof TypeError, te instanceof Error)
`, "true false\nfalse true true")
}

// A boxed Error renders and reads its fields at a general `any` site — String()
// gives `Name: message` (not "[object Object]"), and `.message`/`.name` read
// the real fields (TDD-00222).
func TestE2EBoxedErrorRenderAndMembers(t *testing.T) {
	assertOutput(t, `
const arr: any[] = [new Error("boom"), new TypeError("bad")]
console.log(String(arr[0]))
console.log(arr[1].message, arr[1].name)
console.log(JSON.stringify(arr))
`, "Error: boom\nbad TypeError\n"+`[{},{}]`)
}

// ADR-01002: a Promise.allSettled rejected `reason` is materialised as a real
// errorObjType, so introspecting an Error reason is byte-exact to Node and no
// longer segfaults (the former `.reason.message`-on-an-Error crash).

func TestE2EAllSettledErrorReasonIntrospection(t *testing.T) {
	// An Error rejection reason keeps its real message/name, and String(reason)
	// is Node's `Name: message` — all read from a real Error object, not a
	// stringified pointer (which used to crash on the member read).
	assertOutput(t, `
async function run() {
    const r = await Promise.allSettled([Promise.reject(new Error("boom"))])
    const a = r[0] as any
    console.log(a.status)
    console.log(a.reason.message)
    console.log(a.reason.name)
    console.log(String(a.reason))
}
run()
`, "rejected\nboom\nError\nError: boom")
}

// The JSON arm reads the reason's message field rather than dereferencing the
// errorObjType slot as a raw string — a rejected element serializes without a
// crash, and a string reject value renders its own text.
func TestE2EAllSettledReasonJSONNoCrash(t *testing.T) {
	assertOutput(t, `
async function run(): Promise<string> {
    return JSON.stringify(await Promise.allSettled([Promise.resolve(1), Promise.reject("e")]))
}
run().then((v) => { console.log(v) })
`, `[{"status":"fulfilled","value":1},{"status":"rejected","reason":"e"}]`)
}

// TDD-00222 subclass closure: an error-SUBCLASS instance as a rejection
// reason recovers .message/.name and renders `Name: message` at the boxed
// site, exactly like a built-in Error — a `class X extends Error` instance is
// errorObjType-prefix-compatible, so the boxed-error probe accepts any
// flagged field-0, not only builtin kinds. (Formerly rendered
// `[object Object]` and threw on the member read.)
func TestE2EAllSettledErrorSubclassReason(t *testing.T) {
	assertOutput(t, `
class MyErr extends Error {
    tier: number = 0
    constructor(msg: string) { super(msg); this.name = "MyErr"; this.tier = 7 }
}
async function run() {
    const r = await Promise.allSettled([Promise.reject(new MyErr("boom"))])
    const a = r[0] as any
    console.log(a.reason.message)
    console.log(a.reason.name)
    console.log(String(a.reason))
    console.log(a.reason instanceof Error)
    console.log(a.reason instanceof MyErr)
}
run()
`, "boom\nMyErr\nMyErr: boom\ntrue\ntrue")
}
