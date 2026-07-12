// =============================================================================
// Basics: literals, variables, concatenation, comparison
// =============================================================================

let hello: string = 'hello'
let world: string = 'world'
console.log(hello)   // hello
console.log(world)   // world

// Concatenation
let hw: string = hello + ' ' + world
console.log(hw)       // hello world

let greeting: string = 'Hi, ' + hello + '!'
console.log(greeting) // Hi, hello!

// Equality comparison
let a: string = 'foo'
let b: string = 'foo'
let c: string = 'bar'

console.log(a === b)  // 1 (true)
console.log(a === c)  // 0 (false)
console.log(a !== c)  // 1 (true)
console.log(a !== b)  // 0 (false)

// Ordering comparison
console.log(a > c)    // 1  ('foo' > 'bar')
console.log(c < a)    // 1  ('bar' < 'foo')
console.log(a <= b)   // 1  ('foo' <= 'foo')
console.log(a >= c)   // 1  ('foo' >= 'bar')

// String as function parameter and return
function greet(name: string): string {
    return 'Hello, ' + name + '!'
}
console.log(greet('Alice'))  // Hello, Alice!
console.log(greet('Alexandros'))    // Hello, Alexandros!

// String in conditional
let lang: string = 'TypeScript'
if (lang === 'TypeScript') {
    console.log('correct')  // correct
} else {
    console.log('wrong')
}

// =============================================================================
// .length and .slice()
// =============================================================================

let s: string = 'hello'
console.log(s.length)              // 5
console.log('world'.length)        // 5
console.log(''.length)             // 0

let msg: string = 'Hello, World!'
console.log(msg.length)            // 13

// .slice(start)
console.log(msg.slice(7))          // World!
console.log(s.slice(1))            // ello
console.log(s.slice(0))            // hello

// .slice(start, end)
console.log(msg.slice(0, 5))       // Hello
console.log(msg.slice(7, 12))      // World
console.log(s.slice(1, 3))         // el

// .length in expressions
let x: string = 'foo'
let y: string = 'foobar'
console.log(y.length - x.length)   // 3

// .slice result used in concatenation
let tag: string = '<b>' + msg.slice(0, 5) + '</b>'
console.log(tag)                   // <b>Hello</b>

// .length in a function
function longer(x: string, y: string): string {
    if (x.length >= y.length) {
        return x
    }
    return y
}
console.log(longer('hi', 'hello'))        // hello
console.log(longer('typescript', 'go'))   // typescript

// .slice in a function
function firstN(str: string, n: number): string {
    return str.slice(0, n)
}
console.log(firstN('abcdef', 3))   // abc
console.log(firstN('hello', 4))    // hell

// =============================================================================
// .indexOf() and .includes()
// =============================================================================

let haystack: string = 'hello world'

// indexOf: found
console.log(haystack.indexOf('hello'))   // 0
console.log(haystack.indexOf('world'))   // 6
console.log(haystack.indexOf('o'))       // 4
console.log(haystack.indexOf('llo'))     // 2

// indexOf: not found
console.log(haystack.indexOf('xyz'))     // -1
console.log(haystack.indexOf('HELLO'))   // -1

// indexOf of empty string
console.log(haystack.indexOf(''))        // 0

// includes: true
console.log(haystack.includes('hello'))  // 1
console.log(haystack.includes('world'))  // 1
console.log(haystack.includes('o w'))    // 1

// includes: false
console.log(haystack.includes('xyz'))    // 0
console.log(haystack.includes('HELLO'))  // 0

// indexOf return value used in condition
let path: string = 'src/main.ts'
if (path.indexOf('.ts') !== -1) {
    console.log('typescript')     // typescript
}

// includes in condition
let csv: string = 'apple,banana,cherry'
if (csv.includes('banana')) {
    console.log('has banana')     // has banana
}
if (!csv.includes('grape')) {
    console.log('no grape')       // no grape
}

// indexOf used to compute a slice
let url: string = 'https://example.com/page'
let slashIdx: number = url.indexOf('//')
console.log(url.slice(slashIdx + 2))  // example.com/page

// startsWith implemented with indexOf
function startsWith(str: string, prefix: string): boolean {
    return str.indexOf(prefix) === 0
}
console.log(startsWith('typescript', 'type'))  // 1
console.log(startsWith('typescript', 'go'))    // 0
