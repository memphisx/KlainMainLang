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
| [00017](ADR-00017.md) | Extend Date.parse to support +HH:MM/-HH:MM timezone offsets | Extends [ADR-00015](ADR-00015.md) |
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
| [00041](ADR-00041.md) | Infer return types for unannotated functions and arrow functions | Extends [ADR-00012](ADR-00012.md), [ADR-00037](ADR-00037.md) |
| [00042](ADR-00042.md) | Reject non-numeric arguments to unannotated parameters at call sites | Implements [TDD-00005](../tdd/TDD-00005.md) |
| [00043](ADR-00043.md) | Fix forEach/HOF callbacks with console.log bodies or non-numeric elements | |
| [00044](ADR-00044.md) | Fix array index out-of-bounds reads/writes with a runtime bounds check | |
| [00045](ADR-00045.md) | Reject const reassignment with a Symbol.IsConst check in emitAssign | |
| [00046](ADR-00046.md) | Fix (FuncType)[] parser gap and enable calling closures stored in arrays/object fields | |
| [00047](ADR-00047.md) | Fix bitwise shift operators to use JS's 32-bit semantics | |
| [00048](ADR-00048.md) | select()-based event loop ([TDD-00006](../tdd/TDD-00006.md) Part 1) and a minimal HTTP server ([TDD-00004](../tdd/TDD-00004.md) V1) | Implements [TDD-00004](../tdd/TDD-00004.md), [TDD-00006](../tdd/TDD-00006.md) |
| [00049](ADR-00049.md) | Concurrent HTTP connection handling via fibers ([TDD-00006](../tdd/TDD-00006.md) Part 2, first real slice) | Extends [ADR-00048](ADR-00048.md). Implements [TDD-00006](../tdd/TDD-00006.md) |
| [00050](ADR-00050.md) | Non-blocking await fetch(...) via libcurl's multi-interface ([TDD-00006](../tdd/TDD-00006.md) Part 2, second real slice) | Extends [ADR-00049](ADR-00049.md). Implements [TDD-00006](../tdd/TDD-00006.md) |
| [00051](ADR-00051.md) | Fix ucontext_t's size/layout being hardcoded to this dev machine's platform | Extends [ADR-00049](ADR-00049.md), [ADR-00050](ADR-00050.md) |
| [00052](ADR-00052.md) | Fix alloca's inside loop bodies causing unbounded stack growth | Extends [ADR-00049](ADR-00049.md), [ADR-00050](ADR-00050.md) |
| [00053](ADR-00053.md) | Implement Map.entries()/.forEach()/.clear() and Set.forEach()/.clear() | |
| [00054](ADR-00054.md) | Implement Object.assign(target, ...src) | |
| [00055](ADR-00055.md) | Implement Object.freeze(obj) / Object.seal(obj) | |
| [00056](ADR-00056.md) | Fix Array.prototype.splice's out-of-bounds read and missing insert-item support | |
| [00057](ADR-00057.md) | Implement findLast/findLastIndex, toSorted/toReversed/toSpliced, with, keys/values/entries, copyWithin, and Array.of | Extends [ADR-00056](ADR-00056.md) |
| [00058](ADR-00058.md) | Fix Array\<T\>/Map\<K,V\>/Set\<T\> silently resolving to i64 as a plain type annotation | |
| [00059](ADR-00059.md) | Fix Map/Set method calls, .size, and for...of only recognizing a plain named variable | Extends [ADR-00058](ADR-00058.md) |
| [00060](ADR-00060.md) | Fix for...in and return-of-an-array only recognizing a plain named variable | Extends [ADR-00059](ADR-00059.md) |
| [00061](ADR-00061.md) | Fix array-typed object/interface fields losing their length entirely | Extends [ADR-00060](ADR-00060.md) |
| [00062](ADR-00062.md) | Classes Stage 0 — lexer/parser/AST groundwork | Implements [TDD-00009](../tdd/TDD-00009.md) |
| [00063](ADR-00063.md) | Classes Stage 1 (+1a) — methods, constructors, this, new, class-based for...of iterator protocol | Extends [ADR-00062](ADR-00062.md). Implements [TDD-00009](../tdd/TDD-00009.md) |
| [00064](ADR-00064.md) | Fix emitObjectVarDecl's narrow initializer whitelist and self-referential class field type staleness | Extends [ADR-00063](ADR-00063.md) |
| [00065](ADR-00065.md) | Implement Number.toPrecision/toExponential/toString(radix), Math.clz32/fround/imul, Object.hasOwn/hasOwnProperty | |
| [00066](ADR-00066.md) | Computed property keys (`{ [expr]: value }`) as dynamic objects backed by Map\<string,V\> | Implements [TDD-00012](../tdd/TDD-00012.md) |
| [00067](ADR-00067.md) | Classes Stage 2 — runtime type tags and instanceof | Extends [ADR-00063](ADR-00063.md), [ADR-00064](ADR-00064.md). Implements [TDD-00009](../tdd/TDD-00009.md) |
| [00068](ADR-00068.md) | Add native Go fuzz testing for the lexer and parser | |
| [00069](ADR-00069.md) | Fix division/modulo by zero producing undefined behavior instead of a catchable Error | |
| [00070](ADR-00070.md) | Full-pipeline (codegen-through-binary) fuzz testing | Extends [ADR-00068](ADR-00068.md). Implements [TDD-00014](../tdd/TDD-00014.md) |
| [00071](ADR-00071.md) | `-mm=gc` — Boehm GC mode | Implements [TDD-00001](../tdd/TDD-00001.md) |
| [00072](ADR-00072.md) | `http.listen` — request headers, query string, request body, response headers | Extends [ADR-00048](ADR-00048.md), [ADR-00049](ADR-00049.md) |
| [00073](ADR-00073.md) | `Promise.all` / `.race` / `.allSettled` | Extends [ADR-00049](ADR-00049.md), [ADR-00050](ADR-00050.md). Implements [TDD-00016](../tdd/TDD-00016.md) |
| [00074](ADR-00074.md) | `fetch()` client parity — custom method, headers, request body | Extends [ADR-00050](ADR-00050.md). Implements [TDD-00017](../tdd/TDD-00017.md) |
| [00075](ADR-00075.md) | Split oversized codegen/parser files into domain files | |
| [00076](ADR-00076.md) | `URL` / `URLSearchParams` | |
| [00077](ADR-00077.md) | Coerce object literal fields against their declared type | Implements [TDD-00007](../tdd/TDD-00007.md) |
| [00078](ADR-00078.md) | `ArrayBuffer` / TypedArrays | Implements [TDD-00018](../tdd/TDD-00018.md) |
| [00079](ADR-00079.md) | POSIX signal handling — `process.on('SIGINT'/'SIGTERM', handler)` | Extends [ADR-00072](ADR-00072.md). Implements [TDD-00019](../tdd/TDD-00019.md) |
| [00080](ADR-00080.md) | Fix `-mm=gc` startup crash on Ubuntu's `libgc-dev` build | Extends [ADR-00071](ADR-00071.md) |
| [00081](ADR-00081.md) | `path` module (join, resolve, dirname, basename, extname, parse, format, isAbsolute, sep, delimiter) | |
| [00082](ADR-00082.md) | Error subtypes / tagged errors — `TypeError`, `RangeError`, etc. | Implements [TDD-00013](../tdd/TDD-00013.md). Extends [ADR-00067](ADR-00067.md) |
| [00083](ADR-00083.md) | Classes Stage 3 — inheritance (`extends`, `super`, dynamic dispatch) | Extends [ADR-00062](ADR-00062.md), [ADR-00063](ADR-00063.md), [ADR-00067](ADR-00067.md). Implements [TDD-00009](../tdd/TDD-00009.md) |
| [00084](ADR-00084.md) | Classes Stage 4 — `static`, `private`/`protected`, `abstract`, `implements` | Extends [ADR-00083](ADR-00083.md). Implements [TDD-00009](../tdd/TDD-00009.md) |
| [00085](ADR-00085.md) | Numeric separators (`1_000_000`) | |
| [00086](ADR-00086.md) | Optional catch binding (`catch { ... }`) | |
| [00087](ADR-00087.md) | Logical assignment operators (`&&=`, `\|\|=`, `??=`) | |
| [00088](ADR-00088.md) | `Array.from(iterable)` (array-like overload) | Extends [ADR-00063](ADR-00063.md). Implements [TDD-00009](../tdd/TDD-00009.md) |
| [00089](ADR-00089.md) | `EventEmitter<T>` (`events` module), including `class X extends EventEmitter<T>` | Extends [ADR-00083](ADR-00083.md). Implements [TDD-00023](../tdd/TDD-00023.md) |
| [00090](ADR-00090.md) | `os` module (Darwin `freemem()`/`cpus().times` unverified) | Implements [TDD-00024](../tdd/TDD-00024.md) |
| [00091](ADR-00091.md) | `in` operator (`key in obj`) | |
| [00092](ADR-00092.md) | `setImmediate(fn)` / `clearImmediate(id)` | Extends [ADR-00031](ADR-00031.md) |
| [00093](ADR-00093.md) | Fix missing `GC_stackbottom` restore on `http.listen`'s read-loop yield (4th swapcontext site ADR-00071 missed) | Extends [ADR-00071](ADR-00071.md) |
| [00094](ADR-00094.md) | Fix fetch/fs embedded-null-byte truncation via `Response.arrayBuffer()` / `fs.readFileSyncBytes()` / binary-aware `writeFileSync`/`appendFileSync` | Extends [ADR-00021](ADR-00021.md), [ADR-00023](ADR-00023.md), [ADR-00078](ADR-00078.md) |
| [00095](ADR-00095.md) | Remove dead `__kml_fetch` (blocking single-transfer fetch, superseded by ADR-00050) | Extends [ADR-00094](ADR-00094.md). Superseded by [ADR-00050](ADR-00050.md) |
| [00096](ADR-00096.md) | Replace httpbin.org with a local httpbin-lite fixture in `make examples` (fixes flaky CI) | Extends [ADR-00021](ADR-00021.md), [ADR-00073](ADR-00073.md), [ADR-00074](ADR-00074.md) |
| [00097](ADR-00097.md) | Multi-process clustering for `http.listen()` (`{ workers: N }`) | Implements [TDD-00025](../tdd/TDD-00025.md). Extends [ADR-00048](ADR-00048.md), [ADR-00049](ADR-00049.md) |
| [00098](ADR-00098.md) | Fix dangling connection-array pointer causing intermittent `http.listen` hangs under concurrent load | Extends [ADR-00049](ADR-00049.md), [ADR-00052](ADR-00052.md), [ADR-00072](ADR-00072.md) |
| [00099](ADR-00099.md) | `GC_set_handle_fork(1)` for `-mm=gc` + `http.listen` clustering — fixed one real crash, one residual hang left unresolved | Extends [ADR-00097](ADR-00097.md), [ADR-00098](ADR-00098.md). Extended by [ADR-00101](ADR-00101.md) |
| [00100](ADR-00100.md) | Fix `-mm=gc` startup crash under AddressSanitizer, and add ASan/UBSan test-build helpers | Extends [ADR-00071](ADR-00071.md), [ADR-00080](ADR-00080.md) |
| [00101](ADR-00101.md) | Root cause of the `-mm=gc` clustering hang — `GC_stackbottom` never restored when a fiber runs to completion | Implements [TDD-00025](../tdd/TDD-00025.md). Extends [ADR-00071](ADR-00071.md), [ADR-00093](ADR-00093.md), [ADR-00099](ADR-00099.md) |
| [00103](ADR-00103.md) | User-defined generics V1 — monomorphization for functions, interfaces, classes | Implements [TDD-00010](../tdd/TDD-00010.md) |
| [00104](ADR-00104.md) | Array/Map/Set/EventEmitter literals as general expressions | Implements [TDD-00028](../tdd/TDD-00028.md) |
| [00105](ADR-00105.md) | Array-of-arrays (nested array) storage representation | Implements [TDD-00029](../tdd/TDD-00029.md) |
| [00106](ADR-00106.md) | Binary-safe `http.listen()` request/response bodies | Implements [TDD-00026](../tdd/TDD-00026.md) |
| [00107](ADR-00107.md) | `Array.prototype.flat(depth?)` / `.flatMap(fn)` | Extends [ADR-00105](ADR-00105.md) (the `.flat()`/`.flatMap()` follow-on left open by [TDD-00029](../tdd/TDD-00029.md)) |
| [00108](ADR-00108.md) | `i64::MIN / -1` UB guard, and `%=` (was entirely unimplemented) | |
| [00109](ADR-00109.md) | Destructured function parameters | |
| [00110](ADR-00110.md) | Getters / setters on classes | Implements [TDD-00030](../tdd/TDD-00030.md) |
| [00111](ADR-00111.md) | `performance.mark(name)` / `performance.measure(name, start, end?)` | |
| [00112](ADR-00112.md) | `TextEncoder`/`TextDecoder` (UTF-8 only, V1) | |
| [00113](ADR-00113.md) | `structuredClone(obj)` — recursive deep copy dispatched on static type | |
| [00114](ADR-00114.md) | RegExp Stage 0 — construction, literal syntax, field reads | Implements [TDD-00035](../tdd/TDD-00035.md) |
| [00115](ADR-00115.md) | RegExp Stage 1 — `.test(str)` | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md) |
| [00116](ADR-00116.md) | RegExp Stage 2 — `.exec(str)`, and fixing 3 general truthiness/null-comparison bugs it exposed (bare ptr truthiness, array-vs-null comparison, string content-based truthiness) | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md), [ADR-00115](ADR-00115.md) |
| [00117](ADR-00117.md) | RegExp Stage 3 — `str.match()`/`str.matchAll()`, correcting a TDD design mistake (global `.match()` with zero matches returns `null`, not `[]`) | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md), [ADR-00115](ADR-00115.md), [ADR-00116](ADR-00116.md) |
| [00118](ADR-00118.md) | RegExp Stage 4 — `str.replace()`/`str.replaceAll()`, incl. `$1`/`$&`/`$$` backreferences and a fixed-arity `(match, offset, string)` callback form | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md), [ADR-00115](ADR-00115.md), [ADR-00116](ADR-00116.md), [ADR-00117](ADR-00117.md) |
| [00119](ADR-00119.md) | RegExp Stage 5 — `str.split()`/`str.search()`, on a new stateless match primitive (`.split()`/`.search()` don't use `lastIndex` the way every earlier stage does) | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md), [ADR-00115](ADR-00115.md), [ADR-00116](ADR-00116.md), [ADR-00117](ADR-00117.md), [ADR-00118](ADR-00118.md) |
| [00120](ADR-00120.md) | RegExp Stage 6 — `--static` linking verification (bare Linux + real Docker `scratch` container, both confirmed working with zero extra flags) | Implements [TDD-00035](../tdd/TDD-00035.md). Extends [ADR-00114](ADR-00114.md), [ADR-00115](ADR-00115.md), [ADR-00116](ADR-00116.md), [ADR-00117](ADR-00117.md), [ADR-00118](ADR-00118.md), [ADR-00119](ADR-00119.md) |
| [00121](ADR-00121.md) | User-defined generics V2 — `@erased` opt-in type erasure | Implements [TDD-00010](../tdd/TDD-00010.md). Extends [ADR-00103](ADR-00103.md) |
| [00122](ADR-00122.md) | `EventSource` (SSE) Stage 0 — connection plumbing | Implements [TDD-00038](../tdd/TDD-00038.md) |
| [00123](ADR-00123.md) | `EventSource` Stage 1 — SSE record parsing and `onmessage` | Implements [TDD-00038](../tdd/TDD-00038.md). Extends [ADR-00122](ADR-00122.md) |
| [00124](ADR-00124.md) | `EventSource` Stage 2 — named events, `onopen`/`onerror` | Implements [TDD-00038](../tdd/TDD-00038.md). Extends [ADR-00123](ADR-00123.md) |
| [00125](ADR-00125.md) | `WebSocket` Stage 0 — shared frame codec + SHA-1 | Implements [TDD-00039](../tdd/TDD-00039.md) (Stage 0 of 4) |
| [00126](ADR-00126.md) | `WebSocket` Stage 1 — server-side upgrade + persistent echo loop | Implements [TDD-00039](../tdd/TDD-00039.md). Extends [ADR-00125](ADR-00125.md) |
| [00127](ADR-00127.md) | `WebSocket` Stage 2 — automatic ping/pong + close handshake | Implements [TDD-00039](../tdd/TDD-00039.md). Extends [ADR-00126](ADR-00126.md) |
| [00128](ADR-00128.md) | `WebSocket` Stage 3 — client-side `new WebSocket(url)` | Implements [TDD-00039](../tdd/TDD-00039.md). Extends [ADR-00127](ADR-00127.md) |
| [00129](ADR-00129.md) | `EventSource` Stage 3 — CRLF boundaries, terminal failure, auto-reconnect | Implements [TDD-00038](../tdd/TDD-00038.md). Extends [ADR-00122](ADR-00122.md), [ADR-00123](ADR-00123.md), [ADR-00124](ADR-00124.md) |
| [00130](ADR-00130.md) | Real `Request`/`Headers` classes, and freeing up `Request` from `http.listen`'s server-side type | Supersedes [ADR-00074](ADR-00074.md). Implements [TDD-00040](../tdd/TDD-00040.md) |
| [00131](ADR-00131.md) | `XMLHttpRequest` — legacy synchronous-style client on top of `fetch`'s own non-blocking primitives | Extends [ADR-00050](ADR-00050.md), [ADR-00073](ADR-00073.md). Implements [TDD-00040](../tdd/TDD-00040.md) |
| [00132](ADR-00132.md) | Multiple type parameters for user-defined generics (`<K, V>`) | Extends [ADR-00103](ADR-00103.md), [ADR-00121](ADR-00121.md). Implements [TDD-00037](../tdd/TDD-00037.md) |
| [00133](ADR-00133.md) | Fix two `EventSource` auto-reconnect hangs in the event loop's own `select()` wait | Extends [ADR-00122](ADR-00122.md), [ADR-00123](ADR-00123.md), [ADR-00124](ADR-00124.md), [ADR-00129](ADR-00129.md). Implements [TDD-00038](../tdd/TDD-00038.md) |
| [00134](ADR-00134.md) | True per-file module scope via mangled internal names | Extends [ADR-00022](ADR-00022.md). Implements [TDD-00041](../tdd/TDD-00041.md) |
| [00135](ADR-00135.md) | `export default`, default imports, and namespace imports | Extends [ADR-00022](ADR-00022.md), [ADR-00134](ADR-00134.md). Implements [TDD-00042](../tdd/TDD-00042.md) |
| [00136](ADR-00136.md) | General union types beyond `T \| null` (V1: scalar members) | Extends [ADR-00008](ADR-00008.md). Implements [TDD-00043](../tdd/TDD-00043.md) |
| [00137](ADR-00137.md) | `process.stdout.write(s)` / `process.stderr.write(s)` | |
| [00138](ADR-00138.md) | `symbol` V1 (opaque unique values) | Implements [TDD-00044](../tdd/TDD-00044.md) |
| [00139](ADR-00139.md) | `querystring` module (`.parse`/`.stringify`) | |
| [00140](ADR-00140.md) | `assert` module | |
