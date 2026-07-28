// Division and modulo by zero throw a catchable Error at runtime, even
// without an explicit guard around the divisor — no manual `if (b === 0)`
// check is needed, unlike examples/try_catch/try_catch.ts.
try {
  console.log(10 / 0)
} catch (e) {
  console.log('caught: ' + e.message)
}

try {
  console.log(10 % 0)
} catch (e) {
  console.log('caught: ' + e.message)
}

console.log(10 / 2)
console.log(10 % 3)
