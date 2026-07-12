// charCodeAt string method

const s: string = 'Hello'

// basic access
console.log(s.charCodeAt(0))   // 72   H
console.log(s.charCodeAt(1))   // 101  e
console.log(s.charCodeAt(2))   // 108  l
console.log(s.charCodeAt(4))   // 111  o

// on a literal
console.log('A'.charCodeAt(0))   // 65
console.log('Z'.charCodeAt(0))   // 90
console.log('0'.charCodeAt(0))   // 48

// round-trip with String.fromCharCode
const code = 'TypeGo'.charCodeAt(0)
console.log(code)                          // 84
console.log(String.fromCharCode(code))     // T

// use in arithmetic
const lower = 'A'.charCodeAt(0) + 32
console.log(String.fromCharCode(lower))    // a

// iterate over string and print codes
const word: string = 'abc'
let i: number = 0
for (i = 0; i < word.length; i++) {
    console.log(word.charCodeAt(i))        // 97 98 99
}

// check if character is uppercase
function isUpper(ch: string): boolean {
    const c = ch.charCodeAt(0)
    return c >= 65 && c <= 90
}
console.log(isUpper('A'))   // 1
console.log(isUpper('z'))   // 0
console.log(isUpper('M'))   // 1

// codePointAt — this compiler's strings are plain byte sequences, not real
// UTF-16 like actual JS strings, so there's no surrogate-pair/multi-byte
// decoding here: codePointAt is exactly charCodeAt's byte value under a
// second name. Correct for ASCII/Latin-1 text (where a "code point" and a
// "char code" are the same number).
console.log(s.codePointAt(0))                          // 72
console.log(s.codePointAt(0) === s.charCodeAt(0))      // 1 (true)
