// console.dir / time / timeEnd / count / countReset / group / groupEnd
//
// (console.log/.error/.warn/.info/.debug/.trace/.assert are covered in
// other examples — this one focuses on the newer, less basic console
// methods.)

// --- console.dir(obj, { depth?, colors? }) ---
// Prints a single value like a single-argument console.log, at Node's
// util.inspect default nesting depth of 2. The depth option overrides it
// (a number caps nesting, null is unlimited); beyond the cap a nested value
// renders as [Object]. (colors is accepted but ignored — no ANSI here.)
console.dir('hello')   // hello
console.dir(42)        // 42
const nested = { a: { b: { c: 1 } } }
console.dir(nested, { depth: 0 })     // { a: [Object] }
console.dir(nested, { depth: null })  // { a: { b: { c: 1 } } }

// --- console.time(label?) / console.timeEnd(label?) ---
// Each label tracks an independent timer (a per-label backing map),
// matching real Node — nested/concurrent timers don't clobber each
// other. The elapsed value itself
// isn't deterministic (and can even print exactly 0ms if -O2 collapses a
// simple timed loop into a closed-form constant — a known, harmless
// LLVM optimization artifact, not a bug), so it isn't checked here.
console.time('work')
let total: number = 0
for (let i = 0; i < 1000; i++) {
    total = total + i
}
console.timeEnd('work')   // work: <N>ms
console.log(total)        // 499500

// default label, if omitted, is exactly "default" (matching real Node)
console.time()
console.timeEnd()   // default: <N>ms

// --- console.count(label?) / console.countReset(label?) ---
// Unlike console.time's single-slot V1 above, count tracks every label
// independently and correctly (backed by a real Map<string, number>) —
// matching real Node's actual per-label semantics, not a narrowed V1.
console.count()            // default: 1
console.count()            // default: 2
console.count('apples')    // apples: 1
console.count()            // default: 3
console.count('apples')    // apples: 2
console.countReset('apples')
console.count('apples')    // apples: 1 — reset back to 0, then incremented

// --- console.group(label?) / console.groupEnd() ---
// Indents every console.* line (log/error/warn/info/debug/trace/assert/
// dir/time/timeEnd/count) by two spaces per active nesting level.
console.log('top level')
console.group('Section A')
console.log('inside A')
console.group('Section A.1')
console.log('inside A.1')
console.groupEnd()
console.log('back in A')
console.groupEnd()
console.log('back to top level')

// an extra, unbalanced groupEnd() is harmless — depth floors at 0, never
// goes negative
console.groupEnd()
console.log('still top level')

// every argument to a single console.log call gets its own indented line
// (this compiler prints one argument per line, not space-joined on one
// line like real Node's console.log)
console.group('multi-arg')
console.log('a', 'b', 'c')
console.groupEnd()

// --- console.table(rows) ---
// Renders an array of objects (one column per field) or an array of
// primitives (a single "Values" column) as Node's Unicode box-drawing
// table — byte-for-byte the same layout Node produces. Cells are
// left-aligned and the column is sized to its widest entry; string cells
// are shown quoted, exactly like console.log's inspector.
console.table([{ id: 1, city: 'Thessaloniki' }, { id: 2, city: 'Athens' }])
console.table([10, 20, 300])

// A non-array argument falls back to console.log, matching Node.
console.table(42)   // 42
