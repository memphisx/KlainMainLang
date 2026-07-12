// String.prototype.search / String.prototype.localeCompare

// --- .search(pattern) ---
// Real JS coerces `pattern` to a RegExp; this compiler has no RegExp type or
// regex literal syntax at all (0% implemented), so a plain string is the
// only value that could ever reach this call — meaning .search here is
// exactly .indexOf under a second name, not a partial regex implementation.
const sentence: string = 'the quick brown fox'
console.log(sentence.search('brown'))                       // 10
console.log(sentence.search('missing'))                     // -1
console.log(sentence.search('brown') === sentence.indexOf('brown'))  // 1 (true)

// --- .localeCompare(other) ---
// Byte-order comparison via strcmp, normalized to exactly -1/0/1 — not real
// Unicode collation (this compiler has no locale/Intl infrastructure), the
// same documented scope narrowing already used for toLocaleDateString.
console.log('apple'.localeCompare('banana'))   // -1 (apple sorts before banana)
console.log('banana'.localeCompare('apple'))   // 1  (banana sorts after apple)
console.log('apple'.localeCompare('apple'))    // 0  (equal)

// useful for sorting an array of strings
const words: string[] = ['banana', 'apple', 'cherry']
words.sort((a: string, b: string) => a.localeCompare(b))
for (const w of words) {
    console.log(w)   // apple, banana, cherry
}
