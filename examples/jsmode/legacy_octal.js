// Sloppy JavaScript keeps Annex B's legacy forms, which strict code (a
// "use strict" directive, a module, a class body, every TypeScript file)
// rejects as a SyntaxError.

// Legacy octal escapes, \0–\377, and \8 / \9, the digits themselves.
console.log("\101\102\103", "\8\9")   // ABC 89

// Legacy octal and leading-zero decimal literals.
console.log(010, 0777, 08, 09.5)       // 8 511 8 9.5
