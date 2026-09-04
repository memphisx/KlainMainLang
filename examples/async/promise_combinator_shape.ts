// Promise.any value fidelity and Promise.allSettled result shape — ADR-00689.
//
// Two combinator details that must match Node/JS exactly:
//
//  1. Promise.any resolves to the first *fulfilled* member's value, skipping
//     rejected ones — including when the rejected member comes first and the
//     fulfilled winner is a number (its value must not be mis-decoded).
//  2. Promise.allSettled reports each member as either
//     { status: "fulfilled", value } or { status: "rejected", reason } — only
//     the applicable key, and the raw rejection value as `reason`.

// ── Promise.any: reject-first, numeric winner ───────────────────────────────
async function firstNumber(): Promise<number> {
    return await Promise.any([Promise.reject("skipped"), Promise.resolve(2)])
}
firstNumber().then((v) => {
    console.log(v) // 2
})

// ── Promise.any: reject-first, string winner ────────────────────────────────
async function firstString(): Promise<string> {
    return await Promise.any([Promise.reject("skipped"), Promise.resolve("hi")])
}
firstString().then((v) => {
    console.log(v) // hi
})

// ── Promise.allSettled: per-element shape via JSON ──────────────────────────
async function settledShape(): Promise<string> {
    return JSON.stringify(
        await Promise.allSettled([Promise.resolve(1), Promise.reject("e")]),
    )
}
settledShape().then((v) => {
    // [{"status":"fulfilled","value":1},{"status":"rejected","reason":"e"}]
    console.log(v)
})

// ── Promise.allSettled: direct property access ──────────────────────────────
const results = await Promise.allSettled([
    Promise.resolve(7),
    Promise.reject("boom"),
])
console.log(results[0].status) // fulfilled
console.log(results[0].value)  // 7
console.log(results[1].status) // rejected
