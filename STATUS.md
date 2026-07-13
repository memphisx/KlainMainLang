# KlainMainLang — Implementation Status

> TypeScript → native compiler written in Go. Emits LLVM IR text, compiled with `clang -O2`.
> Targets whatever architecture the host clang defaults to (arm64 on Apple Silicon, x86-64 on Linux, etc.).
> Multi-file compilation exists (named `import`/`export` only, V1 scope — see the Modules section below); the entry file's top-level statements still all run in one `main()`, and imported files may only contain declarations.
> No garbage collector — every heap allocation is `malloc`'d and (almost) never `free`d. See "Memory Management" below.
> Programs are pure libc by default; a program only needs `libcurl` installed on the build machine if it actually calls `fetch` (compiled binaries automatically link `-lcurl` only when used — see `docs/adr/ADR-00020.md`).

## Contents

- [What Is Implemented](#what-is-implemented) — core JavaScript/TypeScript language & standard library (works the same in any JS host)
- [Web Platform APIs](#web-platform-apis) — WHATWG/browser-standard APIs (also implemented by Node.js, but not part of the JS *language* itself)
- [Node.js APIs](#nodejs-apis) — `fs`, `process`, and the scoped-but-not-started HTTP server — Node-specific runtime globals with no browser equivalent
- [What Is NOT Implemented](#what-is-not-implemented) — core language gaps, by priority/complexity
- [Known Limitations & Bugs](#known-limitations--bugs)
- [Design Documents (TDDs)](#design-documents-tdds)
- [Coverage Summary](#coverage-summary)
- [Roadmap](#roadmap)

---

## What Is Implemented

### Language Constructs

| Feature | Status | Notes |
|---|---|---|
| `const` / `let` / `var` declarations | ✅ | All three treated as mutable allocas |
| Numeric literals (`42`, `3.14`, `0xFF`, `0b101`, `0o77`) | ✅ | |
| String literals (single/double quote) | ✅ | |
| Boolean literals (`true` / `false`) | ✅ | |
| `null` literal | ✅ | `T \| null` union type supported |
| `undefined` literal | ✅ | |
| Template literals `` `Hello ${name}` `` | ✅ | Arbitrary interpolation depth |
| Arithmetic operators `+ - * / % **` | ✅ | |
| Comparison operators `== === != !== < > <= >=` | ✅ | String comparison via `strcmp` |
| Logical operators `&& \|\| !` | ✅ | Short-circuit evaluation |
| Bitwise operators `& \| ^ ~ << >> >>>` | ✅ | |
| Assignment operators `+= -= *= /= %= &= \|= ^= <<= >>= >>>=` | ✅ | |
| Increment / decrement `++ --` (prefix & postfix) | ✅ | |
| Ternary `cond ? a : b` | ✅ | |
| Nullish coalescing `??` | ✅ | Works on `T \| null` and string |
| Optional chaining `?.` | ✅ | Null-guards ptr fields; returns null on null receiver |
| `typeof` operator | ✅ | Compile-time constant; resolved from inferred type |
| `if` / `else if` / `else` | ✅ | |
| `while` loop | ✅ | |
| `do…while` loop | ✅ | |
| `for (init; cond; update)` | ✅ | |
| `for…of` over arrays, `Map` (iterates values), and `Set` (iterates elements) | ✅ | No `[key,value]` destructuring in for-of, so Map iterates values, not entries — use `.keys()` for keys; see `docs/adr/ADR-00011.md` |
| `for…in` over object keys | ✅ | |
| `switch` / `case` / `default` / `break` | ✅ | Numeric, string, and boolean discriminants |
| `break` / `continue` in loops, including labeled (`outer: for (...) { break outer; }`) | ✅ | See `docs/adr/ADR-00010.md` |
| `return` | ✅ | Typed; `void` implicit return handled |
| `throw new Error(msg)` | ✅ | Via `setjmp` / `longjmp` |
| `try` / `catch` / `finally` | ✅ | Single catch variable; `finally` always runs |
| Function declarations (top-level) | ✅ | Named, typed params, typed return |
| Arrow functions / lambdas | ✅ | Full closures; captures via heap-allocated env struct |
| Default parameter values | ✅ | |
| Optional parameters (`param?`) | ✅ | |
| Rest parameters (`...args: number[]`) | ✅ | |
| Spread in array literals `[...a, ...b]` | ✅ | |
| Array destructuring `const [a, b] = arr` | ✅ | |
| Object destructuring `const { x, y } = obj` | ✅ | |
| `async` functions | ✅ | Returns `Promise<T>`; malloc-based slot. Named `async function` declarations and `async (...) => ...` arrow functions both supported (the arrow-function case was a real gap found and fixed alongside `docs/adr/ADR-00050.md` — it silently returned its value unwrapped instead of via the Promise slot). |
| `await` expressions | ✅ | Loads value from slot, frees it — except `await` on `fetch()`'s own `Promise<Response>`, which really waits (yielding via a fiber if inside an `http.listen` connection handler) since `docs/adr/ADR-00050.md`, not just an already-resolved read. |
| Enums (numeric) | ✅ | Auto-increment, explicit values |
| Enums (string) | ✅ | |
| Interfaces (structural) | ✅ | Heap-allocated objects |
| Type aliases | ✅ | |
| Object literals `{ key: value }` | ✅ | |
| `new Error(msg)` | ✅ | |
| `new Array<T>(n)` | ✅ | |
| `new Map<K,V>()` | ✅ | |
| `new Set<T>()` | ✅ | |

### Modules

Whole-program compilation, not separate compilation units: `resolver.ResolveProgram` parses the entry file plus everything it transitively imports and merges them into one `*ast.Program` before codegen runs — there is no linker step, no per-file LLVM module boundary, and `codegen/llvm` never sees an `import`/`export` node. See `docs/adr/ADR-00022.md` and `resolver/resolver.go`'s package doc for the full design.

| Feature | Status | Notes |
|---|---|---|
| `export function` / `const`/`let`/`var` / `interface` / `type` / `enum` | ✅ | A declaration-level modifier, nothing more — consumed entirely by the resolver |
| `import { a, b } from './relative/path'` | ✅ | Named imports only; relative paths only (`./`, `../`), `.ts` auto-appended if omitted; resolved against the importing file's own directory, not `cwd` |
| Circular imports | ✅ | Supported for the declarations-only case — verified directly with two files calling each other's exported functions |
| Diamond-shaped import graphs | ✅ | A file imported from multiple places is parsed once and merged once (memoized by absolute path) |
| Imported (non-entry) files may run top-level side-effecting code | ❌ | **Deliberate V1 scope narrowing, not an oversight** — imported files may only contain declarations (and their own imports); only the entry file's top-level statements execute. Real ES modules run a file's top-level code once, in dependency order, the first time it's imported — that "run once, in order, guard against re-running on cycles" semantics is real design/implementation work of its own, intentionally deferred. **Revisit this later**: build the fuller, real-ES-modules-shaped version, possibly gated behind a compiler flag/configuration so callers can choose between the fast/simple current behavior and full module-execution semantics once both exist. |
| True per-file module scope (mangled internal names) | ❌ | All top-level declaration names must currently be unique across *every* merged file, not just within one file — there's no real per-file scoping yet, so two unrelated files can't both declare a same-named function/interface/enum if both end up reachable from the same entry file. A real fix needs per-file symbol registries and internal name mangling (sketched in `ADR-00022`'s Investigation) — bigger than V1's scope. |
| Import aliasing (`import { a as b }`) | ❌ | Parsed, but rejected with a clear error — no AST-level renaming is attempted (risk of colliding with local shadowing in the importing file) |
| `export default` | ❌ | Not implemented |
| `import * as ns from '...'` (namespace import) | ❌ | Not implemented |
| Re-exports (`export { x } from './other'`) | ❌ | Not implemented |
| Bare/package-style imports (`import x from 'somepackage'`) | ❌ | No package ecosystem here — only relative paths resolve to anything |

### Type System

| Feature | Status | Notes |
|---|---|---|
| `number` → `i64` | ✅ | |
| `string` → `ptr` | ✅ | |
| `boolean` → `i1` | ✅ | |
| `void` | ✅ | |
| `null` / `undefined` | ✅ | Sentinel `ptr null` |
| `T \| null` (nullable) | ✅ | Nullable flag; only one non-null branch |
| Object types (interfaces / inline `{}`) | ✅ | |
| Array types `T[]` | ✅ | `{ptr, i64}` aggregate |
| `Promise<T>` | ✅ | |
| Function types `(a: T) => R` | ✅ | Closure struct `{funcPtr, envPtr}` |
| JSDoc extended integers | ✅ | `@type {int8\|int16\|int32\|int64\|uint8…uint64}` |
| JSDoc extended floats | ✅ | `@type {float32\|float64}` |
| `Map<K,V>` | ✅ | Separate helpers for `<string,number>`, `<string,string>`, etc. |
| `Set<T>` | ✅ | |
| Union types beyond `T \| null` | ❌ | Parser discards every union member except the first for anything other than `null`/`undefined` — needs parser work, separate from the `any`/`unknown` tagged-value system below; see `docs/adr/ADR-00008.md` |
| Intersection types | ❌ | |
| Tuple types | ❌ | |
| Mapped / conditional types | ❌ | |
| `any` | ✅ (Staged V1: declare/assign/reassign/print/`typeof`/`===`; arithmetic and use as a function param/return/array/object-field type are ❌ with a clean compile error — see `docs/adr/ADR-00008.md`) | |
| `never` | ✅ | A function typed `(): never` that always throws works correctly |
| `unknown` | ✅ (same Staged V1 scope as `any` — see above) | |
| `symbol` | ❌ | |
| `bigint` | ❌ | |
| Generics on user functions/interfaces | ❌ | Only built-in generics (`T[]`, `Promise<T>`) |

### String Methods

| Method | Status |
|---|---|
| `+` (concatenation) | ✅ |
| `.length` | ✅ |
| `.slice(start, end?)` | ✅ |
| `.substring(start, end?)` | ✅ |
| `.indexOf(substr)` | ✅ |
| `.includes(substr)` | ✅ |
| `.startsWith(prefix)` | ✅ |
| `.endsWith(suffix)` | ✅ |
| `.replace(from, to)` | ✅ |
| `.split(sep)` | ✅ (empty separator splits into individual characters, matching JS — previously hung; see `docs/adr/ADR-00004.md`) |
| `.trim()` | ✅ |
| `.trimStart()` / `.trimEnd()` | ✅ |
| `.toUpperCase()` | ✅ |
| `.toLowerCase()` | ✅ |
| `.repeat(n)` | ✅ |
| `.padStart(len, pad?)` | ✅ (empty pad string is a no-op, matching JS — previously corrupted output; see `docs/adr/ADR-00004.md`) |
| `.padEnd(len, pad?)` | ✅ (same empty-pad fix as `.padStart`) |
| `.charCodeAt(i)` | ✅ |
| `.at(i)` | ✅ |
| `.charAt(i)` | ✅ (unlike `.at()`, never wraps a negative index from the end — always `""` for any out-of-range `i`, matching real JS's distinction between the two methods. See `docs/adr/ADR-00028.md`.) |
| `.codePointAt(i)` | ✅ (this compiler's strings are plain byte sequences, not real UTF-16 — no surrogate-pair/multi-byte decoding, so this is exactly `.charCodeAt(i)`'s byte value under a second name. Correct for ASCII/Latin-1 text; a documented scope narrowing for anything needing real Unicode decoding. See `docs/adr/ADR-00028.md`.) |
| `.normalize()` | ❌ (deliberately deferred, not attempted — needs real Unicode normalization tables (NFC/NFD/NFKC/NFKD) this compiler has no infrastructure for at all; a fake identity-only implementation would silently mis-normalize any non-ASCII composed/decomposed text, exactly the "silent wrong output" failure mode this project avoids) |
| `.match()` / `.matchAll()` | ❌ (needs a real `RegExp` engine — tracked separately, see "What Is NOT Implemented" below) |
| `.search(pattern)` | ✅ (real JS coerces `pattern` to a `RegExp`; this compiler has no `RegExp` type or regex literal syntax at all, so a plain string is the *only* value that could ever reach this call — making this exactly `.indexOf`'s behavior under a second name, not a partial regex implementation. See `docs/adr/ADR-00028.md`.) |
| `.replaceAll()` | ✅ (empty search is a no-op, not JS's insert-between-chars behavior — see `docs/adr/ADR-00003.md`) |
| `.localeCompare(other)` | ✅ (byte-order comparison via `strcmp`, normalized to exactly `-1`/`0`/`1` — not real Unicode collation, this compiler has no locale/`Intl` infrastructure, the same scope narrowing already used for `toLocaleDateString`. See `docs/adr/ADR-00028.md`.) |
| `String.fromCharCode(n)` | ✅ |
| `String.fromCodePoint(n)` | ✅ |
| `String.raw` tag | ❌ |

### Array Methods

| Method | Status |
|---|---|
| Literal `[a, b, c]` | ✅ |
| `new Array<T>(n)` | ✅ |
| `.length` | ✅ |
| `.push(...items)` | ✅ |
| `.pop()` | ✅ |
| `.shift()` | ✅ |
| `.unshift(...items)` | ✅ |
| `.splice(start, delete?, ...items)` | ✅ |
| `.slice(start, end?)` | ✅ |
| `.at(i)` | ✅ |
| `.indexOf(item)` | ✅ |
| `.includes(item)` | ✅ |
| `.find(fn)` | ✅ |
| `.findIndex(fn)` | ✅ |
| `.some(fn)` | ✅ |
| `.every(fn)` | ✅ |
| `.map(fn)` | ✅ |
| `.filter(fn)` | ✅ |
| `.reduce(fn, init?)` | ✅ |
| `.forEach(fn)` | ✅ |
| `.join(sep?)` | ✅ |
| `.sort(fn?)` | ✅ |
| `.reverse()` | ✅ |
| `.fill(val, start?, end?)` | ✅ |
| `.concat(...arrays)` | ✅ |
| `.flat(depth?)` | ❌ |
| `.flatMap(fn)` | ❌ |
| `.findLast(fn)` / `.findLastIndex(fn)` | ❌ |
| `.toSorted()` / `.toReversed()` / `.toSpliced()` | ❌ |
| `.with(i, val)` | ❌ |
| `.keys()` / `.values()` / `.entries()` | ❌ |
| `.copyWithin()` | ❌ |
| `Array.isArray(x)` | ✅ |
| `Array.from(iterable)` | ❌ |
| `Array.of(...items)` | ❌ |

### Number / Math

| Feature | Status |
|---|---|
| `Number.isInteger(x)` | ✅ |
| `Number.isFinite(x)` | ✅ |
| `Number.isNaN(x)` | ✅ |
| `Number.isSafeInteger(x)` | ✅ |
| `Number.parseInt(s)` | ✅ |
| `Number.parseFloat(s)` | ✅ |
| `Number.MAX_SAFE_INTEGER` | ✅ |
| `Number.MIN_SAFE_INTEGER` | ✅ |
| `Number.EPSILON` | ✅ |
| `Number.MAX_VALUE` | ✅ |
| `Number.MIN_VALUE` | ✅ |
| `Number.POSITIVE_INFINITY` | ✅ |
| `Number.NEGATIVE_INFINITY` | ✅ |
| `Number.NaN` | ✅ |
| `Number.prototype.toFixed(n)` | ✅ |
| `Number.prototype.toString(radix?)` | ❌ |
| `Number.prototype.toPrecision(n)` | ❌ |
| `Number.prototype.toExponential(n)` | ❌ |
| `parseInt(s, radix?)` (global) | ✅ |
| `parseFloat(s)` (global) | ✅ |
| `isNaN(x)` (global) | ✅ |
| `isFinite(x)` (global) | ✅ |
| `Math.floor/ceil/round/trunc` | ✅ |
| `Math.abs` | ✅ |
| `Math.sqrt/pow/hypot` | ✅ |
| `Math.log/log2/log10` | ✅ |
| `Math.sin/cos/tan` | ✅ |
| `Math.min/max` | ✅ |
| `Math.sign` | ✅ |
| `Math.random()` | ✅ |
| `Math.PI/E/LN2/LN10/SQRT2/LOG2E/LOG10E` | ✅ |
| `Math.cbrt/expm1/log1p` | ✅ |
| `Math.asin/acos/atan/atan2` | ✅ |
| `Math.sinh/cosh/tanh` | ✅ |
| `Math.clz32/fround/imul` | ❌ |

### Object / Collections

| Feature | Status |
|---|---|
| Object literals `{ a: 1 }` | ✅ |
| Field access `obj.field` | ✅ |
| Object destructuring | ✅ |
| `Object.keys(obj)` | ✅ |
| `Object.values(obj)` | ✅ |
| `Object.entries(obj)` | ✅ |
| `Object.groupBy(arr, fn)` | ✅ |
| `Object.assign()` | ❌ |
| `Object.create()` | ❌ |
| `Object.freeze()` / `.seal()` | ❌ |
| `Object.hasOwn()` / `.hasOwnProperty()` | ❌ |
| `Object.fromEntries()` | ❌ |
| Object spread `{ ...obj, key: val }` | ✅ |
| Computed property keys | ❌ |
| Shorthand property `{ x }` | ✅ |
| `Map.set/get/has/delete/keys/values` | ✅ |
| `Map.size` | ✅ |
| `Map.entries()` / `.forEach()` | ❌ |
| `Map.clear()` | ❌ |
| `Set.add/has/delete/values` | ✅ |
| `Set.size` | ✅ |
| `Set.forEach()` | ❌ |
| `Set.clear()` | ❌ |
| `WeakMap` / `WeakSet` / `WeakRef` | ❌ |

### JSON

| Feature | Status |
|---|---|
| `JSON.stringify(number)` | ✅ |
| `JSON.stringify(string)` | ✅ |
| `JSON.stringify(number[])` | ✅ |
| `JSON.stringify(string[])` | ✅ |
| `JSON.stringify(object)` | ✅ |
| `JSON.stringify(boolean[])` | ✅ |
| `JSON.stringify(object[])` | ✅ |
| `JSON.parse(s)` → number | ✅ |
| `JSON.parse(s)` → object | ✅ (flat objects, primitive fields only — nested object fields give a clean compile error; see `docs/adr/ADR-00007.md`) — a missing *string* field's default was fixed from a crash-causing `null` to an empty string; see `docs/adr/ADR-00024.md` |

### console

| Feature | Status |
|---|---|
| `console.log(...)` | ✅ |
| `console.error(...)` | ✅ (stderr) |
| `console.warn(...)` | ✅ (stderr) |
| `console.info(...)` | ✅ |
| `console.debug(...)` | ✅ |
| `console.trace(...)` | ✅ |
| `console.assert(cond, msg)` | ✅ |
| `console.table()` | ❌ (deliberately deferred, not attempted — needs a genuinely new algorithm (dynamic per-column width computation, box-drawing header/index rows over arbitrarily-shaped input), not a quick extension of existing print machinery like the other rows below) |
| `console.time()` / `.timeEnd()` | ✅ (V1 scope: a single global monotonic-time slot, not a per-label map — calling `time()` again overwrites the one running timer regardless of label. See `docs/adr/ADR-00029.md`.) |
| `console.count()` / `.countReset()` | ✅ (backed by a real `Map<string, number>` — matches real Node's per-label semantics exactly, unlike `time`'s single-slot narrowing above. See `docs/adr/ADR-00029.md`.) |
| `console.group()` / `.groupEnd()` | ✅ (indents every subsequent `console.*` line by two spaces per nesting level; an unbalanced extra `groupEnd()` floors at depth 0 rather than going negative. See `docs/adr/ADR-00029.md`.) |
| `console.dir()` | ✅ (prints a single value exactly like a single-argument `console.log`; the real API's second `options` argument — depth/color controls — is accepted syntactically but ignored. See `docs/adr/ADR-00029.md`.) |

### Global Functions & Constants

JavaScript language-level globals unrelated to any browser API.

| Feature | Status | Notes |
|---|---|---|
| `isNaN(x)` | ✅ | |
| `isFinite(x)` | ✅ | |
| `parseInt(s, radix?)` | ✅ | |
| `parseFloat(s)` | ✅ | |
| `NaN` (global constant) | ✅ | A local variable of the same name still shadows it. See `docs/adr/ADR-00024.md`. |
| `Infinity` (global constant) | ✅ | Same shadowing rule as `NaN`. See `docs/adr/ADR-00024.md`. |
| `undefined` (global constant) | ✅ | As a literal value |
| `globalThis` | ❌ | Not meaningful in a native single-file context |
| `encodeURI(s)` | ✅ | Leaves the unreserved *and* reserved (`;/?:@&=+$,#`) character sets unescaped. See `docs/adr/ADR-00024.md`. |
| `decodeURI(s)` | ✅ | Does **not** decode a `%XX` escape representing a reserved character (leaves it as literal `%XX` text) — the one real behavioral difference from `decodeURIComponent`. Permissive on malformed input (passes a bad/truncated escape through as literal text) rather than throwing a `URIError`. See `docs/adr/ADR-00024.md`. |
| `encodeURIComponent(s)` | ✅ | Leaves only the unreserved set (letters, digits, `-_.!~*'()`) unescaped. See `docs/adr/ADR-00024.md`. |
| `decodeURIComponent(s)` | ✅ | Decodes every valid `%XX` escape unconditionally. See `docs/adr/ADR-00024.md`. |
| `atob(s)` | ✅ | Base64 decode. Permissive: malformed length/characters decode as best-effort rather than throwing. Operates byte-for-byte on the input string (this compiler's strings are already plain byte sequences — no separate "binary string" type needed). See `docs/adr/ADR-00024.md`. |
| `btoa(s)` | ✅ | Base64 encode, `=`-padded (RFC 4045). See `docs/adr/ADR-00024.md`. |
| `structuredClone(obj)` | ❌ | Deep copy; medium complexity |
| `queueMicrotask(fn)` | ❌ | Needs event loop |
| `eval(s)` | ❌ | Won't implement (requires a JIT) |

---

## Web Platform APIs

WHATWG/W3C-standard APIs — the kind a browser **and** Node.js both implement (`fetch`, `URL`, `TextEncoder`, `crypto.getRandomValues`, streams, timers, …). Not part of the JS *language* itself (ECMA-262), but not Node-specific either. Filtered to those that make sense outside a browser context (i.e. useful in server-side / native / CLI TypeScript); pure browser-only APIs (DOM, Canvas, WebGL, CSS, Gamepad, etc.) are excluded as out of scope for a native compiler.

Node.js's own runtime-specific globals (`fs`, `process`, the future `http` server) are **not** in this section — see [Node.js APIs](#nodejs-apis) below.

Entries below are ❌ not yet implemented unless marked otherwise. They are listed here to track scope and inform the roadmap.

### Timers

`setTimeout`/`setInterval` needed a sleep-until-next-due loop, not the full general-purpose event loop — see [`docs/tdd/TDD-00002.md`](docs/tdd/TDD-00002.md) for the full design. `setImmediate`/`queueMicrotask` are a separate, smaller follow-on, not yet picked up.

| API | Status | Notes |
|---|---|---|
| `setTimeout(fn, ms)` / `clearTimeout(id)` | ✅ | Bare global functions, matching real JS (not a namespace). Callback must be a zero-argument, `void`-returning closure — a bare reference to a top-level named function isn't supported as a value yet, a pre-existing general limitation, not specific to timers. See `docs/adr/ADR-00031.md`. |
| `setInterval(fn, ms)` / `clearInterval(id)` | ✅ | Same scope as `setTimeout`. An active interval that's never cleared keeps the process running indefinitely, matching real Node — the first feature in this compiler where that's true. See `docs/adr/ADR-00031.md`. |
| `setImmediate(fn)` / `clearImmediate(id)` | ❌ | Next-tick (Node.js extension) — a natural, separable follow-on now that the core timer-queue mechanism exists |
| `queueMicrotask(fn)` | ❌ | Microtask queue (also a JS language global) |

### Encoding / Text

Can be implemented on top of C `iconv` or hand-rolled UTF-8 routines. (`atob`/`btoa` and `encodeURI(Component)`/`decodeURI(Component)` are already implemented — tracked as bare globals in the Global Functions & Constants table above, not repeated here to avoid double-counting.)

| API | Status | Notes |
|---|---|---|
| `TextEncoder` | ❌ | UTF-8 encode string → `Uint8Array` |
| `TextDecoder` | ❌ | Decode bytes → string; supports UTF-8, UTF-16, Latin-1 |

### URL

Stateless parsing; can be implemented with C string routines, no networking required.

| API | Notes |
|---|---|
| `URL` | Full URL parsing: `href`, `origin`, `protocol`, `host`, `pathname`, `search`, `hash` |
| `URLSearchParams` | Query string parsing and serialization |
| `URLPattern` | Pattern matching against URL structures |

### Binary Data & Typed Arrays

Binary views over a raw `ArrayBuffer`. Essential for networking, crypto, and file I/O.

| API | Notes |
|---|---|
| `ArrayBuffer` | Fixed-length raw binary buffer |
| `Uint8Array` / `Int8Array` | 8-bit typed views |
| `Uint16Array` / `Int16Array` | 16-bit typed views |
| `Uint32Array` / `Int32Array` | 32-bit typed views |
| `Float32Array` / `Float64Array` | 32 / 64-bit float views |
| `BigInt64Array` / `BigUint64Array` | 64-bit integer views (needs `bigint` type) |
| `Uint8ClampedArray` | 8-bit clamped (0–255) |
| `DataView` | Arbitrary-endian reads/writes over an `ArrayBuffer` |
| `Blob` | Immutable binary data object with MIME type |
| `SharedArrayBuffer` | Shared memory between workers; needs worker support first |
| `Atomics` | Atomic operations on `SharedArrayBuffer` |

### Cryptography (Web Crypto API)

`crypto.subtle.*` can delegate to OpenSSL or Apple CommonCrypto via C FFI — none of that is implemented yet. `crypto.getRandomValues`/`randomUUID` needed only a real CSPRNG (`arc4random_buf`/`getrandom()`), no external library.

| API | Status | Notes |
|---|---|---|
| `crypto.getRandomValues(buffer)` | ✅ | Fills a plain `number[]` (not a `TypedArray` — this compiler has none yet) with random byte values, one per element. See `docs/adr/ADR-00024.md`. |
| `crypto.randomUUID()` | ✅ | RFC 4122 version-4 UUID string. See `docs/adr/ADR-00024.md`. |
| `crypto.subtle.digest(algo, data)` | ❌ | SHA-1, SHA-256, SHA-384, SHA-512 |
| `crypto.subtle.encrypt` / `.decrypt` | ❌ | AES-GCM, AES-CBC, RSA-OAEP |
| `crypto.subtle.sign` / `.verify` | ❌ | HMAC, ECDSA, RSA-PSS |
| `crypto.subtle.generateKey` | ❌ | Key generation |
| `crypto.subtle.importKey` / `.exportKey` | ❌ | Key serialization |
| `crypto.subtle.deriveKey` / `.deriveBits` | ❌ | PBKDF2, HKDF |

### Performance & Timing

`performance.*` can be implemented with a single `clock_gettime()` call.

| API | Status | Notes |
|---|---|---|
| `performance.now()` | ✅ | `CLOCK_MONOTONIC`-based milliseconds, as a `double` (sub-millisecond precision) — unlike Date.now(), not tied to wall-clock time. No fixed "time origin" like the browser spec (process/page start); returns the raw monotonic reading instead, which is exactly as valid for subtracting two calls to measure elapsed time. See `docs/adr/ADR-00024.md`. |
| `performance.mark(name)` / `performance.measure(name, start, end)` | ❌ | Named timing marks |
| `Date` | ✅ | `new Date()` / `new Date(ms)` / `new Date(isoString)` (the string form parses via the same logic as `Date.parse`, including its `-1`-on-unparseable sentinel — see `docs/adr/ADR-00038.md`) / `new Date(year, month, day?, hours?, minutes?, seconds?, ms?)` (month 0-indexed, matching `getMonth()`; omitted trailing fields default like real JS — day to 1, everything after that to 0; see `docs/adr/ADR-00039.md`); `getFullYear/Month/Date/Day/Hours/Minutes/Seconds/Milliseconds`, `getTime`/`valueOf`, `toISOString` — all UTC, not local time, for deterministic output regardless of the machine/CI timezone (a documented deviation from real JS's local-time default — note the multi-argument constructor form is a special case of this: real JS treats its fields as *local* time, this compiler always treats them as UTC). See `docs/adr/ADR-00014.md`. |
| `Date.now()` | ✅ | Milliseconds since epoch, via `clock_gettime(CLOCK_REALTIME, ...)` |
| `Date.parse(string)` | ✅ | ISO 8601 strings, with or without milliseconds: `Z` (UTC), a `+HH:MM`/`-HH:MM` timezone offset (converted to UTC), or a bare `YYYY-MM-DD` date. Unparseable input returns `-1` (a documented sentinel — this compiler's Date has no NaN representation). See `docs/adr/ADR-00015.md` and `docs/adr/ADR-00017.md`. |
| `Date` setters (`setFullYear`, `setMonth`, `setDate`, `setHours`, `setMinutes`, `setSeconds`, `setMilliseconds`, `setTime`) | ✅ | Mutate a named Date variable in place and return the new timestamp, matching real JS. Requires a named-variable receiver (not a field access or call result — this compiler's Date is a plain number, not a reference object, so there's no heap location to mutate otherwise); only the single-argument form of each setter (no `setFullYear(y, m, d)`-style overloads). See `docs/adr/ADR-00016.md`. |
| `Date` arithmetic (`date ± durationMs`, `date - date`, `date += durationMs`) | ✅ | `Date - Date` gives the difference in milliseconds (a number), matching real JS. `Date ± number` gives a new Date (a deliberate deviation from real JS, where `+` on a Date string-concatenates instead — numeric duration arithmetic is far more useful for this compiler's plain-number Date representation). `Date + Date`, `number - Date`, and compound-assigning a Date into a Date are all rejected at compile time. See `docs/adr/ADR-00018.md`. |
| `Date.prototype.toDateString()` | ✅ | Fixed `"Www Mon DD YYYY"` shape (e.g. `"Thu Jan 01 1970"`), matching real JS exactly except always UTC, not local time. See `docs/adr/ADR-00019.md`. |
| `Date.prototype.toLocaleDateString()` | ✅ | One fixed `"M/D/YYYY"` format (the default en-US shape), always UTC; no locale argument or full `Intl`-style locale support — a documented scope narrowing. See `docs/adr/ADR-00019.md`. |

### Networking

All require linking a network library (libcurl for fetch/HTTP; system sockets for WebSocket).

| API | Status | Notes |
|---|---|---|
| `fetch(url)` | ✅ | GET only — no custom method/headers/request body yet, and no `Request`/`Headers` objects. Real non-blocking I/O since `docs/adr/ADR-00050.md`: uses libcurl's multi-interface, driven by the same `select()`-based event loop `http.listen` uses, so `await fetch(...)` yields instead of blocking when called from inside a connection-handler fiber — one slow upstream call no longer freezes every other connection or timer. Outside any fiber (plain top-level code, no `http.listen`), there's nothing else to overlap with, so it busy-spins the same multi-interface calls to the same effect as a blocking wait. A network-level failure (DNS, connection refused, TLS, timeout) throws; a non-2xx HTTP status still resolves normally (`.ok` distinguishes it), matching real `fetch`. See `docs/adr/ADR-00021.md` (original V1) and `docs/adr/ADR-00050.md` (non-blocking rework). |
| `Response` (`.status`, `.ok`, `.body`, `.text()`, `.json()`) | ✅ | Plain object with `status`/`ok`/`body` fields (readable directly, not hidden) plus `text()`/`json()` methods. `.json()` reuses `JSON.parse`'s existing machinery, including its scope (flat objects with primitive fields only — nested JSON fields aren't supported yet, same as bare `JSON.parse`). Response bodies are plain null-terminated strings: binary bodies with embedded null bytes will silently truncate at the first one (no `ArrayBuffer`/`TypedArrays` yet to represent raw bytes faithfully) — fine for the REST/JSON use case this was built for, not for binary downloads. See `docs/adr/ADR-00021.md`. |
| `Request` / `Headers` objects | ❌ | Not implemented — `fetch` takes a bare URL string only for now |
| `WebSocket` | ❌ | Full-duplex TCP connection |
| `EventSource` | ❌ | Server-sent events (SSE) over HTTP |
| `XMLHttpRequest` | ❌ | Legacy HTTP; lower priority than `fetch` |

A server-side HTTP listener (`http.listen(port, handler)`) is tracked under [Node.js APIs → HTTP Server](#http-server) below, not here — listening for incoming connections has no browser-side Web API equivalent.

### Streams API

`ReadableStream`, `WritableStream`, and `TransformStream` are the backbone of pipeline-style data processing.

| API | Notes |
|---|---|
| `ReadableStream` | Pull-based readable data source |
| `WritableStream` | Writable sink |
| `TransformStream` | Duplex transform (e.g. compress, encrypt) |
| `CompressionStream` / `DecompressionStream` | gzip / deflate via `zlib` |
| `Blob.stream()` / `Blob.text()` / `Blob.arrayBuffer()` | Depends on `Blob` + Streams |

### Events & Cancellation

| API | Notes |
|---|---|
| `EventTarget` / `addEventListener` / `dispatchEvent` | Generic event bus; prerequisite for many APIs |
| `Event` / `CustomEvent` | Base event types |
| `AbortController` / `AbortSignal` | Cancellation token for fetch, streams, timers |

### Concurrency (Workers)

Requires spawning threads or processes and sharing memory.

| API | Notes |
|---|---|
| `Worker` (Web Workers API) | Run code on a background thread |
| `BroadcastChannel` | Pub/sub across workers |
| `MessageChannel` / `MessagePort` | Bidirectional channel between contexts |

### Notifications & Misc (Low priority / browser-specific)

These are mostly browser-specific and unlikely to be useful in a native CLI context. Tracked here for completeness.

| API | Notes |
|---|---|
| Notifications API | Browser-only desktop notifications; `node-notifier` equivalent not in scope |
| Push API | Requires Service Worker and browser push infrastructure |
| Service Worker API | Browser-only background script; N/A for native |
| Storage API (`localStorage` / `sessionStorage`) | Browser session concept; N/A for native |
| IndexedDB | Browser embedded database; out of scope |
| Clipboard API | Requires desktop GUI; N/A for native |
| Geolocation API | Hardware sensor; N/A for native CLI |
| Canvas / WebGL / WebGPU | Graphics; N/A for native CLI |

---

## Node.js APIs

Node.js-specific runtime globals — not part of any Web/browser standard (a real browser has no filesystem or process object at all, for sandboxing reasons), but essential for the CLI-application and microservice use cases this project actually targets. Recognized as pseudo-namespaces (`fs.*`, `process.*`), like `Math`/`JSON` — not real importable modules.

### File System (fs)

Node-`fs`-shaped synchronous file I/O for reading/writing config, data, and logs — not `File`/`FileReader`/`FileSystemFileHandle` (those model browser sandbox/permission concepts — a file picker dialog, a `Blob` — that don't exist for a native CLI/microservice program with direct filesystem access).

| API | Status | Notes |
|---|---|---|
| `fs.readFileSync(path)` | ✅ | Reads the whole file into a string. Throws a catchable `Error` (built from `strerror(errno)`) if the file can't be opened. Text-only — a file with embedded null bytes reads back shorter than its real size, the same limitation `fetch`'s response bodies have (`.length` is `strlen`-based). See `docs/adr/ADR-00023.md`. |
| `fs.writeFileSync(path, data)` | ✅ | Creates or truncates the file with `data`. Throws on failure. |
| `fs.appendFileSync(path, data)` | ✅ | Like `writeFileSync`, but appends instead of truncating (creates the file if it doesn't exist). Throws on failure. |
| `fs.existsSync(path)` | ✅ | Plain existence check via POSIX `access()`. Deliberately does **not** throw for a missing path — matches real Node's own `existsSync`, one of the few `fs` functions that reports "doesn't exist" as `false` rather than an error. |
| `fs.unlinkSync(path)` | ✅ | Deletes a file. Throws on failure. |
| `fs.mkdirSync(path)` | ✅ | Creates a directory via POSIX `mkdir()`, mode `0777` reduced by the process umask as usual. No `{recursive: true}` option — throws (e.g. `EEXIST`) if the path already exists or a parent directory is missing. See `docs/adr/ADR-00027.md`. |
| `fs.rmdirSync(path)` | ✅ | Removes an *empty* directory via POSIX `rmdir()` — deliberately directory-only (fails on a plain file, unlike `remove()`/`unlinkSync`). No recursive-delete option, matching `mkdirSync`'s lack of one. See `docs/adr/ADR-00027.md`. |
| `fs.readdirSync(path)` | ✅ | Lists a directory's entries (excluding `.`/`..`) as a `string[]`, in whatever order the OS's own `readdir()` returns them — no ordering guarantee, matching real Node. Built from `struct dirent`'s `d_name` field at a `runtime.GOOS`-conditional byte offset, independently verified by a compiled `offsetof` probe on both Darwin and (via Docker, see `docs/adr/ADR-00051.md`) x86-64 Linux. See `docs/adr/ADR-00027.md`. |
| `fs.renameSync(oldPath, newPath)` | ✅ | Renames/moves a file via POSIX `rename()`. Throws on failure. See `docs/adr/ADR-00027.md`. |
| `fs.copyFileSync(src, dest)` | ✅ | Composes the existing `readFileSync`/`writeFileSync` helpers — no new C-level I/O code. Inherits `readFileSync`'s text-only limitation (a source file with embedded null bytes copies back shorter than its real size). See `docs/adr/ADR-00027.md`. |
| Async variants (`fs.readFile`, callback/Promise-based) | ❌ | Everything here is synchronous and blocking, matching this compiler's lack of an event loop — no non-blocking variant exists to offer |
| `File` / `FileReader` / `FileSystemFileHandle` (browser-flavored File API) | ❌ | Not planned — see the framing note above; these model browser concepts this compiler has no equivalent for |

### Process / CLI I/O

What CLI tools and containerized services actually need day-to-day (argument parsing, stdin, environment config, exit codes). Prioritized because the long-term project direction favors CLI/microservice use cases. `console.log`/`.error` already write to stdout/stderr respectively (✅, see the `console` table above) — the gaps below are everything *else* a CLI program needs.

| API | Status | Notes |
|---|---|---|
| Command-line arguments (`process.argv`) | ✅ | Mirrors C's `argv` directly (`argv[0]` is the binary's own path), not Node's two-prefix convention — see `docs/adr/ADR-00002.md` |
| Environment variables (`process.env.KEY` / `process.env["KEY"]`) | ✅ | Both dot and bracket notation; returns a possibly-null string (same convention as `.find()`), so `process.env.X ?? "default"` works |
| Exit codes (`process.exit(code)`) | ✅ | Calls C `exit()`; never returns, code after it is correctly unreachable |
| Reading stdin (sync line read) | ✅ | `process.readLineSync()` — one line via POSIX `getline()` (handles arbitrarily long lines), stripped of its trailing `\n`/`\r\n`. Returns `null` at EOF (same possibly-null convention as `process.env`). See `docs/adr/ADR-00024.md`. |
| Simple synchronous file read/write (`fs.readFileSync`/`writeFileSync`-style) | ✅ | See the File System (fs) section above — `fs.readFileSync`/`writeFileSync`/`appendFileSync`/`existsSync`/`unlinkSync` |
| `process.execFileSync(file, args?)` | ✅ | Node's `child_process.execFileSync`, not `execSync`: `fork()`+`execvp()`s `file` directly with no shell involved, so shell metacharacters in `args` are never interpreted. Returns captured stdout as a string; throws a catchable `Error` on a non-zero exit status or a signal death. V1 scope: no options object (no `cwd`/`env`/`timeout`/`stdio` overrides yet); stderr is inherited (visible on the terminal live), not captured. See `docs/adr/ADR-00025.md`. |
| `process.cwd()` | ✅ | Current working directory, via POSIX `getcwd(NULL, 0)` (auto-sizing). See `docs/adr/ADR-00026.md`. |
| `process.chdir(path)` | ✅ | Changes the current working directory via POSIX `chdir()`. Throws a catchable `Error` (same "`<opDesc>` '`<path>`': `<strerror>`" shape `fs.*`'s failures already use) if the path doesn't exist or isn't a directory. See `docs/adr/ADR-00026.md`. |
| `process.pid` | ✅ | The current process ID, via POSIX `getpid()`. A property read, not a call, matching `process.argv`. See `docs/adr/ADR-00026.md`. |
| `process.platform` | ✅ | A pure compile-time constant (`"darwin"`/`"linux"`/`"win32"`/...) baked in from the Go compiler's own `runtime.GOOS` — no runtime code at all, since this compiler doesn't cross-compile. See `docs/adr/ADR-00026.md`. |
| `process.kill(pid, signal?)` | ✅ | Sends `signal` (defaults to `15`/`SIGTERM`, matching real Node) to `pid` via POSIX `kill()`. Throws a catchable `Error` if the target process doesn't exist or the signal can't be sent; signal `0` is the standard POSIX "does this process exist" check and never actually delivers a signal. See `docs/adr/ADR-00026.md`. |

### HTTP Server

| API | Status | Notes |
|---|---|---|
| `http.listen(port, handler)` | ✅ | Concurrent connection handling: each accepted connection runs on its own fiber (`ucontext.h`-based, no custom assembly — see `docs/tdd/TDD-00006.md`'s Part 2 prototype), so a connection that hasn't sent its request yet no longer blocks any other connection from being accepted and answered. GET-only request line (method + path); no headers, query-string, or request body yet; no `.close()`; response writes stay a single blocking call (a deliberate V1 simplification — small responses essentially never block). Built on the generalized `select()`-based event loop ([`docs/tdd/TDD-00006.md`](docs/tdd/TDD-00006.md) Part 1), so the listening socket, every open connection, and any pending `setTimeout`/`setInterval` timers all share one wait. See `docs/adr/ADR-00048.md` (V1) and `docs/adr/ADR-00049.md` (concurrent connections). |

---

## What Is NOT Implemented

### High priority / low complexity

These are the most natural next steps — each is self-contained and commonly used:

| Feature | Complexity | Notes |
|---|---|---|
| `Map.clear()` / `Set.clear()` | Low | Free + reinit |
| `Map.entries()` | Low | Pair of parallel arrays |
| `Set.forEach()` / `Map.forEach()` | Low | Loop over values/entries |
| `Object.assign(target, ...src)` | Medium | Field-by-field copy |
| Computed property keys `{ [k]: v }` | Medium | Dynamic key; needs hash map backing |

### Medium priority / medium complexity

| Feature | Complexity | Notes |
|---|---|---|
| `Array.flat(depth?)` | Medium | Recursive flatten with dynamic realloc |
| `Array.flatMap(fn)` | Medium | `map` then `flat(1)` |
| `Array.from(iterable)` | Medium | Only the array-like overload is needed initially |
| `Array.keys()` / `.values()` / `.entries()` | Medium | Index iterators |
| `String.prototype.matchAll()` | Medium-High | Needs regex engine or `strstr` loop |
| `Number.prototype.toString(radix)` | Medium | `sprintf` + radix conversion |
| `instanceof` | Medium | Needs type tag stored on heap objects |
| `in` operator | Medium | Object key presence check |
| User-defined generic functions | High | Full generic instantiation pass |
| Intersection types `A & B` | Medium | Merge struct fields |
| Tuple types `[number, string]` | Medium | Fixed-size struct |

### High priority / high complexity

| Feature | Complexity | Notes |
|---|---|---|
| `class` (methods, constructors) | High | VTable or static dispatch |
| `class` inheritance / `extends` | High | Needs virtual dispatch or monomorphization |
| ~~`import` / `export` (multi-file)~~ | ~~High~~ | ✅ done (named imports/exports, whole-program compile) — see `docs/adr/ADR-00022.md` and the Modules section above. Still open: real per-file module scope (mangled names), `export default`/namespace imports/re-exports, and side-effecting imported files |
| Nested function declarations | Medium | Separate from closures; mostly a scoping change |
| `RegExp` | High | Needs PCRE or similar C library |
| `Error` subtypes (`TypeError`, etc.) | Medium | Tagged error values |
| `Promise.all` / `.race` / `.allSettled` | Medium | Requires awaiting arrays of promises |
| `Symbol` | High | Unique runtime IDs; affects `for…of`, iterators |
| Generator functions / iterators | High | Suspend/resume; requires coroutine machinery |
| Decorators | Very high | Requires metadata reflection |
| `Proxy` / `Reflect` | Very high | Dynamic property intercept; likely impractical |

---

## Known Limitations & Bugs

Deviations from expected behavior: either features marked ✅ above with incorrect/incomplete behavior, or ❌ (not-implemented) features whose failure mode is worse than a clean rejection — silent wrong output or a crash instead of a clear "unsupported" error.

| Limitation | Notes |
|---|---|
| `any`/`unknown` boxed booleans print as `"true"`/`"false"` | `console.log`ing a plain (non-`any`) `boolean` prints `1`/`0` in this compiler (an existing, unrelated quirk — see `examples/strings/string_methods.ts`'s comments), but an `any`-typed variable currently holding a boolean prints `"true"`/`"false"` instead, since the dynamic-value formatter shares one code path for both `console.log` and template literals and mirrors the template-literal convention (which already uses `"true"`/`"false"`) rather than special-casing `console.log`'s raw-boolean convention differently per call site. Deliberate, documented simplification — see `docs/adr/ADR-00008.md`. |
| An unannotated function calling another unannotated function *declared later in the same file*, when the callee returns an object/array/closure/Date | `function makeA() { return makeB() }; function makeB() { return { x: 1 } }` — `makeA`'s own inferred return type is computed once, in source order, before `makeB` has been registered, so `makeA`'s inference sees a not-yet-known callee and falls back to void. Fails cleanly (`field access on non-object`), not silently — a known, accepted boundary of `docs/adr/ADR-00041.md`'s single-pass, best-effort inference, not a general fixed-point/multi-pass type inference system. Reorder the declarations, or add an explicit return-type annotation to `makeA`, to work around it. |
| `fetch()` response bodies containing embedded null bytes silently truncate | Confirmed directly: fetching 50,000 bytes of random binary data (`httpbin.org/bytes/50000`) came back as a 383-byte string. Root cause isn't in `fetch` itself — every string in this compiler is a plain null-terminated C string (`.length` is `strlen`-based), so *any* string value with an embedded `\0`, from any source, reads as shorter than it actually is; `fetch` just makes this reachable from live, uncontrolled external data for the first time. Verified the underlying growable-buffer/`realloc` logic itself is correct by re-testing with a large all-text (no embedded nulls) body, which came back complete and exact. Fine for the JSON/text REST API use case `fetch` was built for; a real fix needs `ArrayBuffer`/`TypedArrays` (0% implemented, tracked separately) to represent raw bytes at all. |
| An object literal's field values are never coerced against a declared interface/expected type, only against their own literal-inferred type | `interface Point { x: number; y: number }; const p: Point = { x: 1, y: 40.6 }; console.log(p.y)` prints `4630910759336725709`, the raw IEEE-754 bit pattern of `40.6` reinterpreted as an `i64` — not a truncation, not a rejection, silent corruption readable through any later field access. Bidirectional (a `@type {float64}`-annotated field given an integer literal corrupts the same way in reverse) and depth-independent (reproduces in a flat, one-level interface; has nothing to do with nesting despite how it was originally reported). Same failure shape as the unannotated-parameter bug `docs/adr/ADR-00042.md` fixed, just in a different, never-audited code path (object literal construction, not function calls). See `docs/tdd/TDD-00007.md` for the full design. |

---

## Design Documents (TDDs)

Anything big enough to need a design pass before implementation gets scoped out in a Technical Design Document under `docs/tdd/` first — full context, options considered, and prerequisites, kept out of this file so it stays scannable. Each of these is a genuinely significant piece of work in its own right, not a quick follow-on:

- **[Memory Management](docs/tdd/TDD-00001.md)** — no garbage collector yet. Stage 1 of the manual-release plan (`Memory.free(x)`) is done (`ADR-00030`); the GC path and Stages 2/3 of the manual plan are still design-only.
- **[Timers](docs/tdd/TDD-00002.md)** — done (`ADR-00031`); kept as a TDD since the design writeup (why it *doesn't* need the general event loop) is still useful context.
- **[Alternative fetch Backend](docs/tdd/TDD-00003.md)** — a Go helper instead of libcurl. Scoped, not started, low priority.
- **[HTTP Server](docs/tdd/TDD-00004.md)** — the piece that unlocks this project's microservice direction. V1 done (`ADR-00048`).
- **[Unannotated Parameter Typing](docs/tdd/TDD-00005.md)** — clean rejection at call sites is done (`ADR-00042`); the two further options (call-site inference, real `any` semantics) are scoped, not started.
- **[Event Loop](docs/tdd/TDD-00006.md)** — this project's single biggest structural gap, now substantially closed. Part 1 (the `select()`-based wait loop) done (`ADR-00048`); Part 2's fiber-based scheduler is real, shipped for HTTP connection concurrency (`ADR-00049`) and for real non-blocking `await fetch(...)` (`ADR-00050`, via libcurl's multi-interface merged into the same loop). Remaining gap: `Promise.all`/`.race`/`.allSettled` (awaiting multiple promises concurrently from a single call site) aren't implemented yet — today's concurrency comes from multiple independent connection handlers each awaiting their own fetch, not from one handler awaiting several at once.
- **[Object Literal Field Coercion](docs/tdd/TDD-00007.md)** — object literals never coerce field values against a declared type, only their own literal-inferred type; silent bit-reinterpretation corruption, not a clean rejection. Scoped, not started.
- **[External Conformance Suites (TypeScript + Test262) as a Test-Coverage Benchmark](docs/tdd/TDD-00008.md)** — the TypeScript suite tests the type checker's output, not runtime behavior, so it can't be used directly; Test262 turned out to be execution-based and often directly portable instead, at least for spec-mandated value semantics this compiler intends to match. First real ports (shift-operator categories) landed alongside `docs/adr/ADR-00047.md`'s shift-semantics fix. Tracked in [`docs/testing/CONFORMANCE-COVERAGE.md`](docs/testing/CONFORMANCE-COVERAGE.md). Partially Implemented.

---

## Coverage Summary

### TypeScript Core Language

| Category | Implemented | Total meaningful features | Coverage |
|---|---|---|---|
| Control flow statements | 10 | 10 | 100% |
| Operators | 35 | 38 | ~92% |
| Variable declarations | 3 | 3 | 100% |
| Functions & closures | 7 | 9 | ~78% |
| Type primitives | 8 | 14 | ~57% |
| Async / Promise | 3 | 9 | ~33% |
| String methods | 26 | 33 | ~79% |
| Array methods | 22 | 34 | ~65% |
| Number / Math | 32 | 35 | ~91% |
| Object & collections | 15 | 24 | ~63% |
| JSON | 9 | 9 | 100% |
| console | 11 | 12 | ~92% |
| Global functions & constants | 13 | 17 | ~76% |
| Type system features | 15 | 23 | ~65% |
| Classes / OOP | 0 | 8 | 0% |
| Modules | 4 | 11 | ~36% |
| **Core language total** | **213** | **289** | **~74%** |

### Web Platform APIs

WHATWG/browser-standard APIs (also implemented by Node.js) — see the [Web Platform APIs](#web-platform-apis) section above. Excludes `fs`/`process`/HTTP-server, tracked separately below.

| Category | Implemented | Total tracked | Coverage |
|---|---|---|---|
| Timers | 2 | 4 | 50% |
| Encoding / Text | 0 | 2 | 0% |
| URL | 0 | 3 | 0% |
| Binary data & Typed Arrays | 0 | 11 | 0% |
| Web Crypto | 2 | 8 | 25% |
| Performance & Timing (incl. Date) | 8 | 9 | ~89% |
| Networking (fetch, WebSocket, SSE) | 2 | 6 | ~33% |
| Streams | 0 | 5 | 0% |
| Events & Cancellation | 0 | 5 | 0% |
| Workers / Concurrency | 0 | 3 | 0% |
| **Web Platform total** | **14** | **~56** | **25%** |

### Node.js APIs

Node-specific runtime globals with no browser equivalent — see the [Node.js APIs](#nodejs-apis) section above.

| Category | Implemented | Total tracked | Coverage |
|---|---|---|---|
| File System (fs) | 10 | 12 | ~83% |
| Process / CLI I/O | 11 | 11 | 100% |
| HTTP Server | 1 | 1 | 100% |
| **Node.js total** | **22** | **24** | **~92%** |

---

## Roadmap

Grouped by kind of work rather than a fixed sequence number, since priorities shift and bug fixes get picked up opportunistically rather than in strict order. Core-language feature gaps already have their own priority/complexity breakdown in [What Is NOT Implemented](#what-is-not-implemented) above — not repeated here.

### Next up — bugs found but not yet fixed

Pulled from [Known Limitations & Bugs](#known-limitations--bugs) above: the ones worth fixing outright, as opposed to the ones documented there as deliberate, permanent scope narrowings (e.g. `any`'s boolean-printing convention).

| Fix | Effort | Notes |
|---|---|---|
| `fetch`/`fs` bodies containing embedded null bytes silently truncate | Deferred | Root cause is this compiler having no `ArrayBuffer`/TypedArrays yet (0% implemented) — not fixable in isolation; tracked as a consequence of that gap in the Web Platform & Node.js APIs backlog below |

### Structural priorities

The three biggest cross-cutting gaps — each affects multiple features rather than being one self-contained item, and each already has its own detailed writeup above:

1. **Memory management — no garbage collector** (see [`docs/tdd/TDD-00001.md`](docs/tdd/TDD-00001.md)). Decision already made (Boehm GC: swap `@malloc`/`@realloc` for `@GC_malloc`/`@GC_realloc`, link `-lgc`); not started. A non-issue for today's short-lived CLI programs, but a real limitation now that the HTTP server below actually exists — every request currently leaks its `Request` object and any allocations the handler itself makes, fine for a demo, not for a genuinely long-running service.
2. **Event loop — Part 1 and Part 2 both have real, shipped slices now** (see [`docs/tdd/TDD-00006.md`](docs/tdd/TDD-00006.md)). Part 1 (a `select()`-based wait loop merging with the existing timer queue) shipped alongside the HTTP server below — see `docs/adr/ADR-00048.md`. Part 2 (real suspension) was scoped around three candidate mechanisms; a direct prototyping spike ruled out LLVM coroutine intrinsics (confirmed incompatible with this compiler's `setjmp`/`longjmp` exception model — a `try`/`catch` spanning a suspend point segfaults) and confirmed hand-rolled fibers (`ucontext.h`, no custom assembly needed) work correctly instead. `docs/adr/ADR-00049.md` used that mechanism to make `http.listen` handle connections concurrently; `docs/adr/ADR-00050.md` extended it to make `await fetch(...)` genuinely non-blocking (libcurl's multi-interface, merged into the same event loop) — confirmed directly, not just by unit test, that two concurrent connections each awaiting a different-latency upstream complete independently rather than serializing. Still missing: `Promise.all`/`.race`/`.allSettled` (concurrently awaiting several promises from one call site, rather than relying on separate connection handlers each awaiting their own).
3. **HTTP server** (see [`docs/tdd/TDD-00004.md`](docs/tdd/TDD-00004.md)) — V1 done (`docs/adr/ADR-00048.md`), concurrent connection handling done on top of it (`docs/adr/ADR-00049.md`, using Event Loop Part 2's fiber mechanism), GET-only request line. Remaining: headers/query-string/request-body parsing and graceful shutdown, tracked as separable V2 follow-ups.

Prefer picking up work that advances REST API interaction / file I/O / process interaction over other equal-effort items — these three items are exactly that category, alongside the `fs`/`process` work already done.

### Web Platform & Node.js APIs backlog

Not-yet-implemented items from the [Web Platform APIs](#web-platform-apis) and [Node.js APIs](#nodejs-apis) sections above, grouped by effort. Within a tier, the same tiebreaker applies — prefer whichever unlocks REST API interaction / file I/O / process interaction.

**Low effort (C stdlib or a simple wrapper, no event loop needed):**
- `TextEncoder` / `TextDecoder` — UTF-8 is the only required encoding; hand-roll or use `iconv`
- `URL` / `URLSearchParams` — C string parsing, no external dependency needed
- `performance.mark(name)` / `performance.measure(...)` — named timing marks on top of the existing `performance.now()`
- `structuredClone(obj)` — recursive deep-copy of heap objects

**Medium effort (new dependency, subsystem, or an event-loop prerequisite):**
- `ArrayBuffer` + TypedArrays — new IR representation (a contiguous memory block with typed views); also the prerequisite for actually fixing the `fetch`/`fs` null-byte-truncation bug above, and for `crypto.subtle` below
- `fetch`'s `Request`/`Headers` objects, custom method/headers/request body — extends the existing GET-only V1
- `CompressionStream` / `DecompressionStream` — link `zlib`
- `EventTarget` / `Event` / `CustomEvent` — generic event bus; prerequisite for `AbortController` and others
- `AbortController` / `AbortSignal` — cancellation token; straightforward once `EventTarget` exists
- `setImmediate` / `clearImmediate` — a separate, smaller follow-on once Timers' core mechanism exists
- `WebSocket` — TCP + HTTP upgrade; hand-roll on POSIX sockets or use `libwebsockets`

**High effort (needs the event loop + a concurrency model, or a new external dependency):**
- `Worker` (Web Workers) — threads via `pthreads`; requires `SharedArrayBuffer` + `Atomics` too
- `crypto.subtle` (digest, encrypt, sign) — delegate to OpenSSL or Apple CommonCrypto
- `ReadableStream` / `WritableStream` / `TransformStream` — full streaming pipeline; complex backpressure model
- `EventSource` (SSE) — depends on `fetch` + the event loop

---

*Last updated: 2026-07-12. Update this file whenever a new feature is added or removed.*
