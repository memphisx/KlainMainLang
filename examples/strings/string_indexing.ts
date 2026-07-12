// String indexing: s[i] returns a one-character string at position i.

// Index a local variable
const word: string = 'hello'
const first = word[0]
console.log(first)  // h

let i: number = 4
const last = word[i]
console.log(last)  // o

// Index a string literal via a function parameter
function charAt(s: string, n: number): string {
    return s[n]
}
console.log(charAt('world', 1))  // o
console.log(charAt('abc', 2))    // c

// Use an indexed character in a comparison
const s: string = 'cat'
if (s[0] === 'c') {
    console.log('starts with c')
}

// Walk each character with a for loop
function reverseLog(str: string): void {
    for (let j: number = str.length - 1; j >= 0; j--) {
        console.log(str[j])
    }
}
reverseLog('hi')  // i then h

// Extract a character and test it in a helper
function isVowel(ch: string): boolean {
    return ch === 'a' || ch === 'e' || ch === 'i' || ch === 'o' || ch === 'u'
}

const test: string = 'apple'
const c = test[0]
console.log(isVowel(c))   // 1  (a is a vowel)
console.log(isVowel(test[2]))  // 0  (p is not a vowel)

// The built-in .charAt(i) method behaves like s[i] for an in-range index,
// but — unlike bracket indexing or .at() — never wraps a negative index
// from the end, and returns "" (not undefined) for any out-of-range index.
const greeting: string = 'hi'
console.log(greeting.charAt(0))         // h
console.log(greeting.charAt(0) === greeting[0])   // 1 (true)
console.log("[" + greeting.charAt(10) + "]")   // [] — out of range, empty string
console.log("[" + greeting.charAt(-1) + "]")   // [] — negative never wraps
