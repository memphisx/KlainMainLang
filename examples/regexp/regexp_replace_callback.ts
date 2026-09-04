// replace()/replaceAll() with a replacer callback whose parameters are
// left untyped. The replacer signature is (match, offset, string): the
// first argument is the matched substring (a string), the middle offset a
// number, and the last the whole subject string. Untyped parameters take
// those defaults, so string methods dispatch correctly on `match`.

// The matched substring is a string, not a number.
console.log("hello".replace(/l/g, (m) => m)) // hello

// String methods work on the untyped match parameter.
console.log("hello".replace(/l/g, (m) => m.toUpperCase())) // heLLo

// A constant string replacement.
console.log("hello".replace(/l/g, (m) => "X")) // heXXo

// match + numeric offset.
console.log("hello".replace(/l/g, (m, o) => `${m}@${o}`)) // hel@2l@3o

// All three: match, offset, and whole string.
console.log("a1b2".replace(/[0-9]/g, (m, o, s) => `[${m}/${o}/${s.length}]`))
// a[1/1/4]b[2/3/4]

// replaceAll behaves the same way.
console.log("a.b.c".replaceAll(/\./g, (m) => m + "-")) // a.-b.-c
