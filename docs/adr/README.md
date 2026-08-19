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
| [00141](ADR-00141.md) | Import-gated built-in bindings, Stage 1 (default/namespace form) | Implements [TDD-00049](../tdd/TDD-00049.md) |
| [00142](ADR-00142.md) | Import-gated built-in bindings, Stage 2 (named per-member imports) | Extends [ADR-00141](ADR-00141.md). Implements [TDD-00049](../tdd/TDD-00049.md) |
| [00143](ADR-00143.md) | Reserved ambient-global names — `-globals=strict\|permissive` | Implements [TDD-00050](../tdd/TDD-00050.md). Extended by [ADR-00217](ADR-00217.md) (the `-globals` flag was later absorbed into `-compat`) |
| [00144](ADR-00144.md) | Re-exports (`export { a } from './x'`, `export * from './x'`) | Implements [TDD-00051](../tdd/TDD-00051.md) |
| [00145](ADR-00145.md) | Top-level side-effecting code in imported files (dependency-ordered, cycle-guarded) | Implements [TDD-00052](../tdd/TDD-00052.md) |
| [00146](ADR-00146.md) | klmpm Stage 1 — compiler-side `klain_modules` resolution | Implements [TDD-00054](../tdd/TDD-00054.md) (Stage 1 only) |
| [00147](ADR-00147.md) | klmpm Stage 2 — `klain.json`/`klain.lock` manifest parsing | Implements [TDD-00054](../tdd/TDD-00054.md) (Stage 2 only). Extends [ADR-00146](ADR-00146.md) |
| [00148](ADR-00148.md) | `import.meta.url` (dynamic `import(...)` grammar recognized, cleanly rejected) | Implements [TDD-00055](../tdd/TDD-00055.md) (Stage 1 only) |
| [00149](ADR-00149.md) | Nested function declarations (V1: hoisted, own scope, no capture) | Implements [TDD-00057](../tdd/TDD-00057.md) |
| [00150](ADR-00150.md) | Fixed-point unannotated-return-type inference | Extends [ADR-00041](ADR-00041.md); Implements [TDD-00058](../tdd/TDD-00058.md) |
| [00151](ADR-00151.md) | Tagged template literals | Implements [TDD-00059](../tdd/TDD-00059.md); Extended by [ADR-00152](ADR-00152.md) |
| [00152](ADR-00152.md) | Closing gaps found on a second pass over ADR-00151 (array-typed closures, class-method rest params, erased-generic forward references) | Extends [ADR-00151](ADR-00151.md), [ADR-00105](ADR-00105.md), [ADR-00150](ADR-00150.md) |
| [00153](ADR-00153.md) | Test262 full-corpus conformance harness | Implements [TDD-00008](../tdd/TDD-00008.md) (Design V2) |
| [00154](ADR-00154.md) | Object literal string/numeric-literal property keys | Extends [ADR-00153](ADR-00153.md) |
| [00155](ADR-00155.md) | `#x` private names | Implements [TDD-00021](../tdd/TDD-00021.md); Extends [ADR-00153](ADR-00153.md) |
| [00156](ADR-00156.md) | Multi-declarator `let`/`const`/`var`, for-loop comma-update | Extends [ADR-00153](ADR-00153.md) |
| [00157](ADR-00157.md) | Uninitialized-heap-memory reads (optional fields, class fields, array destructuring bounds) | Extends [ADR-00153](ADR-00153.md) |
| [00158](ADR-00158.md) | Destructuring default values (`[a = expr]`, `{ a = expr }`) | Extends [ADR-00153](ADR-00153.md), [ADR-00157](ADR-00157.md) |
| [00159](ADR-00159.md) | `new Set(iterable)` | Extends [ADR-00153](ADR-00153.md) |
| [00160](ADR-00160.md) | Destructuring assignment (`[a, b] = expr`, `({ a, b } = expr)`) | Extends [ADR-00153](ADR-00153.md), [ADR-00157](ADR-00157.md), [ADR-00158](ADR-00158.md) |
| [00161](ADR-00161.md) | Array rest destructuring (`[a, ...rest]`) | Extends [ADR-00153](ADR-00153.md), [ADR-00157](ADR-00157.md), [ADR-00160](ADR-00160.md) |
| [00162](ADR-00162.md) | `instanceof` against built-in types (`Array`, `Map`, `Set`, `Date`, `RegExp`) | Extends [ADR-00153](ADR-00153.md) |
| [00163](ADR-00163.md) | `.reduce()` with no initial value | Extends [ADR-00153](ADR-00153.md) |
| [00164](ADR-00164.md) | Optional (`param?: T`) function parameters | Extends [ADR-00157](ADR-00157.md), [ADR-00158](ADR-00158.md) |
| [00165](ADR-00165.md) | String concatenation with a null operand | Found while implementing [ADR-00164](ADR-00164.md) |
| [00166](ADR-00166.md) | Status-doc accuracy audit and the Strict Coverage metric | Found while implementing [ADR-00164](ADR-00164.md) |
| [00167](ADR-00167.md) | Guard pop and shift against empty arrays | Fixes findings from [ADR-00166](ADR-00166.md) |
| [00168](ADR-00168.md) | Function expressions (V1: anonymous only) + early-error checks that fix the conformance regressions it exposed | Implements [TDD-00060](../tdd/TDD-00060.md) |
| [00169](ADR-00169.md) | Generalized call dispatch + object literal method shorthand | Extends [ADR-00153](ADR-00153.md) |
| [00170](ADR-00170.md) | Destructured catch binding (`catch ({ message, name }) {}`) | Extends [ADR-00086](ADR-00086.md) |
| [00171](ADR-00171.md) | Generator function front-end (`function* f() { yield x; }` lexer/parser/AST) — parses, cleanly rejected by codegen | Implements the front-end slice of [TDD-00061](../tdd/TDD-00061.md) |
| [00172](ADR-00172.md) | Generator function suspend/resume codegen (construction, `yield`, `.next()`) | Implements [TDD-00061](../tdd/TDD-00061.md); extends [ADR-00171](ADR-00171.md) |
| [00173](ADR-00173.md) | `for...of` over a generator | Implements the remainder of [TDD-00061](../tdd/TDD-00061.md); extends [ADR-00172](ADR-00172.md) |
| [00174](ADR-00174.md) | Box union/dynamic arguments at static-method call sites (fixes `assert.sameValue`) | |
| [00175](ADR-00175.md) | Conformance harness — kill the whole process group and bound the pipe wait | Tooling |
| [00176](ADR-00176.md) | Bare `any`/`unknown` as a function/method parameter and return type (V2) | Implements [TDD-00062](../tdd/TDD-00062.md); extends [ADR-00008](ADR-00008.md)/[ADR-00174](ADR-00174.md) |
| [00177](ADR-00177.md) | Box arrays (by reference) and fix reference-type toString for `any`/`unknown` | Extends [ADR-00176](ADR-00176.md); implements [TDD-00062](../tdd/TDD-00062.md) |
| [00178](ADR-00178.md) | Named function expressions (self-reference binding) | Implements [TDD-00060](../tdd/TDD-00060.md); extends [ADR-00168](ADR-00168.md) |
| [00179](ADR-00179.md) | The comma / sequence operator (`(a, b, c)`) | |
| [00180](ADR-00180.md) | Class field initializers (TDD-00063 Stage 1) | Implements [TDD-00063](../tdd/TDD-00063.md) Stage 1; extends [ADR-00155](ADR-00155.md) |
| [00181](ADR-00181.md) | Async class methods + method-modifier parsing (TDD-00063 Stage 2a) | Implements [TDD-00063](../tdd/TDD-00063.md) Stage 2a; extends [ADR-00180](ADR-00180.md) |
| [00182](ADR-00182.md) | Generator methods (TDD-00063 Stage 2b) | Implements [TDD-00063](../tdd/TDD-00063.md) Stage 2b; extends [ADR-00181](ADR-00181.md) |
| [00183](ADR-00183.md) | `console.log(boolean)` prints `true`/`false`, not `1`/`0` | |
| [00184](ADR-00184.md) | Computed class member names (TDD-00063 Stage 3) | Implements [TDD-00063](../tdd/TDD-00063.md) Stage 3; extends [ADR-00182](ADR-00182.md) |
| [00185](ADR-00185.md) | Class expressions (TDD-00063 Stage 4) | Implements [TDD-00063](../tdd/TDD-00063.md) Stage 4; extends [ADR-00184](ADR-00184.md) |
| [00186](ADR-00186.md) | `&&` / `||` short-circuit instead of evaluating both operands | Extends [ADR-00087](ADR-00087.md); corrects a caveat flagged by [ADR-00183](ADR-00183.md) |
| [00187](ADR-00187.md) | The `**` exponentiation operator (and `**=`) | Fixes the `**` ❌ row flagged by [ADR-00166](ADR-00166.md) |
| [00188](ADR-00188.md) | `!=`/`!==` on floats use unordered `fcmp une` so `NaN != NaN` is true | Fixes the NaN-comparison bug flagged by [ADR-00166](ADR-00166.md) |
| [00189](ADR-00189.md) | `JSON.parse` into an array-typed field is a clean rejection, not invalid IR | Fixes a bug tracked under [TDD-00015](../tdd/TDD-00015.md) |
| [00190](ADR-00190.md) | Unannotated `let b = true` / `let n = !cond` / `let z = -3.5` infer their real type | Follows [ADR-00183](ADR-00183.md) (fixes the storage side of boolean printing) |
| [00191](ADR-00191.md) | `finally` runs on `return`/`break`/`continue`, not only on fall-through | Fixes the finally-on-return bug flagged by [ADR-00166](ADR-00166.md) |
| [00192](ADR-00192.md) | Destructuring the loop variable of a for-of (`for (const [a,b] of …)`) | Implements [TDD-00065](../tdd/TDD-00065.md) Stage 1 |
| [00193](ADR-00193.md) | Nested destructuring patterns (`[[a,b],c]`, `{ inner: { v } }`) across declarations, for-of, and parameters | Implements [TDD-00065](../tdd/TDD-00065.md) Stage 2; extends [ADR-00192](ADR-00192.md) |
| [00194](ADR-00194.md) | Full string-literal escape-sequence decoding (`\xHH`, `\uHHHH`, `\u{…}`, octal, line continuation, NonEscapeCharacter) | Found via a new conformance `-faillist`; corpus +101 |
| [00195](ADR-00195.md) | Conformance report: category descriptions, pipeline-phase breakdown, per-reason example files | Extends [ADR-00153](ADR-00153.md) |
| [00196](ADR-00196.md) | `String.fromCharCode`/`fromCodePoint` reject a non-number argument instead of emitting invalid IR | Surfaced by [ADR-00195](ADR-00195.md)'s phase breakdown |
| [00197](ADR-00197.md) | Reject numeric separators in legacy-octal / non-octal-decimal literals (`0_0`, `08_0`) | Extends [ADR-00085](ADR-00085.md); surfaced by [ADR-00195](ADR-00195.md) |
| [00198](ADR-00198.md) | Static-string `eval` fast path — compile a constant expression in place, no embedded engine | Implements a subset of [TDD-00046](../tdd/TDD-00046.md); corpus +45 |
| [00199](ADR-00199.md) | Presence-flagged `{ i1, T }` representation for nullable non-pointer scalars (`number \| null`, …) | Implements [TDD-00064](../tdd/TDD-00064.md) |
| [00200](ADR-00200.md) | Named functions as first-class values, via an env-dropping trampoline | |
| [00201](ADR-00201.md) | Tuple types `[T0, T1, ...]` — fixed-shape positional struct | Implements [TDD-00066](../tdd/TDD-00066.md) |
| [00202](ADR-00202.md) | Fix invalid SSA in the WebSocket frame-decode and client-scan runtime templates | Extends [ADR-00125](ADR-00125.md), [ADR-00128](ADR-00128.md) |
| [00203](ADR-00203.md) | String/numeric-literal keys in object destructuring patterns (`{ "k": v }`, `{ 0: v }`) | Implements [TDD-00065](../tdd/TDD-00065.md) Stage 3a; extends [ADR-00193](ADR-00193.md) |
| [00204](ADR-00204.md) | Object rest `{ ...rest }` over a statically-known source shape | Implements [TDD-00065](../tdd/TDD-00065.md) Stage 3b; extends [ADR-00203](ADR-00203.md) |
| [00205](ADR-00205.md) | One denominator for Strict Coverage — always the page's total feature count | Extends [ADR-00166](ADR-00166.md) |
| [00206](ADR-00206.md) | RegExp ECMAScript-dialect alignment (Options A + B) and the `-regex` mode selector | Implements [TDD-00067](../tdd/TDD-00067.md); extends [ADR-00114](ADR-00114.md) |
| [00207](ADR-00207.md) | RegExp `es-utf16` index mode and the global empty-match advance | Implements [TDD-00067](../tdd/TDD-00067.md) Stage 3; extends [ADR-00206](ADR-00206.md) |
| [00208](ADR-00208.md) | RegExp `ecmascript` mode — the Option C source-normalization pass (v1) and the default advance | Implements [TDD-00067](../tdd/TDD-00067.md) Stage 4; extends [ADR-00206](ADR-00206.md), [ADR-00207](ADR-00207.md) |
| [00209](ADR-00209.md) | `const` requires an initializer; `eval`/`arguments` reserved as strict-mode binding names | Extends [ADR-00181](ADR-00181.md), [ADR-00168](ADR-00168.md) |
| [00210](ADR-00210.md) | `let`/`const`/`var` scope semantics — function-scoped `var`, block-scoped redeclaration early-errors | Implements [TDD-00070](../tdd/TDD-00070.md); extends [ADR-00209](ADR-00209.md) |
| [00211](ADR-00211.md) | Cross-block `var`/lexical redeclaration early-error (`let x; { var x }`) | Extends [ADR-00210](ADR-00210.md); implements [TDD-00070](../tdd/TDD-00070.md) |
| [00212](ADR-00212.md) | Temporal-dead-zone early error, and the block-shadowing bug behind it | Implements [TDD-00071](../tdd/TDD-00071.md) (Stage 1); extends [ADR-00210](ADR-00210.md) |
| [00213](ADR-00213.md) | Definite-assignment early error for a typed `var`/`let` | Implements [TDD-00071](../tdd/TDD-00071.md) (Stage 2); extends [ADR-00210](ADR-00210.md) |
| [00214](ADR-00214.md) | Definite-assignment precision for `do/while` and `switch` | Extends [ADR-00213](ADR-00213.md); implements [TDD-00071](../tdd/TDD-00071.md) |
| [00215](ADR-00215.md) | Zero-initialize an uninitialized `let`/`const` slot (deterministic default for definite-assignment escapes) | Extends [ADR-00210](ADR-00210.md), [ADR-00214](ADR-00214.md) |
| [00216](ADR-00216.md) | `bigint` V1 — arbitrary-precision integers behind a selectable `-bigint=libtommath\|gmp` backend | Implements [TDD-00074](../tdd/TDD-00074.md) |
| [00217](ADR-00217.md) | `-compat` compatibility axis (step 1) — absorb `-globals`, add bigint cross-type comparison | Implements [TDD-00075](../tdd/TDD-00075.md); extends [ADR-00143](ADR-00143.md), [ADR-00216](ADR-00216.md) |
| [00218](ADR-00218.md) | Object-to-string — Node-style `console.log` inspection + `-compat`-gated `[object Object]` coercion | Implements [TDD-00075](../tdd/TDD-00075.md); extends [ADR-00217](ADR-00217.md) |
| [00219](ADR-00219.md) | Fix a class-typed interface/type-alias field resolving to `i64` (placeholder-registration ordering) | Extends [ADR-00218](ADR-00218.md) |
| [00220](ADR-00220.md) | `&&` / `\|\|` value-preserving under `-compat=js` | Implements [TDD-00075](../tdd/TDD-00075.md); extends [ADR-00186](ADR-00186.md), [ADR-00217](ADR-00217.md) |
| [00221](ADR-00221.md) | Bound the object inspector's recursion (fix compile-time infinite loop on recursive types) | Extends [ADR-00218](ADR-00218.md) |
| [00222](ADR-00222.md) | `JSON.stringify` `space` pretty-printing and generic `toJSON()` | Implements [TDD-00077](../tdd/TDD-00077.md) (Track S) |
| [00223](ADR-00223.md) | A validating JSON parse-tree (P1) — `JSON.parse` throws `SyntaxError` on malformed input | Implements [TDD-00077](../tdd/TDD-00077.md) (Track P, P1); extends [ADR-00189](ADR-00189.md), [ADR-00007](ADR-00007.md) |
| [00224](ADR-00224.md) | Type-directed JSON projection off the parse tree (P3) — nested objects, array fields, top-level `T[]` | Implements [TDD-00077](../tdd/TDD-00077.md) (Track P, P3); extends [ADR-00223](ADR-00223.md); supersedes [ADR-00007](ADR-00007.md), [ADR-00189](ADR-00189.md), [ADR-00166](ADR-00166.md) |
| [00225](ADR-00225.md) | Intersection types (`A & B`) via field-merge into one struct | Implements [TDD-00078](../tdd/TDD-00078.md) |
| [00226](ADR-00226.md) | Fix exponential-time `inferExprType` on deep binary expressions (a whole-corpus conformance hang) | — |
| [00227](ADR-00227.md) | User-facing output — drop doc references, wrap `--help` | — |
| [00228](ADR-00228.md) | Built-in utility types, stage 1a — Partial/Required/Readonly/NonNullable | Implements [TDD-00079](../tdd/TDD-00079.md) (Stage 1a) |
| [00229](ADR-00229.md) | Utility types, stage 1b — Pick/Omit/Record + string-literal types | Implements [TDD-00079](../tdd/TDD-00079.md) (Stage 1b); extends [ADR-00228](ADR-00228.md) |
| [00230](ADR-00230.md) | General mapped types — keyof, indexed access, `{ [K in …]: V }` | Implements [TDD-00079](../tdd/TDD-00079.md) (Stage 2); extends [ADR-00229](ADR-00229.md) |
| [00231](ADR-00231.md) | Conditional types + infer + generic type aliases + structural assignability | Implements [TDD-00079](../tdd/TDD-00079.md) (Stage 3); extends [ADR-00230](ADR-00230.md) |
| [00232](ADR-00232.md) | Type-system caveat reductions — tuple `.length` + element assignment; `A \| B \| null`; intersection same-name object fields | Extends [ADR-00201](ADR-00201.md), [ADR-00225](ADR-00225.md) |
| [00233](ADR-00233.md) | JS-faithful float-to-string (shortest round-trip) | Implements [TDD-00080](../tdd/TDD-00080.md); supersedes [ADR-00166](ADR-00166.md) |
