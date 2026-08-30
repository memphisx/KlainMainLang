# Architecture Decision Records

This folder tracks every non-trivial implementation decision, feature addition, and bug fix made in KlainMainLang from this point forward. Every new feature and every bug fix requires an ADR — see below for the required sections.

An ADR is written *after* a feature/fix is implemented, tested, and verified — it's a log of what was decided and why while building something, not a design proposal (that's what a `docs/tdd/` Technical Design Document is for, written *before* implementation, when the design might still change). Because of that, an ADR has no "is this done yet" status to track: by the time one exists, the work it describes is already finished. There's no `Proposed` state in this project's ADRs, and never will be — a not-yet-implemented idea belongs in a TDD instead.

## Numbering

- Files are named `ADR-NNNNN.md`, zero-padded to 5 digits, starting at [ADR-00001](ADR-00001.md).
- Numbers are assigned sequentially and never reused, even if an ADR is later superseded or reverted.
- Before creating a new one, check the **Index** below for the last number used.

## Cross-referencing

Every mention of another ADR or a TDD — in prose, in the `Relations` field, in `STATUS.md`, anywhere — is a real markdown link to that file (`[ADR-00047](ADR-00047.md)` from within `docs/adr/`, `[TDD-00006](../tdd/TDD-00006.md)` from outside it), not a bare `ADR-00047` or a backtick-quoted path. The one exception is a document referring to *itself* (its own title, or a phrase like "the pre-ADR-00041 compiler" inside ADR-00041 itself) — linking a page to itself has no navigation value, so those stay plain text. A generic placeholder like `ADR-NNNNN` (e.g. in this file's own `Relations` section below, or `TEMPLATE.md`) is never a real reference and is never linked either.

## Relations

Optional field, omitted entirely when there's nothing to note. Captures how this ADR connects to others, or to the TDD it originated from:

- **`Extends ADR-NNNNN`** / **`Extended by ADR-NNNNN`** — this ADR built on an earlier one's deliberately narrowed scope (or was later built upon), additive rather than a reversal. Written on *both* ends of the relationship, so either ADR is discoverable from the other without searching.
- **`Supersedes ADR-NNNNN`** / **`Superseded by ADR-NNNNN`** — a later ADR actually overturned or replaced an earlier one's decision, not just built on top of it.
- **`Implements TDD-NNNNN`** — this ADR is the (or a) real implementation of a design scoped out in `docs/tdd/`.

Combine as needed on one line, e.g. `Extends ADR-00012, ADR-00037`. A scope narrowing that hasn't been picked up by any later ADR yet (e.g. "`fetch` is GET-only for now") gets no `Relations` line at all — the *deferred* item is tracked in `STATUS.md`'s roadmap, not treated as this ADR being incomplete.

## Format

Copy [`TEMPLATE.md`](TEMPLATE.md) as a starting point. At minimum, an ADR must cover:

- **Context** — the problem or need, and how it was discovered (repro steps, what surfaced it).
- **Investigation** — what was read/tested to understand the root cause before deciding on a fix; cite concrete file:line references.
- **Decision** — the approach taken, and briefly why alternatives were rejected.
- **Implementation notes** — files touched, and anything non-obvious that came up while implementing.
- **Side effects discovered** — any other bugs, limitations, or surprises found along the way, even if not fixed (link to where they ended up tracked, e.g. `STATUS.md`).
- **Verification** — how the fix/feature was confirmed to work (tests added, manual repros, build/test/example runs).

## Index

<!-- The table below is GENERATED from the ADR files by `make status` (do not edit rows by hand):
     Title comes from each `docs/adr/ADR-*.md` heading; Relations is that file's own Relations bullet,
     normalized (references linkified). Edit the ADR files, then regenerate. -->

| # | Title | Relations |
|---|---|---|
| [00001](ADR-00001.md) | Fix closure capture-by-value bug (share mutable state with enclosing scope) | |
| [00002](ADR-00002.md) | Implement process.argv, process.exit(code), process.env | |
| [00003](ADR-00003.md) | Implement String.prototype.replaceAll(from, to) | |
| [00004](ADR-00004.md) | Fix empty-string-argument bugs in .split(), .padStart(), .padEnd() | |
| [00005](ADR-00005.md) | Implement String.prototype.trimStart() / trimEnd() | |
| [00006](ADR-00006.md) | Fix JSON.stringify(boolean[]) crash and JSON.stringify(object[]) garbage output | |
| [00007](ADR-00007.md) | Implement JSON.parse(s) → object (flat objects, primitive fields) | |
| [00008](ADR-00008.md) | Implement any/unknown as a runtime-tagged value (Staged V1) | |
| [00009](ADR-00009.md) | Implement Math.asin/acos/atan/atan2, sinh/cosh/tanh, cbrt/expm1/log1p | |
| [00010](ADR-00010.md) | Implement labeled break/continue | |
| [00011](ADR-00011.md) | Implement for...of over Map and Set | |
| [00012](ADR-00012.md) | Implement shorthand object properties { x } | Extended by [ADR-00041](ADR-00041.md) |
| [00013](ADR-00013.md) | Implement object spread { ...obj, key: val } | |
| [00014](ADR-00014.md) | Implement Date (UTC-only) | |
| [00015](ADR-00015.md) | Implement Date.parse(string) | Extended by [ADR-00017](ADR-00017.md) |
| [00016](ADR-00016.md) | Implement Date setters (setFullYear, setMonth, setDate, setHours, setMinutes, setSeconds, setMilliseconds, setTime) | |
| [00017](ADR-00017.md) | Extend Date.parse to support "+HH:MM"/"-HH:MM" timezone offsets | Extends [ADR-00015](ADR-00015.md) |
| [00018](ADR-00018.md) | Implement Date arithmetic (adding/subtracting durations) | |
| [00019](ADR-00019.md) | Implement Date formatting (toDateString / toLocaleDateString) | |
| [00020](ADR-00020.md) | Link-flags plumbing (compiled programs can depend on external libraries) | |
| [00021](ADR-00021.md) | Implement fetch(url) and Response (GET only, V1) | |
| [00022](ADR-00022.md) | Implement import/export (named, declarations-only, V1) | |
| [00023](ADR-00023.md) | Implement fs.readFileSync/writeFileSync/appendFileSync/existsSync/unlinkSync | Extended by [ADR-00027](ADR-00027.md) |
| [00024](ADR-00024.md) | Near-zero-effort roadmap batch (NaN/Infinity, performance.now, atob/btoa, encodeURI(Component)/decodeURI(Component), crypto.getRandomValues/randomUUID, process.readLineSync) | |
| [00025](ADR-00025.md) | Implement process.execFileSync(file, args?) (V1: no options object) | |
| [00026](ADR-00026.md) | Implement process.cwd/chdir/pid/platform/kill | |
| [00027](ADR-00027.md) | Complete the fs.* API (mkdirSync/rmdirSync/renameSync/copyFileSync/readdirSync) | Extends [ADR-00023](ADR-00023.md) |
| [00028](ADR-00028.md) | Implement String.prototype charAt/codePointAt/search/localeCompare | |
| [00029](ADR-00029.md) | Implement console.time/timeEnd, count/countReset, group/groupEnd, dir | |
| [00030](ADR-00030.md) | Implement Memory.free(x) (Stage 1 of the manual-memory-management plan) | Implements [TDD-00001](../tdd/TDD-00001.md) |
| [00031](ADR-00031.md) | Implement setTimeout/clearTimeout/setInterval/clearInterval | Implements [TDD-00002](../tdd/TDD-00002.md) |
| [00032](ADR-00032.md) | Add a --static CLI flag for statically-linked binaries (Linux only) | |
| [00033](ADR-00033.md) | Verify --static with fetch/libcurl on Alpine/musl | |
| [00034](ADR-00034.md) | Fix missing -lm link flag for Math builtins on Linux | |
| [00035](ADR-00035.md) | Fix JSON.stringify truncating float-typed values to integers | |
| [00036](ADR-00036.md) | Fix JSON.stringify serializing Date fields as raw ms numbers | |
| [00037](ADR-00037.md) | Fix parenthesized function-type return annotations | Extended by [ADR-00041](ADR-00041.md) |
| [00038](ADR-00038.md) | Fix new Date(aStringLiteral) crashing instead of parsing | |
| [00039](ADR-00039.md) | Implement the multi-argument new Date(year, month, ...) constructor | |
| [00040](ADR-00040.md) | Support JSDoc @type overrides on interface fields | |
| [00041](ADR-00041.md) | Infer return types for unannotated functions and arrow functions | Extends [ADR-00012](ADR-00012.md), [ADR-00037](ADR-00037.md); Extended by [ADR-00150](ADR-00150.md) |
| [00042](ADR-00042.md) | Reject non-numeric arguments to unannotated parameters at call sites | Implements [TDD-00005](../tdd/TDD-00005.md) |
| [00043](ADR-00043.md) | Fix forEach/HOF callbacks with console.log bodies or non-numeric elements | |
| [00044](ADR-00044.md) | Fix array index out-of-bounds reads/writes with a runtime bounds check | |
| [00045](ADR-00045.md) | Reject const reassignment with a Symbol.IsConst check in emitAssign | |
| [00046](ADR-00046.md) | Fix (FuncType)[] parser gap and enable calling closures stored in arrays/object fields | |
| [00047](ADR-00047.md) | Fix bitwise shift operators to use JS's 32-bit semantics | Implements [TDD-00008](../tdd/TDD-00008.md) (partially — first real Test262 test ports) |
| [00048](ADR-00048.md) | select()-based event loop ([TDD-00006](../tdd/TDD-00006.md) Part 1) and a minimal HTTP server ([TDD-00004](../tdd/TDD-00004.md) V1) | Implements [TDD-00004](../tdd/TDD-00004.md), [TDD-00006](../tdd/TDD-00006.md) (Part 1 only — Part 2, real async suspension, still not started) |
| [00049](ADR-00049.md) | Concurrent HTTP connection handling via fibers ([TDD-00006](../tdd/TDD-00006.md) Part 2, first real slice) | Extends [ADR-00048](ADR-00048.md). Implements [TDD-00006](../tdd/TDD-00006.md) (Part 2 — first real implementation slice; `async`/`await`/`fetch()` itself is not yet wired through this mechanism) |
| [00050](ADR-00050.md) | Non-blocking await fetch(...) via libcurl's multi-interface ([TDD-00006](../tdd/TDD-00006.md) Part 2, second real slice) | Extends [ADR-00049](ADR-00049.md). Implements [TDD-00006](../tdd/TDD-00006.md) (Part 2 — `fetch()` slice; general `Promise.all`/`.race`/`.allSettled` still not started) |
| [00051](ADR-00051.md) | Fix ucontext_t's size/layout being hardcoded to this dev machine's platform | Extends [ADR-00049](ADR-00049.md), [ADR-00050](ADR-00050.md) |
| [00052](ADR-00052.md) | Fix `alloca`s inside loop bodies causing unbounded stack growth | Extends [ADR-00049](ADR-00049.md), [ADR-00050](ADR-00050.md) |
| [00053](ADR-00053.md) | Implement Map.entries()/.forEach()/.clear() and Set.forEach()/.clear() | |
| [00054](ADR-00054.md) | Implement Object.assign(target, ...src) | |
| [00055](ADR-00055.md) | Implement Object.freeze(obj) / Object.seal(obj) | |
| [00056](ADR-00056.md) | Fix Array.prototype.splice's out-of-bounds read and missing insert-item support | |
| [00057](ADR-00057.md) | Implement findLast/findLastIndex, toSorted/toReversed/toSpliced, with, keys/values/entries, copyWithin, and Array.of | Extends [ADR-00056](ADR-00056.md) |
| [00058](ADR-00058.md) | Fix Array\<T\>/Map\<K,V\>/Set\<T\> silently resolving to i64 as a plain type annotation | |
| [00059](ADR-00059.md) | Fix Map/Set method calls, .size, and for...of only recognizing a plain named variable | Extends [ADR-00058](ADR-00058.md) |
| [00060](ADR-00060.md) | Fix for...in and return-of-an-array only recognizing a plain named variable | Extends [ADR-00059](ADR-00059.md) |
| [00061](ADR-00061.md) | Fix array-typed object/interface fields losing their length entirely | Extends [ADR-00060](ADR-00060.md) |
| [00062](ADR-00062.md) | Classes Stage 0 — lexer/parser/AST groundwork | Implements [TDD-00009](../tdd/TDD-00009.md) (Stage 0 of 4) |
| [00063](ADR-00063.md) | Classes Stage 1 (+1a) — methods, constructors, `this`, `new`, and a class-based `for...of` iterator protocol | Extends [ADR-00062](ADR-00062.md). Implements [TDD-00009](../tdd/TDD-00009.md) (Stages 1 and 1a of 4). Extended by [ADR-00278](ADR-00278.md) |
| [00064](ADR-00064.md) | Fix `emitObjectVarDecl`'s narrow initializer whitelist and self-referential class field type staleness | Extends [ADR-00063](ADR-00063.md) |
| [00065](ADR-00065.md) | Implement Number.toPrecision/toExponential/toString(radix), Math.clz32/fround/imul, and Object.hasOwn/hasOwnProperty | |
| [00066](ADR-00066.md) | Implement computed property keys `{ [expr]: value }` | Implements [TDD-00012](../tdd/TDD-00012.md) |
| [00067](ADR-00067.md) | Classes Stage 2 — runtime type tags and `instanceof` | Extends [ADR-00063](ADR-00063.md), [ADR-00064](ADR-00064.md). Implements [TDD-00009](../tdd/TDD-00009.md) (Stage 2 of 4) |
| [00068](ADR-00068.md) | Add native Go fuzz testing for the lexer and parser | Extended by [ADR-00070](ADR-00070.md) |
| [00069](ADR-00069.md) | Fix division/modulo by zero producing undefined behavior instead of a catchable Error | |
| [00070](ADR-00070.md) | Full-pipeline (codegen-through-binary) fuzz testing | Extends [ADR-00068](ADR-00068.md). Implements [TDD-00014](../tdd/TDD-00014.md) |
| [00071](ADR-00071.md) | `-mm=gc` — Boehm GC mode | Implements [TDD-00001](../tdd/TDD-00001.md) |
| [00072](ADR-00072.md) | `http.listen` — request headers, query string, request body, response headers | Extends [ADR-00048](ADR-00048.md), [ADR-00049](ADR-00049.md) |
| [00073](ADR-00073.md) | Promise.all / .race / .allSettled | Extends [ADR-00049](ADR-00049.md), [ADR-00050](ADR-00050.md). Implements [TDD-00016](../tdd/TDD-00016.md) |
| [00074](ADR-00074.md) | fetch() client parity — custom method, headers, request body | Extends [ADR-00050](ADR-00050.md). Implements [TDD-00017](../tdd/TDD-00017.md). Superseded by [ADR-00130](ADR-00130.md) (its Request/Headers-class rejection only — the custom method/headers/body mechanism this ADR built is unchanged and still how a plain `init` object works) |
| [00075](ADR-00075.md) | Split oversized codegen/parser files into domain files | |
| [00076](ADR-00076.md) | URL / URLSearchParams | |
| [00077](ADR-00077.md) | Coerce object literal fields against their declared type | Implements [TDD-00007](../tdd/TDD-00007.md) |
| [00078](ADR-00078.md) | ArrayBuffer / TypedArrays | Implements [TDD-00018](../tdd/TDD-00018.md) |
| [00079](ADR-00079.md) | POSIX signal handling — `process.on('SIGINT'/'SIGTERM', handler)` | Implements [TDD-00019](../tdd/TDD-00019.md). Extends [ADR-00072](ADR-00072.md), which explicitly deferred graceful shutdown pending this exact infrastructure. |
| [00080](ADR-00080.md) | Fix `-mm=gc` startup crash on Ubuntu's `libgc-dev` build | Extends [ADR-00071](ADR-00071.md), which introduced `-mm=gc` and only ever exercised it against Homebrew's `bdw-gc` build on macOS/arm64. |
| [00081](ADR-00081.md) | `path` module (join, resolve, dirname, basename, extname, parse, format, isAbsolute, sep, delimiter) | |
| [00082](ADR-00082.md) | Error subtypes / tagged errors — `TypeError`, `RangeError`, etc. | Implements [TDD-00013](../tdd/TDD-00013.md) (Option A). Extends [ADR-00067](ADR-00067.md), class Stage 2's runtime type tag / `instanceof` mechanism, reusing the same shape for a sibling, non-class case. |
| [00083](ADR-00083.md) | Classes Stage 3 — inheritance (`extends`, `super`, dynamic dispatch) | Extends [ADR-00062](ADR-00062.md), [ADR-00063](ADR-00063.md), [ADR-00067](ADR-00067.md). Implements [TDD-00009](../tdd/TDD-00009.md). |
| [00084](ADR-00084.md) | Classes Stage 4 — `static`, `private`/`protected`, `abstract`, `implements` | Extends [ADR-00083](ADR-00083.md). Implements [TDD-00009](../tdd/TDD-00009.md). |
| [00085](ADR-00085.md) | Numeric separators (`1_000_000`) | |
| [00086](ADR-00086.md) | Optional catch binding (`catch { ... }`) | |
| [00087](ADR-00087.md) | Logical assignment operators (`&&=`, `\|\|=`, `??=`) | |
| [00088](ADR-00088.md) | `Array.from(iterable)` (array-like overload) | Extends [ADR-00063](ADR-00063.md). Implements [TDD-00009](../tdd/TDD-00009.md)'s Stage 1a iterator-protocol consumer side. |
| [00089](ADR-00089.md) | `EventEmitter<T>` (`events` module) | Extends [ADR-00083](ADR-00083.md). Implements [TDD-00023](../tdd/TDD-00023.md). |
| [00090](ADR-00090.md) | `os` module | Implements [TDD-00024](../tdd/TDD-00024.md). |
| [00091](ADR-00091.md) | `in` operator (`key in obj`) | |
| [00092](ADR-00092.md) | `setImmediate(fn)` / `clearImmediate(id)` | Extends [ADR-00031](ADR-00031.md) |
| [00093](ADR-00093.md) | Fix missing `GC_stackbottom` restore on `http.listen`'s read-loop yield (4th swapcontext site ADR-00071 missed) | Extends [ADR-00071](ADR-00071.md), which introduced the `GC_stackbottom` fiber-swap fix this ADR completes. Extended by [ADR-00101](ADR-00101.md), which found this restore was still incomplete (didn't cover a fiber running to completion without yielding again) and moved it to the resumer side. |
| [00094](ADR-00094.md) | Fix fetch/fs embedded-null-byte truncation via `Response.arrayBuffer()` / `fs.readFileSyncBytes()` / binary-aware `writeFileSync`/`appendFileSync` | Extends [ADR-00021](ADR-00021.md) (`Response`), [ADR-00023](ADR-00023.md) (`fs.readFileSync`), [ADR-00078](ADR-00078.md) (`ArrayBuffer`/TypedArrays, this fix's prerequisite); "no `Buffer` class" half superseded by [ADR-00315](ADR-00315.md) (the WHATWG fetch/fs return surface decided here stands) |
| [00095](ADR-00095.md) | Remove dead `__kml_fetch` (blocking single-transfer fetch, superseded by ADR-00050) | Extends [ADR-00094](ADR-00094.md), which found this during its own investigation but deferred removing it; superseded by [ADR-00050](ADR-00050.md)'s non-blocking multi-interface rewrite, which is what actually made this dead |
| [00096](ADR-00096.md) | Replace httpbin.org with a local httpbin-lite fixture in `make examples` | Extends [ADR-00021](ADR-00021.md) (`fetch.ts`), [ADR-00073](ADR-00073.md) (`promise_all.ts`), [ADR-00074](ADR-00074.md) (`fetch_init.ts`), each of which originally pointed its example at `httpbin.org` |
| [00097](ADR-00097.md) | Multi-process clustering for `http.listen()` | Implements [TDD-00025](../tdd/TDD-00025.md); Extends [ADR-00048](ADR-00048.md)/[ADR-00049](ADR-00049.md) (event loop, fiber-based connection concurrency); see also [ADR-00098](ADR-00098.md) (a pre-existing connection-handling bug found while testing this feature, unrelated to clustering itself but required to make it reliably testable) |
| [00098](ADR-00098.md) | Fix dangling connection-array pointer causing intermittent `http.listen` hangs under concurrent load | Extends [ADR-00049](ADR-00049.md) (fiber-based concurrent connection handling), [ADR-00072](ADR-00072.md) (the dispatcher this bug lives in); same general bug class as [ADR-00052](ADR-00052.md) (hand-written IR in this same connection-handling machinery, a different invariant broken the same way — a value computed once outside a loop that should have been recomputed on every entry); found while working on [ADR-00097](ADR-00097.md)/[TDD-00025](../tdd/TDD-00025.md) |
| [00099](ADR-00099.md) | `GC_set_handle_fork(1)` for `-mm=gc` + `http.listen` clustering — fixed one real crash, one residual hang remains unresolved despite extensive investigation | Extends [ADR-00097](ADR-00097.md) (manual-mode clustering), [ADR-00098](ADR-00098.md); Implements [TDD-00025](../tdd/TDD-00025.md)'s `-mm=gc` follow-on (partially — see Status). Extended by [ADR-00101](ADR-00101.md), which root-caused and fixed the residual hang left open here. |
| [00100](ADR-00100.md) | Fix `-mm=gc` startup crash under AddressSanitizer, and add ASan/UBSan test-build helpers | Extends [ADR-00071](ADR-00071.md) (`-mm=gc`), [ADR-00080](ADR-00080.md) (the previous earliest-known pre-constructor-malloc crash, same general class of bug) |
| [00101](ADR-00101.md) | Root cause of the `-mm=gc` clustering hang — `GC_stackbottom` never restored when a fiber runs to completion | Extends [ADR-00071](ADR-00071.md) (the original `GC_stackbottom`-repointing scheme), [ADR-00093](ADR-00093.md) (the read-loop yield's own restore fix — turned out to still be incomplete), [ADR-00099](ADR-00099.md) (the exhaustive-but-unsuccessful first investigation pass); Implements [TDD-00025](../tdd/TDD-00025.md)'s `-mm=gc` follow-on (now complete — see Status) |
| [00102](ADR-00102.md) | `http.close()` — listener teardown | Implements [TDD-00027](../tdd/TDD-00027.md); references [TDD-00019](../tdd/TDD-00019.md)/[ADR-00079](ADR-00079.md) (settles that TDD's open question about whether a future `.close()` should reuse its pending-flag mechanism) |
| [00103](ADR-00103.md) | User-defined generics V1 — monomorphization for functions, interfaces, classes | Implements [TDD-00010](../tdd/TDD-00010.md) |
| [00104](ADR-00104.md) | Array/Map/Set/EventEmitter literals as general expressions | Implements [TDD-00028](../tdd/TDD-00028.md); surfaced and split out [TDD-00029](../tdd/TDD-00029.md) (array-of-arrays storage) |
| [00105](ADR-00105.md) | Array-of-arrays (nested array) storage representation | Implements [TDD-00029](../tdd/TDD-00029.md); Extended by [ADR-00152](ADR-00152.md) |
| [00106](ADR-00106.md) | Binary-safe `http.listen()` request/response bodies | Implements [TDD-00026](../tdd/TDD-00026.md) |
| [00107](ADR-00107.md) | `Array.prototype.flat(depth?)` / `.flatMap(fn)` | Implements the `.flat()`/`.flatMap()` follow-on left open by [TDD-00029](../tdd/TDD-00029.md)/[ADR-00105](ADR-00105.md) |
| [00108](ADR-00108.md) | `i64::MIN / -1` UB guard, and `%=` (was entirely unimplemented) | |
| [00109](ADR-00109.md) | Destructured function parameters | |
| [00110](ADR-00110.md) | Getters / setters on classes | Implements [TDD-00030](../tdd/TDD-00030.md) |
| [00111](ADR-00111.md) | `performance.mark(name)` / `performance.measure(name, start, end?)` | |
| [00112](ADR-00112.md) | TextEncoder / TextDecoder | |
| [00113](ADR-00113.md) | structuredClone(obj) | |
| [00114](ADR-00114.md) | RegExp Stage 0 — construction, literal syntax, field reads | Implements [TDD-00035](../tdd/TDD-00035.md) |
| [00115](ADR-00115.md) | RegExp Stage 1 — .test(str) | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md) |
| [00116](ADR-00116.md) | RegExp Stage 2 — .exec(str), and three general truthiness/null-comparison bugs it exposed | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md), [ADR-00115](ADR-00115.md) |
| [00117](ADR-00117.md) | RegExp Stage 3 — str.match() and str.matchAll() | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md), [ADR-00115](ADR-00115.md), [ADR-00116](ADR-00116.md) |
| [00118](ADR-00118.md) | RegExp Stage 4 — str.replace() and str.replaceAll() | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md), [ADR-00115](ADR-00115.md), [ADR-00116](ADR-00116.md), [ADR-00117](ADR-00117.md) |
| [00119](ADR-00119.md) | RegExp Stage 5 — str.split() and str.search() | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md), [ADR-00115](ADR-00115.md), [ADR-00116](ADR-00116.md), [ADR-00117](ADR-00117.md), [ADR-00118](ADR-00118.md) |
| [00120](ADR-00120.md) | RegExp Stage 6 — `--static` linking verification | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md), [ADR-00115](ADR-00115.md), [ADR-00116](ADR-00116.md), [ADR-00117](ADR-00117.md), [ADR-00118](ADR-00118.md), [ADR-00119](ADR-00119.md) |
| [00121](ADR-00121.md) | User-defined generics V2 — `@erased` opt-in type erasure | Implements [TDD-00010](../tdd/TDD-00010.md), Extends [ADR-00103](ADR-00103.md) |
| [00122](ADR-00122.md) | EventSource (Server-Sent Events) Stage 0 — connection plumbing | Implements [TDD-00038](../tdd/TDD-00038.md) |
| [00123](ADR-00123.md) | EventSource Stage 1 — SSE record parsing and `onmessage` | Implements [TDD-00038](../tdd/TDD-00038.md), Extends [ADR-00122](ADR-00122.md) |
| [00124](ADR-00124.md) | EventSource Stage 2 — named events, `onopen`/`onerror` | Implements [TDD-00038](../tdd/TDD-00038.md), Extends [ADR-00123](ADR-00123.md) |
| [00125](ADR-00125.md) | WebSocket Stage 0 — shared frame codec + SHA-1 | Implements [TDD-00039](../tdd/TDD-00039.md) (Stage 0 only — Stages 1-3 remain) |
| [00126](ADR-00126.md) | WebSocket Stage 1 — server-side upgrade + persistent echo loop | Implements [TDD-00039](../tdd/TDD-00039.md) (Stage 1 of 4 — Stage 0 was [ADR-00125](ADR-00125.md); Stages 2-3 remain) |
| [00127](ADR-00127.md) | WebSocket Stage 2 — automatic ping/pong + close handshake | Implements [TDD-00039](../tdd/TDD-00039.md) (Stage 2 of 4 — Stage 0 was [ADR-00125](ADR-00125.md), Stage 1 was [ADR-00126](ADR-00126.md); Stage 3 remains) |
| [00128](ADR-00128.md) | WebSocket Stage 3 — client-side `new WebSocket(url)` | Implements [TDD-00039](../tdd/TDD-00039.md) (Stage 3 of 4, the last — Stage 0 was [ADR-00125](ADR-00125.md), Stage 1 was [ADR-00126](ADR-00126.md), Stage 2 was [ADR-00127](ADR-00127.md)); fixes a real bug in [ADR-00126](ADR-00126.md)'s own `ensureBase64EncodeBytes` |
| [00129](ADR-00129.md) | EventSource Stage 3 — CRLF boundaries, terminal failure, auto-reconnect | Implements [TDD-00038](../tdd/TDD-00038.md), Extends [ADR-00122](ADR-00122.md), [ADR-00123](ADR-00123.md), [ADR-00124](ADR-00124.md) |
| [00130](ADR-00130.md) | Real `Request`/`Headers` classes, and freeing up `Request` from `http.listen`'s server-side type | Supersedes the `Request`/`Headers` decision in [ADR-00074](ADR-00074.md). Implements [TDD-00040](../tdd/TDD-00040.md) (Stage 1 of 2 — `XMLHttpRequest` is Stage 2, a separate ADR) |
| [00131](ADR-00131.md) | `XMLHttpRequest` — legacy synchronous-style client on top of `fetch`'s own non-blocking primitives | Implements [TDD-00040](../tdd/TDD-00040.md) (Stage 2 of 2 — Stage 1, `Request`/`Headers`, was [ADR-00130](ADR-00130.md)). Extends [ADR-00050](ADR-00050.md), [ADR-00073](ADR-00073.md) |
| [00132](ADR-00132.md) | Multiple type parameters for user-defined generics (`<K, V>`) | Extends [ADR-00103](ADR-00103.md), [ADR-00121](ADR-00121.md). Implements [TDD-00037](../tdd/TDD-00037.md) |
| [00133](ADR-00133.md) | Fix two `EventSource` auto-reconnect hangs in the event loop's own `select()` wait | Extends [ADR-00122](ADR-00122.md), [ADR-00123](ADR-00123.md), [ADR-00124](ADR-00124.md), [ADR-00129](ADR-00129.md). Implements [TDD-00038](../tdd/TDD-00038.md) |
| [00134](ADR-00134.md) | True per-file module scope via mangled internal names | Extends [ADR-00022](ADR-00022.md). Implements [TDD-00041](../tdd/TDD-00041.md) |
| [00135](ADR-00135.md) | `export default`, default imports, and namespace imports | Extends [ADR-00022](ADR-00022.md), [ADR-00134](ADR-00134.md). Implements [TDD-00042](../tdd/TDD-00042.md) |
| [00136](ADR-00136.md) | General union types beyond `T \| null` (V1: scalar members) | Extends [ADR-00008](ADR-00008.md). Implements [TDD-00043](../tdd/TDD-00043.md) |
| [00137](ADR-00137.md) | `process.stdout.write(s)` / `process.stderr.write(s)` | |
| [00138](ADR-00138.md) | `symbol` V1 (opaque unique values) | Implements [TDD-00044](../tdd/TDD-00044.md) |
| [00139](ADR-00139.md) | `querystring` module (`.parse`/`.stringify`) | |
| [00140](ADR-00140.md) | `assert` module | |
| [00141](ADR-00141.md) | Import-gated built-in bindings, Stage 1 (default/namespace form) | Implements [TDD-00049](../tdd/TDD-00049.md) |
| [00142](ADR-00142.md) | Import-gated built-in bindings, Stage 2 (named per-member imports) | Extends [ADR-00141](ADR-00141.md). Implements [TDD-00049](../tdd/TDD-00049.md) |
| [00143](ADR-00143.md) | Reserved ambient-global names — `-globals=strict\|permissive` | Implements [TDD-00050](../tdd/TDD-00050.md). Extended by [ADR-00217](ADR-00217.md) (the `-globals` flag was later absorbed into `-compat`) |
| [00144](ADR-00144.md) | Re-exports (`export { a } from './x'`, `export * from './x'`) | Implements [TDD-00051](../tdd/TDD-00051.md) |
| [00145](ADR-00145.md) | Top-level side-effecting code in imported files (dependency-ordered, cycle-guarded) | Implements [TDD-00052](../tdd/TDD-00052.md) |
| [00146](ADR-00146.md) | klmpm Stage 1 — compiler-side `klain_modules` resolution | Implements [TDD-00054](../tdd/TDD-00054.md) (Stage 1 only) |
| [00147](ADR-00147.md) | klmpm Stage 2 — `klain.json`/`klain.lock` manifest parsing | Implements [TDD-00054](../tdd/TDD-00054.md) (Stage 2 only). Extends [ADR-00146](ADR-00146.md) |
| [00148](ADR-00148.md) | `import.meta.url` (Stage 1 of dynamic `import(...)`/`import.meta`) | Implements [TDD-00055](../tdd/TDD-00055.md) (Stage 1 only) |
| [00149](ADR-00149.md) | Nested function declarations (V1: hoisted, own scope, no capture) | Implements [TDD-00057](../tdd/TDD-00057.md) |
| [00150](ADR-00150.md) | Fixed-point unannotated-return-type inference | Extends [ADR-00041](ADR-00041.md); Implements [TDD-00058](../tdd/TDD-00058.md); Extended by [ADR-00152](ADR-00152.md) |
| [00151](ADR-00151.md) | Tagged template literals | Implements [TDD-00059](../tdd/TDD-00059.md); Extended by [ADR-00152](ADR-00152.md) |
| [00152](ADR-00152.md) | Closing gaps found on a second pass over ADR-00151 (array-typed closures, class-method rest params, erased-generic forward references) | Extends [ADR-00151](ADR-00151.md), [ADR-00105](ADR-00105.md), [ADR-00150](ADR-00150.md) |
| [00153](ADR-00153.md) | Test262 full-corpus conformance harness (TDD-00008 Design V2) | Implements [TDD-00008](../tdd/TDD-00008.md) (Design V2) |
| [00154](ADR-00154.md) | Object literal string/numeric-literal property keys | Extends [ADR-00153](ADR-00153.md) (Test262 full-corpus conformance harness) |
| [00155](ADR-00155.md) | `#x` private names | Implements [TDD-00021](../tdd/TDD-00021.md); Extends [ADR-00153](ADR-00153.md) (Test262 full-corpus conformance harness) |
| [00156](ADR-00156.md) | Multi-declarator `let`/`const`/`var`, for-loop comma-update | Extends [ADR-00153](ADR-00153.md) (Test262 full-corpus conformance harness) |
| [00157](ADR-00157.md) | Uninitialized-heap-memory reads (optional fields, class fields, array destructuring bounds) | Extends [ADR-00153](ADR-00153.md) (Test262 full-corpus conformance harness) |
| [00158](ADR-00158.md) | Destructuring default values (`[a = expr]`, `{ a = expr }`) | Extends [ADR-00153](ADR-00153.md) (Test262 full-corpus conformance harness), [ADR-00157](ADR-00157.md) (uninitialized-heap-memory reads) |
| [00159](ADR-00159.md) | `new Set(iterable)` | Extends [ADR-00153](ADR-00153.md) (Test262 full-corpus conformance harness) |
| [00160](ADR-00160.md) | Destructuring assignment (`[a, b] = expr`, `({ a, b } = expr)`) | Extends [ADR-00153](ADR-00153.md), [ADR-00157](ADR-00157.md), [ADR-00158](ADR-00158.md) |
| [00161](ADR-00161.md) | Array rest destructuring (`[a, ...rest]`) | Extends [ADR-00153](ADR-00153.md), [ADR-00157](ADR-00157.md), [ADR-00160](ADR-00160.md) |
| [00162](ADR-00162.md) | `instanceof` against built-in types (`Array`, `Map`, `Set`, `Date`, `RegExp`) | Extends [ADR-00153](ADR-00153.md) |
| [00163](ADR-00163.md) | `.reduce()` with no initial value | Extends [ADR-00153](ADR-00153.md) |
| [00164](ADR-00164.md) | Optional (`param?: T`) function parameters | Extends [ADR-00157](ADR-00157.md), [ADR-00158](ADR-00158.md) |
| [00165](ADR-00165.md) | String concatenation with a null operand | Found while implementing [ADR-00164](ADR-00164.md) |
| [00166](ADR-00166.md) | Status-doc accuracy audit and the Strict Coverage metric | Found while implementing [ADR-00164](ADR-00164.md); prompted a full audit of `docs/status/` |
| [00167](ADR-00167.md) | Guard pop and shift against empty arrays | Found by [ADR-00166](ADR-00166.md) |
| [00168](ADR-00168.md) | Function expressions (V1: anonymous only) + early-error checks that fix the conformance regressions it exposed | Implements [TDD-00060](../tdd/TDD-00060.md) |
| [00169](ADR-00169.md) | Generalized call dispatch + object literal method shorthand | Extends [ADR-00153](ADR-00153.md); reuses [TDD-00060](../tdd/TDD-00060.md)'s function-expression machinery |
| [00170](ADR-00170.md) | Destructured catch binding (`catch ({ message, name }) {}`) | Extends [ADR-00086](ADR-00086.md); reuses `unpackObjectPatternInto` (shared with destructured function parameters and `const {..} = ..`) |
| [00171](ADR-00171.md) | Generator function front-end (`function* f() { yield x; }` lexer/parser/AST) — parses, cleanly rejected by codegen | Implements the front-end slice of [TDD-00061](../tdd/TDD-00061.md) |
| [00172](ADR-00172.md) | Generator function suspend/resume codegen (construction, `yield`, `.next()`) | Implements [TDD-00061](../tdd/TDD-00061.md); extends [ADR-00171](ADR-00171.md)'s front-end |
| [00173](ADR-00173.md) | `for...of` over a generator | Implements the remainder of [TDD-00061](../tdd/TDD-00061.md)'s V1 scope; extends [ADR-00172](ADR-00172.md) |
| [00174](ADR-00174.md) | Box union/dynamic arguments at static-method call sites (fixes `assert.sameValue`) | |
| [00175](ADR-00175.md) | Conformance harness — kill the whole process group and bound the pipe wait | |
| [00176](ADR-00176.md) | Bare `any`/`unknown` as a function/method parameter and return type (TDD-00062 Staged V2) | `Implements [TDD-00062](../tdd/TDD-00062.md)`. `Extends [ADR-00008](ADR-00008.md)` (the original `any`/`unknown` staging), `Extends [ADR-00174](ADR-00174.md)` (call-site dynamic-argument boxing). |
| [00177](ADR-00177.md) | Box arrays (by reference) and fix reference-type toString for `any`/`unknown` | `Extends [ADR-00176](ADR-00176.md)`, `Implements [TDD-00062](../tdd/TDD-00062.md)` (fills its deferred array-boxing item). |
| [00178](ADR-00178.md) | Named function expressions (self-reference binding) | `Implements [TDD-00060](../tdd/TDD-00060.md)` (V2 — the named-expression case [ADR-00168](ADR-00168.md) deferred). `Extends [ADR-00168](ADR-00168.md)`. |
| [00179](ADR-00179.md) | The comma / sequence operator (`(a, b, c)`) | |
| [00180](ADR-00180.md) | Class field initializers (TDD-00063 Stage 1) | `Implements [TDD-00063](../tdd/TDD-00063.md)` (Stage 1 of 4). `Extends [ADR-00155](ADR-00155.md)` (fills the field-initializer syntax [TDD-00021](../tdd/TDD-00021.md) deferred). |
| [00181](ADR-00181.md) | Async class methods + method-modifier parsing (TDD-00063 Stage 2a) | `Implements [TDD-00063](../tdd/TDD-00063.md)` (Stage 2a of the Stage 2 method-modifier work). `Extends [ADR-00180](ADR-00180.md)`. |
| [00182](ADR-00182.md) | Generator methods (TDD-00063 Stage 2b) | `Implements [TDD-00063](../tdd/TDD-00063.md)` (Stage 2b). `Extends [ADR-00181](ADR-00181.md)` (Stage 2a parsed the forms; this makes `*m()` real). Builds on [ADR-00172](ADR-00172.md) (the generator fiber machinery) and [ADR-00063](ADR-00063.md) (class method dispatch). |
| [00183](ADR-00183.md) | `console.log(boolean)` prints `true`/`false`, not `1`/`0` | |
| [00184](ADR-00184.md) | Computed class member names (TDD-00063 Stage 3) | `Implements [TDD-00063](../tdd/TDD-00063.md)` (Stage 3). `Extends [ADR-00182](ADR-00182.md)`. Mirrors [ADR-00066](ADR-00066.md) (object-literal computed keys, [TDD-00012](../tdd/TDD-00012.md)). |
| [00185](ADR-00185.md) | Class expressions (TDD-00063 Stage 4) | `Implements [TDD-00063](../tdd/TDD-00063.md)` (Stage 4, the final stage). `Extends [ADR-00184](ADR-00184.md)`. The nominal-binding posture mirrors [ADR-00168](ADR-00168.md)/[ADR-00178](ADR-00178.md) (function expressions, [TDD-00060](../tdd/TDD-00060.md)). |
| [00186](ADR-00186.md) | `&&` / `\|\|` short-circuit instead of evaluating both operands | Extends [ADR-00087](ADR-00087.md); corrects a caveat flagged by [ADR-00183](ADR-00183.md) |
| [00187](ADR-00187.md) | The `**` exponentiation operator (and `**=`) | Fixes the `**` ❌ row flagged by [ADR-00166](ADR-00166.md) |
| [00188](ADR-00188.md) | `!=` / `!==` on floats use the unordered predicate so `NaN != NaN` is true | Fixes the NaN-comparison bug flagged by [ADR-00166](ADR-00166.md) |
| [00189](ADR-00189.md) | `JSON.parse` into an array-typed field is a clean rejection, not invalid IR | Fixes a bug tracked under [TDD-00015](../tdd/TDD-00015.md) |
| [00190](ADR-00190.md) | Unannotated `let b = true` / `let n = !cond` / `let z = -3.5` infer their real type | Follows [ADR-00183](ADR-00183.md) (fixes the storage side of boolean printing) |
| [00191](ADR-00191.md) | `finally` runs on `return`/`break`/`continue`, not only on fall-through | Fixes the finally-on-return bug flagged by [ADR-00166](ADR-00166.md) |
| [00192](ADR-00192.md) | Destructuring the loop variable of a for-of (TDD-00065 Stage 1) | Implements [TDD-00065](../tdd/TDD-00065.md) Stage 1; reuses the destructuring codegen core from [ADR-00157](ADR-00157.md)/[ADR-00158](ADR-00158.md)/[ADR-00161](ADR-00161.md)/[ADR-00109](ADR-00109.md) |
| [00193](ADR-00193.md) | Nested destructuring patterns (TDD-00065 Stage 2) | Implements [TDD-00065](../tdd/TDD-00065.md) Stage 2; extends [ADR-00192](ADR-00192.md) (Stage 1); builds on the destructuring codegen core from [ADR-00157](ADR-00157.md)/[ADR-00109](ADR-00109.md) |
| [00194](ADR-00194.md) | Full string-literal escape-sequence decoding | |
| [00195](ADR-00195.md) | Conformance report — category descriptions, pipeline-phase breakdown, per-reason examples | Extends [ADR-00153](ADR-00153.md) (the conformance runner); follows [ADR-00194](ADR-00194.md) (which added `-faillist`) |
| [00196](ADR-00196.md) | String.fromCharCode/fromCodePoint reject a non-number argument instead of emitting invalid IR | Surfaced by [ADR-00195](ADR-00195.md) (the pipeline-phase breakdown) |
| [00197](ADR-00197.md) | Reject numeric separators in legacy-octal / non-octal-decimal literals | Extends [ADR-00085](ADR-00085.md) (numeric separators); surfaced by [ADR-00195](ADR-00195.md)'s report breakdown |
| [00198](ADR-00198.md) | Static-string `eval` fast path — compile a constant expression in place, no embedded engine | Implements a subset of [TDD-00046](../tdd/TDD-00046.md) (the embedded-engine `eval`) ahead of and independent of the engine itself |
| [00199](ADR-00199.md) | Presence-flagged representation for nullable non-pointer scalars | `Implements [TDD-00064](../tdd/TDD-00064.md)` |
| [00200](ADR-00200.md) | Named functions as first-class values (via an env-dropping trampoline) | |
| [00201](ADR-00201.md) | Tuple types `[T0, T1, ...]` | `Implements [TDD-00066](../tdd/TDD-00066.md)` |
| [00202](ADR-00202.md) | Fix invalid SSA in the WebSocket frame-decode and client-scan runtime templates | `Extends [ADR-00125](ADR-00125.md), [ADR-00128](ADR-00128.md)` |
| [00203](ADR-00203.md) | String- and numeric-literal keys in object destructuring patterns | `Implements [TDD-00065](../tdd/TDD-00065.md)` (Stage 3a); `Extends [ADR-00192](ADR-00192.md), [ADR-00193](ADR-00193.md)` |
| [00204](ADR-00204.md) | Object rest `{ ...rest }` over a statically-known source shape | `Implements [TDD-00065](../tdd/TDD-00065.md)` (Stage 3b); `Extends [ADR-00193](ADR-00193.md), [ADR-00203](ADR-00203.md)` |
| [00205](ADR-00205.md) | One denominator for Strict Coverage — always the page's total feature count | `Extends [ADR-00166](ADR-00166.md)` |
| [00206](ADR-00206.md) | RegExp ECMAScript-dialect alignment (Options A + B) and the `-regex` mode selector | `Implements [TDD-00067](../tdd/TDD-00067.md)`. `Extends [ADR-00114](ADR-00114.md)` (RegExp construction / PCRE2 compile plumbing). |
| [00207](ADR-00207.md) | RegExp `es-utf16` index mode and the global empty-match advance | `Implements [TDD-00067](../tdd/TDD-00067.md)` (Stage 3). `Extends [ADR-00206](ADR-00206.md)` (Options A + B / the `-regex` mode selector). |
| [00208](ADR-00208.md) | RegExp `ecmascript` mode — the Option C source-normalization pass (v1) and the default advance | `Implements [TDD-00067](../tdd/TDD-00067.md)` (Stage 4 / Option C). `Extends [ADR-00206](ADR-00206.md)` (Options A + B / `-regex` selector), `[ADR-00207](ADR-00207.md)` (Stage 3 / `es-utf16`). |
| [00209](ADR-00209.md) | `const` requires an initializer; `eval`/`arguments` reserved as strict-mode binding names | `Extends [ADR-00181](ADR-00181.md), [ADR-00168](ADR-00168.md)` (both shipped the adjacent strict-mode/early-error parameter-name rules and disclosed the remaining binding-name gaps). |
| [00210](ADR-00210.md) | `let`/`const`/`var` scope semantics — function-scoped `var`, block-scoped redeclaration early-errors | `Implements [TDD-00070](../tdd/TDD-00070.md)`. `Extends [ADR-00209](ADR-00209.md)` (which added the `const`-initializer and strict binding-name early-errors this builds directly on). |
| [00211](ADR-00211.md) | cross-block `var`/lexical redeclaration early-error (`let x; { var x }`) | `Extends [ADR-00210](ADR-00210.md)` (which shipped block-scoped redeclaration checking but left this one intersection case out); `Implements [TDD-00070](../tdd/TDD-00070.md)` (closes the cross-nested-block item in its Open questions). |
| [00212](ADR-00212.md) | Temporal-dead-zone early error, and the block-shadowing bug behind it | `Implements [TDD-00071](../tdd/TDD-00071.md)` (Stage 1 of 2 — TDZ; definite-assignment is Stage 2). `Extends [ADR-00210](ADR-00210.md)` (the scoping work that left the TDZ caveat). |
| [00213](ADR-00213.md) | definite-assignment early error for a typed `var`/`let` | `Implements [TDD-00071](../tdd/TDD-00071.md)` (Stage 2 of 2 — completes it, alongside [ADR-00212](ADR-00212.md)'s Stage 1). `Extends [ADR-00210](ADR-00210.md)` (whose function-scoped `var` created the case this now catches). |
| [00214](ADR-00214.md) | definite-assignment precision for `do/while` and `switch` | `Extends [ADR-00213](ADR-00213.md)` (the definite-assignment pass this sharpens); `Implements [TDD-00071](../tdd/TDD-00071.md)`. |
| [00215](ADR-00215.md) | zero-initialize an uninitialized `let`/`const` slot (deterministic default for definite-assignment escapes) | `Extends [ADR-00210](ADR-00210.md)` (which zero-initialized `var` slots) and `[ADR-00214](ADR-00214.md)` (which surfaced this as the `let` escape). Related to [TDD-00071](../tdd/TDD-00071.md). |
| [00216](ADR-00216.md) | `bigint` V1 — arbitrary-precision integers behind a selectable backend | Implements [TDD-00074](../tdd/TDD-00074.md) |
| [00217](ADR-00217.md) | `-compat` compatibility axis (step 1) — absorb `-globals`, add bigint cross-type comparison | Implements [TDD-00075](../tdd/TDD-00075.md) (step 1); extends [ADR-00143](ADR-00143.md) (absorbs its `-globals` flag) and [ADR-00216](ADR-00216.md) (bigint) |
| [00218](ADR-00218.md) | Object-to-string — Node-style `console.log` inspection + `-compat`-gated `[object Object]` coercion | Implements [TDD-00075](../tdd/TDD-00075.md) (a further inhabitant); extends [ADR-00217](ADR-00217.md) |
| [00219](ADR-00219.md) | Fix a class-typed interface/type-alias field resolving to `i64` (placeholder-registration ordering) | Extends [ADR-00218](ADR-00218.md) (surfaced while testing object inspection) |
| [00220](ADR-00220.md) | `&&` / `\|\|` value-preserving under `-compat=js` | Implements [TDD-00075](../tdd/TDD-00075.md) (a further inhabitant); extends [ADR-00186](ADR-00186.md) (the bool form) and [ADR-00217](ADR-00217.md) (the `-compat` axis) |
| [00221](ADR-00221.md) | Bound the object inspector's recursion (fix compile-time infinite loop on recursive types) | Extends [ADR-00218](ADR-00218.md) (fixes a bug in its inspector) |
| [00222](ADR-00222.md) | `JSON.stringify` `space` pretty-printing and generic `toJSON()` (TDD-00077 Track S) | `Implements [TDD-00077](../tdd/TDD-00077.md)` (Track S) |
| [00223](ADR-00223.md) | A validating JSON parse-tree (P1) — `JSON.parse` throws `SyntaxError` on malformed input | `Implements [TDD-00077](../tdd/TDD-00077.md)` (Track P, P1); `Extends [ADR-00189](ADR-00189.md)`, `[ADR-00007](ADR-00007.md)` |
| [00224](ADR-00224.md) | Type-directed JSON projection off the parse tree (P3) — nested objects, array fields, top-level `T[]` | `Implements [TDD-00077](../tdd/TDD-00077.md)` (Track P, P3); `Extends [ADR-00223](ADR-00223.md)`; `Supersedes [ADR-00007](ADR-00007.md)`, `[ADR-00189](ADR-00189.md)`, `[ADR-00166](ADR-00166.md)` |
| [00225](ADR-00225.md) | Intersection types (`A & B`) via field-merge into one struct | `Implements [TDD-00078](../tdd/TDD-00078.md)` |
| [00226](ADR-00226.md) | Fix exponential-time `inferExprType` on deep binary expressions | |
| [00227](ADR-00227.md) | User-facing output — drop doc references, wrap `--help` | |
| [00228](ADR-00228.md) | Built-in utility types, stage 1a — Partial/Required/Readonly/NonNullable | `Implements [TDD-00079](../tdd/TDD-00079.md)` (Stage 1a) |
| [00229](ADR-00229.md) | Utility types, stage 1b — Pick/Omit/Record + string-literal types | `Implements [TDD-00079](../tdd/TDD-00079.md)` (Stage 1b); `Extends [ADR-00228](ADR-00228.md)` |
| [00230](ADR-00230.md) | General mapped types — keyof, indexed access, `{ [K in …]: V }` | `Implements [TDD-00079](../tdd/TDD-00079.md)` (Stage 2); `Extends [ADR-00229](ADR-00229.md)` |
| [00231](ADR-00231.md) | Conditional types + infer + generic type aliases + structural assignability | `Implements [TDD-00079](../tdd/TDD-00079.md)` (Stage 3); `Extends [ADR-00230](ADR-00230.md)` |
| [00232](ADR-00232.md) | Type-system caveat reductions — tuple `.length` + element assignment; `A \| B \| null`; intersection same-name object fields | `Extends [ADR-00201](ADR-00201.md)`, `[ADR-00225](ADR-00225.md)` |
| [00233](ADR-00233.md) | JS-faithful float-to-string (shortest round-trip) | `Implements [TDD-00080](../tdd/TDD-00080.md)`; `Supersedes [ADR-00166](ADR-00166.md)` (the `%g` float-print behavior) |
| [00234](ADR-00234.md) | Event / CustomEvent objects (Events & Cancellation, stage 1) | `Implements [TDD-00081](../tdd/TDD-00081.md)` (Stage 1) |
| [00235](ADR-00235.md) | EventTarget bus (Events & Cancellation, stage 2) | `Implements [TDD-00081](../tdd/TDD-00081.md)` (Stage 2); `Extends [ADR-00234](ADR-00234.md)` |
| [00236](ADR-00236.md) | AbortController / AbortSignal (Events & Cancellation, stage 3a) | `Implements [TDD-00081](../tdd/TDD-00081.md)` (Stage 3); `Extends [ADR-00235](ADR-00235.md)` |
| [00237](ADR-00237.md) | Wire AbortSignal into fetch (Events & Cancellation, stage 3b) | `Implements [TDD-00081](../tdd/TDD-00081.md)` (Stage 3b); `Extends [ADR-00236](ADR-00236.md)`, `[ADR-00050](ADR-00050.md)` |
| [00238](ADR-00238.md) | AbortSignal.timeout + mid-flight fetch cancellation (stage 3c) | `Implements [TDD-00081](../tdd/TDD-00081.md)` (Stage 3c); `Extends [ADR-00237](ADR-00237.md)` |
| [00239](ADR-00239.md) | Fold fetch AbortSignal.timeout deadlines into the event-loop select() | `Implements [TDD-00081](../tdd/TDD-00081.md)` (Stage 3c); `Extends [ADR-00238](ADR-00238.md)` |
| [00240](ADR-00240.md) | Abort/timeout errors are DOMException; `instanceof DOMException` + `new DOMException` | `Implements [TDD-00081](../tdd/TDD-00081.md)`; `Extends [ADR-00238](ADR-00238.md)`, `[ADR-00239](ADR-00239.md)`; builds on [ADR-00013](ADR-00013.md) (Error-kind enum, [TDD-00013](../tdd/TDD-00013.md) Option A) |
| [00241](ADR-00241.md) | `await` of a non-thenable is identity — fixes the Response body-accessor crash | Fixes a 2026-08-11-audit bug ([ADR-00166](ADR-00166.md)); touches `Response.text()`/`.json()`/`.arrayBuffer()` ([ADR-00021](ADR-00021.md), [ADR-00094](ADR-00094.md)) and the JSON projection ([ADR-00224](ADR-00224.md)) |
| [00242](ADR-00242.md) | Deterministic correctly-rounded `Math.cbrt` (fdlibm), not platform libm | Fixes a cross-platform CI failure; sibling of the other Math builtins ([ADR-00166](ADR-00166.md) audit context) |
| [00243](ADR-00243.md) | CI robustness — link libpcre2 explicitly, de-flake performance.mark overwrite test | Surfaced while replicating [ADR-00242](ADR-00242.md)'s cbrt CI failure in Docker |
| [00244](ADR-00244.md) | True async-function suspension (coroutine tasks) + concurrent combinators | `Implements [TDD-00083](../tdd/TDD-00083.md)` (Stage 2); builds on [ADR-00050](ADR-00050.md) (non-blocking fetch), [ADR-00073](ADR-00073.md) (combinators over fetch), [TDD-00006](../tdd/TDD-00006.md) (fibers/event loop) |
| [00245](ADR-00245.md) | Microtasks — queueMicrotask + Promise.prototype.then/.catch/.finally | `Implements [TDD-00083](../tdd/TDD-00083.md)` (Stage 3); builds on [ADR-00244](ADR-00244.md) (coroutine tasks + rejection) |
| [00246](ADR-00246.md) | Presence-aware logical compound assignment on a nullable-scalar field | `Implements [TDD-00064](../tdd/TDD-00064.md)` (closes the [ADR-00199](ADR-00199.md) "Side effects discovered" remainder) |
| [00247](ADR-00247.md) | A string enum's name resolves to its string backing type as an annotation | none (fixes a [ADR-00166](ADR-00166.md) 2026-08-11-audit find) |
| [00248](ADR-00248.md) | Promise.any skip-rejected + AggregateError, and .then/.catch/.finally value-chaining | `Implements [TDD-00083](../tdd/TDD-00083.md)` (Stage 3 follow-on); builds on [ADR-00244](ADR-00244.md), [ADR-00245](ADR-00245.md) |
| [00249](ADR-00249.md) | Every async function returns a real promise — inline catch-and-settle (TDD-00084 Part A) | `Implements [TDD-00084](../tdd/TDD-00084.md)` (Part A); builds on [ADR-00244](ADR-00244.md), [ADR-00248](ADR-00248.md) |
| [00250](ADR-00250.md) | One task-aware event loop — unifying the two program-exit loops (TDD-00084 Part B) | `Implements [TDD-00084](../tdd/TDD-00084.md)` (Part B); builds on [ADR-00249](ADR-00249.md), [ADR-00244](ADR-00244.md), [TDD-00006](../tdd/TDD-00006.md) |
| [00251](ADR-00251.md) | Promise.any over raw fetches, and destructured/nullable params in a suspending fn (TDD-00084 Part C) | `Implements [TDD-00084](../tdd/TDD-00084.md)` (Part C, completing it); builds on [ADR-00248](ADR-00248.md), [ADR-00073](ADR-00073.md), [ADR-00199](ADR-00199.md) |
| [00252](ADR-00252.md) | Re-resolve an assigned variable's storage after the RHS runs (closure-capture promotion) | fixes a latent bug; complements [ADR-00001](ADR-00001.md) (closure-capture boxing) |
| [00253](ADR-00253.md) | Async generators (`async function*`) and `for await...of` | `Implements [TDD-00085](../tdd/TDD-00085.md)` (Stages 2–3); builds on [ADR-00172](ADR-00172.md) (sync generators), [ADR-00244](ADR-00244.md) (coroutine tasks), [ADR-00249](ADR-00249.md) (settled task promises) |
| [00254](ADR-00254.md) | Async generator methods (`async *m()`) | `Implements [TDD-00085](../tdd/TDD-00085.md)` (Stage 4, completing it); builds on [ADR-00253](ADR-00253.md) (async generators), [ADR-00182](ADR-00182.md) (sync generator methods) |
| [00255](ADR-00255.md) | A `.catch`/onRejected callback's error parameter is the error object | refines [ADR-00248](ADR-00248.md) (task-promise `.then`/`.catch`/`.finally` value-chaining); uses the arrow param-hint path from [ADR-00245](ADR-00245.md) |
| [00256](ADR-00256.md) | Rest parameters in a genuinely-suspending async function | refines [ADR-00249](ADR-00249.md) / [TDD-00084](../tdd/TDD-00084.md) Part C (nullable-scalar + destructured params through the task boundary); reuses the array-param bundle path |
| [00257](ADR-00257.md) | Destructuring loop variable in `for await...of` (and over sync generators), plus tuple/object `yield` | refines [ADR-00253](ADR-00253.md) (async generators / `for await`), [ADR-00192](ADR-00192.md) (for-of destructuring loop variable), [TDD-00085](../tdd/TDD-00085.md); reuses the `unpack*PatternInto` core from [ADR-00193](ADR-00193.md)/[TDD-00065](../tdd/TDD-00065.md) |
| [00258](ADR-00258.md) | `.then`/`.catch`/`.finally` directly on a raw fetch `Promise<Response>` | extends [ADR-00248](ADR-00248.md) (task-promise `.then`/`.catch`/`.finally` value-chaining); builds on the fetch-await path ([ADR-00095](ADR-00095.md)) and `buildResponseFromStatusBody`; superseded by [ADR-00280](ADR-00280.md) (the synchronous drive only — the reaction machinery stands) |
| [00259](ADR-00259.md) | Generator protocol completion — `.throw()`, `.return()`, `yield*` | `Implements [TDD-00086](../tdd/TDD-00086.md)`; builds on [ADR-00172](ADR-00172.md) (generator fiber), [ADR-00244](ADR-00244.md) (fiber-safe exceptions) |
| [00260](ADR-00260.md) | `.throw()` / `.return()` on async generators (unified generator swap) | extends [ADR-00259](ADR-00259.md) (sync generator protocol) to async generators; the async-generator scope cut [TDD-00086](../tdd/TDD-00086.md) named as a follow-on. Builds on [ADR-00253](ADR-00253.md) (async generators), [ADR-00244](ADR-00244.md) |
| [00261](ADR-00261.md) | `yield*` delegation over an async generator | extends [ADR-00259](ADR-00259.md) (sync `yield*`) and [ADR-00260](ADR-00260.md) (async `.throw()`/`.return()`), which scoped async `yield*` out. Part of [TDD-00086](../tdd/TDD-00086.md) |
| [00262](ADR-00262.md) | `Promise.resolve` / `Promise.reject`, and awaiting a `.then`/`.catch` chain | builds on [ADR-00248](ADR-00248.md) (task-promise `.then` chaining), [ADR-00249](ADR-00249.md) (task-promise struct); fixes a use-after-free in the lightweight await path |
| [00263](ADR-00263.md) | `new Promise((resolve, reject) => …)` — the executor constructor | `Implements [TDD-00087](../tdd/TDD-00087.md)`; builds on [ADR-00249](ADR-00249.md) (task promise struct), [ADR-00248](ADR-00248.md) (`.then` reactions), [ADR-00262](ADR-00262.md) (`Promise.resolve`/`.reject`) |
| [00264](ADR-00264.md) | `Promise.reject` is `Promise<never>` (a bottom type) | refines [ADR-00262](ADR-00262.md) (`Promise.reject`); closes that row's only caveat |
| [00265](ADR-00265.md) | Unify async-method promises with async functions; declared `Promise<T>` await; async-return flattening | refines [ADR-00249](ADR-00249.md) (task-struct promise, [TDD-00084](../tdd/TDD-00084.md) Part A); closes the `await`-row and `new Promise`-row promise-representation caveats |
| [00266](ADR-00266.md) | Event-driven waiter list for `Promise.race`/`.any` over tasks; `new Promise<void>` | refines [ADR-00073](ADR-00073.md)/[TDD-00083](../tdd/TDD-00083.md) (combinators); closes the `.race` busy-poll caveat |
| [00267](ADR-00267.md) | `new Promise` executor may be a function expression or closure-typed value | refines [ADR-00263](ADR-00263.md) (the executor constructor) |
| [00268](ADR-00268.md) | Microtask-accurate `await` ordering — every `await` yields a tick | `Implements [TDD-00088](../tdd/TDD-00088.md)`; refines [ADR-00249](ADR-00249.md) ([TDD-00084](../tdd/TDD-00084.md) Part A's inline async fns), builds on [ADR-00248](ADR-00248.md) (reactions), the fiber scheduler |
| [00269](ADR-00269.md) | `Symbol.asyncIterator` — user-defined async iterables in `for await...of` | Implements [TDD-00089](../tdd/TDD-00089.md); extends [ADR-00253](ADR-00253.md), [ADR-00254](ADR-00254.md) (async generators / `for await...of`); builds on [ADR-00265](ADR-00265.md) (unified async-method promises), [ADR-00268](ADR-00268.md) (`await` await machinery); extended by [ADR-00278](ADR-00278.md), [ADR-00279](ADR-00279.md) |
| [00270](ADR-00270.md) | A Promise is a reusable value — `await`/combinators stop consuming the slot | Implements [TDD-00090](../tdd/TDD-00090.md); refines [ADR-00249](ADR-00249.md), [ADR-00265](ADR-00265.md) (task-promise model), [ADR-00073](ADR-00073.md) (combinators); corrects the strict claim on the `await` row; extended by [ADR-00277](ADR-00277.md), [ADR-00280](ADR-00280.md) |
| [00271](ADR-00271.md) | Thenable adoption — `resolve(aPromise)` in a `new Promise` executor | Implements [TDD-00091](../tdd/TDD-00091.md); extends [ADR-00263](ADR-00263.md), [ADR-00267](ADR-00267.md) (the executor constructor); reuses [ADR-00248](ADR-00248.md)'s reaction/microtask machinery |
| [00272](ADR-00272.md) | `for await...of` over a sync array; generic-type array-suffix parse fix | Implements [TDD-00092](../tdd/TDD-00092.md); extends [ADR-00253](ADR-00253.md), [ADR-00269](ADR-00269.md) (`for await` paths); extended by [ADR-00277](ADR-00277.md) |
| [00273](ADR-00273.md) | Module-level variables — a named `function` can read a top-level `const`/`let` | Implements [TDD-00093](../tdd/TDD-00093.md); relates to [ADR-00001](ADR-00001.md) (closure capture/boxing) |
| [00274](ADR-00274.md) | Nested generators (by-reference capture), async `yield*` over an async iterable, and the nested-function resolver fix | Implements [TDD-00094](../tdd/TDD-00094.md) stages 1–3; extends [ADR-00253](ADR-00253.md), [ADR-00259](ADR-00259.md)–[ADR-00261](ADR-00261.md) (generators); reuses [ADR-00001](ADR-00001.md) (closure capture) and [ADR-00269](ADR-00269.md) (`Symbol.asyncIterator`) |
| [00275](ADR-00275.md) | Async generator `.next()` is a genuinely-pending promise (deferred body, microtask-ordered) — the settle-without-drain deadlock, fixed; plus a `yield*`-async-iterable `.throw`/`.return` swallow fix | Completes [TDD-00094](../tdd/TDD-00094.md) stage 4; supersedes [ADR-00274](ADR-00274.md)'s reverted deferral; builds on the microtask FIFO ([TDD-00088](../tdd/TDD-00088.md)) and the async-generator fiber/settle machinery ([ADR-00260](ADR-00260.md)/[TDD-00086](../tdd/TDD-00086.md)); premise corrected by [ADR-00281](ADR-00281.md), fixed by [ADR-00283](ADR-00283.md) (node/V8 runs the body synchronously to the first await/yield — the deferral this ADR shipped diverges; the settle-through-__kml_promise_settle fix stands) |
| [00276](ADR-00276.md) | `yield*` delegates `.throw`/`.return` into a user async-iterable's optional methods — plus reserved words as class member names | Closes the last caveat of [TDD-00094](../tdd/TDD-00094.md) (async-generators row → strict); completes the `yield*`-async-iterable path of [ADR-00274](ADR-00274.md)/[ADR-00275](ADR-00275.md) |
| [00277](ADR-00277.md) | `for await...of` over sync generators, `Map`/`Set`, and arrays of raw fetches | Extends [ADR-00272](ADR-00272.md) (for-await over a sync array), [ADR-00270](ADR-00270.md) (Promise as a reusable value) |
| [00278](ADR-00278.md) | Sync `[Symbol.iterator]()` protocol for `for...of` and `for await...of` | Extends [ADR-00269](ADR-00269.md) (`[Symbol.asyncIterator]` classes), [ADR-00063](ADR-00063.md) (structural `next(): T \| null` for-of) |
| [00279](ADR-00279.md) | Object-literal `[Symbol.asyncIterator]` as a `for await` iterable | Extends [ADR-00269](ADR-00269.md) ([Symbol.asyncIterator] classes), [ADR-00277](ADR-00277.md) (for-await iterable coverage) |
| [00280](ADR-00280.md) | Deferred (non-blocking) drive for `.then` on a raw fetch `Promise<Response>` | Supersedes [ADR-00258](ADR-00258.md)'s synchronous drive (the reaction machinery it added stands); extends [ADR-00270](ADR-00270.md) (Promise as a reusable value) |
| [00281](ADR-00281.md) | Async ordering fixes from a conformance/node-diff sweep — non-promise `await` ticks, absent `.then` callbacks, `for await` suspension classification | Extends [ADR-00268](ADR-00268.md) (await-tick model / [TDD-00088](../tdd/TDD-00088.md)), [ADR-00248](ADR-00248.md) (`.then` reactions); corrects a premise recorded in [ADR-00275](ADR-00275.md) (see Side effects); extended by [ADR-00283](ADR-00283.md), which closes the disclosed async-generator divergence |
| [00282](ADR-00282.md) | `typeof` correctness cluster — built-in values are "object", namespaces/constructors/undeclared answer statically | Extends the compile-time `typeof` model (the Operators row); found by the same sweep as [ADR-00281](ADR-00281.md) |
| [00283](ADR-00283.md) | Spec-faithful async-generator step model — synchronous start, park-at-await, request queueing | Supersedes [ADR-00275](ADR-00275.md)'s deferred body start (its settle-through-`__kml_promise_settle` rule stands and is load-bearing here); closes the divergence disclosed in [ADR-00281](ADR-00281.md); extends [ADR-00253](ADR-00253.md)/[ADR-00254](ADR-00254.md) (the generator-fiber/await fusion) |
| [00284](ADR-00284.md) | Array mutators accept any mutable receiver — object/class fields and nested-array elements | Extends [ADR-00056](ADR-00056.md) (splice clamping) and [ADR-00061](ADR-00061.md) (why a mutator needs a length *storage location*, not just a length value) |
| [00285](ADR-00285.md) | `console.log` prints Node-faithfully — space-joined arguments, bare-newline no-arg call, unprefixed `warn`, `-0` display | Extends [ADR-00183](ADR-00183.md) (the "deferred shortcuts get fixed on sight" precedent — boolean printing) and [TDD-00080](../tdd/TDD-00080.md)'s float formatter |
| [00286](ADR-00286.md) | Math NaN/±Infinity correctness — float-preserving `floor`/`ceil`/`round`/`trunc`/`sign`, NaN-propagating `min`/`max`, JS `round` ties | Extends [ADR-00187](ADR-00187.md) (integer-vs-float operator split); same audit source as [ADR-00284](ADR-00284.md)/[ADR-00285](ADR-00285.md) |
| [00287](ADR-00287.md) | `parseInt`/`parseFloat` return a double and produce real `NaN`; `charCodeAt`/`codePointAt` bounds-checked with `NaN` out of range | Same 2026-08-11 audit cluster as [ADR-00284](ADR-00284.md)–[ADR-00286](ADR-00286.md); charCodeAt's byte-space semantics per [ADR-00166](ADR-00166.md) unchanged |
| [00288](ADR-00288.md) | Blocked-by histogram in the conformance runner | Extends [TDD-00082](../tdd/TDD-00082.md)'s runner ([TDD-00008](../tdd/TDD-00008.md) Design V2 report format) |
| [00289](ADR-00289.md) | Built-in error constructors as first-class values (boxed funcrefs) | Extends the `any` box ([TDD-00062](../tdd/TDD-00062.md)) with a new tag; picked from the [ADR-00288](ADR-00288.md) blocked-by histogram (`TypeError` 1042 + `ReferenceError` 335 + `SyntaxError` 258 files gated) |
| [00290](ADR-00290.md) | TS `namespace` declarations with function merging | Implements [TDD-00095](../tdd/TDD-00095.md); unblocks the Test262 harness shim's bare-`assert` form (631 files gated per the [ADR-00288](ADR-00288.md) histogram) |
| [00291](ADR-00291.md) | `String(x)` / `Number(x)` / `Boolean(x)` conversion calls | Picked from the [ADR-00288](ADR-00288.md) histogram (`String` 453 + `Number` 341 + `Boolean` 274 files gated); `Number`'s string parse shares [ADR-00287](ADR-00287.md)'s NaN conventions |
| [00292](ADR-00292.md) | Mixed int/float arithmetic promotes to double | Extends [ADR-00187](ADR-00187.md)'s integer-vs-float operator split; found while testing [ADR-00293](ADR-00293.md)'s yield inference |
| [00293](ADR-00293.md) | Generator expressions (bound form) + yield-based element-type inference | Implements [TDD-00096](../tdd/TDD-00096.md); extends [ADR-00171](ADR-00171.md)–[ADR-00173](ADR-00173.md) (generator machinery); picked from the [ADR-00288](ADR-00288.md) histogram (the `expected (, got *` 2,086-file cluster and the 662-file annotation-requirement bucket) |
| [00294](ADR-00294.md) | `DataView` over `ArrayBuffer` | Extends [TDD-00018](../tdd/TDD-00018.md)'s ArrayBuffer/TypedArray family; picked from the [ADR-00288](ADR-00288.md) histogram (264 files gated) and the roadmap's Binary/`Blob` enabler tier |
| [00295](ADR-00295.md) | Near-miss alignment batch — Unicode `trim`, JS `pow` edge, oversized int literals, DataView float offsets, SameValue NaN in the shim | Driven by [ADR-00288](ADR-00288.md)'s near-miss bucket (RUNTIME_NONZERO_EXIT — files that compiled and ran but diverged: each one is either a fix or a missing status-page caveat); touches [ADR-00294](ADR-00294.md) (DataView) and [ADR-00292](ADR-00292.md)'s numeric family |
| [00296](ADR-00296.md) | `ReadableStream<T>` core — queue/high-water-mark/pull state machine, readers, strategies, `from()`, `for await` | Implements [TDD-00097](../tdd/TDD-00097.md) Stage 1; builds on [ADR-00263](ADR-00263.md) (promise settle), [ADR-00268](ADR-00268.md) (microtask-accurate await), [ADR-00270](ADR-00270.md) (reusable promises) |
| [00297](ADR-00297.md) | `WritableStream<T>` — writer lock, serialized sink writes, `ready` backpressure | Implements [TDD-00097](../tdd/TDD-00097.md) Stage 2; extends [ADR-00296](ADR-00296.md) |
| [00298](ADR-00298.md) | `pipeTo`/`pipeThrough`, `TransformStream<I, O>`, `tee()` — the reaction-driven pipeline | Implements [TDD-00097](../tdd/TDD-00097.md) Stage 3; extends [ADR-00296](ADR-00296.md), [ADR-00297](ADR-00297.md) |
| [00299](ADR-00299.md) | fetch `Response.body` streaming — resolve-at-headers, curl pause/unpause | Implements [TDD-00097](../tdd/TDD-00097.md) Stage 4; extends [ADR-00296](ADR-00296.md)–[ADR-00298](ADR-00298.md); reshapes [ADR-00050](ADR-00050.md)'s await point and [ADR-00280](ADR-00280.md)'s `.then` bridge |
| [00300](ADR-00300.md) | Chunked HTTP responses from a ReadableStream body | Implements [TDD-00097](../tdd/TDD-00097.md) Stage 5 (response side); extends [ADR-00296](ADR-00296.md)–[ADR-00299](ADR-00299.md) |
| [00301](ADR-00301.md) | Streaming http request bodies — `req.stream()`, headers-complete dispatch | Implements [TDD-00097](../tdd/TDD-00097.md) Stage 5b; extends [ADR-00296](ADR-00296.md)–[ADR-00300](ADR-00300.md) |
| [00302](ADR-00302.md) | `CompressionStream` / `DecompressionStream` over zlib | Implements [TDD-00097](../tdd/TDD-00097.md) Stage 6; extends [ADR-00298](ADR-00298.md) |
| [00303](ADR-00303.md) | EventEmitter event maps + `instanceof` for built-in emitters/streams | Implements [TDD-00097](../tdd/TDD-00097.md) Stage 7; extends [TDD-00023](../tdd/TDD-00023.md)/[ADR-00089](ADR-00089.md) (EventEmitter), [ADR-00162](ADR-00162.md) (built-in `instanceof`) |
| [00304](ADR-00304.md) | Node's `stream` module — Readable/Writable/Transform, events, `.pipe()`, `stream/promises` | Implements [TDD-00097](../tdd/TDD-00097.md) Stage 8 (the final stage); extends [ADR-00296](ADR-00296.md)–[ADR-00303](ADR-00303.md) |
| [00305](ADR-00305.md) | Worker threads — spawn/join + typed copy-only postMessage (manual mode) | Implements [TDD-00098](../tdd/TDD-00098.md) (stages 1–3); Extended by [ADR-00306](ADR-00306.md) |
| [00306](ADR-00306.md) | Worker threads — `-mm=gc` support, error/termination semantics, browser surface | Implements [TDD-00098](../tdd/TDD-00098.md) (stages 4–6); Extends [ADR-00305](ADR-00305.md) |
| [00307](ADR-00307.md) | Event-loop lost-wakeup deadlocks — resumable-task check, conn poke, curl deadline fold | |
| [00308](ADR-00308.md) | `SharedArrayBuffer` + `Atomics` — shared memory across worker threads | Implements [TDD-00099](../tdd/TDD-00099.md); extends [ADR-00305](ADR-00305.md) (Worker), [ADR-00078](ADR-00078.md) (ArrayBuffer) |
| [00309](ADR-00309.md) | `BroadcastChannel` + `MessageChannel`/`MessagePort` | Implements [TDD-00099](../tdd/TDD-00099.md); extends [ADR-00305](ADR-00305.md), [ADR-00306](ADR-00306.md) (Worker channels) |
| [00310](ADR-00310.md) | Free-variable scanner missed `new TypedArray/ArrayBuffer/DataView(...)` arguments | Extends [ADR-00104](ADR-00104.md) (closure captures); found during [ADR-00308](ADR-00308.md) |
| [00311](ADR-00311.md) | URLPattern — route matching over compiled per-component regexes | Implements [TDD-00100](../tdd/TDD-00100.md) |
| [00312](ADR-00312.md) | Binary-data caveat batch — `Atomics.isLockFree`, `ArrayBuffer`/`SharedArrayBuffer.slice`, 3-argument TypedArray views, DataView BigInt accessors | Extends [ADR-00078](ADR-00078.md), [ADR-00294](ADR-00294.md), [ADR-00308](ADR-00308.md) |
| [00313](ADR-00313.md) | `BigInt64Array`/`BigUint64Array` and `Uint8ClampedArray` — the TypedArray store/load conversion layer | Implements [TDD-00101](../tdd/TDD-00101.md); extends [ADR-00078](ADR-00078.md), [ADR-00308](ADR-00308.md) |
| [00314](ADR-00314.md) | `Blob` — immutable binary data with a MIME type | Implements [TDD-00102](../tdd/TDD-00102.md) |
| [00315](ADR-00315.md) | Node `Buffer` — a flagged Uint8Array with codecs and binary accessors | Implements [TDD-00103](../tdd/TDD-00103.md); supersedes [ADR-00094](ADR-00094.md)'s "no Buffer class" half (its WHATWG fetch/fs return surface stands); extends [ADR-00078](ADR-00078.md), [ADR-00294](ADR-00294.md) |
| [00316](ADR-00316.md) | `-crypto` backend flag, `__kml_crypto_*` subtle ABI, and `crypto.subtle.digest` | Implements [TDD-00104](../tdd/TDD-00104.md) (Phase 1); extends [ADR-00024](ADR-00024.md)'s crypto namespace |
| [00317](ADR-00317.md) | `crypto.getRandomValues` accepts real TypedArrays and ArrayBuffers | Implements [TDD-00104](../tdd/TDD-00104.md) (Phase 1); extends [ADR-00024](ADR-00024.md); closes the migration deferred in [ADR-00078](ADR-00078.md) |
| [00318](ADR-00318.md) | CryptoKey model + symmetric `crypto.subtle` (HMAC, AES-GCM/CBC, keys, JWK oct) | Implements [TDD-00104](../tdd/TDD-00104.md) (Phase 2); extends [ADR-00316](ADR-00316.md) |
| [00319](ADR-00319.md) | Asymmetric `crypto.subtle` — RSA-OAEP/RSA-PSS/ECDSA, CryptoKeyPair, pkcs8/spki/raw/JWK | Implements [TDD-00104](../tdd/TDD-00104.md) (Phase 3); extends [ADR-00316](ADR-00316.md), [ADR-00318](ADR-00318.md) |
| [00320](ADR-00320.md) | `crypto.subtle.deriveBits`/`deriveKey` — PBKDF2 and HKDF | Implements [TDD-00104](../tdd/TDD-00104.md) (Phase 4, completing it); extends [ADR-00316](ADR-00316.md), [ADR-00318](ADR-00318.md) |
| [00321](ADR-00321.md) | Node `zlib` module — one-shot compress/decompress family | Extends [ADR-00302](ADR-00302.md) (CompressionStream/DecompressionStream libz runtime); Implements [TDD-00049](../tdd/TDD-00049.md) (import-gated built-in modules, Stage 2 named members) |
| [00322](ADR-00322.md) | Async `child_process` — spawn / exec / execFile | Extends [ADR-00025](ADR-00025.md) (`process.execFileSync`), [TDD-00098](../tdd/TDD-00098.md) (Worker event-loop fd integration); builds on the EventEmitter dispatch posture of [TDD-00098](../tdd/TDD-00098.md)'s `Worker` |
| [00323](ADR-00323.md) | Interactive `readline` — createInterface, 'line', question, close | Extends [ADR-00322](ADR-00322.md) (child_process event-loop fd integration) |
| [00324](ADR-00324.md) | Node `net` TCP server | Implements [TDD-00006](../tdd/TDD-00006.md) (the select()-based event loop); Extends [ADR-00322](ADR-00322.md) (child_process fd-folding precedent) |
| [00325](ADR-00325.md) | Node `util` — `util.inspect` / `util.format` | |
| [00326](ADR-00326.md) | Node `dns` — `dns.lookup` | |
| [00327](ADR-00327.md) | Node `dgram` — UDP sockets | Implements [TDD-00006](../tdd/TDD-00006.md) (the select()-based event loop); Extends [ADR-00324](ADR-00324.md) (shares the loop's hook-trio integration) |
| [00328](ADR-00328.md) | Node `net.connect` — TCP client sockets | Extends [ADR-00324](ADR-00324.md) (net TCP server — reuses its socket machinery) |
| [00329](ADR-00329.md) | Node `dns` extras — `resolve4` and `promises.lookup` | Extends [ADR-00326](ADR-00326.md) (dns.lookup — reuses its getaddrinfo helper) |
| [00330](ADR-00330.md) | Closure capture of a not-yet-initialized binding (self-reference in initializer) | Extends [ADR-00001](ADR-00001.md) (capture-time promotion / boxing); surfaced by [ADR-00328](ADR-00328.md) (net.connect) |
| [00331](ADR-00331.md) | Node `cluster` module — fork + re-exec worker model | Implements [TDD-00105](../tdd/TDD-00105.md); extends the http.listen({ workers }) fork machinery ([TDD-00025](../tdd/TDD-00025.md)) |
| [00332](ADR-00332.md) | process introspection + `process.nextTick` | |
| [00333](ADR-00333.md) | writable `process.env` | |
| [00334](ADR-00334.md) | process lifecycle — `on('exit'/'uncaughtException')` + `exitCode` | |
| [00335](ADR-00335.md) | Spread in call arguments (`f(...arr)`) | Implements [TDD-00106](../tdd/TDD-00106.md) |
| [00336](ADR-00336.md) | Deduplicate the getaddrinfo/freeaddrinfo externs | |
| [00337](ADR-00337.md) | Spread in call arguments V2 — multiple/mixed spreads into a rest parameter | Implements [TDD-00106](../tdd/TDD-00106.md) (V2); follows [ADR-00335](ADR-00335.md) (V1) |
| [00338](ADR-00338.md) | Spread into the variadic builtins `console.*` and `Math.min`/`Math.max` | Implements [TDD-00106](../tdd/TDD-00106.md) (V2 cont.); follows [ADR-00335](ADR-00335.md), [ADR-00337](ADR-00337.md) |
| [00339](ADR-00339.md) | Streaming `process.stdin` — the `'data'`/`'end'` events | Extends [ADR-00323](ADR-00323.md) (readline fd-0 event-loop integration), [ADR-00322](ADR-00322.md) (child_process fd integration) |
| [00340](ADR-00340.md) | Asynchronous `fs` — callback and Promise (`fs/promises`) forms | Implements [TDD-00107](../tdd/TDD-00107.md); reuses [ADR-00023](ADR-00023.md)/00027/00094 (sync fs), the settled-promise plumbing (dns.promises/crypto precedent), and the `setjmp` catch primitive |
| [00341](ADR-00341.md) | `Blob.stream()` — a ReadableStream over a blob's bytes | Extends [ADR-00314](ADR-00314.md) (Blob), [TDD-00097](../tdd/TDD-00097.md) (Streams) |
| [00342](ADR-00342.md) | Promote top-level class-instance / Blob / Date bindings to module globals | Extends [TDD-00093](../tdd/TDD-00093.md) (module-global promotion) |
| [00343](ADR-00343.md) | `fs.createReadStream` / `fs.createWriteStream` | Implements [TDD-00108](../tdd/TDD-00108.md); builds on [TDD-00097](../tdd/TDD-00097.md) (Streams), [TDD-00107](../tdd/TDD-00107.md) (async fs) |
| [00344](ADR-00344.md) | `tls` module — `tls.connect` (TLS client) | Implements [TDD-00109](../tdd/TDD-00109.md); builds on the net client ([ADR-00328](ADR-00328.md)) and the OpenSSL crypto backend ([TDD-00104](../tdd/TDD-00104.md)) |
| [00345](ADR-00345.md) | `tls.createServer` (TLS server) | Implements [TDD-00110](../tdd/TDD-00110.md); extends [ADR-00344](ADR-00344.md) (`tls.connect`) |
| [00346](ADR-00346.md) | `wss://` — the WebSocket client over TLS | Extends [TDD-00039](../tdd/TDD-00039.md) (WebSocket) Stage 4; reuses [ADR-00344](ADR-00344.md) (`tls` / libssl) |
| [00347](ADR-00347.md) | `new Map(entries)` — the `[K, V][]` initial-entries constructor overload | Implements [TDD-00066](../tdd/TDD-00066.md) (tuple type); extends [ADR-00159](ADR-00159.md) (`new Set(iterable)`) |
| [00348](ADR-00348.md) | `Object.fromEntries` — a dynamic object from a `[string, V][]` array | Implements [TDD-00012](../tdd/TDD-00012.md) (dynamic objects); extends [ADR-00347](ADR-00347.md) (`new Map(entries)`) |
| [00349](ADR-00349.md) | `WeakMap` / `WeakSet` / `WeakRef` — mode-dependent weak collections | Implements [TDD-00112](../tdd/TDD-00112.md) |
| [00350](ADR-00350.md) | Fix `-mm=gc` collection crash on class-instance churn (frozen-set is thread-local, unscanned by Boehm) | Extends [ADR-00055](ADR-00055.md) (`Object.freeze` frozen set); relates to [ADR-00071](ADR-00071.md) (GC allocator shim) |
| [00351](ADR-00351.md) | Fix Linux `-mm=gc` + `Worker` exit crash (GC shim `free()` on a foreign OpenSSL pointer) | Extends [ADR-00071](ADR-00071.md), [ADR-00100](ADR-00100.md) (GC allocator shim) |
| [00352](ADR-00352.md) | Object type arguments in generics (structural instantiation mangling) | Implements [TDD-00069](../tdd/TDD-00069.md); extends [TDD-00010](../tdd/TDD-00010.md), [TDD-00037](../tdd/TDD-00037.md) |
| [00353](ADR-00353.md) | Generic type-parameter constraints (`<T extends X>`) | Implements [TDD-00113](../tdd/TDD-00113.md); builds on [ADR-00352](ADR-00352.md) (object type arguments) |
| [00354](ADR-00354.md) | Flow-based narrowing of union types | Implements [TDD-00114](../tdd/TDD-00114.md); extends [TDD-00043](../tdd/TDD-00043.md) (union types), [TDD-00064](../tdd/TDD-00064.md) (nullable narrowing seam) |
| [00355](ADR-00355.md) | Object members in union types (usable via narrowing) | Implements [TDD-00115](../tdd/TDD-00115.md); extends [TDD-00043](../tdd/TDD-00043.md) (unions), [TDD-00114](../tdd/TDD-00114.md) (narrowing); closes [TDD-00065](../tdd/TDD-00065.md) Stage 3c's object-union case |
| [00356](ADR-00356.md) | Discriminated unions (tagged object unions) | Implements [TDD-00116](../tdd/TDD-00116.md); extends [TDD-00115](../tdd/TDD-00115.md) (object union members), [TDD-00114](../tdd/TDD-00114.md) (narrowing) |
| [00357](ADR-00357.md) | HTTP/2 server — h2c (cleartext) via nghttp2 | Implements [TDD-00111](../tdd/TDD-00111.md) Stage 3a; builds on the dispatcher refactor (`emitHTTPCallHandler`) and Stage 2 ALPN ([ADR-00345](ADR-00345.md) server ctx) |
| [00358](ADR-00358.md) | NUL-terminate the net dispatch 'data' chunk buffer | |
| [00359](ADR-00359.md) | Cross-worker `http.close()` via a shared mmap flag | Implements [TDD-00117](../tdd/TDD-00117.md). Extends [ADR-00097](ADR-00097.md) (clustering fork), [ADR-00102](ADR-00102.md) (`http.close()`). |
| [00360](ADR-00360.md) | `http.closeAllConnections()` via socket shutdown | Implements [TDD-00118](../tdd/TDD-00118.md). Extends [ADR-00359](ADR-00359.md) (shared cluster flag), [ADR-00102](ADR-00102.md) (`http.close()`). |
| [00361](ADR-00361.md) | Hide HttpRequest's internal fields from Object.keys/JSON | |
| [00362](ADR-00362.md) | `req.body` after `req.stream()` throws (body already disturbed) | Extends [ADR-00301](ADR-00301.md), [ADR-00307](ADR-00307.md) (streaming request body). Implements part of [TDD-00097](../tdd/TDD-00097.md) Stage 5b's fidelity. |
| [00363](ADR-00363.md) | Union response bodies (`string \| ReadableStream`) for `http.listen` | Implements [TDD-00119](../tdd/TDD-00119.md). Extends [ADR-00300](ADR-00300.md) (streaming response body), builds on [TDD-00043](../tdd/TDD-00043.md)/[TDD-00115](../tdd/TDD-00115.md) (constrained unions). |
| [00364](ADR-00364.md) | Binary-safe string consumers — length/compare/search switch | `Implements [TDD-00120](../tdd/TDD-00120.md)` |
| [00365](ADR-00365.md) | In-scope Test262 subset counter beside the raw full-corpus number | `Implements [TDD-00121](../tdd/TDD-00121.md)` |
| [00366](ADR-00366.md) | Node-core conformance track — full test/parallel corpus runner | `Implements [TDD-00121](../tdd/TDD-00121.md)` |
| [00367](ADR-00367.md) | TypeScript acceptance oracle — front-end accept/reject vs baselines | `Implements [TDD-00121](../tdd/TDD-00121.md)` |
| [00368](ADR-00368.md) | path.basename suffix strip is binary-safe (length header, not NUL) | `Extends [ADR-00364](ADR-00364.md)`, `Implements [TDD-00120](../tdd/TDD-00120.md)` |
| [00369](ADR-00369.md) | Static CommonJS `require('<literal>')` desugars to an ES import | `Extends [ADR-00135](ADR-00135.md)` |
| [00370](ADR-00370.md) | Native `test` builtin — `mustCall` family via a counting-closure trampoline | `Implements [TDD-00122](../tdd/TDD-00122.md)` |
| [00371](ADR-00371.md) | TypeScript type assertions (`as T` / `as const` / `satisfies`) — parsed and erased | |
| [00372](ADR-00372.md) | Treat a UTF-8 BOM as whitespace; TS false-reject leverage map | Implements [TDD-00121](../tdd/TDD-00121.md) |
| [00373](ADR-00373.md) | Parse-and-erase three common TS constructs — `debugger`, `readonly T[]`, `this` parameter | Extends [ADR-00372](ADR-00372.md); Implements [TDD-00121](../tdd/TDD-00121.md) |
| [00374](ADR-00374.md) | Accept a class with bare (uninitialized) fields and no constructor | Extends [ADR-00157](ADR-00157.md); Implements [TDD-00063](../tdd/TDD-00063.md), [TDD-00121](../tdd/TDD-00121.md) |
| [00375](ADR-00375.md) | Static field initializers (`static x = expr`) | Extends [ADR-00374](ADR-00374.md); Implements [TDD-00063](../tdd/TDD-00063.md), [TDD-00121](../tdd/TDD-00121.md) |
| [00376](ADR-00376.md) | `++`/`--` on a member or index target | Extends [ADR-00375](ADR-00375.md) |
| [00377](ADR-00377.md) | `number` defaults to IEEE-754 double (TDD-00123 Stage 1) | Implements [TDD-00123](../tdd/TDD-00123.md) |
| [00378](ADR-00378.md) | Invalid-IR hardening + conformance-runner C-runtime linking | Extends [ADR-00377](ADR-00377.md); Implements [TDD-00123](../tdd/TDD-00123.md), [TDD-00082](../tdd/TDD-00082.md) |
| [00379](ADR-00379.md) | Bitwise/count results return `number`; exact int64 literals (TDD-00123 Stages 2–3, 5) | Extends [ADR-00377](ADR-00377.md); Implements [TDD-00123](../tdd/TDD-00123.md) |
| [00380](ADR-00380.md) | `@param`/`@returns` JSDoc typing for untyped functions | Implements [TDD-00125](../tdd/TDD-00125.md) |
| [00381](ADR-00381.md) | `@typedef`/`@callback` synthesized into type aliases (TDD-00125 Stage 2) | Extends [ADR-00380](ADR-00380.md); Implements [TDD-00125](../tdd/TDD-00125.md) |
| [00382](ADR-00382.md) | `@template` JSDoc generics (TDD-00125 Stage 3) | Extends [ADR-00380](ADR-00380.md); Implements [TDD-00125](../tdd/TDD-00125.md) |
| [00383](ADR-00383.md) | JSDoc type-expression sub-parser (TDD-00125 Stage 4) | Extends [ADR-00380](ADR-00380.md); Implements [TDD-00125](../tdd/TDD-00125.md) |
| [00384](ADR-00384.md) | JSDoc class/documentation tags + `import()` types (TDD-00125 Stages 5–6) | Extends [ADR-00380](ADR-00380.md), [ADR-00383](ADR-00383.md); Implements [TDD-00125](../tdd/TDD-00125.md) |
| [00385](ADR-00385.md) | Rest parameters in function type annotations | Extends [ADR-00166](ADR-00166.md) |
| [00386](ADR-00386.md) | Closure capture for nested function declarations (Stage 1) | Implements [TDD-00129](../tdd/TDD-00129.md); Extends [ADR-00149](ADR-00149.md) ([TDD-00057](../tdd/TDD-00057.md)), [ADR-00178](ADR-00178.md) |
| [00387](ADR-00387.md) | The `arguments` object, synthesized from parameters | |
| [00388](ADR-00388.md) | Ambient declarations (`declare`), parsed and erased | |
| [00389](ADR-00389.md) | `typeof` type queries | |
| [00390](ADR-00390.md) | Index signatures (`{ [k: string]: T }`), map-backed | Implements [TDD-00130](../tdd/TDD-00130.md); Extends ADR ([TDD-00012](../tdd/TDD-00012.md)'s dynamic object) |
| [00391](ADR-00391.md) | Node's `http.createServer` / `(req, res)` (TDD-00131 Stage 1) | Implements [TDD-00131](../tdd/TDD-00131.md) |
| [00392](ADR-00392.md) | Multi-argument EventEmitter events (TDD-00131 Stage 2) | Implements [TDD-00131](../tdd/TDD-00131.md); Extends [ADR-00089](ADR-00089.md) ([TDD-00023](../tdd/TDD-00023.md)) |
| [00393](ADR-00393.md) | Node streams as real classes (TDD-00132 Stages A–B) | Implements [TDD-00132](../tdd/TDD-00132.md); Extends [ADR-00089](ADR-00089.md) ([TDD-00023](../tdd/TDD-00023.md) EventEmitter synthetic root), [ADR-00304](ADR-00304.md) (options-form Node streams) |
| [00394](ADR-00394.md) | Scrub absolute paths from conformance report failure reasons | Extends the conformance runner ([TDD-00121](../tdd/TDD-00121.md)) |
| [00395](ADR-00395.md) | `process.execPath` and `process.emitWarning` (TDD-00131 process stage) | Implements [TDD-00131](../tdd/TDD-00131.md) (per-module Node API fidelity); Extends [ADR-00026](ADR-00026.md) (process introspection) |
| [00396](ADR-00396.md) | `process.version` / `process.versions` reporting (TDD-00136 decision) | Implements [TDD-00136](../tdd/TDD-00136.md); Extends [ADR-00395](ADR-00395.md) (process module), [ADR-00026](ADR-00026.md) |
| [00397](ADR-00397.md) | `net.Server.address()` and the `listen(0)` ephemeral-port idiom | Implements [TDD-00131](../tdd/TDD-00131.md) (per-module Node API fidelity); Extends [ADR-00324](ADR-00324.md), [ADR-00358](ADR-00358.md) (net runtime) |
| [00398](ADR-00398.md) | `Function.prototype.call` / `.apply` / `.bind` (TDD-00137) | Implements [TDD-00137](../tdd/TDD-00137.md) |
| [00399](ADR-00399.md) | `net` completion — `isIP` family, options-object connect, socket options | Implements [TDD-00131](../tdd/TDD-00131.md) (per-module Node API fidelity); Extends [ADR-00324](ADR-00324.md), [ADR-00358](ADR-00358.md), [ADR-00397](ADR-00397.md) (net runtime) |
| [00400](ADR-00400.md) | Accept any function-typed expression as a callback | Implements [TDD-00131](../tdd/TDD-00131.md) (per-module Node API fidelity — front-end enabler); Extends [ADR-00398](ADR-00398.md) |
| [00401](ADR-00401.md) | `assert.deepStrictEqual` and `process.on('warning')` | Implements [TDD-00131](../tdd/TDD-00131.md) (per-module Node API fidelity); Extends [ADR-00395](ADR-00395.md) (`process.emitWarning`) |
| [00402](ADR-00402.md) | Node conformance — resolve `require('../common/fixtures')` via a generated typed shim | Extends [TDD-00121](../tdd/TDD-00121.md) (Conformance V4, Node oracle) |
| [00403](ADR-00403.md) | Specific "no method" diagnostic instead of the "only simple function calls" catch-all | Extends [TDD-00072](../tdd/TDD-00072.md) (diagnostics), [TDD-00121](../tdd/TDD-00121.md) (conformance leverage map) |
| [00404](ADR-00404.md) | Node `http` client `http.get`/`http.request` (TDD-00138 Stage 1) | Implements [TDD-00138](../tdd/TDD-00138.md); Extends [ADR-00021](ADR-00021.md)/[ADR-00050](ADR-00050.md) (fetch/libcurl) |
| [00405](ADR-00405.md) | Async event-loop delivery for the `http` client (TDD-00138 Stage 2) | Implements [TDD-00138](../tdd/TDD-00138.md); Extends [ADR-00404](ADR-00404.md) (Stage 1), [ADR-00050](ADR-00050.md) (fetch/event loop) |
| [00406](ADR-00406.md) | Variable-bound http.createServer handle (listen/close/address) | Extends [ADR-00391](ADR-00391.md) ([TDD-00131](../tdd/TDD-00131.md) http.createServer), Extends [ADR-00027](ADR-00027.md)-era http.listen/http.close model |
| [00407](ADR-00407.md) | Node oracle — reclassify shimmable harness helpers as in scope | Extends [ADR-00402](ADR-00402.md), [TDD-00121](../tdd/TDD-00121.md) Track B |
| [00408](ADR-00408.md) | Qualified constructor & base-class parsing — `new mod.Class(...)`, `extends mod.Class` | Extends [ADR-00406](ADR-00406.md) |
| [00409](ADR-00409.md) | Node oracle — unimplemented core modules count as FAIL, not SKIP | Extends [ADR-00407](ADR-00407.md), [TDD-00121](../tdd/TDD-00121.md) |
| [00410](ADR-00410.md) | `https` module (client) and `stream/web` re-exports | Extends [TDD-00138](../tdd/TDD-00138.md) (http client), [TDD-00097](../tdd/TDD-00097.md) Stage 8 (streams); Extends [ADR-00409](ADR-00409.md) |
| [00411](ADR-00411.md) | child_process spawnSync/execSync/execFileSync (embedded C core) | Extends [ADR-00322](ADR-00322.md) (async child_process); Extends [ADR-00409](ADR-00409.md) |
| [00412](ADR-00412.md) | The Node test idiom end-to-end — wrapper typing, options-object client, post-loop flush | Extends [ADR-00406](ADR-00406.md), [TDD-00138](../tdd/TDD-00138.md), [TDD-00122](../tdd/TDD-00122.md) |
| [00413](ADR-00413.md) | tls.createServer options as a value; net.connect(port) arity | Extends [TDD-00110](../tdd/TDD-00110.md) (tls server), [ADR-00358](ADR-00358.md) (net client); Extends [ADR-00412](ADR-00412.md) |
| [00414](ADR-00414.md) | http2 module Stage 1 — resolution, server handle, compat API | Implements [TDD-00139](../tdd/TDD-00139.md) Stage 1; Extends [ADR-00406](ADR-00406.md), [ADR-00357](ADR-00357.md) |
| [00415](ADR-00415.md) | http2 Stage 2 — core streams API + Map bracket access | Implements [TDD-00139](../tdd/TDD-00139.md) Stage 2; Extends [ADR-00414](ADR-00414.md), [ADR-00357](ADR-00357.md) |
| [00416](ADR-00416.md) | http2 Stage 3 — client sessions (`http2.connect` / `session.request`) | Implements [TDD-00139](../tdd/TDD-00139.md) Stage 3; Extends [ADR-00415](ADR-00415.md), [ADR-00357](ADR-00357.md) |
| [00417](ADR-00417.md) | http2 Stage 4 — constants and the settings helpers | Implements [TDD-00139](../tdd/TDD-00139.md) Stage 4; Extends [ADR-00416](ADR-00416.md) |
| [00418](ADR-00418.md) | Node oracle — the "dynamic require" label was mostly wrong | Extends [ADR-00409](ADR-00409.md), [TDD-00121](../tdd/TDD-00121.md); relates to [ADR-00369](ADR-00369.md) (static CommonJS require) |
| [00419](ADR-00419.md) | The node:test runner — test/describe, TestContext, hooks | Implements [TDD-00140](../tdd/TDD-00140.md); Extends [TDD-00122](../tdd/TDD-00122.md) (test builtin), [ADR-00412](ADR-00412.md) |
| [00420](ADR-00420.md) | diagnostics_channel V1 — named pub/sub with string messages | Extends [ADR-00412](ADR-00412.md) (wrapper contextual typing) |
| [00421](ADR-00421.md) | Linux CI fixes — empty-header-block NUL, libm for frem, float fuzz oracle | Extends [ADR-00072](ADR-00072.md) (response headers), [TDD-00123](../tdd/TDD-00123.md) (float `number` semantics), [TDD-00014](../tdd/TDD-00014.md) (fuzz lanes) |
| [00422](ADR-00422.md) | stream named exports — PassThrough, callback pipeline/finished, duplexPair | Extends [ADR-00304](ADR-00304.md) (Node stream module), [ADR-00412](ADR-00412.md) (contextual typing through `test` wrappers); Implements part of [TDD-00131](../tdd/TDD-00131.md) |
| [00423](ADR-00423.md) | chained `createServer().listen()` binding + function-less `mustCall()` | Extends [ADR-00406](ADR-00406.md) (server handle), [ADR-00370](ADR-00370.md) (`mustCall`); Implements part of [TDD-00131](../tdd/TDD-00131.md) |
| [00424](ADR-00424.md) | `process.<stdio>.isTTY` + zero-param `createServer` listener | Extends [ADR-00423](ADR-00423.md) (histogram-driven idiom closures), [ADR-00406](ADR-00406.md) |
| [00425](ADR-00425.md) | child_process.fork — self-fork with a NODE_CHANNEL_FD IPC channel | Implements [TDD-00141](../tdd/TDD-00141.md); Extends [ADR-00423](ADR-00423.md), [ADR-00412](ADR-00412.md) |
| [00426](ADR-00426.md) | promote http server-handle bindings to module globals | Extends [ADR-00342](ADR-00342.md) (module-global promotion), [ADR-00423](ADR-00423.md) |
| [00427](ADR-00427.md) | cluster workers ride the fork IPC channel | Extends [ADR-00425](ADR-00425.md) (fork IPC), [ADR-00331](ADR-00331.md) (cluster re-exec); part of [TDD-00105](../tdd/TDD-00105.md)'s deferred messaging |
| [00428](ADR-00428.md) | assert.match/doesNotMatch, process.getuid family, honest marker diagnostics | Extends [ADR-00195](ADR-00195.md) (assert module), [ADR-00026](ADR-00026.md) (process POSIX reads) |
| [00429](ADR-00429.md) | http client — variable-bound options objects + the `agent` key | Extends [ADR-00412](ADR-00412.md) (options-object client) |
| [00430](ADR-00430.md) | the ClientRequest handle — request().end(), abort, response/error events | Extends [ADR-00429](ADR-00429.md), [ADR-00412](ADR-00412.md); part of [TDD-00138](../tdd/TDD-00138.md) |
| [00431](ADR-00431.md) | worker_threads re-exports MessageChannel/MessagePort/BroadcastChannel | Extends [ADR-00369](ADR-00369.md) ([TDD-00099](../tdd/TDD-00099.md) channels), member-gap item from [NODE-GAP-ANALYSIS](../testing/NODE-GAP-ANALYSIS.md) |
| [00432](ADR-00432.md) | `new http.Agent(...)` as an inert pool-config token | Extends [ADR-00429](ADR-00429.md), [ADR-00430](ADR-00430.md) |
| [00433](ADR-00433.md) | spawn options — cwd wired through, shell/stdio:'pipe' tolerated | Extends [ADR-00322](ADR-00322.md) (async child_process), [ADR-00411](ADR-00411.md) (the *Sync options) |
| [00434](ADR-00434.md) | the Node crypto module — generateKeyPair(Sync), randomBytes | Extends [TDD-00104](../tdd/TDD-00104.md)'s keygen ABI ([ADR-00317](ADR-00317.md) line), [ADR-00370](ADR-00370.md) (mustCall ABI) |
| [00435](ADR-00435.md) | `const { subtle } = globalThis.crypto` binding | Extends [ADR-00434](ADR-00434.md), [TDD-00104](../tdd/TDD-00104.md)'s subtle dispatch |
| [00436](ADR-00436.md) | klain:webview Stage 0 — C++ embedded-source plumbing + LocateWebview | Implements [TDD-00142](../tdd/TDD-00142.md); Extends [ADR-00411](ADR-00411.md), [ADR-00020](ADR-00020.md) |
| [00437](ADR-00437.md) | klain:webview Stage 1 — the Webview handle and window methods | Implements [TDD-00142](../tdd/TDD-00142.md); Extends [ADR-00436](ADR-00436.md), [ADR-00432](ADR-00432.md) |
| [00438](ADR-00438.md) | klain:webview Stage 2 — bind/unbind, page↔native IPC, served SPA | Implements [TDD-00142](../tdd/TDD-00142.md); Extends [ADR-00437](ADR-00437.md), [ADR-00425](ADR-00425.md) |
| [00439](ADR-00439.md) | klain:webview Stage 3 — loop fusion (page-tick pump) + async bind | Implements [TDD-00142](../tdd/TDD-00142.md); Extends [ADR-00438](ADR-00438.md), [ADR-00437](ADR-00437.md) |
| [00440](ADR-00440.md) | klain:webview Stage 4 — packaging (`.app` bundle / `.desktop` launcher) | Implements [TDD-00142](../tdd/TDD-00142.md); Extends [ADR-00437](ADR-00437.md) |
| [00441](ADR-00441.md) | klain:webview Stage 5 — typed bind + the `bindings` object | Implements [TDD-00142](../tdd/TDD-00142.md); Extends [ADR-00438](ADR-00438.md), [ADR-00439](ADR-00439.md), [ADR-00224](ADR-00224.md) |
| [00442](ADR-00442.md) | klain:webview Stage 6 — window `.d.ts`, async typed bind, nested-tuple params | Implements [TDD-00142](../tdd/TDD-00142.md); Extends [ADR-00441](ADR-00441.md), [ADR-00439](ADR-00439.md) |
| [00443](ADR-00443.md) | klain:webview Stage 7 — embed a SPA bundle into the executable | Implements [TDD-00142](../tdd/TDD-00142.md); Extends [ADR-00437](ADR-00437.md), [ADR-00411](ADR-00411.md) |
| [00444](ADR-00444.md) | docs/status flipped to a JSON source of truth with generated Markdown | Implements [TDD-00145](../tdd/TDD-00145.md) |
| [00445](ADR-00445.md) | statusgen importer retained as on-demand tooling (TDD-00145 Phase 5) | Implements [TDD-00145](../tdd/TDD-00145.md) Phase 5; Extends [ADR-00444](ADR-00444.md) |
| [00446](ADR-00446.md) | TS overload signatures parsed and erased | |
| [00447](ADR-00447.md) | constructor parameter properties | |
| [00448](ADR-00448.md) | object-type method signatures and bare call signatures | |
| [00449](ADR-00449.md) | Node stream constructors default to string chunks | Extends [ADR-00422](ADR-00422.md) |
| [00450](ADR-00450.md) | namespaces V2 — module synonym, non-exported/type members, sibling references | Implements [TDD-00148](../tdd/TDD-00148.md); Extends [ADR-00290](ADR-00290.md) ([TDD-00095](../tdd/TDD-00095.md) V1) |
| [00451](ADR-00451.md) | old-style angle-bracket type assertions `<T>expr` | Extends [ADR-00371](ADR-00371.md) |
| [00452](ADR-00452.md) | interface `extends` lists with field merging; constructor types erased | |
| [00453](ADR-00453.md) | namespaces V3 — nesting, dotted names, relative references | Implements [TDD-00148](../tdd/TDD-00148.md) (extends its shipped scope); Extends [ADR-00450](ADR-00450.md) |
| [00454](ADR-00454.md) | `var` exempted from definite-assignment (no TDZ) | Extends [ADR-00215](ADR-00215.md) ([TDD-00071](../tdd/TDD-00071.md)); Extends [ADR-00452](ADR-00452.md) (the generic depth cap it retunes) |
| [00455](ADR-00455.md) | callable interfaces (lone call/construct signature) | Extends [ADR-00448](ADR-00448.md) |
| [00456](ADR-00456.md) | import-equals namespace aliases (`import X = Y.Z`) | Extends [ADR-00453](ADR-00453.md) ([TDD-00148](../tdd/TDD-00148.md)) |
| [00457](ADR-00457.md) | Node-strict net.isIP / isIPv4 / isIPv6 | |
| [00458](ADR-00458.md) | atob throws InvalidCharacterError; typeof DOMException | |
| [00459](ADR-00459.md) | numeric-literal types; string-named enum members | Extends [ADR-00246](ADR-00246.md)-era [TDD-00079](../tdd/TDD-00079.md) literal types |
| [00460](ADR-00460.md) | arrow functions capture the enclosing method's lexical `this` | |
| [00461](ADR-00461.md) | number index signatures | Extends [TDD-00130](../tdd/TDD-00130.md)'s string index signatures |
| [00462](ADR-00462.md) | ambient namespace members; construct signatures in object types | Extends [ADR-00450](ADR-00450.md), [ADR-00448](ADR-00448.md) |
| [00463](ADR-00463.md) | N-ary array concat; zero-arg Array constructor | |
| [00464](ADR-00464.md) | `arguments` object in class method bodies | Extends [ADR-00387](ADR-00387.md) |
| [00465](ADR-00465.md) | URL component assignment is a clean rejection | |
| [00466](ADR-00466.md) | class + interface declaration merging (coexistence) | Extends [ADR-00452](ADR-00452.md) |
| [00467](ADR-00467.md) | array literal elisions | |
| [00468](ADR-00468.md) | namespace-body statements; `export declare` pass-through | Extends [ADR-00453](ADR-00453.md), [ADR-00462](ADR-00462.md) |
| [00469](ADR-00469.md) | generic function types erased (`<T>(x: T) => T`) | Extends [ADR-00371](ADR-00371.md)'s erasure family |
| [00470](ADR-00470.md) | qualified type references (`ns.Type`) | Extends [ADR-00450](ADR-00450.md), [ADR-00408](ADR-00408.md) |
| [00471](ADR-00471.md) | ambient value declarations become real bindings | Extends the ambient-erasure ADR behind `parseAmbientDeclaration` |
| [00472](ADR-00472.md) | conformance oracles measure under `-compat=js` shadowing | Extends [TDD-00121](../tdd/TDD-00121.md), [TDD-00075](../tdd/TDD-00075.md) |
| [00473](ADR-00473.md) | explicit call-site type arguments (`id<string>(x)`) | Extends [TDD-00010](../tdd/TDD-00010.md)'s monomorphization; Extends [ADR-00452](ADR-00452.md)'s depth cap |
| [00474](ADR-00474.md) | type predicates, bare class fields, ambient namespaces | Extends [ADR-00471](ADR-00471.md), [ADR-00450](ADR-00450.md); the field default follows [ADR-00042](ADR-00042.md)'s unannotated-parameter convention |
| [00475](ADR-00475.md) | uninitialized unions; generic-method rejection; any-return closure bug (deferred) | Extends [ADR-00454](ADR-00454.md), [ADR-00469](ADR-00469.md) |
| [00476](ADR-00476.md) | class index-sig erasure; comma statements; ambient enums | Extends [ADR-00461](ADR-00461.md), [ADR-00471](ADR-00471.md) |
| [00477](ADR-00477.md) | closure adapter trampolines (the any-boundary boxing fix) | Fixes the bug deferred in [ADR-00475](ADR-00475.md); Extends [ADR-00469](ADR-00469.md) |
| [00478](ADR-00478.md) | adapter aggregate coverage; any-boxed arrays keep their length | Completes [ADR-00477](ADR-00477.md); Extends [ADR-00375](ADR-00375.md)-era boxing ([TDD-00062](../tdd/TDD-00062.md)) and [TDD-00064](../tdd/TDD-00064.md)'s nullable scalars |
| [00479](ADR-00479.md) | interface merging; overloaded call signatures; void-init binds undefined | Extends [ADR-00466](ADR-00466.md), [ADR-00455](ADR-00455.md) |
| [00480](ADR-00480.md) | caveat clearing — readonly fields, enum brackets, namespace type-member chains | Extends [ADR-00447](ADR-00447.md), [ADR-00450](ADR-00450.md)/[ADR-00453](ADR-00453.md) |
| [00481](ADR-00481.md) | `for (const [k, v] of map)` decomposes entries | Clears [ADR-00011](ADR-00011.md)'s values-only caveat |
| [00482](ADR-00482.md) | Array.from iterables; JSON.stringify of map-backed dicts | Clears [TDD-00130](../tdd/TDD-00130.md)'s stringify deferral; extends [ADR-00107](ADR-00107.md)-era Array.from |
| [00483](ADR-00483.md) | Node stream destroy() and setEncoding() | Extends [ADR-00449](ADR-00449.md); clears part of the streams "No `.read()`/`.unshift()`/`.destroy()`/`.setEncoding()`" caveat |
| [00484](ADR-00484.md) | synchronous Readable.read() | Completes the [ADR-00483](ADR-00483.md) stream-method caveat sweep |
| [00485](ADR-00485.md) | Readable.unshift(); Record<string, V> bracket parity | Completes [ADR-00484](ADR-00484.md)'s stream-method sweep; clears [TDD-00130](../tdd/TDD-00130.md)'s last deferral |
| [00486](ADR-00486.md) | HTTP status reason phrases | |
| [00487](ADR-00487.md) | delete operator; fs.mkdirSync recursive | |
| [00488](ADR-00488.md) | Date multi-argument setters; Symbol.for registry | Extends [TDD-00044](../tdd/TDD-00044.md) (symbols) and the Date model |
| [00489](ADR-00489.md) | Blob parts from a string[] variable | |
| [00490](ADR-00490.md) | Response headers + XHR getResponseHeader/getAllResponseHeaders | |
| [00491](ADR-00491.md) | Array.from mapFn argument | |
| [00492](ADR-00492.md) | Typed Object.values/entries for homogeneous shapes | |
| [00493](ADR-00493.md) | new Duplex(options) | |
| [00494](ADR-00494.md) | Growable SharedArrayBuffer / resizable ArrayBuffer | |
| [00495](ADR-00495.md) | fs.statSync | |
| [00496](ADR-00496.md) | mustCall for any fixed callback signature | |
| [00497](ADR-00497.md) | Path-based fs sync ops batch | |
| [00498](ADR-00498.md) | fd-based fs ops (openSync/closeSync/readSync/writeSync/fstatSync) | |
| [00499](ADR-00499.md) | assert.ifError + assert.doesNotThrow | |
| [00500](ADR-00500.md) | http.get/request method + headers options | |
| [00501](ADR-00501.md) | net socket 'close' + 'connect'/'ready' listeners | |
| [00502](ADR-00502.md) | http.Server 'listening' event | |
| [00503](ADR-00503.md) | createServer({requireHostHeader: false}) | |
| [00504](ADR-00504.md) | HTTP/2 over TLS server — `http2.createSecureServer` | `Implements [TDD-00111](../tdd/TDD-00111.md)` (Stage 3b), `[TDD-00139](../tdd/TDD-00139.md)` (`createSecureServer`); `Extends [ADR-00357](ADR-00357.md)` (h2c driver), `[ADR-00344](ADR-00344.md)`/`[ADR-00345](ADR-00345.md)` (TLS server CTX/ALPN) |
| [00505](ADR-00505.md) | HTTPS/1.1 server — `https.createServer` + `allowHTTP1` | `Implements [TDD-00111](../tdd/TDD-00111.md)` (the residual 1.1-over-TLS items); `Extends [ADR-00504](ADR-00504.md)` (h2-over-TLS accept branch), `[ADR-00344](ADR-00344.md)`/`[ADR-00345](ADR-00345.md)` (TLS server CTX/ALPN), `[ADR-00346](ADR-00346.md)` (wss fd→SSL registry pattern) |
| [00506](ADR-00506.md) | fetch header-buffer segfault on Linux (out-of-bounds slot-3 read) | fixes a crash in the `fetch` runtime (`runtime_fetch.go`); relates to [ADR-00490](ADR-00490.md) (header capture), [ADR-00050](ADR-00050.md) (async fetch buffer) |
| [00507](ADR-00507.md) | process.memoryUsage() | |
| [00508](ADR-00508.md) | globalThis as an alias-peeling desugar | |
| [00509](ADR-00509.md) | Generate the ADR & TDD index READMEs and the status backlog from the record files | Implements [TDD-00149](../tdd/TDD-00149.md) |
| [00510](ADR-00510.md) | `class X extends Duplex` (Node stream subclassing, Stage C1) | `Implements [TDD-00132](../tdd/TDD-00132.md)` |
| [00511](ADR-00511.md) | `class X extends Transform` (Node stream subclassing, Stage C2) | `Implements [TDD-00132](../tdd/TDD-00132.md)`, `Extends [ADR-00510](ADR-00510.md)` |
| [00512](ADR-00512.md) | TypedArray construction as a general expression | `Implements [TDD-00018](../tdd/TDD-00018.md)`, `Extends [ADR-00104](ADR-00104.md)` |
| [00513](ADR-00513.md) | `res.statusCode` getter/setter on Node's `ServerResponse` | `Implements [TDD-00131](../tdd/TDD-00131.md)` |
| [00514](ADR-00514.md) | Non-blocking `server.listen` + the buffered-sink Writable surface on `res` | `Implements [TDD-00131](../tdd/TDD-00131.md)`, `Extends [ADR-00513](ADR-00513.md)`, `[ADR-00391](ADR-00391.md)` |
| [00515](ADR-00515.md) | Lazy dynamic `import()` via `dlopen`'d shared-library islands | `Implements [TDD-00056](../tdd/TDD-00056.md)` |
| [00516](ADR-00516.md) | `/** @pure */` — compile-time purity enforcement | `Implements [TDD-00128](../tdd/TDD-00128.md)` |
| [00517](ADR-00517.md) | Object-reference array representation (shared headers) | `Implements [TDD-00127](../tdd/TDD-00127.md)` |
| [00518](ADR-00518.md) | Terminal-control primitives (raw mode, tty size, SIGWINCH, raw reads) | `Implements [TDD-00031](../tdd/TDD-00031.md)`. `Extends [ADR-00079](ADR-00079.md)` (signal allowlist), `[ADR-00424](ADR-00424.md)` (isTTY). |
| [00519](ADR-00519.md) | Native `klain:tui` — vendored Yoga flexbox + double-buffered ANSI diff painter | `Implements [TDD-00150](../tdd/TDD-00150.md)` (Stage 1). `Extends [ADR-00518](ADR-00518.md)` (terminal primitives), `[ADR-00131](ADR-00131.md)`/`klain:` namespace, `[ADR-00020](ADR-00020.md)` (conditional linking). |
| [00520](ADR-00520.md) | `klain:tty` `readKey(timeoutMs)` — a polling raw-key read | `Extends [ADR-00518](ADR-00518.md)` (terminal primitives). Enables `klain:tui` live-refresh loops ([ADR-00519](ADR-00519.md)/[TDD-00150](../tdd/TDD-00150.md)). |
