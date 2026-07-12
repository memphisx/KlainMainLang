// Null coalescing (??) and optional chaining (?.)

// ?? with strings — falls back to default when left would be null
function greet(name: string): string {
    return name + '!'
}

const a: string = greet('hello') ?? 'default'
console.log(a)     // hello!

// ?? with number: non-ptr types always return left
const x: number = 42
const y: number = x ?? 99
console.log(y)     // 42

// ?. optional member access — string .length
const s: string = 'hello'
const len: number = s?.length ?? 0
console.log(len)   // 5

// Chained ?? — common pattern for providing defaults
const words: string[] = ['apple', 'banana', 'cherry']
const first: string = words[0] ?? 'none'
console.log(first) // apple

// ?? with template literal
const greeting: string = 'world'
const msg: string = greeting ?? 'stranger'
console.log(`hello, ${msg}`)   // hello, world
