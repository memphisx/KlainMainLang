// Object.groupBy examples

// ── group strings by first character ─────────────────────────────────────────
const words: string[] = ['apple', 'avocado', 'banana', 'blueberry', 'cherry', 'apricot']
const byLetter = Object.groupBy(words, (w) => w.substring(0, 1))

const keys = Object.keys(byLetter)
console.log(keys.length)          // 3

// access each group by key
const aWords = byLetter['a']
console.log(aWords.length)        // 3  (apple, avocado, apricot)
console.log(aWords[0])            // apple
console.log(aWords[1])            // avocado
console.log(aWords[2])            // apricot

const bWords = byLetter['b']
console.log(bWords.length)        // 2
console.log(bWords[0])            // banana
console.log(bWords[1])            // blueberry

const cWords = byLetter['c']
console.log(cWords.length)        // 1
console.log(cWords[0])            // cherry

// ── iterate all groups via Object.keys ────────────────────────────────────────
let total: number = 0
let k: number = 0
for (k = 0; k < keys.length; k++) {
    const group = byLetter[keys[k]]
    total = total + group.length
}
console.log(total)                // 6  (all words accounted for)

// ── group numbers by even/odd ─────────────────────────────────────────────────
const nums: number[] = [1, 2, 3, 4, 5, 6, 7, 8]

function parity(n: number): string {
    if (n % 2 === 0) {
        return 'even'
    }
    return 'odd'
}

const byParity = Object.groupBy(nums, parity)
const evenNums = byParity['even']
const oddNums  = byParity['odd']

console.log(evenNums.length)      // 4
console.log(oddNums.length)       // 4
console.log(evenNums[0])          // 2
console.log(evenNums[3])          // 8
console.log(oddNums[0])           // 1
console.log(oddNums[3])           // 7
