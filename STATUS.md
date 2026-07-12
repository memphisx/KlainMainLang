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
- [Memory Management](#memory-management--todo-no-garbage-collector)
- [Timers — Design Notes](#timers--design-notes-done)
- [Alternative fetch Backend (Go helper)](#alternative-fetch-backend-a-go-helper-instead-of-libcurl--scoping-not-started-low-priority)
- [HTTP Server — Scoping](#http-server--scoping-not-started)
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
| `async` functions | ✅ | Returns `Promise<T>`; malloc-based slot |
| `await` expressions | ✅ | Loads value from slot, frees it |
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

`setTimeout`/`setInterval` needed a sleep-until-next-due loop, not the full general-purpose event loop — see [Timers — Design Notes](#timers--design-notes-done) below for the full design. `setImmediate`/`queueMicrotask` are a separate, smaller follow-on, not yet picked up.

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
| `fetch(url)` | ✅ | GET only — no custom method/headers/request body yet, and no `Request`/`Headers` objects. Blocking under the hood (libcurl's synchronous `curl_easy_perform`), wrapped in an already-resolved `Promise<Response>` — the same "synchronous V1" async model the rest of this compiler already uses (`ADR-00020`), not real non-blocking I/O. A network-level failure (DNS, connection refused, TLS, timeout) throws; a non-2xx HTTP status still resolves normally (`.ok` distinguishes it), matching real `fetch`. See `docs/adr/ADR-00021.md`. |
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
| `fs.readdirSync(path)` | ✅ | Lists a directory's entries (excluding `.`/`..`) as a `string[]`, in whatever order the OS's own `readdir()` returns them — no ordering guarantee, matching real Node. Built from `struct dirent`'s `d_name` field at a `runtime.GOOS`-conditional byte offset (verified directly on Darwin via a compiled `offsetof` probe; Linux's offset comes from glibc's own current source, not independently compiled on a Linux machine in this sandbox). See `docs/adr/ADR-00027.md`. |
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
| `http.listen(port, handler)` | ❌ | Not started — scoped but not implemented. See [HTTP Server — Scoping](#http-server--scoping-not-started) below for the full design, prerequisites, and open decisions. |

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
| Calling a closure returned from a `const`-bound **arrow function**, even with an explicit return-type annotation | `const middle = (): () => void => { ...; return innerFn }; const inner = middle(); inner()` fails with `undefined function or closure 'inner'` regardless of the return-type annotation. Only fixed by rewriting `middle` as a plain `function middle(): () => void { ... }` **declaration** — confirmed directly (`docs/adr/ADR-00037.md`'s investigation) that the explicit-annotation workaround only actually works for that form, not for a `const`-bound arrow function of any annotation shape. Looks like a type-inference gap specific to arrow functions: `inner`'s inferred type from `middle()`'s call isn't marked as a function/closure type when `middle` is an arrow function, regardless of whether it has a return-type annotation. |
| `any`/`unknown` boxed booleans print as `"true"`/`"false"` | `console.log`ing a plain (non-`any`) `boolean` prints `1`/`0` in this compiler (an existing, unrelated quirk — see `examples/strings/string_methods.ts`'s comments), but an `any`-typed variable currently holding a boolean prints `"true"`/`"false"` instead, since the dynamic-value formatter shares one code path for both `console.log` and template literals and mirrors the template-literal convention (which already uses `"true"`/`"false"`) rather than special-casing `console.log`'s raw-boolean convention differently per call site. Deliberate, documented simplification — see `docs/adr/ADR-00008.md`. |
| Functions with no explicit return-type annotation cannot return an object literal | `function makePoint(x, y) { return { x: x, y: y } }` (or the shorthand `{ x, y }`) fails with `field access on non-object` when the caller accesses a field on the result — works fine with an explicit return type (`function makePoint(x, y): Point { ... }`). A related, but distinct, gap from the already-tracked closure-return-type entry above — this one affects plain object literals, not closures. Found while adding shorthand object property tests (`docs/adr/ADR-00012.md`). |
| Interface fields cannot be declared as `float64`/`float32` | Object/interface field types resolve through the plain type-name path, which has no JSDoc-override mechanism (unlike variable declarations, where `/** @type {float64} */` works) — a field annotated only `number` always resolves to `i64`. A field CAN still end up float-typed via literal inference in a plain object literal (e.g. `{ score: 9.5 }` infers `score` as `float`), just not via an explicit interface declaration. Found while testing `JSON.parse`'s float-field code path (`docs/adr/ADR-00007.md`), which is consequently unreachable via any interface today despite being implemented correctly. |
| `fetch()` response bodies containing embedded null bytes silently truncate | Confirmed directly: fetching 50,000 bytes of random binary data (`httpbin.org/bytes/50000`) came back as a 383-byte string. Root cause isn't in `fetch` itself — every string in this compiler is a plain null-terminated C string (`.length` is `strlen`-based), so *any* string value with an embedded `\0`, from any source, reads as shorter than it actually is; `fetch` just makes this reachable from live, uncontrolled external data for the first time. Verified the underlying growable-buffer/`realloc` logic itself is correct by re-testing with a large all-text (no embedded nulls) body, which came back complete and exact. Fine for the JSON/text REST API use case `fetch` was built for; a real fix needs `ArrayBuffer`/`TypedArrays` (0% implemented, tracked separately) to represent raw bytes at all. |

---

## Memory Management — TODO: no garbage collector

**Current state**: every heap allocation (arrays growing/`push`ing, object literals, closure environments, string concatenation/slicing/template literals, `Map`/`Set` backing tables, JSON/Date formatting scratch buffers, boxed `any`/`unknown` payloads, …) goes through a plain `@malloc`/`@realloc` call and is never `free`d. The one exception is `await`, which frees a `Promise`'s slot immediately after reading its resolved value. There is no ownership tracking, no reference counting, and no reachability analysis anywhere in the emitted code — ­a program's resident memory only ever grows for as long as it runs.

**Why this hasn't mattered yet**: every example and test compiles to a short-lived CLI-style process that runs for milliseconds and then exits, at which point the OS reclaims everything in one shot. This is a real, common, and completely legitimate way to run native programs — it's exactly how plenty of tiny C utilities behave too.

**Why it will matter**: the project's stated longer-term direction includes long-running microservice-style processes (an event loop + a listening HTTP server are tracked elsewhere as the biggest missing structural piece). A process that's meant to stay up and keep handling requests, but that never frees a single allocation, will grow without bound and eventually get OOM-killed — that's a hard blocker for the microservice use case specifically, even though it's a complete non-issue for the CLI use case.

### What to do about it (options considered)

1. **Reference counting** (increment/decrement a count on every store/scope-exit for each heap-allocated type, free at zero). Deterministic, no pause times — but it means threading inc/dec logic through nearly every existing codegen path (arrays, objects, closures, strings, `Map`/`Set`, `any`/`unknown` boxing all live in separate `emit_*.go` files today, each would need this bookkeeping added). It also doesn't handle reference cycles on its own, and cycles get more likely, not less, once `class`/`extends` (already on the roadmap) lets user code build arbitrary mutable object graphs — refcounting would likely need revisiting again at that point anyway. Highest implementation cost of the realistic options, for a project this size.
2. **Arena/region allocation with scope-based bulk free** (allocate from a per-call bump-pointer arena, free the whole arena — one pointer reset — when the call returns). Cheap at run time and simple to reason about for genuinely local temporaries (a lot of the scratch buffers this compiler already allocates — e.g. every `Date` formatter's `sprintf` buffer — are handed to `console.log` and never touched again, a perfect fit). But it only works for values that provably never escape their allocating scope, which needs a real escape-analysis pass to detect — and this language already returns objects/arrays/closures from functions and stores them in longer-lived variables constantly, so a large fraction of allocations would still need to fall back to today's "never freed" behavior (or be promoted to a longer-lived arena) without that analysis. A good complementary optimization for hot, clearly-local allocations later, not a full fix on its own.
3. **A precise, stack-map-driven tracing collector** (the V8/JVM-style "real" GC). Needs exact enumeration of every live root at every potential collection point — safepoints, stack maps, a relocating (or at least precisely-scanned) heap. This is a serious, multi-month compiler-engineering project in its own right, and disproportionate to what a personal, exploratory compiler like this one needs right now.
4. **A conservative garbage collector, e.g. the Boehm–Demers–Weiser collector (`libgc`)** — swap the declared `@malloc`/`@realloc` symbols for `@GC_malloc`/`@GC_realloc` (a handful of `ensure*()` edits in `runtime.go`), link `-lgc`, and stop there. The collector conservatively scans the native stack, registers, and its own managed heap for anything that looks like a pointer into a live allocation, and reclaims blocks nothing points to — no ownership discipline, no stack maps, and no changes needed to any of the existing per-feature codegen files beyond the allocation call sites themselves. It's decades-old, production-hardened (used by Mono's older runtimes, GCC's Objective-C runtime, GNU Guile, and others), and it's specifically designed for exactly the "every pointer is just a plain pointer, freely copied around, with zero ownership bookkeeping" world this compiler already lives in.

**Decision**: option 4 (Boehm GC) is the actual fix — by a wide margin, the best effort-to-correctness ratio for this codebase's current shape, since it requires no redesign of how any existing feature allocates or passes around heap values. Trade-offs worth knowing: it's the project's first non-libc external dependency (adds a build/deploy requirement that doesn't exist today), it can rarely retain a block slightly longer than necessary if a stray bit pattern happens to look like a pointer (never a use-after-free — only ever a delayed free), and it introduces stop-the-world collection pauses that would need re-evaluating once/if the event-loop/networking work makes this a latency-sensitive long-running service. Option 2 (arenas) is worth revisiting later as a targeted optimization on top of a GC, for the specific allocations that are provably short-lived — not as a replacement for it.

### Alternative/complementary direction: user-controlled (manual) memory release

A GC (above) is the "safe by default" answer. The other side of this coin is giving the *program itself* a way to explicitly release memory it knows it's done with — useful as an escape hatch even after a GC exists (e.g. to control latency-sensitive frees in a future long-running service), and potentially useful as an interim step before a GC is built at all. Worth understanding the landscape before picking a design, since manual memory management is a much older and better-studied problem than it might first appear.

**The three families of memory management, in general:**

1. **Manual (C/C++)** — the programmer calls `free`/`delete` explicitly. Fast and precise, zero runtime overhead, but the programmer is fully responsible for correctness: forget to free → leak; free twice → corruption; free something still referenced elsewhere → use-after-free, often surfacing as a crash somewhere completely unrelated later. This is the world KlainMainLang lives in today, just without even the `free()` half.
2. **Automatic tracing GC (Java, Go, JS, Python-ish)** — the runtime periodically determines which heap values are still reachable from the stack/globals/registers and frees everything else. Safe by construction (nothing is ever freed while still reachable), at the cost of runtime overhead and some kind of pause/throughput hit. This is the Boehm-GC option discussed above.
3. **Ownership/borrowing, checked entirely at compile time (Rust)** — no runtime GC *and* no manual `free()` calls, because the compiler statically proves exactly when a value's last use happens and inserts the cleanup itself.

**How Rust's model actually works, since it's the interesting third option:**

- Every value has exactly one **owner** (a variable binding). When that owner's scope ends, the compiler automatically inserts a call to the value's destructor (`drop`) — this is RAII (a discipline borrowed from C++), made mandatory and compiler-enforced everywhere rather than opt-in.
- **Assignment moves ownership**, and the compiler tracks it: `let b = a;` (for a non-trivially-copyable type) transfers ownership from `a` to `b`; afterwards, using `a` again is a *compile-time* error, not a runtime check. This is what rules out double-free and use-after-free — the compiler simply refuses to build a program that would do either.
- **Borrowing** (`&T` shared, `&mut T` exclusive) lets code use a value without taking ownership of it. The borrow checker enforces, statically: many shared borrows *or* one exclusive borrow (never both), and no borrow may outlive the value it points to. This replaces "the GC keeps it alive as long as something points to it" with "the compiler proves nothing can point to it after it's gone."
- When single ownership genuinely isn't expressive enough (data that's legitimately shared — e.g. a graph node, or state shared across threads), Rust's answer is `Rc<T>`/`Arc<T>` — i.e. **reference counting, opted into per-value** for the specific cases that need it, not a whole-program GC. Even Rust's own escape hatch is refcounting.
- The reason none of this needs a runtime GC is that the entire discipline is enforced *before the program runs*, by a dedicated compiler pass (the borrow checker) doing lifetime inference across each function. That pass is one of the most complex parts of the whole Rust compiler — a multi-year, still-evolving piece of engineering (the "non-lexical lifetimes" rewrite alone took years) — and building the equivalent here would be disproportionate to this project's scope. It'd also be a language-design mismatch: TypeScript programmers don't think in terms of "who owns this value," and bolting `&`/`move` syntax onto TS-shaped code would mean designing a new language, not extending this one.

**What's actually feasible for KlainMainLang — a staged plan, from cheapest to most Rust-like:**

- **Stage 1 — a raw, unsafe `Memory.free(x)` builtin.** ✅ **Done** — see `docs/adr/ADR-00030.md`. Resolves `x`'s underlying heap pointer (array data pointer, object struct pointer, closure header + environment, Map/Set backing buffers) and frees it. Shallow only: frees the value's own top-level allocation(s), never anything reachable *through* it (a string field inside a freed object, a captured variable's shared cell) — no analysis, no safety net beyond nulling out a named variable's own storage after freeing it. The programmer is fully responsible, exactly like C — including the same C-shaped footgun where a string *literal* is a compile-time global constant, not malloc'd, so freeing one crashes exactly like C's own `free("literal")`.
- **Stage 2 — scope-exit auto-free via a JSDoc annotation, plus a cheap escape check.** e.g. `/** @free */ let buffer: number[] = loadBigThing()` — the compiler inserts `free(buffer)` at every exit path of the enclosing block (return, break, throw, fallthrough), the same places `finally`-block cleanup already hooks into. The part that actually matters for safety: before allowing `@free`, do a conservative, purely local check — does this identifier ever appear in a `return`, get assigned to a variable from an outer scope, get passed somewhere it might be stored, or get captured by a closure? If it might escape the block, reject `@free` at compile time rather than silently creating a dangling reference elsewhere. This is *not* Rust's lifetime inference — it's a much smaller, local check — but it gets most of the ergonomic and safety value of "drop at end of scope" without needing anything like a borrow checker, since scopes here are already lexical/block-based.
- **Stage 3 — a `@owned`/linear-value annotation with last-use liveness analysis.** Closer to Rust in spirit than Stage 2: mark a value as single-owner, and have the compiler free it right after its statically-determined *last use* — not at block-exit (which can be too late for a long block, or express the wrong granularity), but exactly where data flow says it's no longer needed. Natural fit for functional/pipeline-style code, where each stage consumes its input and produces a new output (e.g. `function transform(/** @owned */ input) { const out = input.map(...); return out }` — the compiler frees `input` right after the `.map()` line, with no explicit free call needed anywhere). Needs a liveness analysis (a real, well-understood, bounded compiler pass — nowhere near a full borrow checker, but a genuine step up from Stage 2's block-exit model) plus the same escape check as Stage 2, applied at the last-use point instead of the block boundary.
- **Packaging** (multi-file/import support now exists — see the Modules section above): whether this lives as a global (`Memory.free`, as in Stage 1) or behind an explicit `import { free } from "std/memory"` is a separate, orthogonal decision about API discoverability/opt-in visibility — not about the underlying safety mechanism. Cheapest path: ship it as a global first, move it into a real module later (a pure reorganization).

**Sequencing**: Stage 1 is done (see above). Stage 2 is the natural next step and good value for the cost, since the escape check is simple and the scope-exit hooks already exist elsewhere in the emitter. Stage 3 is worth keeping on the roadmap as the more complete answer, best attempted once Stage 2's block-granularity has been felt to be too coarse in practice, rather than designed abstractly up front.

**Status**: Stage 1 (`Memory.free(x)`) done; Stages 2 and 3 still design-only, not started. Not yet decided against the GC path above (they're complementary, not mutually exclusive — a program could have a GC *and* an explicit `Memory.free` escape hatch for latency-sensitive spots).

---

## Timers — Design Notes (done)

Tracked in the summary tables as [Web Platform APIs → Timers](#timers) above. `setTimeout`/`clearTimeout`/`setInterval`/`clearInterval` — the STATUS.md line for these used to say "require an event loop," but that overstated the prerequisite: timers only needed a much smaller piece of infrastructure than the general-purpose, I/O-multiplexing event loop the HTTP Server section below calls out as its own biggest blocker. All a timer needed was a sorted-by-fire-time queue and a loop that sleeps until the next one is due — no socket/file-descriptor readiness polling involved at all. Written up here before any code was implemented, and left in place afterward as the design record — the entry-layout detail below (`intervalMs = -1` as the cancellation sentinel, rather than a separate flag) is the one thing that changed between this write-up and what actually shipped.

**Why this is a genuinely new execution shape, the same way the HTTP server is:** every program this compiler has ever produced runs its top-level code once, top to bottom, and exits. Timers need a phase *after* that top-level code finishes where the program can still be doing something — sleeping and then calling back into user code — for the first time ever outside of the top-level statement list itself.

### Design

- **API shape**: `setTimeout(callback: () => void, delayMs?: number): number` / `clearTimeout(id: number): void`, and the same shape for `setInterval`/`clearInterval`. Bare global functions (like `fetch`/`btoa`/`parseInt`), not a namespace — matching how real JS/Node expose them. Callback takes no arguments and returns nothing for V1 (real JS also allows extra args passed through to the callback after the delay; deferred, a natural, separable follow-up, the same kind of incremental scope-narrowing `fetch` and `execFileSync` both started with).
- **The timer queue**: a global growable array of fixed-size entries (`{ i64 id, i64 fireAtNs, i64 intervalMs, ptr closureHdr }` — 32 bytes, every field naturally 8-byte aligned with no padding ambiguity to reason about; `intervalMs = -1` doubles as the "cancelled / already fired and done, never consider again" sentinel, chosen over a separate flag field specifically to keep every field a plain i64/ptr), using the exact same `{ ptr data, i64 len, i64 cap }` realloc-doubling growable-buffer shape already proven three times over (`__kml_fetch`'s curl write callback, `__kml_exec_file_sync`'s stdout capture, `__kml_fs_readdir`'s entry list) — just holding fixed-size structs instead of bytes or `ptr`s this time. A **linear scan** to find the next-due entry on each loop iteration, not a real priority queue/heap — O(n) per tick is fine for what's realistically a handful of concurrent timers in a CLI/microservice tool, and a sorted heap is real complexity this V1 doesn't need to take on.
- **Firing**: `clock_gettime(CLOCK_MONOTONIC)` for "what time is it" (the same monotonic clock `performance.now()` already uses) and `nanosleep` to wait for the next one — both already-used-elsewhere primitives, no new C dependency. `clearTimeout`/`clearInterval` just flip the `cancelled` bit on a linear-scan match by id; a bogus or already-fired id is a silent no-op, matching real JS's own lenient behavior.
- **Calling the callback closure is not new ground.** Same `call RetTy (ptr, ArgTys...) %fp(ptr %envPtr, args...)` convention the `qsort` comparator trampolines already prove (`ensureSortTrampolineI64`/`F64`, `runtime.go`) and the HTTP Server design below also plans to reuse — a callback with no arguments and a `void` return is the *simplest* possible case of this pattern, simpler even than the sort comparators.
- **Where the drain loop goes**: `EmitProgram` (`codegen/llvm/emitter.go:409`) currently emits the top-level statement list followed directly by `ret i32 0`. The timer-drain loop (`while` any non-cancelled entry remains: find the soonest, sleep if it isn't due yet, call it, reschedule if it's a `setInterval` entry or drop it otherwise) needs to run *after* the last top-level statement and *before* that final `ret i32 0` — gated behind a `usedTimers`-style flag so a program that never calls `setTimeout`/`setInterval` doesn't pay for the check at all.
- **The one real, new-territory behavior change**: every example and test this compiler has ever produced runs and exits immediately. An active `setInterval` with nothing ever calling `clearInterval` on it means the drain loop's queue is never empty — the process simply never exits on its own, matching real Node's actual behavior (an active interval keeps the event loop, and therefore the process, alive). `clearInterval` or `process.exit()` become the only ways out. This needs deliberate care in tests (bound every test's timers with a `clearInterval` or a matching one-shot `setTimeout`, and pick small delays so the suite doesn't slow down) and in the example (documented prominently, not left as a surprise).
- **Uncaught exceptions from inside a fired callback**: should fall through to the exact same top-level uncaught-exception handling every other uncaught throw already goes through (`__kml_throw`/the top-level `setjmp` — `emit_exceptions.go`), since the drain loop is just more code running inside `main()`, inside that same enclosing scope. Worth confirming directly during implementation rather than assumed, but not expected to need any new plumbing.

### Prerequisites — how this sits against what's already built or still missing

| Dependency | Status | Notes |
|---|---|---|
| Closure-calling-from-C-trampoline pattern | ✅ already proven | The `qsort` comparator trampolines are a direct, verified precedent — see Design above |
| Monotonic clock (`clock_gettime(CLOCK_MONOTONIC)`) | ✅ already done | `performance.now()` (`ADR-00024`) already uses exactly this |
| Growable-buffer-of-fixed-size-structs pattern | ✅ already proven (for bytes/ptrs) | `__kml_fetch`/`__kml_exec_file_sync`/`__kml_fs_readdir` all use the same `{ptr,i64,i64}` realloc-doubling shape already — this would be the first use of it for a struct element instead of a byte or a `ptr`, a small, mechanical extension, not new ground |
| Exception/throw mechanism | ✅ already done | An uncaught throw from inside a callback should route through the existing top-level handler with no new plumbing needed |
| General-purpose (I/O-multiplexing) event loop | **not needed for this feature at all** | The HTTP Server section below is the one that actually needs it, for concurrent connection handling — timers only ever need a sleep-until-next-due loop, a meaningfully smaller problem |
| Garbage collection / memory management | ⚠️ not a blocker to *start*, same caveat as the HTTP Server section below | A `setInterval` that runs for a program's entire (now possibly long) lifetime allocates a fresh closure environment on every fire if the callback itself allocates — for a short-lived demo this is a non-issue; for a long-running interval-heavy program it's the same "never free anything" concern already tracked under Memory Management above |

**Status**: done. See `docs/adr/ADR-00031.md` for the implementation, the two real bugs found and fixed along the way (unrelated to timers themselves — `"+"` silently producing invalid IR for a string/non-string operand pair, and a variable's own initializer capturing itself in a closure silently never seeing the real assigned value), and verification.

---

## Alternative fetch Backend: a Go Helper Instead of libcurl — Scoping (not started, low priority)

Came out of `ADR-00033`'s `--static` + `fetch` investigation: getting a statically-linked `fetch`-using binary working on Alpine/musl needed curl's entire transitive dependency chain listed explicitly, a `gcc`-not-`clang` final link step to work around an LTO-format-incompatibility in two of Alpine's static archives, and a distro-specific CA-certificate-path fix — none of which is portable to other distros with any confidence. Worth asking directly: was `libcurl` (`ADR-00021`) the right foundation for `fetch` at all, given this compiler's *own* toolchain is Go, and Go's `net/http`+`crypto/tls` stack is memory-safe and statically-linked (zero external C dependencies) by default with `CGO_ENABLED=0` — precisely the property this project fought hard to get out of `libcurl` in `ADR-00033`. Written up here, not started, and explicitly **not a near-term priority** — this project is still early enough that `libcurl`'s dynamic-linking default already works fine for the common case, and this is a foundational swap worth doing thoughtfully later rather than mid-flight.

### Design

- **The core idea**: build a small, separate Go binary (`net/http` + `crypto/tls`, compiled with `CGO_ENABLED=0`) as part of building the KlainMainLang compiler itself, embed its bytes into the compiler via `//go:embed`, and — for a program that calls `fetch` — have the compiled native binary `fork`+`exec` that helper at runtime instead of calling into `libcurl` directly. This is *not* new ground: `execFileSync` (`ADR-00025`) already proves the exact fork/exec/pipe mechanics needed (spawn a child process, capture its stdout, wait for it), just spawning a purpose-built helper instead of an arbitrary user-named one.
- **Wire protocol**: the helper would need to receive the URL (as `argv[1]`, matching `execFileSync`'s own argv-passing convention already built) and return status/body somehow — simplest shape: the helper prints a small JSON envelope (`{"status":200,"body":"..."}`) to its own stdout, encoded with Go's own `encoding/json` (correct escaping for free, not this compiler's own limited `JSON.stringify`), and the KML-emitted `fetch()` call site parses that envelope the same way `Response.json()` already parses arbitrary JSON today.
- **The `--static` connection**: rather than a fully separate, always-visible toggle, the natural trigger is `--static` itself — use the Go helper automatically when `--static` is requested (exactly the scenario where `libcurl`'s static story is painful), and keep `libcurl` as the default for a normal (dynamic) build, where it already works with no friction. Avoids asking every user to understand a second axis of choice they mostly won't care about.
- **The real, honest cost this doesn't erase**: today, a compiled KlainMainLang program is one file. A companion helper binary means either shipping two files, or the compiled program self-extracting an embedded helper to a temp path before exec'ing it (more moving parts than exist today, though Go's `//go:embed` makes bundling the helper's *bytes* into the compiler itself easy — the harder part is getting those bytes from the compiler binary into something the *compiled program* can exec at its own runtime). This needs a real design decision, not just an implementation pass.
- **Feature parity is a non-issue for the current scope** (GET-only, no headers/body — `net/http` covers that trivially) but would need revisiting if `libcurl`'s more exotic features (proxies, unusual auth schemes) are ever wanted on the `--static` path specifically.

### Prerequisites — how this sits against what's already built or still missing

| Dependency | Status | Notes |
|---|---|---|
| Fork/exec/pipe process-spawning pattern | ✅ already proven | `execFileSync` (`ADR-00025`) is a direct, verified precedent for exactly the mechanics needed |
| Passing an argument + capturing stdout from a spawned process | ✅ already done | Same `execFileSync` machinery — a URL instead of an arbitrary command's argv |
| JSON parsing of the helper's response envelope | ✅ already done | Reuses the existing `JSON.parse`/`Response.json()` machinery (flat objects, primitive fields — the same scope constraint that already applies to `fetch` today) |
| Embedding a second, pre-built Go binary inside the compiler itself | ❌ not attempted | `//go:embed` makes this straightforward in principle, not yet tried here |
| Getting the embedded helper's bytes into something the *compiled program* (not the compiler) can execute at its own runtime | ❌ not designed | The one genuinely new problem — self-extract-to-temp-file vs. ship-a-second-file are the two live options, no decision made yet |
| `--static`-triggered backend selection in `main.go`/codegen | ❌ not attempted | Small once the rest exists — the same "check a flag, branch codegen" shape `--static` itself already uses |

**Status**: scoped, not started, and deliberately deprioritized for now — noted directly by the user as worth revisiting later, not urgent while the project is still early. No ADR yet — write one if and when this is actually picked up.

---

## HTTP Server — Scoping (not started)

Tracked in the summary tables as [Node.js APIs → HTTP Server](#http-server) above (Node-specific — no browser equivalent for listening on a socket). `fetch` (`ADR-00021`) covers the *client* side of HTTP. This scopes the *server* side — exposing a basic web server / REST API, the natural next step and the piece that actually unlocks this project's "microservice" priority. Written up in detail, before any code, specifically so the prerequisites below can be picked off incrementally rather than discovered mid-implementation.

**Why this is a meaningfully different, harder feature than `fetch`, not just "the reverse of it":** every program this compiler has ever produced runs its top-level code once and exits. A server has to keep running indefinitely, accept things happening to it *over time*, and dispatch each one back into user code — a genuinely new execution shape, not just another builtin function.

### Design

- **API shape**: `http.listen(port: number, handler: (req: Request) => T): void`, where `T` is any object type with at least `status: number` and `body: string` fields (checked structurally at the call site, not through a dedicated named response type — see below). A single combined call rather than Node's two-step `http.createServer(handler).listen(port)` — this compiler has no general user-defined-methods mechanism, and V1 has no need for multiple servers, inspecting server state, or `.close()`, so modeling a whole `Server` object with method dispatch (the `Date`/`Response`-style special-type pattern) would be pure overhead for no present benefit. `.listen()` never returns, the same category of thing as `process.exit()` (a call after which the rest of `main()` is correctly unreachable).
- **`Request`**: a new special built-in type (built by the runtime, not by user code — same reasoning as `fetch`'s `Response`), scoped to `{ method: string, path: string }` for V1. No headers, no query-string parsing, no request body — all natural, separable V2 follow-ups, deliberately deferred the same way `fetch` itself started GET-only before gaining offset-parsing, setters, etc. in later passes.
- **The outgoing response needs *no* new special type at all.** Unlike `Request` (which the runtime constructs), the handler's return value is something *user code* builds — an ordinary object literal or interface value. This compiler's existing object machinery (heap-alloc + GEP field access, the same thing every interface/object literal already uses) already handles that with zero new code: since `http.listen`'s call site knows the handler's declared return type statically, the field offsets for `status`/`body` can be computed at compile time, exactly like any other object field read. Only `Request` needs the "special built-in type" treatment; the response side rides entirely on infrastructure that already exists.
- **Dispatching each incoming request into the user's handler closure is not new ground.** Verified directly (not assumed) by re-reading `emitClosureCallByPtr` (`emit_func.go`) and the `qsort` comparator trampolines (`ensureSortTrampolineI64`/`F64`, `runtime.go`): a closure is always called as `call RetTy (ptr, ArgTys...) %fp(ptr %envPtr, args...)`, and the existing sort trampolines already prove the exact pattern needed here — a global slot holds the closure pointer (`@__kml_sort_clos = global ptr null`, set once before the C-level loop starts), and a small hand-written trampoline function loads it, splits it into `{funcPtr, envPtr}`, and calls through using that same convention. An HTTP dispatch trampoline is the same shape of problem: store the handler closure in a global slot once (at the `http.listen` call site), then have the accept-loop's per-connection trampoline load it, build a `Request` object, call through, and read `status`/`body` off whatever came back.
- **Socket/protocol handling: hand-rolled raw POSIX sockets, not a linked HTTP server library — a deliberate choice, not a default.** Checked concretely: `libcurl` (already linked for `fetch`) is client-only and doesn't help here at all. Considered linking a small embeddable server library (e.g. `libmicrohttpd`), mirroring the `libcurl` choice for the client side — but confirmed it isn't installed on this dev machine by default (unlike `libcurl`, which ships with the macOS SDK), while plain POSIX socket functions (`socket`/`bind`/`listen`/`accept`/`read`/`write`/`close`) are always present, no installation required. Given V1's scope is already GET-only with no headers exposed and no persistent connections, the actual parsing job is close to trivial (read up to the blank line, `sscanf` the request line for method + path, ignore the rest, always respond with `Connection: close`) — hand-rolling this avoids a new external dependency for a protocol slice this thin. Revisit linking a real HTTP library if/when fuller HTTP/1.1 compliance (keep-alive, chunked encoding, request bodies) is actually wanted — the thin V1 slice doesn't need one yet.
- **Concurrency: single-threaded, one connection at a time, for V1 — a deliberate, documented limitation, not an oversight.** A slow handler blocks every other client. Doing better needs either a hand-rolled event loop (a real, separate, large piece of work — see Prerequisites) or a one-thread-per-connection model, which would require auditing this compiler's runtime for thread-safety for the first time ever (the exception system's jump-buffer stack, interned string globals, and every growable-buffer pattern all currently assume single-threaded execution, implicitly, everywhere). Starting single-threaded sidesteps that audit entirely for V1.

### Prerequisites — how this sits against what's already built or still missing

| Dependency | Status | Notes |
|---|---|---|
| Link-flags plumbing (`ADR-00020`) | ✅ already done | Only needed at all if a library ends up being linked later (raw sockets need no `-l` flag beyond libc) |
| Closure-calling-from-C-trampoline pattern | ✅ already proven | The `qsort` comparator trampolines are a direct, verified precedent (see Design above) |
| Object heap-alloc + GEP field access | ✅ already done | Used for both the new `Request` type and reading the handler's arbitrary response type |
| Exception/throw mechanism | ✅ already done | Reusable for reporting `bind()` failures (e.g. "address already in use") as a catchable `Error`, matching the `fetch`/`fs` precedent |
| Event loop | ⚠️ not a hard blocker for V1, but the ceiling on how good this can get | V1 sidesteps it entirely (single connection at a time). Real concurrent request handling needs it — this is the single biggest reason a "good" version of this feature is harder than it looks at first glance |
| Garbage collection / memory management | ⚠️ not a blocker to *start*, but a real blocker to this being genuinely useful for anything long-running | Every request currently allocates a `Request` object, string buffers, etc. that never get freed (see "Memory Management" above) — a demo server handling a handful of requests is fine; a real long-running service is exactly the scenario where "never free anything" stops being a footnote. **This feature is the concrete reason to prioritize the Memory Management work sooner rather than later.** |
| Signal handling (`SIGINT`/`SIGTERM`) for graceful shutdown | ❌ not tracked anywhere else | A genuinely new gap this feature surfaces, not previously needed by anything. Deferred for V1 — the process just runs until killed. |
| `Headers`, request bodies, query-string parsing | ❌ deliberately deferred, same pattern as `fetch`'s own GET-only V1 | Natural, separable V2 follow-ups once the core mechanism exists |

**Status**: scoped, not started. No ADR yet — write one when this is picked up, and reconsider whether Memory Management should land first given the dependency noted above.

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
| HTTP Server | 0 | 1 | 0% |
| **Node.js total** | **21** | **24** | **~88%** |

---

## Roadmap

Grouped by kind of work rather than a fixed sequence number, since priorities shift and bug fixes get picked up opportunistically rather than in strict order. Core-language feature gaps already have their own priority/complexity breakdown in [What Is NOT Implemented](#what-is-not-implemented) above — not repeated here.

### Next up — bugs found but not yet fixed

Pulled from [Known Limitations & Bugs](#known-limitations--bugs) above: the ones worth fixing outright, as opposed to the ones documented there as deliberate, permanent scope narrowings (e.g. `any`'s boolean-printing convention).

| Fix | Effort | Notes |
|---|---|---|
| Functions with no explicit return-type annotation can't return an object literal; arrow functions can't have a returned closure called regardless of annotation | Medium | Two symptoms of a related type-inference gap: a value inferred from a call isn't marked as an object/function type when the callee is a plain function with no return-type annotation, or when the callee is an arrow function at all |
| Interface fields can't be declared `float64`/`float32` | Medium | Object/interface field types resolve through a plain type-name path with no JSDoc-override mechanism, unlike variable declarations (where `/** @type {float64} */` already works) |
| `fetch`/`fs` bodies containing embedded null bytes silently truncate | Deferred | Root cause is this compiler having no `ArrayBuffer`/TypedArrays yet (0% implemented) — not fixable in isolation; tracked as a consequence of that gap in the Web Platform & Node.js APIs backlog below |

### Structural priorities

The three biggest cross-cutting gaps — each affects multiple features rather than being one self-contained item, and each already has its own detailed writeup above:

1. **Memory management — no garbage collector** (see [Memory Management](#memory-management--todo-no-garbage-collector) above). Decision already made (Boehm GC: swap `@malloc`/`@realloc` for `@GC_malloc`/`@GC_realloc`, link `-lgc`); not started. A non-issue for today's short-lived CLI programs, but a hard blocker for anything long-running — concretely, for the HTTP server below.
2. **General-purpose (I/O-multiplexing) event loop.** Needed for real non-blocking `fetch`, `Promise.all`/`.race`, and concurrent HTTP request handling. Currently 0% — the single biggest structural gap relative to this project's stated microservice direction. Timers (`setTimeout`/`setInterval`) turned out *not* to need this — see [Timers — Design Notes](#timers--design-notes-done) above — and are already done as a result.
3. **HTTP server** (see [HTTP Server — Scoping](#http-server--scoping-not-started) above). Scoped in detail, not started. The concrete feature that unlocks the "microservice" half of this project's long-term direction; its own prerequisites table already flags memory management as the thing worth landing first.

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
