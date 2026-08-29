// assert — Node's real assertion library (distinct from console.assert,
// which logs to stderr and keeps running; these throw a catchable
// AssertionError instead), the shape scripts and tests actually use.

import assert from 'assert'

// ── assert.ok / bare assert(cond) ─────────────────────────────────────────
assert.ok(1 + 1 === 2)
assert(true, "this message is never seen")

try {
  assert(false, "expected true")
} catch (e) {
  console.log(e.name + ": " + e.message)  // AssertionError: expected true
}

// ── equal / strictEqual (aliases here — this compiler's == is already
// strict, no implicit coercion between differing types) ─────────────────
assert.equal(2 + 2, 4)
assert.strictEqual("a" + "b", "ab")

try {
  assert.equal(1, 2)
} catch (e) {
  console.log(e.message)  // values are not equal
}

// ── notEqual / notStrictEqual ─────────────────────────────────────────────
assert.notEqual(1, 2)

try {
  assert.notStrictEqual(5, 5)
} catch (e) {
  console.log(e.message)  // values are equal
}

// ── fail: always throws ───────────────────────────────────────────────────
try {
  assert.fail("unreachable branch was reached")
} catch (e) {
  console.log(e.message)  // unreachable branch was reached
}

// ── throws: expects the given zero-arg function to throw ─────────────────
assert.throws(() => {
  throw new Error("boom")
})
console.log("assert.throws passed")

try {
  assert.throws(() => {
    const x = 1  // never throws
  }, "expected an error")
} catch (e) {
  console.log(e.message)  // expected an error
}

// ifError fails on any truthy value (the Node callback-style error guard);
// doesNotThrow is throws' inverse.
assert.ifError(null)
assert.doesNotThrow(() => { JSON.parse('{"ok": true}') })
console.log('ifError + doesNotThrow passed')
