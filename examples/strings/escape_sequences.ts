// String-literal escape sequences (ADR-00194).
//
// A string (or template) literal decodes the full ES escape grammar into
// this compiler's UTF-8 string bytes.

// Simple escapes.
console.log("tab\tafter")
console.log("line1\nline2")
console.log("quote: \" and backslash: \\")

// Hex escape (\xHH) — one Latin-1 code unit.
console.log("\x48\x69")                 // Hi

// Unicode escapes: fixed 4-digit (\uHHHH) and variable (\u{H...}).
console.log("\u0041\u0042\u0043 World")   // ABC World
console.log("\u{1F680} launch")         // 🚀 launch

// A backslash before a non-escape character is just that character
// (NonEscapeCharacter), and \8 / \9 are the digits themselves.
console.log("\A\B\C \8\9")              // ABC 89

// Legacy octal escapes (Annex B): \0–\377.
console.log("\101\102\103")             // ABC (0o101 = 65 = 'A', ...)

// A backslash at end of line continues the string (LineContinuation).
console.log("joined \
together")                              // joined together

// Templates decode the exact same grammar.
const who = "there"
console.log(`\x48ello ${who}!`)    // Hello there!
