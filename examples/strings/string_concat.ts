// String.prototype.concat

// --- .concat(...values) ---
// The receiver followed by each argument, left to right. No argument returns
// the string itself; the call chains like any other string method.
const base: string = 'a'
console.log(base.concat())                                  // a
console.log(base.concat('b', 'c', 'd'))                     // abcd
console.log('hello '.concat('world').toUpperCase().concat('!'))  // HELLO WORLD!

// --- a spread array ---
const parts: string[] = ['p', 'q', 'r']
console.log('s:'.concat(...parts))                          // s:pqr
console.log('s:'.concat('-', ...parts, '-'))                // s:-pqr-

// --- arguments are converted with ToString ---
// Like a template literal, and unlike `+`: an object's toString() is asked
// before its valueOf().
const label = { toString() { return 'label' }, valueOf() { return 7 } }
console.log('id:'.concat(label as any))                     // id:label
console.log('id:' + label)                                  // id:7
console.log('v:'.concat(1 as any, true as any, null as any))  // v:1truenull
