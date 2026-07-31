// Logical assignment operators: &&=, ||=, ??=
// Genuinely short-circuiting — the right side only runs when the operator's
// own rule requires it, unlike +=/-=/etc. which always evaluate both sides.

// --- &&= : assign only if the current value is truthy ---
let a: number = 5
a &&= 10
console.log(a)   // 10 (5 was truthy, so a becomes 10)

let b: number = 0
b &&= 10
console.log(b)   // 0 (0 was falsy, right side never runs, b unchanged)

// --- ||= : assign only if the current value is falsy ---
let c: number = 0
c ||= 7
console.log(c)   // 7 (0 was falsy, so c becomes 7)

let d: number = 3
d ||= 7
console.log(d)   // 3 (3 was truthy, right side never runs, d unchanged)

// --- ??= : assign only if the current value is null ---
let e: string | null = null
e ??= 'default'
console.log(e)   // default

let f: string | null = 'keep'
f ??= 'default'
console.log(f)   // keep

// --- The right side genuinely doesn't run when short-circuited ---
function loud(): number {
    console.log('evaluated')
    return 99
}
let g: number = 1
g ||= loud()      // g is truthy, loud() never called, nothing printed
console.log(g)    // 1

let h: number = 0
h ||= loud()      // h is falsy, loud() runs, prints "evaluated"
console.log(h)    // 99

// --- Works against object fields, array elements, and static class fields too ---
interface Box { val: number }
const box: Box = { val: 0 }
box.val ||= 42
console.log(box.val)  // 42

const arr: number[] = [0, 5]
arr[0] ||= 99
arr[1] &&= 3
console.log(arr[0])   // 99
console.log(arr[1])   // 3

class Counter {
    static count: number;
    static {
        Counter.count = 0
    }
}
Counter.count ||= 100
console.log(Counter.count)  // 100
