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
