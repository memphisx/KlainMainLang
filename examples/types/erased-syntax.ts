// TypeScript surface syntax that carries no runtime meaning in an AOT-compiled
// native binary and is therefore parsed and erased rather than rejected — the
// `debugger` statement, the `readonly` array/tuple type modifier, and a leading
// `this` parameter. See docs/status/TYPE-SYSTEM.md and docs/status/LANGUAGE-CONSTRUCTS.md.

// --- debugger: a no-op (no attached inspector), matching plain JS ---
const start = 1
debugger
console.log(start)            // 1

// --- readonly array / tuple types: modifier erased, immutability not enforced ---
const nums: readonly number[] = [10, 20, 30]
console.log(nums[0] + nums[2])   // 40

function total(xs: readonly number[]): number {
  let s = 0
  for (const x of xs) s += x
  return s
}
console.log(total(nums))         // 60

const pair: readonly [number, string] = [7, "seven"]
console.log(`${pair[0]}=${pair[1]}`)   // 7=seven

// --- explicit `this` parameter: erased; real args bind by their own positions ---
function label(this: void, name: string, n: number): string {
  return `${name}:${n}`
}
console.log(label("edges", 12))  // edges:12
