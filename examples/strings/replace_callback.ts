// String-literal-search replace()/replaceAll() with a function replacer
// (ADR-00697). The callback receives (match, offset, string), matching Node.

// replace() with a callback — first occurrence only.
console.log('hello'.replace('l', (m) => m.toUpperCase())) // heLlo

// replaceAll() with a callback — every occurrence.
console.log('hello'.replaceAll('l', (m) => m.toUpperCase())) // heLLo

// The offset argument is the byte position of each match.
console.log('a-b-c'.replaceAll('-', (m, o) => '/' + o + '/')) // a/1/b/3/c

// The third argument is the whole subject string.
console.log('cat'.replace('a', (m, o, s) => '[' + s + ']')) // c[cat]t

// A string-value replacer still works unchanged.
console.log('hello'.replace('l', 'L')) // heLlo
console.log('hello'.replaceAll('l', 'L')) // heLLo

// Untyped callback params default to (string, number, string).
const shout = (m: string) => m + '!'
console.log('one two one'.replaceAll('one', shout)) // one! two one!
