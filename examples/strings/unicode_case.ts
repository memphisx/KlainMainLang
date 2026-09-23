// toUpperCase / toLowerCase are the Unicode Default Case Conversion, as in
// Node: simple mappings on every plane, the SpecialCasing expansions (a single
// character can become two or three), and the context-sensitive final sigma.

console.log("ß".toUpperCase());              // SS
console.log("İ".toLowerCase() === "i̇"); // true  (i + combining dot above)
console.log("ﬁ".toUpperCase());              // FI
console.log("ŉ".toUpperCase());              // ʼN
console.log("ΟΔΥΣΣΕΥΣ".toLowerCase());        // οδυσσευς  (final ς, medial σ)
console.log("ǆ".toUpperCase(), "Ǆ".toLowerCase()); // Ǆ ǆ
console.log("straße".toUpperCase());         // STRASSE
console.log("𐐀𐐨".toLowerCase());             // 𐐨𐐨  (Deseret, outside the BMP)
console.log("ⓐ".toUpperCase(), "Ⅷ".toLowerCase()); // Ⓐ ⅷ
