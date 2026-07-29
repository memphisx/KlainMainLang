# KlainMainLang — Implementation Status

> TypeScript → native compiler written in Go. Emits LLVM IR text, compiled with `clang -O2`.
> Targets whatever architecture the host clang defaults to (arm64 on Apple Silicon, x86-64 on Linux, etc.).
> Multi-file compilation exists (named `import`/`export` only, V1 scope — see the Modules section below); the entry file's top-level statements still all run in one `main()`, and imported files may only contain declarations.
> No garbage collector by default — every heap allocation is `malloc`'d and (almost) never `free`d in `manual` mode. `-mm=gc` opts into a real one (Boehm). See "Memory Management" below.
> Programs are pure libc by default; a program only needs `libcurl` installed on the build machine if it actually calls `fetch` (compiled binaries automatically link `-lcurl` only when used — see [ADR-00020](docs/adr/ADR-00020.md)).

## Contents

- [What Is Implemented](#what-is-implemented) — core JavaScript/TypeScript language & standard library (works the same in any JS host)
- [Web Platform APIs](#web-platform-apis) — WHATWG/browser-standard APIs (also implemented by Node.js, but not part of the JS *language* itself)
- [Node.js APIs](#nodejs-apis) — `fs`, `process`, and a real `http.listen` server — Node-specific runtime globals with no browser equivalent
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
| `for…of` over arrays, `Map` (iterates values), `Set` (iterates elements), and a class implementing `next(): T \| null` | ✅ | No `[key,value]` destructuring in for-of, so Map iterates values, not entries — use `.keys()` for keys; see [ADR-00011](docs/adr/ADR-00011.md). The class case ([TDD-00009](docs/tdd/TDD-00009.md) Stage 1a) is a compile-time structural check (no runtime `Symbol.iterator`), and reuses the same "0/null doubles as absent" sentinel convention `.find()` already uses — see [ADR-00063](docs/adr/ADR-00063.md). |
| `for…in` over object keys | ✅ | |
| `switch` / `case` / `default` / `break` | ✅ | Numeric, string, and boolean discriminants |
| `break` / `continue` in loops, including labeled (`outer: for (...) { break outer; }`) | ✅ | See [ADR-00010](docs/adr/ADR-00010.md) |
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
| `async` functions | ✅ | Returns `Promise<T>`; malloc-based slot. Named `async function` declarations and `async (...) => ...` arrow functions both supported (the arrow-function case was a real gap found and fixed alongside [ADR-00050](docs/adr/ADR-00050.md) — it silently returned its value unwrapped instead of via the Promise slot). |
| `await` expressions | ✅ | Loads value from slot, frees it — except `await` on `fetch()`'s own `Promise<Response>`, which really waits (yielding via a fiber if inside an `http.listen` connection handler) since [ADR-00050](docs/adr/ADR-00050.md), not just an already-resolved read. |
| `Promise.all` / `.race` / `.allSettled` | ✅ | Over `Array<Promise<Response>>` (fetch()'s own Promise type): real concurrency — N in-flight fetches waited on together via the event loop, not one at a time. Over any other `Array<Promise<T>>`: every element is already resolved by construction (this compiler has no pending promises outside `fetch()`), so these honestly collect/pick/report already-settled values rather than faking parallelism. Each combinator call consumes (frees) every element's own Promise slot, same as a plain `await` — the same array can't be fed to a second combinator call. See [ADR-00073](docs/adr/ADR-00073.md)/[TDD-00016](docs/tdd/TDD-00016.md). |
| Enums (numeric) | ✅ | Auto-increment, explicit values |
| Enums (string) | ✅ | |
| Interfaces (structural) | ✅ | Heap-allocated objects |
| Type aliases | ✅ | |
| Object literals `{ key: value }` | ✅ | |
| `new Error(msg)` | ✅ | |
| `new Array<T>(n)` | ✅ | |
| `new Map<K,V>()` | ✅ | |
| `new Set<T>()` | ✅ | |
| `class` (fields, constructor, methods, `this`, `new ClassName(args)`) | ✅ | [TDD-00009](docs/tdd/TDD-00009.md) Stage 1 — instances reuse the same heap-object/GEP machinery interfaces already use; methods compile to plain static calls (`this` as an implicit first arg), no closure indirection. A class with fields requires an explicit constructor (no field initializer syntax yet — every field must be set explicitly, same philosophy object literals already enforce). See [ADR-00063](docs/adr/ADR-00063.md). |
| `class` inheritance (`extends`/`super`) | ❌ | Staged design in [TDD-00009](docs/tdd/TDD-00009.md) — the only remaining stage that needs new dynamic-dispatch machinery |
| `instanceof` (against user-defined classes) | ✅ | [TDD-00009](docs/tdd/TDD-00009.md) Stage 2 — every instance carries a hidden runtime type tag. Before inheritance exists, a class-typed variable's concrete class is already known statically, so this folds to a compile-time constant except for an `any`/`unknown`-typed value, where the tag is read back at runtime — the one case that does real work. See [ADR-00067](docs/adr/ADR-00067.md) for the built-in-type/unregistered-class compile-error behavior and the `Error` subtypes follow-on in [TDD-00013](docs/tdd/TDD-00013.md). |

### Modules

Whole-program compilation, not separate compilation units: `resolver.ResolveProgram` parses the entry file plus everything it transitively imports and merges them into one `*ast.Program` before codegen runs — there is no linker step, no per-file LLVM module boundary, and `codegen/llvm` never sees an `import`/`export` node. See [ADR-00022](docs/adr/ADR-00022.md) and `resolver/resolver.go`'s package doc for the full design.

| Feature | Status | Notes |
|---|---|---|
| `export function` / `const`/`let`/`var` / `interface` / `type` / `enum` | ✅ | A declaration-level modifier, nothing more — consumed entirely by the resolver |
| `import { a, b } from './relative/path'` | ✅ | Named imports only; relative paths only (`./`, `../`), `.ts` auto-appended if omitted; resolved against the importing file's own directory, not `cwd` |
| Circular imports | ✅ | Supported for the declarations-only case — verified directly with two files calling each other's exported functions |
| Diamond-shaped import graphs | ✅ | A file imported from multiple places is parsed once and merged once (memoized by absolute path) |
| Imported (non-entry) files may run top-level side-effecting code | ❌ | **Deliberate V1 scope narrowing, not an oversight** — imported files may only contain declarations (and their own imports); only the entry file's top-level statements execute. Real ES modules run a file's top-level code once, in dependency order, the first time it's imported — that "run once, in order, guard against re-running on cycles" semantics is real design/implementation work of its own, intentionally deferred. **Revisit this later**: build the fuller, real-ES-modules-shaped version, possibly gated behind a compiler flag/configuration so callers can choose between the fast/simple current behavior and full module-execution semantics once both exist. |
| True per-file module scope (mangled internal names) | ❌ | All top-level declaration names must currently be unique across *every* merged file, not just within one file — there's no real per-file scoping yet, so two unrelated files can't both declare a same-named function/interface/enum if both end up reachable from the same entry file. A real fix needs per-file symbol registries and internal name mangling (sketched in [ADR-00022](docs/adr/ADR-00022.md)'s Investigation) — bigger than V1's scope. |
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
| Union types beyond `T \| null` | ❌ | Parser discards every union member except the first for anything other than `null`/`undefined` — needs parser work, separate from the `any`/`unknown` tagged-value system below; see [ADR-00008](docs/adr/ADR-00008.md) |
| Intersection types | ❌ | |
| Tuple types | ❌ | |
| Mapped / conditional types | ❌ | |
| `any` | ✅ (Staged V1: declare/assign/reassign/print/`typeof`/`===`; arithmetic and use as a function param/return/array/object-field type are ❌ with a clean compile error — see [ADR-00008](docs/adr/ADR-00008.md)) | |
| `never` | ✅ | A function typed `(): never` that always throws works correctly |
| `unknown` | ✅ (same Staged V1 scope as `any` — see above) | |
| `symbol` | ❌ | |
| `bigint` | ❌ | |
| Generics on user functions/interfaces | ❌ | Only built-in generics (`T[]`, `Promise<T>`, `Array<T>`, `Map<K,V>`, `Set<T>`) — staged design for user-defined generics in [`docs/tdd/TDD-00010.md`](docs/tdd/TDD-00010.md). A real bug found while researching that TDD — `Array<T>`/`Map<K,V>`/`Set<T>` used as a plain type annotation (not `new X<T>()`) silently defaulting to `i64` — is fixed; see [ADR-00058](docs/adr/ADR-00058.md). |

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
| `.split(sep)` | ✅ (empty separator splits into individual characters, matching JS — previously hung; see [ADR-00004](docs/adr/ADR-00004.md)) |
| `.trim()` | ✅ |
| `.trimStart()` / `.trimEnd()` | ✅ |
| `.toUpperCase()` | ✅ |
| `.toLowerCase()` | ✅ |
| `.repeat(n)` | ✅ |
| `.padStart(len, pad?)` | ✅ (empty pad string is a no-op, matching JS — previously corrupted output; see [ADR-00004](docs/adr/ADR-00004.md)) |
| `.padEnd(len, pad?)` | ✅ (same empty-pad fix as `.padStart`) |
| `.charCodeAt(i)` | ✅ |
| `.at(i)` | ✅ |
| `.charAt(i)` | ✅ (unlike `.at()`, never wraps a negative index from the end — always `""` for any out-of-range `i`, matching real JS's distinction between the two methods. See [ADR-00028](docs/adr/ADR-00028.md).) |
| `.codePointAt(i)` | ✅ (this compiler's strings are plain byte sequences, not real UTF-16 — no surrogate-pair/multi-byte decoding, so this is exactly `.charCodeAt(i)`'s byte value under a second name. Correct for ASCII/Latin-1 text; a documented scope narrowing for anything needing real Unicode decoding. See [ADR-00028](docs/adr/ADR-00028.md).) |
| `.normalize()` | ❌ (deliberately deferred, not attempted — needs real Unicode normalization tables (NFC/NFD/NFKC/NFKD) this compiler has no infrastructure for at all; a fake identity-only implementation would silently mis-normalize any non-ASCII composed/decomposed text, exactly the "silent wrong output" failure mode this project avoids) |
| `.match()` / `.matchAll()` | ❌ (needs a real `RegExp` engine — tracked separately, see "What Is NOT Implemented" below) |
| `.search(pattern)` | ✅ (real JS coerces `pattern` to a `RegExp`; this compiler has no `RegExp` type or regex literal syntax at all, so a plain string is the *only* value that could ever reach this call — making this exactly `.indexOf`'s behavior under a second name, not a partial regex implementation. See [ADR-00028](docs/adr/ADR-00028.md).) |
| `.replaceAll()` | ✅ (empty search is a no-op, not JS's insert-between-chars behavior — see [ADR-00003](docs/adr/ADR-00003.md)) |
| `.localeCompare(other)` | ✅ (byte-order comparison via `strcmp`, normalized to exactly `-1`/`0`/`1` — not real Unicode collation, this compiler has no locale/`Intl` infrastructure, the same scope narrowing already used for `toLocaleDateString`. See [ADR-00028](docs/adr/ADR-00028.md).) |
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
| `.splice(start, delete?, ...items)` | ✅ (`delete` clamps to `[0, len - start]` and `start` normalizes negative indices, matching real JS — an over-large `delete` used to read past the backing allocation and corrupt the array's own length to negative, a real memory-safety bug, not just a wrong-answer one; `...items` insertion wasn't implemented at all despite the row already claiming it. Both fixed together. See [ADR-00056](docs/adr/ADR-00056.md).) |
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
| `.flat(depth?)` | ❌ (blocked on nested-array support — `number[][]`-style literals aren't reliably representable yet: `[[1,2],[3,4]]` fails to compile with "array literal must be used in a variable declaration" for the nested literal. See [ADR-00057](docs/adr/ADR-00057.md) for where this was found.) |
| `.flatMap(fn)` | ❌ (same nested-array blocker as `.flat()`) |
| `.findLast(fn)` / `.findLastIndex(fn)` | ✅ (genuine reverse iteration, not a forward scan keeping the last match — the callback is invoked starting from the last element, matching real JS's own reverse call order, observable via side effects. See [ADR-00057](docs/adr/ADR-00057.md).) |
| `.toSorted()` / `.toReversed()` / `.toSpliced()` | ✅ (non-mutating counterparts of `.sort()`/`.reverse()`/`.splice()` — sort/reverse a fresh copy, or build a fresh spliced result, leaving the original array untouched. See [ADR-00057](docs/adr/ADR-00057.md).) |
| `.with(i, val)` | ✅ (returns a fresh copy with the element at `i` replaced; negative indices count from the end like `.at()`; an index still out of range after normalization throws a catchable Error, matching real JS's `RangeError`. See [ADR-00057](docs/adr/ADR-00057.md).) |
| `.keys()` / `.values()` / `.entries()` | ✅ (all return materialized arrays, not lazy iterators — this compiler has no general iterator protocol, the same convention `Map`/`Set`'s own `.keys()`/`.values()`/`Map.entries()` already use. `.entries()` returns `{index: number, value: T}[]`, not a real `[index, value]` tuple, for the same no-tuple-type reason `Map.entries()`/`Object.entries()` already document. See [ADR-00057](docs/adr/ADR-00057.md).) |
| `.copyWithin(target, start?, end?)` | ✅ (in-place, overlap-safe via `memmove` — copying `arr.copyWithin(0, 3)` on a 5-element array is a self-overlapping copy, the same overlap concern `.shift()`/`.unshift()`/`.splice()`'s own tail shifts already handle. See [ADR-00057](docs/adr/ADR-00057.md).) |
| `Array.isArray(x)` | ✅ |
| `Array.from(iterable)` | ❌ (a user-extensible `for...of` iterable protocol now exists — a class implementing `next(): T \| null`, see [ADR-00063](docs/adr/ADR-00063.md) — but `Array.from` itself, which would drain such an iterable into a real array, isn't implemented yet) |
| `Array.of(...items)` | ✅ (unlike an array literal `[...]`, which can currently only appear in variable-declaration position, `Array.of(...)` is a plain call expression usable anywhere — element type inferred from the first argument, same rule `[...]` literals already use. See [ADR-00057](docs/adr/ADR-00057.md).) |

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
| `Number.prototype.toString(radix?)` | ✅ (hand-rolled digit loop; truncates a non-integer receiver to its integer part first; radix trusted, not validated. See [ADR-00065](docs/adr/ADR-00065.md) for this and the two rows below — same ADR, same `sprintf`-based approach, same cosmetic-only deviations from real JS.) |
| `Number.prototype.toPrecision(n)` | ✅ (`sprintf("%#.*g", ...)`; exponent pads to 2 digits — `e+05` vs real JS's `e+5`.) |
| `Number.prototype.toExponential(n)` | ✅ (`sprintf("%.*e", ...)`; same exponent-padding deviation.) |
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
| `Math.clz32/fround/imul` | ✅ (`clz32` via LLVM's own `llvm.ctlz.i32` intrinsic; `fround` via an `fptrunc`/`fpext` float32 round-trip; `imul` via 32-bit `mul` + sign-extend, giving real 32-bit-wraparound integer multiplication distinct from plain `*`'s double-precision result. See [ADR-00065](docs/adr/ADR-00065.md).) |

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
| `Object.assign(target, ...src)` | ✅ (mutates and returns `target`; every field a source contributes must already exist on `target`'s own struct type — this compiler's objects are fixed-shape heap structs, not a dynamic property bag, so a source field target's type doesn't have is a clean compile error, not silently dropped or grafted on. See [ADR-00054](docs/adr/ADR-00054.md).) |
| `Object.create()` | ❌ |
| `Object.freeze(obj)` | ✅ (real runtime enforcement, not a no-op — tracks `obj`'s heap pointer in a global frozen-object set, checked at every field-write site, so a blocked write throws a catchable Error even through a different alias/function parameter, not just through the variable that called `freeze`. See [ADR-00055](docs/adr/ADR-00055.md).) |
| `Object.seal(obj)` | ✅ (a genuine no-op, not a scope-narrowed approximation of one — seal's real guarantee is "no new/deleted fields," which this compiler's fixed-shape objects already can't do at all, frozen or not, so there's nothing further to enforce. See [ADR-00055](docs/adr/ADR-00055.md).) |
| `Object.hasOwn()` / `.hasOwnProperty()` | ✅ (object shapes are fully structural/static, so this is a compile-time `FieldIndex` lookup, not a runtime scan — the key must be a string literal; a runtime-computed key is a clean compile error, since there's no field-name table at runtime to check it against. See [ADR-00065](docs/adr/ADR-00065.md).) |
| `Object.fromEntries()` | ❌ |
| Object spread `{ ...obj, key: val }` | ✅ |
| Computed property keys `{ [expr]: value }` | ✅ (a *dynamic object* — storage-wise a real `Map<string,V>` reusing `new Map<K,V>()`'s own runtime, with `.field`/`[expr]` sugar layered on top; `V` inferred from the first property only. `...spread` combined with a computed key, and a declared-type form (`{ [key: string]: T }`), aren't supported yet. See [TDD-00012](docs/tdd/TDD-00012.md) / [ADR-00066](docs/adr/ADR-00066.md) for full scope.) |
| Shorthand property `{ x }` | ✅ |
| `Map.set/get/has/delete/keys/values` | ✅ |
| `Map.size` | ✅ |
| `Map.entries()` | ✅ (`{key: K, value: V}[]`, not a real `[key, value]` tuple — this compiler has no tuple type. Same convention `Object.entries()` already uses; iterate with `for (const e of m.entries())` then read `e.key`/`e.value`. See [ADR-00053](docs/adr/ADR-00053.md).) |
| `Map.forEach()` | ✅ (calls `fn(value, key)`, matching real JS's argument order — the 3rd `map` argument real JS also passes is dropped, the same simplification `Array.forEach`'s `(elem, index)` already makes. See [ADR-00053](docs/adr/ADR-00053.md).) |
| `Map.clear()` | ✅ (resets size to 0 in place — doesn't free/reallocate the backing arrays, matching this compiler's "leak by design" memory model; the map is immediately reusable afterward. See [ADR-00053](docs/adr/ADR-00053.md).) |
| `Set.add/has/delete/values` | ✅ |
| `Set.size` | ✅ |
| `Set.forEach()` | ✅ (calls `fn(element[, element])` — real JS's own `Set.prototype.forEach` passes the value twice, `(value, value, set)`, for Map/Set callback-shape parity; mirrored here when the callback declares a 2nd parameter. See [ADR-00053](docs/adr/ADR-00053.md).) |
| `Set.clear()` | ✅ (same in-place reset as `Map.clear()`. See [ADR-00053](docs/adr/ADR-00053.md).) |
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
| `JSON.parse(s)` → object | ✅ (flat objects, primitive fields only — nested object fields give a clean compile error; see [ADR-00007](docs/adr/ADR-00007.md)) — a missing *string* field's default was fixed from a crash-causing `null` to an empty string; see [ADR-00024](docs/adr/ADR-00024.md) |
| `JSON.parse(s)` → object with *nested* object fields | ❌ (clean compile error today, not silent corruption — see [ADR-00007](docs/adr/ADR-00007.md); design scoped in [TDD-00015](docs/tdd/TDD-00015.md)) |
| `JSON.stringify(mixedTypeArray)` | ❌ (array literals, and their inferred element type, are uniform-type only — a heterogeneous array has no single element type to stringify against) |

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
| `console.time()` / `.timeEnd()` | ✅ (V1 scope: a single global monotonic-time slot, not a per-label map — calling `time()` again overwrites the one running timer regardless of label. See [ADR-00029](docs/adr/ADR-00029.md).) |
| `console.count()` / `.countReset()` | ✅ (backed by a real `Map<string, number>` — matches real Node's per-label semantics exactly, unlike `time`'s single-slot narrowing above. See [ADR-00029](docs/adr/ADR-00029.md).) |
| `console.group()` / `.groupEnd()` | ✅ (indents every subsequent `console.*` line by two spaces per nesting level; an unbalanced extra `groupEnd()` floors at depth 0 rather than going negative. See [ADR-00029](docs/adr/ADR-00029.md).) |
| `console.dir()` | ✅ (prints a single value exactly like a single-argument `console.log`; the real API's second `options` argument — depth/color controls — is accepted syntactically but ignored. See [ADR-00029](docs/adr/ADR-00029.md).) |

### Global Functions & Constants

JavaScript language-level globals unrelated to any browser API.

| Feature | Status | Notes |
|---|---|---|
| `isNaN(x)` | ✅ | |
| `isFinite(x)` | ✅ | |
| `parseInt(s, radix?)` | ✅ | |
| `parseFloat(s)` | ✅ | |
| `NaN` (global constant) | ✅ | A local variable of the same name still shadows it. See [ADR-00024](docs/adr/ADR-00024.md). |
| `Infinity` (global constant) | ✅ | Same shadowing rule as `NaN`. See [ADR-00024](docs/adr/ADR-00024.md). |
| `undefined` (global constant) | ✅ | As a literal value |
| `globalThis` | ❌ | Not meaningful in a native single-file context |
| `encodeURI(s)` | ✅ | Leaves the unreserved *and* reserved (`;/?:@&=+$,#`) character sets unescaped. See [ADR-00024](docs/adr/ADR-00024.md). |
| `decodeURI(s)` | ✅ | Does **not** decode a `%XX` escape representing a reserved character (leaves it as literal `%XX` text) — the one real behavioral difference from `decodeURIComponent`. Permissive on malformed input (passes a bad/truncated escape through as literal text) rather than throwing a `URIError`. See [ADR-00024](docs/adr/ADR-00024.md). |
| `encodeURIComponent(s)` | ✅ | Leaves only the unreserved set (letters, digits, `-_.!~*'()`) unescaped. See [ADR-00024](docs/adr/ADR-00024.md). |
| `decodeURIComponent(s)` | ✅ | Decodes every valid `%XX` escape unconditionally. See [ADR-00024](docs/adr/ADR-00024.md). |
| `atob(s)` | ✅ | Base64 decode. Permissive: malformed length/characters decode as best-effort rather than throwing. Operates byte-for-byte on the input string (this compiler's strings are already plain byte sequences — no separate "binary string" type needed). See [ADR-00024](docs/adr/ADR-00024.md). |
| `btoa(s)` | ✅ | Base64 encode, `=`-padded (RFC 4045). See [ADR-00024](docs/adr/ADR-00024.md). |
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
| `setTimeout(fn, ms)` / `clearTimeout(id)` | ✅ | Bare global functions, matching real JS (not a namespace). Callback must be a zero-argument, `void`-returning closure — a bare reference to a top-level named function isn't supported as a value yet, a pre-existing general limitation, not specific to timers. See [ADR-00031](docs/adr/ADR-00031.md). |
| `setInterval(fn, ms)` / `clearInterval(id)` | ✅ | Same scope as `setTimeout`. An active interval that's never cleared keeps the process running indefinitely, matching real Node — the first feature in this compiler where that's true. See [ADR-00031](docs/adr/ADR-00031.md). |
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
| `crypto.getRandomValues(buffer)` | ✅ | Fills a plain `number[]` (not a `TypedArray` — this compiler has none yet) with random byte values, one per element. See [ADR-00024](docs/adr/ADR-00024.md). |
| `crypto.randomUUID()` | ✅ | RFC 4122 version-4 UUID string. See [ADR-00024](docs/adr/ADR-00024.md). |
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
| `performance.now()` | ✅ | `CLOCK_MONOTONIC`-based milliseconds, as a `double` (sub-millisecond precision) — unlike Date.now(), not tied to wall-clock time. No fixed "time origin" like the browser spec (process/page start); returns the raw monotonic reading instead, which is exactly as valid for subtracting two calls to measure elapsed time. See [ADR-00024](docs/adr/ADR-00024.md). |
| `performance.mark(name)` / `performance.measure(name, start, end)` | ❌ | Named timing marks |
| `Date` | ✅ | `new Date()` / `new Date(ms)` / `new Date(isoString)` (the string form parses via the same logic as `Date.parse`, including its `-1`-on-unparseable sentinel — see [ADR-00038](docs/adr/ADR-00038.md)) / `new Date(year, month, day?, hours?, minutes?, seconds?, ms?)` (month 0-indexed, matching `getMonth()`; omitted trailing fields default like real JS — day to 1, everything after that to 0; see [ADR-00039](docs/adr/ADR-00039.md)); `getFullYear/Month/Date/Day/Hours/Minutes/Seconds/Milliseconds`, `getTime`/`valueOf`, `toISOString` — all UTC, not local time, for deterministic output regardless of the machine/CI timezone (a documented deviation from real JS's local-time default — note the multi-argument constructor form is a special case of this: real JS treats its fields as *local* time, this compiler always treats them as UTC). See [ADR-00014](docs/adr/ADR-00014.md). |
| `Date.now()` | ✅ | Milliseconds since epoch, via `clock_gettime(CLOCK_REALTIME, ...)` |
| `Date.parse(string)` | ✅ | ISO 8601 strings, with or without milliseconds: `Z` (UTC), a `+HH:MM`/`-HH:MM` timezone offset (converted to UTC), or a bare `YYYY-MM-DD` date. Unparseable input returns `-1` (a documented sentinel — this compiler's Date has no NaN representation). See [ADR-00015](docs/adr/ADR-00015.md) and [ADR-00017](docs/adr/ADR-00017.md). |
| `Date` setters (`setFullYear`, `setMonth`, `setDate`, `setHours`, `setMinutes`, `setSeconds`, `setMilliseconds`, `setTime`) | ✅ | Mutate a named Date variable in place and return the new timestamp, matching real JS. Requires a named-variable receiver (not a field access or call result — this compiler's Date is a plain number, not a reference object, so there's no heap location to mutate otherwise); only the single-argument form of each setter (no `setFullYear(y, m, d)`-style overloads). See [ADR-00016](docs/adr/ADR-00016.md). |
| `Date` arithmetic (`date ± durationMs`, `date - date`, `date += durationMs`) | ✅ | `Date - Date` gives the difference in milliseconds (a number), matching real JS. `Date ± number` gives a new Date (a deliberate deviation from real JS, where `+` on a Date string-concatenates instead — numeric duration arithmetic is far more useful for this compiler's plain-number Date representation). `Date + Date`, `number - Date`, and compound-assigning a Date into a Date are all rejected at compile time. See [ADR-00018](docs/adr/ADR-00018.md). |
| `Date.prototype.toDateString()` | ✅ | Fixed `"Www Mon DD YYYY"` shape (e.g. `"Thu Jan 01 1970"`), matching real JS exactly except always UTC, not local time. See [ADR-00019](docs/adr/ADR-00019.md). |
| `Date.prototype.toLocaleDateString()` | ✅ | One fixed `"M/D/YYYY"` format (the default en-US shape), always UTC; no locale argument or full `Intl`-style locale support — a documented scope narrowing. See [ADR-00019](docs/adr/ADR-00019.md). |

### Networking

All require linking a network library (libcurl for fetch/HTTP; system sockets for WebSocket).

| API | Status | Notes |
|---|---|---|
| `fetch(url)` / `fetch(url, init)` | ✅ | `init` is any value with some subset of `method: string` / `headers: Map<string,string>` / `body: string` fields — no `Request`/`Headers` classes, matching how this compiler already represents every other bag of string headers (`http.listen`'s `req.headers`, a handler's own optional response `headers` field) as a plain `Map<string,string>` ([ADR-00074](docs/adr/ADR-00074.md)/[TDD-00017](docs/tdd/TDD-00017.md)). Setting a `body` without an explicit `method` sends it as `POST` (real, confirmed libcurl behavior — `CURLOPT_POSTFIELDS` implies `POST` unless overridden); an explicit `method` always wins over that default. Real non-blocking I/O since [ADR-00050](docs/adr/ADR-00050.md): uses libcurl's multi-interface, driven by the same `select()`-based event loop `http.listen` uses, so `await fetch(...)` yields instead of blocking when called from inside a connection-handler fiber — one slow upstream call no longer freezes every other connection or timer. Outside any fiber (plain top-level code, no `http.listen`), there's nothing else to overlap with, so it busy-spins the same multi-interface calls to the same effect as a blocking wait. A network-level failure (DNS, connection refused, TLS, timeout) throws; a non-2xx HTTP status still resolves normally (`.ok` distinguishes it), matching real `fetch`. See [ADR-00021](docs/adr/ADR-00021.md) (original V1), [ADR-00050](docs/adr/ADR-00050.md) (non-blocking rework), [ADR-00052](docs/adr/ADR-00052.md) (fixed a real stack-overflow crash in the busy-spin path, confirmed via a slow-endpoint repro), and [ADR-00074](docs/adr/ADR-00074.md) (custom method/headers/body). |
| `Response` (`.status`, `.ok`, `.body`, `.text()`, `.json()`) | ✅ | Plain object with `status`/`ok`/`body` fields (readable directly, not hidden) plus `text()`/`json()` methods. `.json()` reuses `JSON.parse`'s existing machinery, including its scope (flat objects with primitive fields only — nested JSON fields aren't supported yet, same as bare `JSON.parse`). Response bodies are plain null-terminated strings: binary bodies with embedded null bytes will silently truncate at the first one (no `ArrayBuffer`/`TypedArrays` yet to represent raw bytes faithfully) — fine for the REST/JSON use case this was built for, not for binary downloads. See [ADR-00021](docs/adr/ADR-00021.md). |
| `Request` / `Headers` objects | ❌ | No real `Request`/`Headers` classes — but custom method/headers/body are covered without them: `fetch(url, init)`'s `init` is a plain object with `method`/`headers`/`body` fields, `headers` being a `Map<string,string>` ([ADR-00074](docs/adr/ADR-00074.md)/[TDD-00017](docs/tdd/TDD-00017.md)). Deliberately scoped this way — see the `fetch(url)` row above |
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
| `fs.readFileSync(path)` | ✅ | Reads the whole file into a string. Throws a catchable `Error` (built from `strerror(errno)`) if the file can't be opened. Text-only — a file with embedded null bytes reads back shorter than its real size, the same limitation `fetch`'s response bodies have (`.length` is `strlen`-based). See [ADR-00023](docs/adr/ADR-00023.md). |
| `fs.writeFileSync(path, data)` | ✅ | Creates or truncates the file with `data`. Throws on failure. |
| `fs.appendFileSync(path, data)` | ✅ | Like `writeFileSync`, but appends instead of truncating (creates the file if it doesn't exist). Throws on failure. |
| `fs.existsSync(path)` | ✅ | Plain existence check via POSIX `access()`. Deliberately does **not** throw for a missing path — matches real Node's own `existsSync`, one of the few `fs` functions that reports "doesn't exist" as `false` rather than an error. |
| `fs.unlinkSync(path)` | ✅ | Deletes a file. Throws on failure. |
| `fs.mkdirSync(path)` | ✅ | Creates a directory via POSIX `mkdir()`, mode `0777` reduced by the process umask as usual. No `{recursive: true}` option — throws (e.g. `EEXIST`) if the path already exists or a parent directory is missing. See [ADR-00027](docs/adr/ADR-00027.md). |
| `fs.rmdirSync(path)` | ✅ | Removes an *empty* directory via POSIX `rmdir()` — deliberately directory-only (fails on a plain file, unlike `remove()`/`unlinkSync`). No recursive-delete option, matching `mkdirSync`'s lack of one. See [ADR-00027](docs/adr/ADR-00027.md). |
| `fs.readdirSync(path)` | ✅ | Lists a directory's entries (excluding `.`/`..`) as a `string[]`, in whatever order the OS's own `readdir()` returns them — no ordering guarantee, matching real Node. Built from `struct dirent`'s `d_name` field at a `runtime.GOOS`-conditional byte offset, independently verified by a compiled `offsetof` probe on both Darwin and (via Docker, see [ADR-00051](docs/adr/ADR-00051.md)) x86-64 Linux. See [ADR-00027](docs/adr/ADR-00027.md). |
| `fs.renameSync(oldPath, newPath)` | ✅ | Renames/moves a file via POSIX `rename()`. Throws on failure. See [ADR-00027](docs/adr/ADR-00027.md). |
| `fs.copyFileSync(src, dest)` | ✅ | Composes the existing `readFileSync`/`writeFileSync` helpers — no new C-level I/O code. Inherits `readFileSync`'s text-only limitation (a source file with embedded null bytes copies back shorter than its real size). See [ADR-00027](docs/adr/ADR-00027.md). |
| Async variants (`fs.readFile`, callback/Promise-based) | ❌ | Everything here is synchronous and blocking, matching this compiler's lack of an event loop — no non-blocking variant exists to offer |
| `File` / `FileReader` / `FileSystemFileHandle` (browser-flavored File API) | ❌ | Not planned — see the framing note above; these model browser concepts this compiler has no equivalent for |

### Process / CLI I/O

What CLI tools and containerized services actually need day-to-day (argument parsing, stdin, environment config, exit codes). Prioritized because the long-term project direction favors CLI/microservice use cases. `console.log`/`.error` already write to stdout/stderr respectively (✅, see the `console` table above) — the gaps below are everything *else* a CLI program needs.

| API | Status | Notes |
|---|---|---|
| Command-line arguments (`process.argv`) | ✅ | Mirrors C's `argv` directly (`argv[0]` is the binary's own path), not Node's two-prefix convention — see [ADR-00002](docs/adr/ADR-00002.md) |
| Environment variables (`process.env.KEY` / `process.env["KEY"]`) | ✅ | Both dot and bracket notation; returns a possibly-null string (same convention as `.find()`), so `process.env.X ?? "default"` works |
| Exit codes (`process.exit(code)`) | ✅ | Calls C `exit()`; never returns, code after it is correctly unreachable |
| Reading stdin (sync line read) | ✅ | `process.readLineSync()` — one line via POSIX `getline()` (handles arbitrarily long lines), stripped of its trailing `\n`/`\r\n`. Returns `null` at EOF (same possibly-null convention as `process.env`). See [ADR-00024](docs/adr/ADR-00024.md). |
| Simple synchronous file read/write (`fs.readFileSync`/`writeFileSync`-style) | ✅ | See the File System (fs) section above — `fs.readFileSync`/`writeFileSync`/`appendFileSync`/`existsSync`/`unlinkSync` |
| `process.execFileSync(file, args?)` | ✅ | Node's `child_process.execFileSync`, not `execSync`: `fork()`+`execvp()`s `file` directly with no shell involved, so shell metacharacters in `args` are never interpreted. Returns captured stdout as a string; throws a catchable `Error` on a non-zero exit status or a signal death. V1 scope: no options object (no `cwd`/`env`/`timeout`/`stdio` overrides yet); stderr is inherited (visible on the terminal live), not captured. See [ADR-00025](docs/adr/ADR-00025.md). |
| `process.cwd()` | ✅ | Current working directory, via POSIX `getcwd(NULL, 0)` (auto-sizing). See [ADR-00026](docs/adr/ADR-00026.md). |
| `process.chdir(path)` | ✅ | Changes the current working directory via POSIX `chdir()`. Throws a catchable `Error` (same "`<opDesc>` '`<path>`': `<strerror>`" shape `fs.*`'s failures already use) if the path doesn't exist or isn't a directory. See [ADR-00026](docs/adr/ADR-00026.md). |
| `process.pid` | ✅ | The current process ID, via POSIX `getpid()`. A property read, not a call, matching `process.argv`. See [ADR-00026](docs/adr/ADR-00026.md). |
| `process.platform` | ✅ | A pure compile-time constant (`"darwin"`/`"linux"`/`"win32"`/...) baked in from the Go compiler's own `runtime.GOOS` — no runtime code at all, since this compiler doesn't cross-compile. See [ADR-00026](docs/adr/ADR-00026.md). |
| `process.kill(pid, signal?)` | ✅ | Sends `signal` (defaults to `15`/`SIGTERM`, matching real Node) to `pid` via POSIX `kill()`. Throws a catchable `Error` if the target process doesn't exist or the signal can't be sent; signal `0` is the standard POSIX "does this process exist" check and never actually delivers a signal. See [ADR-00026](docs/adr/ADR-00026.md). |
| `process.stdout.write(s)` / `process.stderr.write(s)` (raw write, no auto-newline) | ❌ | `console.log`/`.error` always append a trailing newline; there's no way to write to stdout/stderr without one (e.g. for a progress indicator or binary-safe output) |
| Writing/deleting `process.env` (`process.env.KEY = val`, `delete process.env.KEY`) | ❌ | `process.env` is read-only today (backed by `getenv()` only) — no `setenv()`/`unsetenv()` path exists |
| `process.on('SIGINT'/'SIGTERM', handler)` (signal handlers) | ❌ | No way for a running program (e.g. an `http.listen` server) to intercept a signal and shut down gracefully instead of dying immediately |

### HTTP Server

| API | Status | Notes |
|---|---|---|
| `http.listen(port, handler)` — bind, accept, dispatch | ✅ | Concurrent connection handling: each accepted connection runs on its own fiber (`ucontext.h`-based, no custom assembly — see [TDD-00006](docs/tdd/TDD-00006.md)'s Part 2 prototype), so a connection that hasn't sent its request yet no longer blocks any other connection from being accepted and answered. Built on the generalized `select()`-based event loop ([`docs/tdd/TDD-00006.md`](docs/tdd/TDD-00006.md) Part 1), so the listening socket, every open connection, and any pending `setTimeout`/`setInterval` timers all share one wait. See [ADR-00048](docs/adr/ADR-00048.md) (V1), [ADR-00049](docs/adr/ADR-00049.md) (concurrent connections), and [ADR-00052](docs/adr/ADR-00052.md) (fixed a stack leak in the main dispatch loop that crashed a running server after ~20,000 requests). |
| Request-line parsing (`req.method`, `req.path`) | ✅ | The request line's method token and path token are parsed generically (`sscanf("%15s %2047s", ...)`) — this is **not** GET-only: verified directly by curling `POST`/`PUT`/`DELETE` requests at a running server and confirming `req.method` reports each one correctly. `req.path` no longer includes a trailing `?...` query string (see below) — [ADR-00072](docs/adr/ADR-00072.md) fixed that. |
| Request headers (`req.headers: Map<string, string>`) | ✅ | Lowercased keys (case-insensitive lookup, matching real HTTP semantics) — [ADR-00072](docs/adr/ADR-00072.md) |
| Query string (`req.query: Map<string, string>`) | ✅ | Split from `req.path` and parsed (`a=b&c=d`), both keys and values percent-decoded via the same `decodeURIComponent` primitive this compiler already had — [ADR-00072](docs/adr/ADR-00072.md) |
| Request body (`req.body: string`) | ✅ | `Content-Length`-aware — the read buffer now accumulates across as many `read()` calls as it takes (growing via `realloc`, capped at 10MiB against a hostile/malformed `Content-Length`) rather than assuming one `read()` call returns an entire request — [ADR-00072](docs/adr/ADR-00072.md) |
| Response headers (optional `headers: Map<string, string>` field) | ✅ | Fully user-controlled, including `Content-Type` if set — omitting the field behaves byte-identically to before this existed — [ADR-00072](docs/adr/ADR-00072.md) |
| `.close()` / graceful shutdown | ❌ | `http.listen` never returns; the only way to stop a running server today is `process.exit()`. Needs POSIX signal handling (`process.on('SIGINT'/'SIGTERM', ...)`), infrastructure that doesn't exist anywhere in this compiler yet — deliberately deferred, not scoped by [ADR-00072](docs/adr/ADR-00072.md) |

---

## What Is NOT Implemented

### High priority / low complexity

These are the most natural next steps — each is self-contained and commonly used. Two items promoted here from Medium priority below, since both turned out lower-effort than originally scoped:

| Feature | Complexity | Notes |
|---|---|---|
| `Array.from(iterable)` | Low-Medium | A general iterable protocol now exists (a class implementing `next(): T \| null`, [ADR-00063](docs/adr/ADR-00063.md)) — just needs draining that (and a plain array) into a fresh array. The array-like overload is the only one needed initially. |
| `in` operator | Medium | Object key presence check — object shapes are already fully structural/static (the same compile-time `FieldIndex` lookup `Object.hasOwn()` already uses), so this is mostly parser/precedence work, not new runtime machinery |

### Medium priority / medium complexity

| Feature | Complexity | Notes |
|---|---|---|
| `Array.flat(depth?)` | Medium | Blocked on nested-array support, not just the flatten logic itself — `number[][]`-style literals aren't reliably representable yet (found while scoping this: `[[1,2],[3,4]]` fails to compile). Real work is fixing that first. |
| `Array.flatMap(fn)` | Medium | `map` then `flat(1)` — same nested-array blocker |
| `String.prototype.matchAll()` | Medium-High | Needs regex engine or `strstr` loop |
| ~~`Number.prototype.toString(radix)`~~ | ~~Medium~~ | ✅ done — see [`docs/adr/ADR-00065.md`](docs/adr/ADR-00065.md) |
| ~~`instanceof`~~ | ~~Medium~~ | ✅ done (against user-defined classes and `any`/`unknown` values holding one) — see [ADR-00067](docs/adr/ADR-00067.md). `Error`/`Date`/`Response`/other built-ins are not yet taggable — see [TDD-00013](docs/tdd/TDD-00013.md). |
| User-defined generic functions | High | Full generic instantiation pass — or type erasure via the existing `any`/`unknown` machinery, a real, cheaper alternative not yet decided between. Staged design in [`docs/tdd/TDD-00010.md`](docs/tdd/TDD-00010.md). |
| Intersection types `A & B` | Medium | Merge struct fields |
| Tuple types `[number, string]` | Medium | Fixed-size struct |

### High priority / high complexity

| Feature | Complexity | Notes |
|---|---|---|
| ~~`class` (methods, constructors)~~ | ~~High~~ | ✅ done (Stage 1, no inheritance) — see [`docs/adr/ADR-00063.md`](docs/adr/ADR-00063.md) and [`docs/tdd/TDD-00009.md`](docs/tdd/TDD-00009.md). Also closed a real, general iterable protocol for `for...of` (Stage 1a) and a pre-existing bug found along the way — see [`docs/adr/ADR-00064.md`](docs/adr/ADR-00064.md). `Date`'s named-variable-only method-call restriction is not addressed by this stage. |
| ~~Runtime type tags + `instanceof` (class Stage 2)~~ | ~~Medium~~ | ✅ done — see [`docs/adr/ADR-00067.md`](docs/adr/ADR-00067.md). |
| `class` inheritance / `extends` | High | Needs virtual dispatch or monomorphization. Last stage in [`docs/tdd/TDD-00009.md`](docs/tdd/TDD-00009.md) — the only piece of the class design that actually needs new runtime dispatch machinery; the earlier stages stand on their own. |
| ~~`import` / `export` (multi-file)~~ | ~~High~~ | ✅ done (named imports/exports, whole-program compile) — see [ADR-00022](docs/adr/ADR-00022.md) and the Modules section above. Still open: real per-file module scope (mangled names), `export default`/namespace imports/re-exports, and side-effecting imported files |
| Nested function declarations | Medium | Separate from closures; mostly a scoping change |
| `RegExp` | High | Needs PCRE or similar C library |
| `Error` subtypes (`TypeError`, etc.) | Medium | Tagged error values — scoped (not started) in [TDD-00013](docs/tdd/TDD-00013.md), a follow-on to class Stage 2's runtime type tags ([ADR-00067](docs/adr/ADR-00067.md)) |
| ~~`Promise.all` / `.race` / `.allSettled`~~ | ~~Medium~~ | ✅ done — see [ADR-00073](docs/adr/ADR-00073.md)/[TDD-00016](docs/tdd/TDD-00016.md) and the Language Constructs table above |
| `Symbol` | High | Unique runtime IDs; affects `for…of`, iterators |
| Generator functions / iterators | High | Suspend/resume; requires coroutine machinery |
| Decorators | Very high | Requires metadata reflection |
| `Proxy` / `Reflect` | Very high | Dynamic property intercept; likely impractical |
| Opt-in dynamic property add/delete on objects | Speculative, not scoped | This compiler's objects are fixed-shape heap structs (an interface's field list is fixed at compile time) — real JS lets any object gain/lose properties at runtime, which `Object.freeze`/`.seal` ([ADR-00055](docs/adr/ADR-00055.md)) currently don't need to enforce since it's already structurally impossible for *any* object, frozen or not. Noted here as a real gap from 100% JS compatibility, not because it's next in line — surfaced while scoping freeze/seal, not researched or designed. If picked up, likely shaped as an explicit compiler flag/opt-in (a genuine dynamic property bag is a different, heavier object representation than the fixed-struct one everything else here assumes) rather than the default. |

---

## Known Limitations & Bugs

Deviations from expected behavior: either features marked ✅ above with incorrect/incomplete behavior, or ❌ (not-implemented) features whose failure mode is worse than a clean rejection — silent wrong output or a crash instead of a clear "unsupported" error.

| Limitation | Notes |
|---|---|
| `any`/`unknown` boxed booleans print as `"true"`/`"false"` | `console.log`ing a plain (non-`any`) `boolean` prints `1`/`0` in this compiler (an existing, unrelated quirk — see `examples/strings/string_methods.ts`'s comments), but an `any`-typed variable currently holding a boolean prints `"true"`/`"false"` instead, since the dynamic-value formatter shares one code path for both `console.log` and template literals and mirrors the template-literal convention (which already uses `"true"`/`"false"`) rather than special-casing `console.log`'s raw-boolean convention differently per call site. Deliberate, documented simplification — see [ADR-00008](docs/adr/ADR-00008.md). |
| An unannotated function calling another unannotated function *declared later in the same file*, when the callee returns an object/array/closure/Date | `function makeA() { return makeB() }; function makeB() { return { x: 1 } }` — `makeA`'s own inferred return type is computed once, in source order, before `makeB` has been registered, so `makeA`'s inference sees a not-yet-known callee and falls back to void. Fails cleanly (`field access on non-object`), not silently — a known, accepted boundary of [ADR-00041](docs/adr/ADR-00041.md)'s single-pass, best-effort inference, not a general fixed-point/multi-pass type inference system. Reorder the declarations, or add an explicit return-type annotation to `makeA`, to work around it. |
| `fetch()` response bodies containing embedded null bytes silently truncate | Confirmed directly: fetching 50,000 bytes of random binary data (`httpbin.org/bytes/50000`) came back as a 383-byte string. Root cause isn't in `fetch` itself — every string in this compiler is a plain null-terminated C string (`.length` is `strlen`-based), so *any* string value with an embedded `\0`, from any source, reads as shorter than it actually is; `fetch` just makes this reachable from live, uncontrolled external data for the first time. Verified the underlying growable-buffer/`realloc` logic itself is correct by re-testing with a large all-text (no embedded nulls) body, which came back complete and exact. Fine for the JSON/text REST API use case `fetch` was built for; a real fix needs `ArrayBuffer`/`TypedArrays` (0% implemented, tracked separately) to represent raw bytes at all. |
| An object literal's field values are never coerced against a declared interface/expected type, only against their own literal-inferred type | `interface Point { x: number; y: number }; const p: Point = { x: 1, y: 40.6 }; console.log(p.y)` prints `4630910759336725709`, the raw IEEE-754 bit pattern of `40.6` reinterpreted as an `i64` — not a truncation, not a rejection, silent corruption readable through any later field access. Bidirectional (a `@type {float64}`-annotated field given an integer literal corrupts the same way in reverse) and depth-independent (reproduces in a flat, one-level interface; has nothing to do with nesting despite how it was originally reported). Same failure shape as the unannotated-parameter bug [ADR-00042](docs/adr/ADR-00042.md) fixed, just in a different, never-audited code path (object literal construction, not function calls). See [TDD-00007](docs/tdd/TDD-00007.md) for the full design. |
| Dividing the minimum representable `i64` value by `-1` is still undefined behavior | `codegen/llvm/emit_exprs.go`'s `sdiv`/`srem` codegen guards against a zero divisor ([ADR-00069](docs/adr/ADR-00069.md)), but LLVM documents a *second*, separate UB case for signed division/remainder: dividend exactly `i64`'s minimum value (`-9223372036854775808`) with divisor `-1` (the mathematical result, `2^63`, doesn't fit back in an `i64`). Not fixed here — found by inspection while scoping [TDD-00014](docs/tdd/TDD-00014.md)'s codegen fuzzer's arithmetic oracle, not by an actual repro; reaching this exact value by chance is astronomically unlikely (1-in-2^64), so it wasn't something worth guarding preemptively in this pass. Would need the same zero-check-style guard `emitDivZeroGuard` already uses, extended to also special-case this one input pair. |
| `JSON.parse` into an array-typed interface field produces invalid LLVM IR (fails to compile) instead of a clean rejection | `interface Bag { tags: string[] }; const b: Bag = JSON.parse('{"tags":["a","b"]}')` — confirmed directly: `emitJSONParseObject`'s upfront rejection loop (`codegen/llvm/emit_call.go`) only checks `f.Ty.IsObject`, not `f.Ty.IsArray`, so an array field falls through to `emitJSONParseFieldValue`'s scalar-only switch, which defaults to `atoll` (`i64`-typed) merged via `phi` against the field's actual `ptr`/aggregate-typed slot — a type mismatch `clang` rejects (`'%tN' defined with type 'i64' but expected 'ptr'`). Not silent corruption (the mismatched IR never produces a runnable binary), but not the clean compile-time error nested-object fields already get either. Found while scoping [TDD-00015](docs/tdd/TDD-00015.md) (nested-object `JSON.parse`); fix is a one-line addition to the existing rejection check, tracked there rather than fixed here since it surfaced mid-design-writeup, not mid-implementation. |

---

## Design Documents (TDDs)

Anything big enough to need a design pass before implementation gets scoped out in a Technical Design Document under `docs/tdd/` first — full context, options considered, and prerequisites, kept out of this file so it stays scannable. Each of these is a genuinely significant piece of work in its own right, not a quick follow-on:

- **[Memory Management](docs/tdd/TDD-00001.md)** — three mutually-exclusive compilation modes, selected by the `-mm` flag: `manual` (default, `Memory.free(x)` — [ADR-00030](docs/adr/ADR-00030.md)) and `gc` (Boehm GC — [ADR-00071](docs/adr/ADR-00071.md)) are both built; `auto` (compiler-inserted frees, no runtime collector) is still design-only.
- **[Timers](docs/tdd/TDD-00002.md)** — done ([ADR-00031](docs/adr/ADR-00031.md)); kept as a TDD since the design writeup (why it *doesn't* need the general event loop) is still useful context.
- **[Alternative fetch Backend](docs/tdd/TDD-00003.md)** — a Go helper instead of libcurl. Scoped, not started, low priority.
- **[HTTP Server](docs/tdd/TDD-00004.md)** — the piece that unlocks this project's microservice direction. V1 done ([ADR-00048](docs/adr/ADR-00048.md)); concurrent connections ([ADR-00049](docs/adr/ADR-00049.md)); headers/query-string/request-body/response-headers ([ADR-00072](docs/adr/ADR-00072.md)). Only graceful shutdown remains, deliberately deferred (needs signal-handling infrastructure this compiler doesn't have yet).
- **[Unannotated Parameter Typing](docs/tdd/TDD-00005.md)** — clean rejection at call sites is done ([ADR-00042](docs/adr/ADR-00042.md)); the two further options (call-site inference, real `any` semantics) are scoped, not started.
- **[Event Loop](docs/tdd/TDD-00006.md)** — this project's single biggest structural gap, now substantially closed. Part 1 (the `select()`-based wait loop) done ([ADR-00048](docs/adr/ADR-00048.md)); Part 2's fiber-based scheduler is real, shipped for HTTP connection concurrency ([ADR-00049](docs/adr/ADR-00049.md)) and for real non-blocking `await fetch(...)` ([ADR-00050](docs/adr/ADR-00050.md), via libcurl's multi-interface merged into the same loop). Two real bugs turned up after shipping and are both fixed: `ucontext_t`'s size/layout was hardcoded from a macOS-only probe and silently corrupted memory on Linux ([ADR-00051](docs/adr/ADR-00051.md), found via CI failing on `ubuntu-latest` but never locally); and several hand-written IR functions had `alloca`s placed inside loop bodies rather than their entry block, leaking a fixed chunk of stack on every iteration — the worst instance crashed a running `http.listen` server after ~20,000 requests ([ADR-00052](docs/adr/ADR-00052.md), found while chasing down an unrelated flaky example). `Promise.all`/`.race`/`.allSettled` (awaiting multiple promises concurrently from a single call site) is now also done — see [ADR-00073](docs/adr/ADR-00073.md)/[TDD-00016](docs/tdd/TDD-00016.md): over `Array<Promise<Response>>` it's real concurrency (N in-flight fetches waited on together via a new group-wait primitive layered onto this same fiber/multi-interface mechanism), on top of the pre-existing per-handler concurrency described above.
- **[Object Literal Field Coercion](docs/tdd/TDD-00007.md)** — object literals never coerce field values against a declared type, only their own literal-inferred type; silent bit-reinterpretation corruption, not a clean rejection. Scoped, not started.
- **[External Conformance Suites (TypeScript + Test262) as a Test-Coverage Benchmark](docs/tdd/TDD-00008.md)** — the TypeScript suite tests the type checker's output, not runtime behavior, so it can't be used directly; Test262 turned out to be execution-based and often directly portable instead, at least for spec-mandated value semantics this compiler intends to match. First real ports (shift-operator categories) landed alongside [ADR-00047](docs/adr/ADR-00047.md)'s shift-semantics fix. Tracked in [`docs/testing/CONFORMANCE-COVERAGE.md`](docs/testing/CONFORMANCE-COVERAGE.md). Partially Implemented.
- **[Classes / OOP](docs/tdd/TDD-00009.md)** — staged: methods + constructors with no inheritance (Stage 1, done — see [`docs/adr/ADR-00063.md`](docs/adr/ADR-00063.md) — includes Stage 1a's `for...of` iterable protocol), then runtime type tags/`instanceof` (Stage 2, done — see [`docs/adr/ADR-00067.md`](docs/adr/ADR-00067.md)), then inheritance/vtables last (Stage 3, the only stage that actually needs new dynamic-dispatch machinery). `Error` subtyping (`instanceof Error`, `TypeError`, etc.) is a separate follow-on, scoped in [TDD-00013](docs/tdd/TDD-00013.md).
- **[Generics on user-defined functions/interfaces](docs/tdd/TDD-00010.md)** — two real options, not yet decided between: monomorphization (generalizing the same approach the built-in generics — `Array<T>`, `Map<K,V>`, `Promise<T>` — already use by hand) vs. type erasure via the existing `any`/`unknown` boxed-value machinery (cheaper to build, arguably more faithful to how real TypeScript's own generics behave, but hits the same "no arithmetic on a dynamic value" ceiling `any`/`unknown` already has). Also surfaced a real, unrelated bug in the existing built-in generics along the way — see `STATUS.md`'s Known Limitations. Scoped, not started.
- **[Computed property keys](docs/tdd/TDD-00012.md)** — done ([ADR-00066](docs/adr/ADR-00066.md)): an object literal with a computed key (`{ [expr]: value }`) is a *dynamic object*, storage-wise a real `Map<string,V>` reusing that runtime directly rather than a new hash table, with `.field`/`[expr]` sugar layered on top.
- **[Error subtypes / tagged errors](docs/tdd/TDD-00013.md)** — real `TypeError`/`RangeError`/etc. and `instanceof Error` support, scoped as a follow-on to class Stage 2's runtime type tags ([ADR-00067](docs/adr/ADR-00067.md)) rather than folded into it. Not started.
- **[Full-pipeline (codegen-through-binary) fuzz testing](docs/tdd/TDD-00014.md)** — done ([ADR-00070](docs/adr/ADR-00070.md)): two `go test -fuzz` lanes living in `tests/`, an arithmetic-expression correctness oracle (compares the compiled binary's output against the same expression evaluated in Go's native `int64` arithmetic) and a broader template-based well-formedness/crash fuzzer spanning more of the language with no oracle beyond "doesn't crash." Extends the lexer/parser-only fuzzing [ADR-00068](docs/adr/ADR-00068.md) added. Differential testing against a real JS engine (e.g. Node) was scoped and explicitly deferred — see the TDD.
- **[Promise.all / .race / .allSettled](docs/tdd/TDD-00016.md)** — done ([ADR-00073](docs/adr/ADR-00073.md)): the event loop's last documented gap. Real concurrency over `Array<Promise<Response>>` (a new group-wait primitive on top of the existing fiber/libcurl-multi-interface mechanism); an honest, order-preserving synchronous read over any other `Array<Promise<T>>`, since every such promise is already resolved by construction in this compiler. Also fixed a real, unrelated parser bug found along the way: nested generics (`Array<Promise<T>>`) failed to parse at all, since the lexer always emits one fused `>>`/`RSHIFT` token for a doubled closing angle bracket and no existing call site un-merged it. A third `go test -fuzz` lane (`FuzzPromiseCombinatorsOrdinary`, `tests/fuzz_codegen_test.go`) was added on top of [TDD-00014](docs/tdd/TDD-00014.md)'s existing two, chaining random combinator calls (kind, count, and array length all randomized) against a Go-computed oracle — the category of bug this targets (compile-time label/register collisions across multiple textual call sites of the same builtin) is exactly the kind a fixed, hand-written test per combinator can't reliably catch.
- **[fetch() client parity](docs/tdd/TDD-00017.md)** — done ([ADR-00074](docs/adr/ADR-00074.md)): `fetch(url, init)`'s `init` is a plain object with optional `method`/`headers`/`body` fields (`headers` a `Map<string,string>`, reusing this compiler's existing convention rather than new `Request`/`Headers` classes). Confirmed two real, easy-to-misjudge libcurl behaviors directly rather than assuming them: a `body` with no explicit `method` sends as `POST` (`CURLOPT_POSTFIELDS` implies it), but an *explicit* `method: "GET"` alongside a `body` still overrides that and sends a genuine GET-with-body. A fourth fuzz lane, `FuzzFetchInitOracle`, caught both on its first run (before its own oracle was fixed to match) — see the ADR's Investigation.

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
| Async / Promise | 4 | 9 | ~44% |
| String methods | 26 | 33 | ~79% |
| Array methods | 37 | 40 | ~93% |
| Number / Math | 35 | 35 | 100% |
| Object & collections | 23 | 26 | ~88% |
| JSON | 9 | 11 | ~82% |
| console | 11 | 12 | ~92% |
| Global functions & constants | 13 | 17 | ~76% |
| Type system features | 15 | 23 | ~65% |
| Classes / OOP | 6 | 8 | ~75% |
| Modules | 4 | 11 | ~36% |
| **Core language total** | **246** | **299** | **~82%** |

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
| Process / CLI I/O | 11 | 14 | ~79% |
| HTTP Server | 6 | 7 | ~86% |
| **Node.js total** | **27** | **33** | **~82%** |

---

## Roadmap

Grouped by kind of work rather than a fixed sequence number, since priorities shift and bug fixes get picked up opportunistically rather than in strict order. Core-language feature gaps already have their own priority/complexity breakdown in [What Is NOT Implemented](#what-is-not-implemented) above — not repeated here.

### Next up — bugs found but not yet fixed

Pulled from [Known Limitations & Bugs](#known-limitations--bugs) above: the ones worth fixing outright, as opposed to the ones documented there as deliberate, permanent scope narrowings (e.g. `any`'s boolean-printing convention).

| Fix | Effort | Notes |
|---|---|---|
| `fetch`/`fs` bodies containing embedded null bytes silently truncate | Deferred | Root cause is this compiler having no `ArrayBuffer`/TypedArrays yet (0% implemented) — not fixable in isolation; tracked as a consequence of that gap in the Web Platform & Node.js APIs backlog below |
| Object literal field values aren't coerced against a declared type, silently corrupting mismatched int/float fields | Medium | Scoped, not started — see [`docs/tdd/TDD-00007.md`](docs/tdd/TDD-00007.md). Same failure shape [ADR-00042](docs/adr/ADR-00042.md) already fixed for unannotated function-call arguments; this is the same fix applied to a different, never-audited code path (object-literal construction) |

### Structural priorities

The three biggest cross-cutting gaps — each affects multiple features rather than being one self-contained item, and each already has its own detailed writeup above. Listed in the order they were originally scoped, not current priority — see item 1's note on why memory management now reads as the most pressing of the three:

1. **Memory management — `-mm=gc` now exists** (see [`docs/tdd/TDD-00001.md`](docs/tdd/TDD-00001.md), [ADR-00071](docs/adr/ADR-00071.md)). Of the three mutually-exclusive compilation modes for the whole binary, `manual` (the default: `Memory.free(x)`) and `gc` (Boehm — an allocator shim, no changes to any existing allocation call site, plus a `GC_stackbottom` fix so `http.listen`'s concurrent fibers collect safely) are both built; `auto` (compiler-inserted frees via static escape/liveness analysis, no runtime collector at all) is still not started, and is a bigger risk to get right than `gc` was — a wrong escape analysis there is a real use-after-free, not just a missed free. `manual` mode is still the default (an unbounded-growth non-issue for today's short-lived CLI programs), but the HTTP server below no longer has to leak every request's allocations forever the way it did before — compiling with `-mm=gc` is a one-flag opt-in fix for exactly that: the stack-safety bugs that used to crash a running `http.listen` server outright after ~20,000 requests are fixed ([ADR-00052](docs/adr/ADR-00052.md)), a 100,000-request load test confirmed the server survives that scale without issue, and `-mm=gc` now closes the remaining "heap only ever grows" gap for genuinely long-running processes.
2. **Event loop — Part 1 and Part 2 both have real, shipped slices now** (see [`docs/tdd/TDD-00006.md`](docs/tdd/TDD-00006.md)). Part 1 (a `select()`-based wait loop merging with the existing timer queue) shipped alongside the HTTP server below — see [ADR-00048](docs/adr/ADR-00048.md). Part 2 (real suspension) was scoped around three candidate mechanisms; a direct prototyping spike ruled out LLVM coroutine intrinsics (confirmed incompatible with this compiler's `setjmp`/`longjmp` exception model — a `try`/`catch` spanning a suspend point segfaults) and confirmed hand-rolled fibers (`ucontext.h`, no custom assembly needed) work correctly instead. [ADR-00049](docs/adr/ADR-00049.md) used that mechanism to make `http.listen` handle connections concurrently; [ADR-00050](docs/adr/ADR-00050.md) extended it to make `await fetch(...)` genuinely non-blocking (libcurl's multi-interface, merged into the same event loop) — confirmed directly, not just by unit test, that two concurrent connections each awaiting a different-latency upstream complete independently rather than serializing. Two stability bugs surfaced after those shipped, both fixed and directly reproduced before/after: [ADR-00051](docs/adr/ADR-00051.md) (the fiber mechanism's `ucontext_t` buffer size was hardcoded from a macOS-only probe, corrupting memory on Linux) and [ADR-00052](docs/adr/ADR-00052.md) (several hand-written IR loops leaked a fixed amount of stack on every iteration — the main `http.listen` dispatch loop crashed a running server after ~20,000 requests under load-testing with Apache Bench). `Promise.all`/`.race`/`.allSettled` (concurrently awaiting several promises from one call site, rather than relying on separate connection handlers each awaiting their own) is now done too — see [ADR-00073](docs/adr/ADR-00073.md)/[TDD-00016](docs/tdd/TDD-00016.md). This closes out the event loop's own roadmap; no further gap is tracked here.
3. **HTTP server** (see [`docs/tdd/TDD-00004.md`](docs/tdd/TDD-00004.md)) — V1 done ([ADR-00048](docs/adr/ADR-00048.md)), concurrent connection handling done on top of it ([ADR-00049](docs/adr/ADR-00049.md), using Event Loop Part 2's fiber mechanism), and request headers/query-string/request-body/response-headers all done now too ([ADR-00072](docs/adr/ADR-00072.md)) — `req.headers`/`req.query` are `Map<string, string>` (headers lowercased, query percent-decoded), `req.body` is read `Content-Length`-aware with the read buffer growing across as many reads as it takes, and a handler's return type can optionally add a `headers: Map<string, string>` field. Only remaining gap: graceful shutdown (`.close()`/`SIGINT`/`SIGTERM`) — deliberately deferred, since it needs POSIX signal handling this compiler has no infrastructure for at all yet, a genuinely separate piece of design work.

Prefer picking up work that advances REST API interaction / file I/O / process interaction over other equal-effort items — these three items are exactly that category, alongside the `fs`/`process` work already done.

### Later — a differentiator feature, deliberately deprioritized

**IndexedDB-compatible storage API** (see [`docs/tdd/TDD-00011.md`](docs/tdd/TDD-00011.md)) — not started, and deliberately scoped to be picked up only after the structural priorities above (and most of the rest of this roadmap) are further along. The idea: expose the real `indexedDB` global/`IDBDatabase`/`IDBObjectStore` API shape (not a bespoke KV API, and not a SQL surface) so hand-written app code using that idiom — and, longer-term, existing npm `IndexedDB` client packages like Dexie.js/localForage, though that specifically also needs `class` support ([TDD-00009](docs/tdd/TDD-00009.md)) first — has somewhere to run. Four backend directions compared (lowest to highest effort/risk): a hand-rolled RESP client proxying to an external Redis (no new dependency at all — just one missing socket primitive, outbound `connect()`); an embedded SQLite (same C-linking pattern `fetch`/libcurl already uses); a from-scratch native storage engine (zero dependency, matching this project's usual ethos, but real crash-safety engineering); or embedding a mature pure-Go engine (BBolt recommended over BadgerDB/Pebble/BuntDB/go-memdb) via a `cgo`-built static archive linked into the compiled output — gated on a direct prototype confirming the Go runtime's own background threading/signal handling coexists safely with this compiler's fiber scheduler, not yet verified either way.

### Web Platform & Node.js APIs backlog

Not-yet-implemented items from the [Web Platform APIs](#web-platform-apis) and [Node.js APIs](#nodejs-apis) sections above, grouped by effort. Within a tier, the same tiebreaker applies — prefer whichever unlocks REST API interaction / file I/O / process interaction.

The event loop existing now ([TDD-00006](docs/tdd/TDD-00006.md)) changes the shape of this backlog: several items below used to be tiered partly by "needs the event loop to exist first," which is no longer a real blocker for any of them. Tiers are re-evaluated against what actually remains, not against that now-satisfied prerequisite.

**Low effort (C stdlib or a simple wrapper):**
- `TextEncoder` / `TextDecoder` — UTF-8 is the only required encoding; hand-roll or use `iconv`
- `URL` / `URLSearchParams` — C string parsing, no external dependency needed
- `performance.mark(name)` / `performance.measure(...)` — named timing marks on top of the existing `performance.now()`
- `structuredClone(obj)` — recursive deep-copy of heap objects
- `setImmediate` / `clearImmediate` — moved down from Medium: its stated prerequisite ("Timers' core mechanism") shipped as part of the unified event loop ([ADR-00048](docs/adr/ADR-00048.md)); this is now a small, unblocked follow-on, not waiting on anything

**Medium effort (new dependency or subsystem):**
- `ArrayBuffer` + TypedArrays — new IR representation (a contiguous memory block with typed views); also the prerequisite for actually fixing the `fetch`/`fs` null-byte-truncation bug above, and for `crypto.subtle` below
- ~~`fetch`'s custom method/headers/request body~~ — ✅ done, see the Web Platform APIs table above ([ADR-00074](docs/adr/ADR-00074.md)/[TDD-00017](docs/tdd/TDD-00017.md)). Deliberately scoped without real `Request`/`Headers` classes — an `init` object with `method`/`headers`/`body` fields, reusing the same `Map<string,string>` convention already used elsewhere in this compiler, matching every other place this project narrows a real Web API down to its structurally-typed equivalent
- `CompressionStream` / `DecompressionStream` — link `zlib`
- `EventTarget` / `Event` / `CustomEvent` — generic event bus; prerequisite for a general-purpose `AbortController` and others
- `AbortController` / `AbortSignal` — a *fetch-specific* cancellation token is now lower effort than the general version implies: the multi-interface machinery [ADR-00050](docs/adr/ADR-00050.md) built already tracks each in-flight transfer via its own easy handle, and `curl_multi_remove_handle` + `curl_easy_cleanup` is a real, already-available way to cancel one mid-transfer. A general, `EventTarget`-based signal usable by other consumers (timers, streams) is still gated on `EventTarget` existing first.
- `WebSocket` — TCP + HTTP upgrade. Originally scoped as hand-rolled POSIX sockets or `libwebsockets` before any event loop existed; now that `http.listen`'s per-connection fiber scheduler is real and load-tested at 100,000 requests ([ADR-00049](docs/adr/ADR-00049.md), [ADR-00052](docs/adr/ADR-00052.md)), a `WebSocket` server could plausibly reuse the same accept-a-connection-onto-its-own-fiber pattern instead of building new concurrency machinery from scratch — likely meaningfully less effort than originally scoped, though the HTTP Upgrade handshake and frame parsing/masking are still real, new work.
- `EventSource` (SSE) — moved down from High: both stated prerequisites (`fetch`'s non-blocking transfers, the event loop) now exist ([ADR-00050](docs/adr/ADR-00050.md)). The remaining work is narrower than originally scoped but still real: SSE needs incremental delivery as chunks arrive (firing a callback per event), whereas today's `fetch` buffers the whole body until the transfer completes — the write callback and await-path both need a genuinely different, streaming-shaped design, not a copy of `fetch`'s buffer-then-return one.

**High effort (needs a concurrency model beyond the event loop's single-fiber cooperative scheduling, or a new external dependency):**
- `Worker` (Web Workers) — threads via `pthreads`; requires `SharedArrayBuffer` + `Atomics` too. The shipped event loop is cooperative, one-fiber-at-a-time concurrency ([TDD-00006](docs/tdd/TDD-00006.md)), not preemptive multi-threading — a genuinely separate mechanism, not an extension of it.
- `crypto.subtle` (digest, encrypt, sign) — delegate to OpenSSL or Apple CommonCrypto
- `ReadableStream` / `WritableStream` / `TransformStream` — full streaming pipeline; complex backpressure model

---

*Last updated: 2026-07-28. Update this file whenever a new feature is added or removed.*
