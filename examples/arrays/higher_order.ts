// Array higher-order methods: map, filter, reduce, find, some, every, join, forEach.

const nums: number[] = [1, 2, 3, 4, 5]

// ── map ───────────────────────────────────────────────────────────────────────
const doubled = nums.map(x => x * 2)
console.log(doubled[0])  // 2
console.log(doubled[4])  // 10

// ── filter ────────────────────────────────────────────────────────────────────
const evens = nums.filter(x => x % 2 === 0)
console.log(evens.length)  // 2
console.log(evens[0])      // 2
console.log(evens[1])      // 4

// ── reduce ────────────────────────────────────────────────────────────────────
const sum = nums.reduce((acc, n) => acc + n, 0)
console.log(sum)  // 15

const product = nums.reduce((acc, n) => acc * n, 1)
console.log(product)  // 120

// ── find ──────────────────────────────────────────────────────────────────────
const first3 = nums.find(x => x > 3)
console.log(first3)  // 4

// ── some ──────────────────────────────────────────────────────────────────────
const hasEven = nums.some(x => x % 2 === 0)
console.log(hasEven)  // 1

const allPositive = nums.every(x => x > 0)
console.log(allPositive)  // 1

const allEven = nums.every(x => x % 2 === 0)
console.log(allEven)  // 0

// ── join ──────────────────────────────────────────────────────────────────────
const words: string[] = ['hello', 'world', 'typego']
const joined = words.join(', ')
console.log(joined)  // hello, world, typego

const nums2: number[] = [1, 2, 3]
const csv = nums2.join(',')
console.log(csv)  // 1,2,3

// ── forEach ───────────────────────────────────────────────────────────────────
nums2.forEach(x => {
  console.log(x * 10)  // 10, 20, 30
})

nums2.forEach((x, i) => {
  console.log(`${i}: ${x}`)  // 0: 1, 1: 2, 2: 3
})

let total = 0
nums2.forEach(x => {
  total += x
})
console.log(total)  // 6

// ── Unannotated callback parameters over a non-numeric (string[]) array ───────
// Every HOF method propagates the array's element type as a hint to an
// unannotated callback parameter — without this, `n` below would silently
// default to `number`, and `n.length` would fail since a number has no
// `.length`. Also covers an expression-bodied callback whose only statement
// is a void-returning call like console.log (a common forEach shape).
const names: string[] = ['apple', 'bob', 'cat']

names.forEach(n => console.log(n))  // apple, bob, cat

const lengths = names.map(n => n.length)
console.log(lengths[0])  // 5
console.log(lengths[2])  // 3

const shortOnes = names.filter(n => n.length === 3)
console.log(shortOnes[0])  // bob
console.log(shortOnes[1])  // cat

console.log(names.find(n => n.length === 3))       // bob
console.log(names.some(n => n.length === 3))        // 1
console.log(names.every(n => n.length <= 5))        // 1
console.log(names.findIndex(n => n.length === 3))   // 1

const totalLen = names.reduce((acc, n) => acc + n.length, 0)
console.log(totalLen)  // 11

const joined2 = names.reduce((acc, n) => acc + n, '')
console.log(joined2)  // applebobcat
