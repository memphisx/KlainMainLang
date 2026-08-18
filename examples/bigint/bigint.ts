// bigint — arbitrary-precision integers (TDD-00074).
//
// Compiled with the default backend (-bigint=libtommath, public domain);
// -bigint=gmp is an opt-in alternative with identical semantics. The backend
// library is linked only because this program actually uses bigint.

// A `123n` literal is a bigint, distinct from a `number`.
const a = 10n;
const b = 20n;

console.log(a + b); // 30n  — console.log shows the trailing `n`
console.log(typeof a); // "bigint"

// Arbitrary precision: this is exact, with no overflow at 2^63 or anywhere.
console.log(2n ** 100n); // 1267650600228229401496703205376
const huge = 123456789012345678901234567890n;
console.log(huge * huge);

// The full operator set: arithmetic (/ truncates toward zero, like JS),
// bitwise, and shifts.
console.log(17n / 5n); // 3n
console.log(-7n % 2n); // -1n
console.log(12n & 10n); // 8n
console.log(1n << 64n); // 18446744073709551616n
console.log(~5n); // -6n

// Comparisons and truthiness (0n is falsy).
console.log(10n < 20n); // true
console.log(50n > 10); // true — a bigint compares with an integer number
console.log(42n == 42); // true — loose cross-type equality (mathematical)
if (0n) {
  console.log("unreachable");
} else {
  console.log("0n is falsy");
}

// Convert to and from number/string with BigInt().
const fromNumber = BigInt(42);
const fromString = BigInt("1000000000000000000000");
console.log(fromNumber + fromString);

// String()/template interpolation render the bare digits (no `n` suffix);
// only console.log adds it.
console.log(`the answer is ${fromNumber}`); // the answer is 42

// Other bases and numeric separators work as literals too.
console.log(0xffn); // 255n
console.log(1_000_000n); // 1000000n

// A factorial, well past what an i64 could hold.
function factorial(n: bigint): bigint {
  let result = 1n;
  let i = 2n;
  while (i <= n) {
    result *= i;
    i++;
  }
  return result;
}
console.log(factorial(30n)); // 265252859812191058636308480000000

// .toString(radix) renders in another base (lowercase, like JS).
console.log((255n).toString(16)); // ff
console.log((5n).toString(2)); // 101

// Division by zero is a catchable Error, not a crash.
try {
  const zero = 0n;
  console.log(1n / zero);
} catch (e) {
  console.log(e.message); // Division by zero
}

// bigint and number deliberately don't mix: `a + 1` would be a compile error,
// exactly as `10n + 1` is a TypeError in real JS. Convert explicitly instead.
console.log(a + BigInt(1)); // 11n
