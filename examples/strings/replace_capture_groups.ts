// replace()/replaceAll() with a function replacer that receives capture groups
// (ADR-00922). Real JS invokes the callback as (match, cap1..capN, offset,
// string), where N is the pattern's capture-group count.

// Swap the letters and digits of each match using the two capture groups.
console.log('abc12 def34'.replace(/([a-z]+)([0-9]+)/, (m, letters, digits) => digits + letters)) // 12abc def34

// Global: every match.
console.log('abc12 def34'.replaceAll(/([a-z]+)([0-9]+)/g, (m, letters, digits) => `${digits}${letters}`)) // 12abc 34def

// A const-bound regex literal works the same — its capture count is known.
const pair = /(\w)=(\w)/g
console.log('a=b c=d'.replaceAll(pair, (m, k, v) => `${v}=${k}`)) // b=a d=c

// After the capture groups come the offset and the whole subject string.
console.log('x5'.replace(/(\d)/, (m, digit, offset) => `${digit}@${offset}`)) // x5@1

// A non-string return value is stringified.
console.log('a1b'.replace(/1/, () => 9)) // a9b
