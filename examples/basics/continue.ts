// continue skips the rest of the current loop iteration and jumps to the next one.
// In a for loop it runs the update expression first; in while it rechecks the condition.

// --- for loop: skip even numbers ---
let sumOdds: number = 0
for (let i = 0; i < 10; i++) {
    if (i % 2 === 0) {
        continue
    }
    sumOdds = sumOdds + i
}
console.log(sumOdds)  // 1+3+5+7+9 = 25

// --- for loop: collect only values above a threshold ---
let total: number = 0
for (let i = 1; i <= 10; i++) {
    if (i <= 5) {
        continue
    }
    total = total + i
}
console.log(total)  // 6+7+8+9+10 = 40

// --- while loop: skip multiples of 3 ---
let count: number = 0
let n: number = 0
while (n < 15) {
    n = n + 1
    if (n % 3 === 0) {
        continue
    }
    count = count + 1
}
console.log(count)  // 10  (15 values minus 5 multiples of 3)

// --- for...of: skip negatives ---
const vals = [-1, 2, -3, 4, -5, 6]
let positiveSum: number = 0
for (const v of vals) {
    if (v < 0) {
        continue
    }
    positiveSum = positiveSum + v
}
console.log(positiveSum)  // 2+4+6 = 12

// --- for...of: skip strings that contain a substring ---
const words = ['apple', 'banana', 'pineapple', 'cherry', 'grape']
let noApple: number = 0
for (const w of words) {
    if (w.includes('apple')) {
        continue
    }
    noApple = noApple + 1
}
console.log(noApple)  // 3  (banana, cherry, grape)

// --- continue in nested loops exits only the inner loop ---
let hits: number = 0
for (let i = 0; i < 4; i++) {
    for (let j = 0; j < 4; j++) {
        if (j === 2) {
            continue  // skips j==2, outer loop keeps running
        }
        hits = hits + 1
    }
}
console.log(hits)  // 4*3 = 12

// --- continue vs break: different behaviour ---
let firstSkip: number = 0
let firstStop: number = 0

for (let i = 0; i < 10; i++) {
    if (i === 5) {
        continue  // skips 5, loop reaches 9
    }
    firstSkip = i
}
console.log(firstSkip)  // 9

for (let i = 0; i < 10; i++) {
    if (i === 5) {
        break  // stops entirely at 5
    }
    firstStop = i
}
console.log(firstStop)  // 4

// --- continue inside a function ---
function sumAbove(arr: number[], threshold: number): number {
    let s: number = 0
    for (const v of arr) {
        if (v <= threshold) {
            continue
        }
        s = s + v
    }
    return s
}

const nums = [3, 8, 1, 9, 4, 7, 2, 6]
console.log(sumAbove(nums, 5))  // 8+9+7+6 = 30
console.log(sumAbove(nums, 0))  // all: 3+8+1+9+4+7+2+6 = 40
