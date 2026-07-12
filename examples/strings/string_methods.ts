// String methods: trim, trimStart, trimEnd, toUpperCase, toLowerCase, startsWith, endsWith, replace, replaceAll, split.

// ── trim / trimStart / trimEnd ──────────────────────────────────────────────────
const padded: string = '  hello world  '
console.log(padded.trim())          // hello world

const tabbed: string = '\t  TypeGo\n'
console.log(tabbed.trim())          // TypeGo

// trimStart/trimEnd only strip one side, unlike trim().
console.log('[' + padded.trimStart() + ']')  // [hello world  ]
console.log('[' + padded.trimEnd() + ']')    // [  hello world]

// ── toUpperCase / toLowerCase ─────────────────────────────────────────────────
const mixed: string = 'Hello World'
console.log(mixed.toUpperCase())    // HELLO WORLD
console.log(mixed.toLowerCase())    // hello world

// Methods can be chained.
const raw: string = '  hello  '
console.log(raw.trim().toUpperCase())  // HELLO

// ── startsWith / endsWith ─────────────────────────────────────────────────────
const url: string = 'https://example.com'
console.log(url.startsWith('https')) // 1
console.log(url.startsWith('http://')) // 0

const file: string = 'report.pdf'
console.log(file.endsWith('.pdf'))  // 1
console.log(file.endsWith('.txt'))  // 0

// Both return false (0) when the string is shorter than the argument.
const short: string = 'hi'
console.log(short.startsWith('hello'))  // 0
console.log(short.endsWith('world'))    // 0

// ── replace ───────────────────────────────────────────────────────────────────
const greeting: string = 'Hello World'
console.log(greeting.replace('World', 'TypeGo'))  // Hello TypeGo

// Returns the original string when the search is not found.
console.log(greeting.replace('xyz', '!'))  // Hello World

// Useful for escaping.
function escapeHtml(s: string): string {
    let r: string = s.replace('&', '&amp;')
    r = r.replace('<', '&lt;')
    r = r.replace('>', '&gt;')
    return r
}
console.log(escapeHtml('<b>bold</b>'))   // &lt;b&gt;bold</b>  (only the first < and first > are replaced)

// ── replaceAll ────────────────────────────────────────────────────────────────
// Unlike replace, every non-overlapping occurrence is replaced, not just the first.
console.log('a-b-a-b'.replaceAll('-', ' '))       // a b a b
console.log('<b>bold</b>'.replaceAll('<', '&lt;').replaceAll('>', '&gt;'))
// &lt;b&gt;bold&lt;/b&gt;

// ── split ─────────────────────────────────────────────────────────────────────
const csv: string = 'one,two,three,four'
const parts: string[] = csv.split(',')
console.log(parts.length)   // 4
console.log(parts[0])       // one
console.log(parts[3])       // four

// Split on whitespace.
const sentence: string = 'the quick brown fox'
const words: string[] = sentence.split(' ')
console.log(words.length)   // 4
console.log(words[2])       // brown

// No separator occurrences → array of one element.
const single: string[] = 'hello'.split(',')
console.log(single.length)  // 1
console.log(single[0])      // hello

// Empty separator → splits into individual characters.
const chars: string[] = 'abc'.split('')
console.log(chars.length)   // 3
console.log(chars[0])       // a
console.log(chars[2])       // c

// ── padStart / padEnd with an empty fill string is a no-op ─────────────────────
console.log('ab'.padStart(5, ''))  // ab (unchanged, not padded)
console.log('ab'.padEnd(5, ''))    // ab (unchanged, not padded)

// ── type inference works without annotation ───────────────────────────────────
const upper = 'world'.toUpperCase()
console.log(upper)          // WORLD

const trimmed = '  hi  '.trim()
console.log(trimmed)        // hi
