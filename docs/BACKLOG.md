# Backlog

The **single file we read to tackle every open issue in the compiler.** Two halves:

1. **This hand-authored top** — the prioritised, high-leverage narrative: big
   architectural work (D1, IOCP, DOM shim, `vm`) and the value ordering to attack
   it in. Not everything here is a status caveat; these are the items that need
   framing a one-line caveat can't give.
2. **The generated caveat index below the marker** — *every* caveat from
   `docs/status/data/*.json`, per area, ordered worst **Strict Coverage** first.
   Every line is an issue to tackle: a missing feature and a behavioural
   divergence from Node/TS/JS both break code someone ports here. It is generated
   by `make status` and diff-guarded by `make status-check`, so it can never drift
   into a stale hand-copied changelog — and it only shrinks when a caveat is
   **fixed and deleted**, never by rewording.

**Strict Coverage is the burn-down metric.** An area's Strict Coverage = its ✅
features carrying zero caveats; the overall figure (top of the generated section)
goes up only when real work lands. That number going *down* over time is the
alarm that we're documenting instead of fixing.

**House rules for this hand-authored top — please keep them. Ignoring them has cost us real bugs:**

- **Done? Delete the line.** Don't rewrite it as "shipped ✅", don't keep it "for
  context". Finished work lives in git history and the ADRs — never here, and not
  narrated in `docs/status/` either (those are delivery checks + caveats, not a
  changelog).
- **Finishing something spawned more work?** Add one short line for the new work,
  delete the old one. A sentence is plenty.
- **Don't take a line here at face value.** These are terse reminders written in a
  hurry and they drift out of date. Before you act on one, open the code and the
  linked TDD/status page and confirm what's actually true. People have shipped
  bugs by trusting a stale backlog line — read first.
- **Keep the prose out.** The "why" and the design live in `docs/tdd/`; the
  current state lives in `docs/status/`. Here, just say what's open, in as few
  words as it takes to find it.

The exhaustive per-caveat list is the generated section at the bottom of this
file; the exhaustive list of every unfinished TDD is the generated table in
[docs/status/README.md](status/README.md).

---

## 1. Highest leverage — do these first

- Conformance headless DOM shim (TDD-00204 Track 5): jsdom tier — element tree,
  events, parser — unlocks the ~20–25K WPT testharness `.html` documents; own TDD
  before code. Plus TS script-mode global merging (top multi-file false-reject).

- **`http.createServer` remaining edges** — custom `IncomingMessage`/
  `ServerResponse` subclass rejected (wants Streams synthetic-root `extends`,
  ADR-00393). HTTP/2 request/response body streaming — body arrives as one chunk
  at `END_STREAM`, no `res.write` flow control (TDD-00218).
- **D1 dynamic object model** (TDD-00155) — Stage 6 residue: method-carrying
  classes into `any`, aliased-binding widening (`const a: any = o`),
  primitive-member dispatch through `any`, `Object.values`/`entries` on dynamic
  objects. Broader: property add/delete on typed structs, full `Proxy` traps,
  well-known symbols as dispatch. Adjacent: TDD-00068. Perf follow-up: TDD-00220.
- **C FFI residue** (TDD-00164) — `using`/`[Symbol.dispose]` disposal, the Windows
  `LoadLibrary` shim, `close()`-invalidates-callbacks. Beyond-Node: TDD-00190.

## 2. Windows port

MacOS/Ubuntu are the main dev machines; the aim is to minimize return trips to the Windows
laptop. Cross-platform work is driven from Mac/Linux (Docker + CI), but anything
that genuinely needs a native Windows box to complete or verify should be
finished while on it — don't defer such work into another comeback.

- **IOCP reactor remaining stages** (TDD-00183, Stage 1 done): Stage 2 overlapped
  sockets (`WSARecv`/`AcceptEx` — removes the last poll slice, the 384-socket
  `select()` cap, `dup2` aliasing); Stage 3 refcounted descriptions; Stage 4
  hi-res timers; fs thread pool onto the port. Plus `cluster` round-robin
  (TDD-00177/00105, 3 skipped tests).
- **Deferred edges (low value / untestable from a dev box):** broken-pipe write
  codes + the TLS-BIO error on long-lived pub/sub, named pipes for `net`,
  non-ASCII console *input*, `readlink` UNC / `chdir` drive vars (all TDD-00180).
- **Out of scope:** `--static` full-static, `-crypto=commoncrypto`, ASan/UBSan.

## 3. Node / runtime surface (mostly small, incremental)

- **fs options residue** — no non-`utf8` encodings; `readFileSync` with no
  encoding returns a string, not a `Buffer`; no `readdirSync` `encoding:'buffer'`;
  `COPYFILE_FICLONE` a no-op.
  Genuinely-open edges with a reason: `rmSync` retries, `birthtimeMs` on Linux,
  the `fdatasync` distinction.
- **Streams** (TDD-00132) — no BYOB/byte controllers; string chunks default; no
  options-form `transform`; a sink `write(chunk, enc, cb)` can't defer completion
  past the call (V1); `extends Transform<In,Out>` parse error;
  `ReadableStream.from()` arrays only.
- **WebSocket** — binary `ev.data` NUL-truncates (typed `string`; use
  `ev.dataBytes()`); `.close()` doesn't await the peer echo.
- **net / dgram / dns** — `net.Socket` `setEncoding`/`ref`/`unref`/`pause`/
  `resume`/`setTimeout` are no-ops; `dgram` udp4-only + `.on('message')` only;
  `dns` absent.
- **child_process** — sync listener dispatch, one listener per event, arrow-only,
  no `removeListener`; `fork` self-fork only; narrow `env`/`timeout`/`stdio`.
- **process / os / console** — no `'unhandledRejection'`;
  `memoryUsage().external`/`arrayBuffers` stay 0; `process.stdin` is flowing-mode
  + string-chunk only (no `.pause`/`.resume`/`.read`/`'readable'`);
  `console.trace()` / `Error.stack` need a runtime call-stack (TDD-00188);
  `node:tty` module surface absent.
- **Partially-done modules** — `perf_hooks` `PerformanceObserver` entry types
  (TDD-00166); `async_hooks` `.then`/listener propagation, `snapshot()`,
  exception-safe `run` (TDD-00168); the remaining Web-global module specifiers
  (TDD-00165); `node:sqlite` dynamic-row `SELECT *`, `db.aggregate()`, error
  `.code`, lazy `iterate()` (TDD-00151); `klain:webview` multi-window
  (TDD-00142, see §6).
- URL/URLSearchParams (TDD-00203): (b) non-special-scheme URLs
  (`new URL('foo:bar')`) — libcurl rejects them, needs a hand-written WHATWG
  parser; (c) IDN of a non-ASCII domain on Mac needs libcurl+libidn2 (platform).
- **Other WPT-surfaced Web-API gaps** (`-suite wpt`, ADR-00871): `instanceof`
  against the ambient `AbortSignal` class is unsupported.
- **Node interpreter re-exec** — a test that re-runs itself via
  `spawnSync(process.execPath, ['-p'/'-e', <expr>])` (and Node-only CLI flags
  like `--max-http-header-size`) cannot pass: a compiled binary is not the Node
  interpreter and can't eval a runtime source string (same limit as `vm`/eval).
  Such invocations now exit cleanly with a nonzero status + diagnostic instead
  of re-running `main` and fork-bombing (ADR-00970). Making them *pass* would
  need per-flag runtime support and, for literal `-p`/`-e` expressions,
  compiling the expression as an argv-dispatched entry point — a TDD-scale
  feature, deferred.
- **Not started** — `vm` (TDD-00046, gated on an embedded JS engine), `domain`,
  `string_decoder`, `util/types`, `assert/strict`, `dns/promises`,
  `readline/promises`, `timers/promises`, `stream/consumers`.

## 4. Core language / TypeScript (mostly small)

- `eval` / `Function(string)` / `vm` / `repl` — embedded engine, TDD-00046.
  Nothing built.
- Lazy Iterator/AsyncIterator protocol — everything materializes (ADR-00057);
  most "not lazy" caveats trace back here.
- RegExp `u`/`v`/`y`/`d` accepted-not-implemented; no `\u{…}`/`\p{…}`; `.exec`/
  `.match` missing `index`/`input`/`groups`; eager `.matchAll`.
- `Intl.*` (no ICU); `Temporal`; `String.normalize()`; `Reflect.apply`/`construct`.
- Spread into fixed-arity / variadic builtins (TDD-00106); `setImmediate` ==
  `setTimeout(0)`.
- Faithful async rejection values (TDD-00169); nested-fn hoisting / generator
  capture (TDD-00129); generalized destructuring (TDD-00065); decorator
  class-replacement + static-field (TDD-00161).
- Union-element arrays (TDD-00200): a *constrained union* element (`(A | B)[]`)
  is rejected (element-level union checking unwired, TDD-00043); a statically-typed
  object/array *value* boxed into an `any[]` element is type-erased — deep
  `JSON.stringify` of it throws (object *literals* are fine).
- Honor `--unhandled-rejections=none`: an unhandled rejection still stringifies
  the rejection value (Node never touches it) — don't eagerly coerce it.
- A thrown plain object's own fields aren't readable after catch (`throw {x:1}`;
  `e.x`) — needs D1 runtime object shape (TDD-00155 Stage 6). Primitives + Errors
  are faithful.
- Dynamic `import()` beyond the eager V1 (TDD-00055); the `-compat`
  per-divergence flags (TDD-00075); `any` residues (TDD-00162).
- `--no-any` strict-lane flag (TDD-00209): Stages 1–2 ship (annotation + inferred
  var-decl `any` rejected); left is value-level `any` in other positions
  (inferred-`any` return, nested any sub-expression).
- `libbf` (MIT) as a third selectable `-bigint` backend alongside
  libtommath/gmp.
- **WebCrypto `crypto.subtle`** — ~7 algorithm ops still "not implemented"; heavy
  format/curve restrictions.
- **Cross-cutting roots (high leverage):** array length-mutation doesn't
  propagate when an object-field / element / HOF-callback array is passed into a
  *callee* (within-scope field aliasing works — TDD-00127);
  nested-array element rejection in `.sort`/`.indexOf`/`.includes`/
  `Object.groupBy` (ADR-00152); `.buffer` absent on TypedArrays, views don't
  track `resize` (ADR-00494/00564). Named-array identity is lost through any
  slot: `arr[0] === namedArray`, and an array-keyed `Map`/`Set`'s
  `keys()[i] === namedArray` (ADR-00948), read `false` (objects keep identity).

## 5. Perf / GC / infra

- Escape analysis / stack allocation (TDD-00134; default-gate TDD-00174) — **the
  dominant native perf gap**: every object literal heap-allocates today.
- Deep reclamation under `-mm=auto` (TDD-00175); precise/moving Immix GC
  (TDD-00135); array amortized-growth `cap` (ADR-00517).
- DWARF debug symbols (TDD-00073); richer diagnostics / strict error-matching
  (TDD-00072); `@readonly`/`@pure` enforcement (TDD-00126/00128).
- Conformance infra: the WPT slice (TDD-00082); statusgen deriving the website
  reference badges from the status source instead of hand-mirroring (TDD-00145).
- Docker builder images (TDD-00193) — GHCR toolchain + sfos-aarch64 images for
  install-free builds and reusable CI lanes (backs the Sailfish CI lane).

## 6. Deliberately deprioritized (later)

- Leaf pseudo-APIs as end-game conformance-unblockers: IndexedDB (TDD-00011);
  Notifications / `localStorage` / Clipboard / Geolocation (TDD-00171); Gamepad
  (TDD-00170); Canvas-2D; the File API family (TDD-00172).
- The TDD-00147 Android port.
- Native desktop GUI — Qt/QML for KDE-Linux + Sailfish Silica (TDD-00192,
  superseding the TDD-00032 placeholder).
- The alt-webview CEF/Qt shims (TDD-00144) + multi-window from TDD-00142.
- Native platform capabilities via C++ shims (TDD-00194) — web-standard proxies
  (Notifications first, per-platform backends) + opt-in `klain:sailfish`.
- The TUI-framework roadmap (TDD-00150).
- `TextDecoder` non-UTF-8 (TDD-00034).
- The `klmpm` package manager (TDD-00054); npm/`node_modules` interop (TDD-00053).
- An alt Go `fetch` backend (TDD-00003).
- Self-hosting (TDD-00124) with its `klain:` module set (TDD-00189).
- `klain:ffi` beyond-Node FFI (TDD-00190).

Reference docs, not queued work: the WASM target (TDD-00048), the Pico (TDD-00036)
and Raspberry Pi (TDD-00045) targets, Immix (TDD-00135).

<!-- STATUS-CAVEATS:BEGIN — generated by `make status` from docs/status/data/*.json; do not edit below this line -->

## Every open issue — the full caveat index

Generated by `make status` from `docs/status/data/*.json`. **Every line here is an issue to tackle.** A missing feature and a behavioural divergence both break code ported from Node/TS/JS, so both count. **Strict Coverage** of an area = the ✅ features carrying zero caveats; the number rises only when a caveat is *fixed and deleted*, never by rewording. Fix a caveat in the status data and it disappears from here; do not edit below the marker by hand.

**Total: 663 caveats · overall Strict Coverage 306/612 (50%).** Areas below are ordered worst Strict Coverage first.

### Concurrency (Workers) — Strict 0/4 (0%) · 24 caveats — [Concurrency (Workers)](status/CONCURRENCY-WORKERS.md)
- `Worker` — One listener per event, arrow-function literals only
- `Worker` — One message type per direction, declared by annotation
- `Worker` — Payloads must be structured-clone-safe; no arrays through `onmessage`/`e.data`
- `Worker` — `'error'` carries the message string, not an Error object; no `self.onerror`
- `Worker` — `terminate()` is cooperative, keeps queued messages, always exits 1
- `Worker` — Worker path must be a string literal; a worker module can't also be imported, can't spawn workers, and its named functions can't read its own top-level bindings
- `Worker` — `-mm=manual` leaks per message; no combining with `http.listen({workers:N})`
- `BroadcastChannel` — Channel name must be a string literal; one message type per channel name, program-wide (first `postMessage` or annotated `onmessage` fixes it)
- `BroadcastChannel` — `onmessage` property only (arrow-function literal, `(e: { data: T })`); no `addEventListener`, no `'messageerror'`
- `BroadcastChannel` — Array payloads not supported through `onmessage`
- `BroadcastChannel` — `close()` leaks the endpoint's two pipe fds by design (closes a race against an in-flight post)
- `BroadcastChannel` — `-mm=manual` leaks per delivery (payload ownership passes to the listener)
- `MessageChannel` / `MessagePort` — Message type fixed at construction (`new MessageChannel<T>()`, default `number`), symmetric for both ports
- `MessageChannel` / `MessagePort` — A port crosses a worker boundary only whole — as `workerData` or as the entire `postMessage` payload; the `MessageChannel` pair box itself never crosses
- `MessageChannel` / `MessagePort` — A sent port is **shared, not neutered** (unlike Node's transfer): the sender must stop using it
- `MessageChannel` / `MessagePort` — `onmessage` property only (arrow literal); no `on('message')`, no `start()`/`'close'` event, no array payloads via `onmessage`
- `MessageChannel` / `MessagePort` — `close()` leaks two pipe fds; `-mm=manual` leaks per message
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — Channel ops are synchronous blocking (`ch.send(v)`/`ch.receive()`), not `await`; element is a fixed 8-byte slot (number/string/object/boolean)
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — A `select` recv handler receives the value but not the closed flag (use a `for (const v of ch)` range, which ends on close, to detect it)
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — Fixed goroutine stacks (default 256 KiB, tunable via `KLAINSYNC_STACK_KB`), no overflow guard — deep recursion is UB; growable/moving stacks gated on precise stack maps
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — Blocking-syscall P-handoff is a bounded sysmon-driven rescue-M approximation, not full `entersyscall`/`exitsyscall`
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — A nil (never-constructed) channel isn't expressible — channels are always `new Channel`
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — Per-iteration capture of a loop `let` directly in a goroutine closure is a pre-existing general closure limitation — pass the value as a function parameter (a `spawn(work)` helper) instead
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — `send` after `close` and `close` of a closed channel abort the process (Go-panic parity); native-only (compile error under plain `tsc`/Node)

### Memory Management — Strict 0/5 (0%) · 22 caveats — [Memory Management](status/MEMORY-MANAGEMENT.md)
- `manual` (default) — Never frees on its own — a program's footprint grows monotonically with runtime unless the programmer calls `Memory.free(x)` by hand
- `manual` (default) — Same footguns as C, including that a string *literal* is a compile-time global constant, not `malloc`'d, so freeing one crashes exactly like C's `free("literal")`
- `gc` — Requires `bdw-gc`/`libgc` installed at build time (`brew install bdw-gc` / `apt-get install libgc-dev` / `apk add gc-dev`)
- `gc` — Not the default (`-mm=manual` is)
- `gc` — Conservative (Boehm) collection is optimizer-dependent: on x86-64 `-O2`, a referent orphaned in straight-line code can stay pinned indefinitely by a stale stack-slot copy, so its WeakRef/FinalizationRegistry death signal never fires ([ADR-00717](adr/ADR-00717.md))
- `auto` — Escape analysis is deliberately conservative: a value returned, stored, aliased, spread, passed to a non-whitelisted call, captured by a closure, or alive across `await` is never freed (leaks, as in `manual`); the whitelists start minimal
- `auto` — Reassignment frees the overwritten value only via an owning-fresh RHS (`s += …`, `s = a + b`, an interpolating template); any other reassignment shape disqualifies the binding
- `auto` — Deep (recursive) reclamation covers **typed `JSON.parse`/`res.json()` trees only** ([TDD-00175](tdd/TDD-00175.md) Stage 1, [ADR-00716](adr/ADR-00716.md)) — graphs built from literals or user calls, mid-block garbage (rbtree-class), and closure graphs still leak beyond the top-level free (Stages 2–3); an interior-pointer extraction into a plain-`=` binding, a graph mutation, iteration, or a method call on the tree downgrades that binding to the shallow free
- `auto` — No per-branch last-use placement for `@owned` — a last use inside `if`/loop frees at the join/after the loop; any use inside `try` falls back to block exit
- `auto` — Thrown paths do not free (a same-function `catch` could resume with the value live) — they leak instead
- `auto` — `@owned` parameters: direct calls to statically-resolved function declarations only; using such a function as a value is a compile error
- `auto` — Unsupported in `async`/generator functions (values may live across suspension points)
- `auto` — `-compat=js` dynamic objects and class instances are never freed (no free routine / methods could retain `this`)
- `-optimize-memory` (orthogonal to `-mm`) — `Map`/`Set` (runtime-created), plain arrays' growable data buffers, and async closures stay heap — stack allocation covers only non-escaping object/closure/tuple literals and class instances
- `-optimize-memory` (orthogonal to `-mm`) — By-value tuple returns cover only ≤2-plain-scalar-field tuples on non-async top-level functions never used as values; larger tuples, aggregate-slot fields (array/nullable/any), async functions, generics, and function values keep the pointer ABI, and only the direct `const [v, err] = f()` consumer is allocation-free
- `-optimize-memory` (orthogonal to `-mm`) — A class instance stays heap-bound unless the whole class passes a conservative `this`-audit — any method returning/storing/passing `this`, extracting a method as a value, capturing `this` in a nested closure, `return this` chaining, or decorators disqualifies every instance
- `-optimize-memory` (orthogonal to `-mm`) — Anything not provably block-local stays heap (same conservative escape analysis as `auto`); explicit `@free`/`@owned` keep their heap+free meaning
- `-optimize-memory` (orthogonal to `-mm`) — Off by default while the analysis matures
- `/** @value */` flat value-type arrays — V1 surface: array-literal construction (no spread elements), index read/write, `.length`, `for...of`, and `.push` — every other array operation (HOFs, `sort`, `slice`, spread, destructuring, other mutators) is a compile-time rejection naming the supported set
- `/** @value */` flat value-type arrays — Element type must be a fixed-shape plain object (interface/object-literal shape) — classes, tuples, nested arrays, and dynamic values are rejected
- `/** @value */` flat value-type arrays — Function-local bindings only — a `@value` array read by other functions (global promotion) is rejected
- `/** @value */` flat value-type arrays — `.push` growth may realloc-move the buffer, invalidating previously-taken element views (`const v = arr[i]`) — the documented Vec/C++-`vector` contract; re-index after a push

### URL — Strict 0/10 (0%) · 16 caveats — [URL](status/URL.md)
- `URL` — libcurl-backed, so a non-special scheme is rejected where WHATWG accepts it (`new URL('foo:bar')` throws; Node: valid with `pathname` `'bar'`) — faithful non-special-scheme parsing needs a hand-written WHATWG parser rather than libcurl ([TDD-00203](tdd/TDD-00203.md)).
- `URLSearchParams` — Spreading a `URLSearchParams` directly (`[...params]`) yields an empty array — the same pre-existing limitation as `[...map]`; use `[...params.entries()]` (which works). Not URLSearchParams-specific.
- `URLPattern` — Object-literal init only, over `protocol`/`hostname`/`port`/`pathname`/`search`/`hash`/`username`/`password` ([ADR-00585](adr/ADR-00585.md)) — no constructor-string form, no `baseURL`
- `URLPattern` — Pattern grammar is literals, `\` escapes, `*`, `:name`, and `:name?` — `{}` groups, `+` modifiers, and inline `(regex)` groups throw a `TypeError` at construction
- `URLPattern` — `.exec()` returns a merged `Map<string, string> \| null` of every named group across components (an unset optional group is absent), not the spec's per-component `URLPatternResult` object — needs the deferred dynamic object model ([TDD-00068](tdd/TDD-00068.md))
- `URLPattern` — `*` wildcards aren't exposed as numbered groups; no `hasRegExpGroups`
- `url.parse(urlString)` (legacy) — Absolute URLs only — a malformed/relative input throws `Invalid URL` (like `new URL()`), rather than Node's never-throw lenient object ([ADR-00669](adr/ADR-00669.md)/[TDD-00165](tdd/TDD-00165.md) Stage 4)
- `url.parse(urlString)` (legacy) — An absent component is `""`, not Node's `null` (same representation as the WHATWG `URL` object above)
- `url.parse(urlString)` (legacy) — `slashes` and the `parseQueryString`-object form of `query` are not produced yet; `url.format`/`fileURLToPath` and the other legacy helpers are separate follow-ons
- `url.format(urlObject)` (legacy) — The `options` argument (`auth`/`fragment`/`search`/`unicode` toggles) is not supported; components are taken verbatim, no re-encoding ([ADR-00670](adr/ADR-00670.md))
- `url.format(urlObject)` (legacy) — Reconstructs `protocol // [auth@] host pathname (search|?query) hash`; a WHATWG `URL` argument serializes to its `href`
- `url.fileURLToPath(url)` (POSIX) — POSIX only; the `options` (`windows`) argument is unsupported, and a non-`localhost` host is ignored rather than rejected ([ADR-00671](adr/ADR-00671.md))
- `url.pathToFileURL(path)` (POSIX) — POSIX only; the `options` argument is unsupported ([ADR-00671](adr/ADR-00671.md))
- `url.resolve(from, to)` (legacy) — Absolute bases only — a scheme-less/malformed base throws `Invalid URL`, the same leniency gap as `url.parse` ([ADR-00672](adr/ADR-00672.md))
- `url.urlToHttpOptions(url)` — Expects a WHATWG `URL`; a legacy `Url`/plain object isn't accepted ([ADR-00672](adr/ADR-00672.md))
- `url.domainToASCII(domain)` / `url.domainToUnicode(domain)` — IDN conversion of a **non-ASCII** domain requires the libcurl build to include an IDN backend (libidn2) — present on typical Linux, **absent on the Mac build** where a non-ASCII domain returns `""` (Node's own failure contract). ASCII domains pass through everywhere ([ADR-00672](adr/ADR-00672.md))

### Events & Cancellation — Strict 0/3 (0%) · 8 caveats — [Events & Cancellation](status/EVENTS-CANCELLATION.md)
- `EventTarget` / `addEventListener` / `dispatchEvent` — Single-target dispatch — no DOM tree, so no capture/bubble/propagation (`stopPropagation` is a no-op; `stopImmediatePropagation` halts the remaining listeners)
- `EventTarget` / `addEventListener` / `dispatchEvent` — `capture`/`passive`/`signal` options accepted but ignored (`signal` cancellation arrives with `AbortController`)
- `EventTarget` / `addEventListener` / `dispatchEvent` — No `class X extends EventTarget` (built-in `extends` targets are rejected)
- `Event` / `CustomEvent` — The fuller WHATWG properties (`target`/`currentTarget`/`bubbles`/`composed`/`timeStamp`/`eventPhase`/`isTrusted`) are absent — V1 surface is `type`, `defaultPrevented`, `cancelable`, `CustomEvent.detail`
- `Event` / `CustomEvent` — `bubbles` in the `new Event(type, { … })` init is accepted but ignored (single-target dispatch)
- `Event` / `CustomEvent` — `stopPropagation()` is a no-op (single-target dispatch, no capture/bubble tree)
- `AbortController` / `AbortSignal` — A manual `controller.abort(reason)` with a custom reason still makes the awaited fetch throw an `AbortError` DOMException rather than that reason value; DOMException carries no legacy numeric `.code`
- `AbortController` / `AbortSignal` — `AbortSignal.any(signals)` snapshots the input signals at construction — a source that aborts *after* the composite is built does not yet propagate to it (needs listener-closure wiring)

### Other Node.js Core Modules — Strict 1/17 (~6%) · 70 caveats — [Other Node.js Core Modules](status/NODE-CORE-MODULES.md)
- `querystring` — Repeated keys collapse to the last value instead of an array — `.parse('a=1&a=3')` yields `{a:'3'}`, not Node's `{a:['1','3']}`
- `assert` — `equal`/`strictEqual` and `deepEqual`/`deepStrictEqual` (and their `not*` counterparts) are aliases of each other — this compiler's `==` has no implicit coercion to distinguish them
- `assert` — `deepStrictEqual`: Map/Set/class instances, nullable-scalar members, and TypedArrays are a clean compile error (V1)
- `assert` — `.match`/`.doesNotMatch`'s regexp argument must be statically a RegExp ([ADR-00428](adr/ADR-00428.md))
- `assert` — `.throws` only checks that *something* was thrown, not the thrown value's type/message
- `assert` — No `assert.rejects`/`doesNotReject` (needs await-a-rejection machinery) and no `AssertionError` class ([ADR-00499](adr/ADR-00499.md))
- `test` / `node:test` — **Run-at-registration**: top-level code after a `test()` runs after it here, before it in Node. See [TDD-00140](tdd/TDD-00140.md), [ADR-00419](adr/ADR-00419.md)
- `test` / `node:test` — `beforeEach`/`afterEach` are single slots (last wins) and also fire for subtests
- `test` / `node:test` — No `mock.*`, `t.plan`, `run()`, concurrency, or snapshots
- `test` / `node:test` — `mustCall` rejects rest-parameter callbacks (a rest tail is not a fixed ABI slot list) ([ADR-00496](adr/ADR-00496.md))
- `test` / `node:test` — `mustNotCall()` returns a `() => void`; using it where a non-zero-arity callback is expected is a type mismatch
- `test` / `node:test` — `mustSucceed` is count-only (the "err is falsy" check is deferred); `expectWarning` runs the thunk but doesn't capture warnings
- `util` — No `.promisify` — the callback builtins (`zlib.gzip`, `child_process.exec`) are name-dispatched calls, not first-class function values to wrap; importing `promisify` fails cleanly ([ADR-00325](adr/ADR-00325.md))
- `util` — `.format` requires a string-**literal** format (compile-time walk); a runtime format string is a compile error
- `util` — `.inspect`'s options object is accepted but ignored (no `depth`/`colors`)
- `net` — `net.connect` uses a **blocking** `connect()` — a slow/unreachable host blocks the event loop until the OS connect timeout ([ADR-00328](adr/ADR-00328.md))
- `net` — A connect failure **throws** a catchable `Error` rather than emitting an async `'error'` event
- `net` — Server binds IPv4 `0.0.0.0` only — no `host`/`path`/options object on `listen`
- `net` — IPv4-native bind: an accepted socket reports `127.0.0.1` where Node's dual-stack default reports `::ffff:127.0.0.1`, and the server reports `IPv4`/`0.0.0.0` not `IPv6`/`::`
- `net` — IPC `{ path }` Unix-domain socket: Linux path-layout unverified (Mac-verified only) ([ADR-00588](adr/ADR-00588.md))
- `net` — No `'error'`/`'close'`/`'connect'` events on server or socket yet
- `net` — `setEncoding`/`ref`/`unref`/`pause`/`resume`/`setTimeout` are accepted but no-ops
- `net` — No back-pressure: `.write` never returns `false` and there's no `'drain'` event
- `net` — Listeners must be arrow/function-expression literals
- `net` — `net.Server`/`net.Socket` are not exposed as constructible classes
- `net` — Socket `'close'` fires with no `hadError` argument, and `'error'` listeners stay a rejection — the runtime's failure paths throw instead of emitting ([ADR-00501](adr/ADR-00501.md))
- `dgram` — `udp4` only — no `udp6`
- `dgram` — No `'listening'`/`'error'` events, options object, or multicast
- `dgram` — Message listeners must be arrow/function-expression literals
- `dgram` — `socket.send(msg, port, host)` positional form only — no `(msg, offset, length, port, address, cb)` form; no `.setTTL()`/membership methods
- `tls` — **Blocking** TCP connect + TLS handshake on both sides (a slow peer stalls the loop), the same simplification `net.connect` makes
- `tls` — OpenSSL-only (libssl) — a `-crypto=commoncrypto` build using `tls` is a clean compile error (Mac's CommonCrypto has no TLS API)
- `tls` — Client options `{ rejectUnauthorized }` only — no client-cert / ALPN / `servername` override (SNI = the host)
- `tls` — Server `{ cert, key }` are PEM **strings** only — one cert per server, no chain/passphrase, no `requestCert`
- `dns` — `dns.resolve4`/`resolve` use `getaddrinfo` (the OS resolver), not real DNS `A` queries — no `resolve6`/`resolveMx`/`resolveTxt`/`reverse`
- `dns` — IPv4 (`AF_INET`) only; `family` is always 4
- `dns` — The callback forms fire synchronously, not on a later event-loop tick
- `dns` — Failure passes a generic `Error` (`getaddrinfo ENOTFOUND`), not a `code`-tagged Node error
- `zlib` — The callback form fires synchronously, not deferred to the next event-loop tick
- `zlib` — No Brotli (`brotliCompress*`) — it would pull in `-lbrotli`, a new system dependency this binding deliberately avoids
- `zlib` — No class/stream forms (`createGzip()`, `zlib.Gzip`, …) — the WHATWG `CompressionStream`/`DecompressionStream` cover the streaming case ([STREAMS.md](STREAMS.md))
- `zlib` — `{ level }` is the only supported option and must be a compile-time constant
- `zlib` — A decompressed result needs an explicit `: Buffer` annotation to call `.toString()` — the return type infers as a bare `Uint8Array`, so `zlib.gunzipSync(g).toString()` unannotated is a compile error
- `diagnostics_channel` — Messages are **strings** (serialize with `JSON.stringify`) — a typed subset can't hand arbitrary object shapes to unknown subscribers
- `diagnostics_channel` — Subscriber params must be string-typed (an untyped pre-wrapped subscriber is a clean rejection, never silent reinterpretation)
- `diagnostics_channel` — `tracingChannel` and the store bindings (`bindStore`/`runStores`) are clean rejections; a Channel handle isn't module-global-promotable (capture it in a closure, not a named top-level function) ([ADR-00420](adr/ADR-00420.md))
- `http` client — `.abort()`/`.destroy()` cancel only a not-yet-fired request — an in-flight transfer isn't torn down
- `http` client — `.on('error')` is accepted but never fired — transport failures throw instead
- `http` client — `http.Agent`/`https.Agent` are inert — pool options are validated but configure nothing (one connection per request), `.destroy()` is a no-op, `.sockets`/`.requests` reject cleanly ([ADR-00432](adr/ADR-00432.md))
- `http` client — `headers` is literal-form-only; request-body chunks are strings only (`req.write()`/`req.end(body)`)
- `http` client — `agent` is accepted and ignored — one connection per request (Node's `agent: false`), no pooling
- `https` — `https.createServer` is HTTPS/1.1 only — an http/1.1-only ALPN forces even an h2-capable client to 1.1
- `https` — Inherits `http.createServer`'s V1 `res` subset and one-request-per-connection (`Connection: close`) posture
- `https` — Same V1 client surface and caveats as `http.get`/`request` ([TDD-00138](tdd/TDD-00138.md))
- `cluster` — Messages are strings; one listener per event; `'online'` fires from a queued microtask (the worker is exec'd by then), not a wire handshake
- `cluster` — No `cluster.on(...)` (cluster-level events), `cluster.workers` map, `cluster.disconnect()`, `setupPrimary()`(`setupMaster`)/`settings` ([TDD-00105](tdd/TDD-00105.md)/[ADR-00331](adr/ADR-00331.md))
- `cluster` — On Darwin, `SO_REUSEPORT` accept distribution is uneven (often one worker) — correctness holds, balancing differs from Linux's kernel load-balancing
- `cluster` — Can't be combined with Worker threads (same restriction as `http.listen({workers})`)
- `http2` **module** — `http2.createServer`/`createSecureServer` `(req, res)` handlers don't receive the request body — `req.on('data')`/`req.stream()` isn't wired to the h2 body (the `klain:http` `http.listen` h2 path delivers it as one buffered `req.body` chunk; TDD-00218)
- `http2` **module** — `stream.close()`/`setEncoding`/`resume`/`pause` are accepted no-ops; responses are written when the handler returns (no mid-stream RST/flow control)
- `http2` **module** — Client `http2.connect` is h2c only — a TLS authority throws
- `http2` **module** — Client requests are body-less (END_STREAM at submit) — `req.write`/`end(body)` are clean rejections
- `http2` **module** — Client response `':status'` arrives as a *string* in the headers map (Node types it as a number); session/stream `'error'`/`'close'` listeners are accepted no-ops
- `http2` **module** — `getDefaultSettings`/`getPackedSettings`/`getUnpackedSettings`: the invalid-value `RangeError` taxonomy is not modeled
- `http2` **module** — `createSecureServer` h2 connections are served one-at-a-time (the TLS drive is blocking in the accept path)
- `http2` **module** — Server/client push streams are unsupported ([TDD-00139](tdd/TDD-00139.md))
- `async_hooks` — `AsyncLocalStorage.snapshot()` is a clean rejection — its runner is fully generic (parameter/return types vary per call site), needing generic first-class closures this compiler doesn't have
- `async_hooks` — `AsyncResource.runInAsyncScope(fn, thisArg, …)` accepts `thisArg` but does not bind it (closures carry no dynamic `this`); a `run(...)` callback that throws does not restore the frame (exception-safe restore is a follow-up)
- `async_hooks` — `.then`/`.catch` reaction callbacks and EventEmitter listeners do not yet propagate context (only `await`, task-spawn, and timers do)
- `async_hooks` — The low-level `createHook`/`executionAsyncId`/`triggerAsyncId`/async-id lifecycle is out of scope — a compiled AOT binary has no V8 promise-hooks or GC-tracked async-ids (a faithful impossibility), so those stay clean rejections

### Cryptography (Web Crypto API) — Strict 1/10 (10%) · 20 caveats — [Cryptography (Web Crypto API)](status/WEB-CRYPTO.md)
- `crypto.getRandomValues(view)` — A whole `ArrayBuffer` is accepted as a deliberate extension (real JS throws `TypeMismatchError` — only integer TypedArrays are spec-legal) ([ADR-00554](adr/ADR-00554.md))
- `crypto.subtle.digest(algo, data)` — Shared subtle caveats above only
- `crypto.subtle.encrypt` / `.decrypt` — On `-crypto=commoncrypto` only: a non-empty RSA-OAEP `label` throws `NotSupportedError` (SecKey has no label parameter)
- `crypto.subtle.encrypt` / `.decrypt` — Shared subtle caveats above
- `crypto.subtle.sign` / `.verify` — On `-crypto=commoncrypto` only: RSA-PSS `saltLength` must equal the hash length or `NotSupportedError` is thrown (SecKey fixes salt = digest size)
- `crypto.subtle.sign` / `.verify` — Shared subtle caveats above
- `crypto.subtle.generateKey` — `publicExponent` restricted to 65537 (the literal `new Uint8Array([1, 0, 1])`, or omit it)
- `crypto.subtle.generateKey` — Shared subtle caveats above
- `crypto.subtle.importKey` / `.exportKey` — `jwk` is surfaced as a `Map<string,string>` (this compiler has no dynamic object model), key-material members only (`kty`/`k`/`crv`/`n`/`e`/`d`/…) — no `key_ops`/`ext`/`alg` members; import never validates `kty`
- `crypto.subtle.importKey` / `.exportKey` — `CryptoKey.algorithm`/`.usages` property reads not implemented (`.type`/`.extractable` work)
- `crypto.subtle.importKey` / `.exportKey` — `raw` export of a non-EC asymmetric key isn't rejected (yields the stored DER)
- `crypto.subtle.importKey` / `.exportKey` — Shared subtle caveats above
- Node `crypto` module: `generateKeyPair`/`generateKeyPairSync`, `randomBytes` (+ `randomUUID`/`getRandomValues` re-exports) — `generateKeyPair` requires both encodings as `{ type: 'spki'/'pkcs8', format: 'pem' }` — KeyObject results, `pkcs1`/`sec1`/`der` formats, and `cipher`/`passphrase` are clean rejections; types are `'rsa'` (modulusLength, 65537 exponent) and `'ec'` (P-256/384/521) only — no `rsa-pss`/`dsa`/`dh`/`ed25519`
- Node `crypto` module: `generateKeyPair`/`generateKeyPairSync`, `randomBytes` (+ `randomUUID`/`getRandomValues` re-exports) — Generation is synchronous — the async form fires its callback inline (async-shaped, not offloaded)
- Node `crypto` module: `generateKeyPair`/`generateKeyPairSync`, `randomBytes` (+ `randomUUID`/`getRandomValues` re-exports) — `randomBytes(size, cb)`'s callback fires `cb(null, buf)` synchronously (async-shaped, not offloaded — generation never fails) ([ADR-00590](adr/ADR-00590.md))
- Node `crypto` module: `generateKeyPair`/`generateKeyPairSync`, `randomBytes` (+ `randomUUID`/`getRandomValues` re-exports) — A namespace import's binding covers the module's own members (`generateKeyPair`(`Sync`)/`randomBytes`/`randomUUID`/`getRandomValues`/`createHash`/`createHmac`) — `crypto.subtle` is reached via the ambient global, not the module
- Node `crypto` module: `createHash(algorithm)` / `createHmac(algorithm, key)` → `Hash`/`Hmac` (`.update(data).digest(encoding?)`) — Algorithms limited to `md5`/`sha1`/`sha256`/`sha384`/`sha512` (a string literal); other names are a clean rejection
- Node `crypto` module: `createHash(algorithm)` / `createHmac(algorithm, key)` → `Hash`/`Hmac` (`.update(data).digest(encoding?)`) — `.digest('latin1')`/`'binary'` share the UTF-8-only string caveat for non-ASCII bytes (same as `Buffer.toString('latin1')` — [TDD-00034](tdd/TDD-00034.md)); encodings other than `hex`/`base64`/`base64url`/`latin1`/`binary` are clean rejections
- `crypto.subtle.deriveKey` / `.deriveBits` — The spec's extractable=false requirement on PBKDF2/HKDF base keys is not enforced
- `crypto.subtle.deriveKey` / `.deriveBits` — Shared subtle caveats above

### Streams API — Strict 1/9 (~11%) · 38 caveats — [Streams API](status/STREAMS.md)
- `ReadableStream` — No BYOB readers / byte controllers (`type: 'bytes'` rejected at compile time)
- `ReadableStream` — `ReadableStream.from()` accepts arrays only, not async iterables
- `ReadableStream` — Source callbacks must be arrow/function-expression/method-shorthand literals; a cancel callback can't take a reason parameter; *async* method shorthand (`{ async pull(c) {…} }`) doesn't parse — use `pull: async (c) => …`
- `ReadableStream` — `read()` on a closed stream whose chunk type is an **array/tuple** (`ReadableStream<number[]>`) yields the chunk's zero-shaped value, not `undefined` (an array slot has no spare absent state — [ADR-00246](adr/ADR-00246.md))
- `ReadableStream` — `releaseLock()` leaves queued pending reads pending instead of rejecting them
- `ReadableStream` — `values()` returns a reader usable in `for await` only (no standalone `.next()`)
- `ReadableStream` — In the no-fiber tier, `await` of an already-settled `read()` skips the microtask drain, so a pending pull can run one tick later than real JS
- `ReadableStream` — A synchronously-throwing pull/start propagates to the caller instead of erroring the stream
- `ReadableStream` — `tee()` forwards the last cancelling branch's reason instead of composing both; a signal abort of `pipeTo` takes effect at the next chunk boundary
- `WritableStream` — `releaseLock()` doesn't reject a pending-write view
- `WritableStream` — A synchronously-throwing sink write propagates instead of erroring the stream (an async rejection is handled per spec)
- `WritableStream` — A sink abort callback can't take a reason parameter; no transformer/sink `start` promise-vs-write interlock beyond the started flag
- `TransformStream` — No transformer `start` callback
- `TransformStream` — Cancelling the readable side doesn't error the writable side
- `TransformStream` — An identity transform (no callback) requires matching I/O chunk representations
- `CompressionStream` / `DecompressionStream` — The format argument must be a string literal ("gzip", "deflate", "deflate-raw") — a runtime-computed format string is a compile error
- `CompressionStream` / `DecompressionStream` — Chunks are `Uint8Array` only (no ArrayBuffer/DataView BufferSource variants)
- `Blob.stream()` — Delivers the blob's bytes as a **single** `Uint8Array` chunk (the whole blob), not incrementally chunked as a real byte source would be
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — All five constructors default to **string chunks** (`<T>` overrides)
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — `new PassThrough()` takes `highWaterMark`/`objectMode` options only; `new Duplex(...)` accepts only `read`/`write`/`final` callbacks plus the shared options
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — No `_flush` override for class-form `Transform` subclasses
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — The options-form `read`/`write` callback receives the stream as a parameter, not as `this` (object-literal callbacks have no `this` binding); the class form has the real `this`
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — The options-form `write` completion `cb()` can't defer completion past the call — the write is treated as complete when the sink returns (same V1 limit as class-form `_write`)
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — A two-parameter `write(chunk, x)` shape is ambiguous (encoding vs. callback) and rejected; the three-param `write(chunk, encoding, callback)` form's `encoding`/`callback` params must be annotated (`enc: string`, `cb: () => void`)
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — No options-form `transform` callback for a `Transform` (`new Transform({ transform(chunk, enc, cb) {…} })` rejected) — use the class-form `_transform(chunk, enc, cb)` override instead
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — `class X extends Transform<In, Out>` (two type arguments) is a parse error — only `extends Transform<T>` parses in an `extends` clause (the two-arg form still parses in `new Transform<In, Out>(…)`)
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — `objectMode` is accepted but has no effect (chunks are already typed by `<T>`, effectively object-mode)
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — A class-form `super({ highWaterMark })` must be a literal/construction-scope constant — it can't reference a constructor parameter
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — A class-form `_write` completion callback is treated as fired when `_write` returns — it can't defer the write past the call
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — Event names must be string literals; supported events are `data`/`end`/`error`/`close`/`finish`/`drain`
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — Synchronous `.read([size])` ignores `size` (empty → null); `.destroy([err])` drops the error argument (no `'error'` re-emission); `.setEncoding('utf8')` is a no-op on the string-chunk default
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — Event delivery can lag Node's synchronous re-entrancy by a microtask (emissions ride the reaction queue)
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — `fs` `createReadStream`/`createWriteStream` don't return Node streams (surfaces stay WHATWG-based or buffered), unlike the HTTP server's `req`
- `stream.pipeline()` / `.finished()` — The callback forms' callback takes zero parameters or one (the error) — no `(err, value)` extras
- `stream.pipeline()` / `.finished()` — `finished(s, cb)` returns nothing, not Node's cleanup function
- `stream.pipeline()` / `.finished()` — Function-stage pipelines (`pipeline(src, async function* …)`) are not supported — stages must be streams
- `stream.duplexPair()` — String chunks only (no `<T>` form)
- `stream.duplexPair()` — The sides are Duplex *handles* (`write`/`end`/`on`/`pipe`/…), not `Duplex` class instances — no `instanceof Duplex`, `cork`/`uncork`, `.read()`, or `'readable'` events

### Binary Data & Typed Arrays — Strict 1/9 (~11%) · 21 caveats — [Binary Data & Typed Arrays](status/BINARY-DATA-TYPED-ARRAYS.md)
- `ArrayBuffer` — `resize(n)` still requires the `{maxByteLength}` construction option — compile-time rejection otherwise ([ADR-00494](adr/ADR-00494.md), [ADR-00564](adr/ADR-00564.md))
- `ArrayBuffer` — A TypedArray view captures its length at construction and does not length-track a later `resize` — re-`new` the view after resizing (spec auto-tracks a no-explicit-length view)
- `Uint8Array` / `Int8Array` / `Uint16Array` / `Int16Array` / `Uint32Array` / `Int32Array` / `Float32Array` / `Float64Array` — No `.buffer` property (would require every TypedArray, including the "own buffer" form, to carry a back-reference to a real `ArrayBuffer` object — a shape change from the plain `{ptr,i64}` array representation)
- `BigInt64Array` / `BigUint64Array` — The missing `.buffer` property above applies here too
- `BigInt64Array` / `BigUint64Array` — Only an explicit method allow-list is supported — indexing r/w, `.length`/`.byteLength`, `.at`, `.set`, `.subarray`, `.slice`, `.fill`, `.reverse`, for-of, `Atomics.*`; everything else (`.map`/`.filter`/`.reduce`/`.indexOf`/`.sort`/`.join`/iterator objects/…) is a compile-time rejection, never a raw-scalar leak
- `BigInt64Array` / `BigUint64Array` — `Atomics.wait`/`notify` stay `Int32Array`-only
- `Uint8ClampedArray` — The missing `.buffer` property above applies here too
- `Blob` — `.stream()` delivers the whole blob as one `Uint8Array` chunk, not incrementally ([ADR-00341](adr/ADR-00341.md))
- `Blob` — The parts argument is an inline array literal or a `string[]` variable ([ADR-00489](adr/ADR-00489.md)); no `endings` option; the `type` string is stored as-is (no spec lowercasing)
- `Blob` — `.text()` truncates at an embedded null byte (the standard string-boundary caveat)
- `SharedArrayBuffer` — `grow(n)` requires the `{maxByteLength}` construction option (compile-time rejection otherwise, vs the spec's runtime TypeError); the maximum is reserved upfront ([ADR-00494](adr/ADR-00494.md))
- `SharedArrayBuffer` — Crosses a worker boundary whole or as an object field; a TypedArray **view** doesn't cross — send the buffer, re-view on the other side
- `SharedArrayBuffer` — An in-thread closure can capture a view ([TDD-00213](tdd/TDD-00213.md) Stage 3), but a view doesn't cross a worker boundary (previous caveat), so a cross-thread handler still re-views the shared buffer inside the handler
- `Atomics` — `wait` blocks the whole calling thread (event loop and fibers included) — allowed on the main thread (Node posture); no `waitAsync`
- `Atomics` — `wait`/`notify` are `Int32Array`-only (spec minus the BigInt64 half)
- `Atomics` — Index is bounds-unchecked, matching ordinary TypedArray indexing
- Node `Buffer` (`Buffer.from`/`.alloc`/`.toString(encoding)`/`.write`/etc.) — No `utf16le`/`ucs2` encodings (the compiler's strings are UTF-8-native)
- Node `Buffer` (`Buffer.from`/`.alloc`/`.toString(encoding)`/`.write`/etc.) — Encoding arguments must be compile-time string literals
- Node `Buffer` (`Buffer.from`/`.alloc`/`.toString(encoding)`/`.write`/etc.) — `Buffer.from(arrayBuffer)` **copies** (Node views); `Buffer.concat`'s list must be an inline array literal
- Node `Buffer` (`Buffer.from`/`.alloc`/`.toString(encoding)`/`.write`/etc.) — String-valued `.indexOf`/`.includes`/`.lastIndexOf` search the needle's UTF-8 bytes ([ADR-00558](adr/ADR-00558.md)), and `.fill(string, offset?, end?)` repeats the needle's bytes ([ADR-00559](adr/ADR-00559.md)) — all single-argument-plus-range forms (no `byteOffset`/`encoding`); no 4-arg `.write(string, offset, length, encoding)` form; no `.swap16/32/64`, `.toJSON`, `Buffer.of`, `Buffer.isAscii`/`Buffer.isUtf8`, arbitrary-width `readIntLE(offset, byteLength)`, `poolSize`/`allocUnsafeSlow`
- Node `Buffer` (`Buffer.from`/`.alloc`/`.toString(encoding)`/`.write`/etc.) — `.toString()` (utf8/latin1) truncates at an embedded null byte

### Networking — Strict 1/6 (~17%) · 18 caveats — [Networking](status/NETWORKING.md)
- `fetch(url)` / `fetch(url, init)` / `fetch(request)` — Setting a `body` without an explicit `method` sends it as `POST` (libcurl's `CURLOPT_POSTFIELDS` implies `POST` unless overridden); an explicit `method` always wins over that default
- `fetch(url)` / `fetch(url, init)` / `fetch(request)` — A fetch carrying an `AbortSignal` resolves at completion, not headers ([ADR-00299](adr/ADR-00299.md))
- `Response` (`.status`, `.ok`, `.headers`, `.body`, `.text()`, `.json()`, `.arrayBuffer()`) — `.text()`/`.json()` are null-terminated strings — a binary body truncates at the first embedded null byte; use `.arrayBuffer()` for a binary body
- `Response` (`.status`, `.ok`, `.headers`, `.body`, `.text()`, `.json()`, `.arrayBuffer()`) — `.text()` after consuming `.body` returns `""` instead of the spec's already-disturbed TypeError; `Object.keys`/JSON of a Response expose an internal `__kml_pending` field
- `WebSocket` — The client `wss://` handshake is blocking and verifies the cert (SNI on, no `rejectUnauthorized` opt-out) ([TDD-00039](tdd/TDD-00039.md))
- `WebSocket` — A binary message's `ev.data` is the `strlen`-terminated string view (truncates at an embedded NUL) — byte-exact payload via `ev.dataBytes(): ArrayBuffer`; the spec's `ev.data: string | ArrayBuffer` union isn't used (`ArrayBuffer` not yet an allowed union member)
- `WebSocket` — `.close()` on either side doesn't wait for the peer's own echo back before actually closing (a deliberate simplification)
- `WebSocket` — The client connect + handshake runs *synchronously* before the constructor returns; `.onopen`/`.onerror` are deferred to the first event-loop pass after construction rather than fired inline
- `EventSource` — A `retry:`-driven reconnect replays the *full* stored `Last-Event-ID`, not a per-listener value
- `XMLHttpRequest` — No default-async, callback-interleaved mode — `.send()`/`.send(body)` runs to completion then fires every registered callback once, synchronously (no re-entrant-into-already-returned-caller callback machinery to support async)
- `XMLHttpRequest` — `.open(..., async?)`'s `async === true` also blocks — no main-thread event loop to defer `.onload` onto (a documented narrowing, not fake async — [ADR-00615](adr/ADR-00615.md))
- `XMLHttpRequest` — The optional `user`/`password` arguments to `.open()` are accepted (and evaluated) but not yet wired to HTTP auth
- `XMLHttpRequest` — `.setRequestHeader(name, value)` accumulates headers, but names are **not** lowercased here (unlike `Headers`' own methods) — a separate V1 narrowing
- `XMLHttpRequest` — `.readyState` is 0/1/4 only (UNSENT/OPENED/DONE); the real spec's HEADERS_RECEIVED/LOADING have no meaning for a transfer that always runs to completion in one call
- `XMLHttpRequest` — `.responseText`/`.response` are identical values — only a text `responseType` is supported; no `responseType` other than the default
- `XMLHttpRequest` — `.onreadystatechange`/`.onload`/`.onerror` callbacks are zero-argument (deliberately — a payload-carrying callback isn't representable for this particular type, see [ADR-00131](adr/ADR-00131.md))
- `XMLHttpRequest` — `.abort()` is a best-effort `readyState` reset — there's nothing to actually interrupt once a synchronous `.send()` has returned
- `XMLHttpRequest` — `.getAllResponseHeaders()` returns headers in arrival order, not the spec’s lowercase-sorted order ([ADR-00490](adr/ADR-00490.md))

### Performance & Timing — Strict 2/10 (20%) · 12 caveats — [Performance & Timing](status/PERFORMANCE-TIMING.md)
- `performance.mark(name)` / `performance.measure(name, start, end?)` — Re-marking a name overwrites its timestamp — last-write-wins, not a full ordered entries list
- `performance.mark(name)` / `performance.measure(name, start, end?)` — `measure()` returns the elapsed milliseconds directly as a plain number rather than a `PerformanceMeasure` object, and its own `name` argument is evaluated but not stored anywhere
- `Date` — UTC-only everywhere (construction, getters, formatting) — diverges from JS's local-time default
- `Date` — The multi-argument constructor treats its fields as UTC; JS treats them as *local* time
- `Date.parse(string)` — Unparseable input returns `-1` (a documented sentinel — this compiler's Date has no NaN representation)
- `Date` setters (`setFullYear`, `setMonth`, `setDate`, `setHours`, `setMinutes`, `setSeconds`, `setMilliseconds`, `setTime`) — Requires a named-variable receiver (not a field access or call result — this compiler's Date is a plain number, not a reference object, so there's no heap location to mutate otherwise)
- `Date` arithmetic (`date ± durationMs`, `date - date`, `date += durationMs`) — `Date ± number` gives a new Date (a deliberate deviation from real JS, where `+` on a Date string-concatenates instead — numeric duration arithmetic is far more useful for this compiler's plain-number Date representation)
- `Date` arithmetic (`date ± durationMs`, `date - date`, `date += durationMs`) — `Date + Date`, `number - Date`, and compound-assigning a Date into a Date are all rejected at compile time
- `Date.prototype.toDateString()` — Always UTC, not local time
- `Date.prototype.toLocaleDateString()` — One fixed `"M/D/YYYY"` format (the default en-US shape), always UTC; no locale argument or full `Intl`-style locale support
- `PerformanceObserver` — Dispatch is **synchronous** (during `mark`/`measure`), not Node's async batched next-microtask delivery
- `PerformanceObserver` — Entry types `mark`/`measure` only; `buffered`, `takeRecords()`, the single-`type` form, and `getEntriesByName`/`getEntriesByType` are unsupported

### File System (fs) — Strict 5/20 (25%) · 28 caveats — [File System (fs)](status/FILE-SYSTEM.md)
- `fs.readFileSync(path[, 'utf8'])` — Text-only by design — a file with embedded null bytes reads back shorter than its real size (`.length` is `strlen`-based); use `fs.readFileSyncBytes(path)` for a binary file
- `fs.readFileSync(path[, 'utf8'])` — With no encoding the result is still a `string`, not a `Buffer` (the text-only default above); a non-`'utf8'` encoding is a clean rejection ([ADR-00785](adr/ADR-00785.md))
- `fs.writeFileSync(path, data[, opts])` — The options argument supports only `{ encoding: 'utf8', flag, mode }` — a non-`'utf8'` encoding is a clean rejection ([ADR-00785](adr/ADR-00785.md), [ADR-00988](adr/ADR-00988.md)).
- `fs.appendFileSync(path, data[, opts])` — The options argument supports only `{ encoding: 'utf8', flag, mode }` — a non-`'utf8'` encoding is a clean rejection ([ADR-00785](adr/ADR-00785.md), [ADR-00988](adr/ADR-00988.md)).
- `fs.statSync(path)` / `fs.lstatSync(path)` — No Date-valued `atime`/`mtime`/`ctime`/`birthtime` accessors — times are integer milliseconds only ([ADR-00495](adr/ADR-00495.md)/[ADR-00497](adr/ADR-00497.md))
- `fs.statSync(path)` / `fs.lstatSync(path)` — `birthtimeMs` is `0` on Linux — glibc's `struct stat` carries no creation time (it needs `statx`); Darwin reports the real `st_birthtimespec`
- `fs.rmSync` / `realpathSync` / `mkdtempSync` / `symlinkSync` / `linkSync` / `readlinkSync` / `chmodSync` / `truncateSync` / `accessSync` — `rmSync` options must be a `{recursive, force}` boolean-literal object; no `maxRetries`/`retryDelay` (a Windows-centric retry knob — the retryable errors don't arise on the POSIX removal path)
- `fs.rmSync` / `realpathSync` / `mkdtempSync` / `symlinkSync` / `linkSync` / `readlinkSync` / `chmodSync` / `truncateSync` / `accessSync` — `readlinkSync` reads targets of any length (a growing buffer — [ADR-00788](adr/ADR-00788.md)); `accessSync`'s mode defaults to existence-only (F_OK) and `fs.constants.F_OK`/`R_OK`/`W_OK`/`X_OK` name the others ([ADR-00795](adr/ADR-00795.md))
- `fs.openSync` / `closeSync` / `readSync` / `writeSync` / `fstatSync` — `openSync` flags must be a string literal (`r r+ w w+ a a+ wx ax`) or a raw numeric mask — not a runtime-computed flag string ([ADR-00795](adr/ADR-00795.md))
- `fs.openSync` / `closeSync` / `readSync` / `writeSync` / `fstatSync` — `writeSync` writes string data only (no Buffer/offset/length variant); `readSync`'s buffer must be a `Uint8Array`/`Buffer` ([ADR-00498](adr/ADR-00498.md))
- `fs.fsyncSync(fd)` / `fdatasyncSync(fd)` / `ftruncateSync(fd[, len])` — `fdatasyncSync` is an alias for `fsyncSync` (the data-only optimization isn't observable in the file's contents)
- `fs.utimesSync(path, …)` / `fs.futimesSync(fd, …)` — `atime`/`mtime` accept a number or `Date` only — a numeric string (which Node also coerces) is not supported
- `fs.mkdirSync(path)` — Options support only the literal `{ recursive: true }` (creates every missing prefix, idempotent — [ADR-00487](adr/ADR-00487.md)); the plain form still throws on an existing path or missing parent
- `fs.readdirSync(path[, { withFileTypes, recursive }])` — No `.path` alias on `Dirent` (Node removed it in v24) — reading `dirent.path` is a compile-time error, not a silent `undefined`
- `fs.readdirSync(path[, { withFileTypes, recursive }])` — A filesystem returning `DT_UNKNOWN` (rare) reports all kind predicates `false` rather than falling back to `lstat`, and is not descended under `{ recursive: true }`
- `fs.readdirSync(path[, { withFileTypes, recursive }])` — `encoding: 'buffer'` unsupported; recursive paths join with `'/'` (Node uses the platform separator)
- `fs.copyFileSync(src, dest[, mode])` — `COPYFILE_FICLONE`/`FICLONE_FORCE` mode bits are accepted but a best-effort no-op (no reflink); `fs.constants.COPYFILE_EXCL`/`COPYFILE_FICLONE`/`COPYFILE_FICLONE_FORCE` name all three ([ADR-00795](adr/ADR-00795.md))
- Async variants — callback (`fs.readFile(path, cb)`) + Promise (`fs.promises.readFile` / `import from 'fs/promises'`) — Windows uses the inline (delivery-only async) path — the off-loop-thread pool is POSIX-only, pending the reactor work ([TDD-00185](tdd/TDD-00185.md))
- Async variants — callback (`fs.readFile(path, cb)`) + Promise (`fs.promises.readFile` / `import from 'fs/promises'`) — `readFile` delivers a string (text) — inherits `readFileSync`'s text-only caveat; no async binary (`Buffer`) form
- Async variants — callback (`fs.readFile(path, cb)`) + Promise (`fs.promises.readFile` / `import from 'fs/promises'`) — No `options`/`encoding` argument
- Async variants — callback (`fs.readFile(path, cb)`) + Promise (`fs.promises.readFile` / `import from 'fs/promises'`) — No *async* `stat` (the sync `fs.statSync`/`lstatSync` exist but have no callback/Promise form yet)
- `fs.createReadStream` / `fs.createWriteStream` — Chunks are **strings**, not `Buffer`s — text-first, like `readFileSync` (an embedded NUL truncates a chunk); binary (`Uint8Array`) streaming is a follow-on
- `fs.createReadStream` / `fs.createWriteStream` — On Windows the eager read-to-EOF fill is kept (the pool runtime is POSIX-only), pending the reactor work ([TDD-00186](tdd/TDD-00186.md))
- `fs.createReadStream` / `fs.createWriteStream` — Options: `createReadStream` takes only `{ highWaterMark }`, `createWriteStream` only `{ flags: "w"\|"a" }` (both compile-time literals); `start`/`end`/`fd`/`encoding` are clean errors
- `fs.watch(path, listener)` → `FSWatcher` — `filename` is the **watched path**, not the changed child, on macOS — kqueue/`EVFILT_VNODE` watches an fd, not a directory's entries (Linux inotify names the entry faithfully) ([ADR-00758](adr/ADR-00758.md))
- `fs.watch(path, listener)` → `FSWatcher` — One listener slot per event (`.on('change'|'rename', …)`, arrow-literal only) — the `Worker`/`ChildProcess` posture, not the full `EventEmitter`
- `fs.watch(path, listener)` → `FSWatcher` — `recursive` is honoured natively where the OS supports it (macOS/Windows); on Linux (inotify) it watches only the named path — not the subtree
- `fs.watch(path, listener)` → `FSWatcher` — `encoding` accepts only `'utf8'`; no coalescing/ordering guarantees (as in Node, event streams are platform-dependent)

### Timers — Strict 1/4 (25%) · 3 caveats — [Timers](status/TIMERS.md)
- `setTimeout(fn, ms)` / `clearTimeout(id)` — Callback is restricted to a zero-argument, `void`-returning function — an arrow/function-expression closure, or a bare reference to a top-level named function ([ADR-00200](adr/ADR-00200.md))
- `setInterval(fn, ms)` / `clearInterval(id)` — Callback must be a zero-argument `() => void`; the extra-args form is a compile error — `setInterval(cb, 10, 42)` is rejected, where Node forwards `42` to `cb` (shares `setTimeout`'s restriction).
- `setImmediate(fn)` / `clearImmediate(id)` — Real Node guarantees `setImmediate` fires before a same-tick `setTimeout(fn, 0)` when scheduled from inside an I/O callback, because its event loop has distinct phases (check vs. timers); this compiler's `__kml_timer_drain` is a single flat fire-time-ordered queue with no phase concept, so the two are genuinely indistinguishable here (both fire at "now")

### FFI (node:ffi) — Strict 4/13 (~31%) · 11 caveats — [FFI (node:ffi)](status/FFI.md)
- `ffi.dlopen(path, definitions?)` → `{ lib, functions }` — `definitions` must be an object literal — signatures are resolved at compile time (a runtime-computed signature has no AOT lowering)
- `new DynamicLibrary(path)` / `lib.close()` / `ffi.dlclose(lib)` — `using` declarations and `[Symbol.dispose]()` are not supported yet — explicit `.close()` is the disposal path (close is idempotent, as in Node)
- `lib.getFunction(name, signature)` / `lib.getFunctions(definitions)` — `signature` must be a compile-time object literal (same reason as `dlopen` definitions)
- `lib.getFunction(name, signature)` / `lib.getFunctions(definitions)` — Re-resolving the same symbol with a different signature is not diagnosed (Node throws)
- `library.functions` / `library.symbols` / no-arg `getFunctions()` / `getSymbols()` — The accumulator is compile-time — a runtime-string name is resolved but not recorded
- Typed calls + marshalling (all scalar types, `string`, `pointer`, `buffer`, `arraybuffer`, 64-bit ↔ `bigint`) — `int64`/`uint64` parameters also accept a plain `number` (Node requires `bigint`); `bool` marshals as 0/1 numbers, matching Node
- `library.registerCallback([sig,] cb)` / `unregisterCallback(ptr)` / `refCallback` / `unrefCallback` — At most 16 concurrently-live callbacks per signature shape (static per-signature trampoline families — no runtime codegen); exceeding it throws, unregistered slots recycle
- `library.registerCallback([sig,] cb)` / `unregisterCallback(ptr)` / `refCallback` / `unrefCallback` — 64-bit/pointer parameters must be declared `bigint` (and `string` as `string`) — validated with a clear compile-time error
- `library.registerCallback([sig,] cb)` / `unregisterCallback(ptr)` / `refCallback` / `unrefCallback` — `refCallback`/`unrefCallback` are validated no-ops (a registered callback is always strongly referenced here), and `library.close()` does not invalidate callbacks — unregister explicitly; Node's same-thread/no-throw/no-promise runtime rules are not enforced
- `ffi.exportString/exportBuffer/exportArrayBuffer/exportArrayBufferView(src, ptr, length)` — `exportString` supports only the `'utf8'` encoding (UTF-8-native strings) and truncates to the capacity — whether Node instead throws on overflow is unverified against a real `--experimental-ffi` build
- `ffi.exportString/exportBuffer/exportArrayBuffer/exportArrayBufferView(src, ptr, length)` — A too-small `length` on the byte-export forms throws, as in Node

### JSDoc — Strict 12/37 (~32%) · 26 caveats — [JSDoc](status/JSDOC.md)
- `@param` (aliases `@arg`, `@argument`) — Fills only a parameter with no inline `: T` annotation and no destructuring pattern (an inline type wins)
- `@param` (aliases `@arg`, `@argument`) — A `{...T}` varargs type is stripped to its base `T` (the count isn't bound as a `...rest` parameter); a body reading `arguments` sees the values actually passed ([ADR-00928](adr/ADR-00928.md)), but for a typed variadic use a declared `...rest: T[]` parameter
- `@returns` (alias `@return`) — Fills only when the function has no inline `: RetType`
- `@typedef` — A `@typedef {T} Name` alias to a **width keyword** (`int32`) does not propagate the integer semantics — the same pre-existing limit a TS `type X = int32` alias has
- `@template` — Inherits the TS generics scope: V1 monomorphization needs an inferable `T`/`T[]`-typed parameter, no explicit call-site type arguments ([TDD-00010](tdd/TDD-00010.md)); a `{Base}` constraint on a multi-name tag applies to the first name (matching TS)
- `@satisfies` — Accepted and erased — the value keeps its own type, no `satisfies`-style excess-property/conformance check (parity with the erased TS `satisfies` operator — [ADR-00371](adr/ADR-00371.md))
- `@enum` — The tagged `const` object works for value access (`Dir.Up`); it is a plain object, not a nominal enum type — no reverse mapping, no enum-member type narrowing
- `@this` — Accepted and erased; a `this` parameter type is not separately modeled (`this` is bound by the class method it lives in)
- `@extends` (alias `@augments`) — Accepted and erased — the class's own `extends` clause does the work; the tag does not add type arguments to a generic base
- `@implements` — Accepted and erased — like a TS `implements` clause here, it does not strictly enforce structural conformance
- `@public` — Accepted and erased, not enforced — the same stance as the TS `public` field modifier here
- `@private` — Accepted and erased, not enforced — no access checking (the TS `private` modifier is likewise erased, not enforced, here)
- `@protected` — Accepted and erased, not enforced
- `@readonly` — Accepted and erased, not enforced — no mutation checking (parity with the erased TS `readonly` modifier — [ADR-00373](adr/ADR-00373.md))
- `@override` — Accepted and erased — no override-consistency check
- `@deprecated` — Documentation-only; accepted with no compile effect, but there is no tooling layer to surface a deprecation warning
- `@import` — The comment-form import statement is not synthesized; use a normal `import` plus an `import("./m").T` type reference (which resolves — see the type-expression table)
- `@constructor` (alias `@class`) — Marks a plain function as a constructor (legacy pre-`class` JS) — unsupported; this compiler uses real `class` declarations, not function-as-constructor
- Optional param (`T=`, `[name]`) — Recognized at the `@param` name/type level and stripped; the parameter is not yet marked structurally optional beyond an inline `?`
- Varargs/rest (`...T`) — Stripped to the base `T`; the varargs body reads its overflow through a declared `...rest` parameter or the `arguments` object ([ADR-00928](adr/ADR-00928.md))
- Union (`A \| B`) — Inherits the union V1 scope (scalar/object/`ReadableStream` members, narrowing rules — see the Type system page)
- Nullable (`?T`) — The leading marker (`?number` → `number \| null`); a `?` buried mid-expression is left to the parser
- Non-null (`!T`) — The marker is stripped (`!number` → `number`) — TS treats non-null as no semantic change
- Function type (`function(A): B`) — Rewritten to the arrow form `(arg0: A) => B`; a nested `function(...)` inside another type is not rewritten (rare)
- `import("./m").T` type — The `import("./m").Name` qualifier is dropped to the bare `Name`, which resolves under whole-program compilation when that module is part of the build (typically because the file also imports from it) — a `Name` not otherwise pulled in won't resolve; the `typeof import(...)` value form is out of scope
- Legacy synonyms (`String`→`string`, …) — `String`/`Number`/`Boolean`/`Void`/`Undefined`/`Null` remap to the lowercase primitive; bare `Object`/`object` are left as-is

### events (EventEmitter) — Strict 3/8 (~38%) · 8 caveats — [events (EventEmitter)](status/EVENT-EMITTER.md)
- `new EventEmitter<T>()` / extending it via `class X extends EventEmitter<T>` — An override's declared signature isn't checked for compatibility against the built-in method it replaces (matching TS's base-declaration-only override checking and this compiler's erased-`as` stance) — a wildly-wrong override signature is the author's problem ([ADR-00631](adr/ADR-00631.md))
- `.on(event, listener)` / `.once(event, listener)` — No open-ended `(...args)` beyond a declared tuple payload
- `.on(event, listener)` / `.once(event, listener)` — A map-typed emitter requires a string-literal event name at every call site
- `.emit(event, ...args)` — Open-ended untyped varargs beyond a declared tuple aren't supported
- `.emit(event, ...args)` — A map-typed emitter requires a string-literal event name
- `events.once(emitter, name)` (static helper) — Single-value and homogeneous multi-argument events → `Promise<args[]>`; a mixed-type multi-argument event is a clean rejection (homogeneous arrays only), and payload-less events are deferred ([ADR-00675](adr/ADR-00675.md))
- `events.on(emitter, name)` (async iterator) — Same payload rules as `once`: single-value / homogeneous multi-argument events; mixed-type multi-argument, payload-less, and non-scalar payloads are clean rejections
- `events.on(emitter, name)` (async iterator) — The buffer is unbounded (Node's default); iterator `.return()`/`.throw()` unregistering the listener is a deferred follow-up ([ADR-00677](adr/ADR-00677.md))

### Type System — Strict 12/31 (~39%) · 45 caveats — [Type System](status/TYPE-SYSTEM.md)
- `number` → `double` — Mixing an explicit integer type with a bare literal promotes to double (`int32 / 2` is `3.5`); use two integer-typed operands for integer division
- `null` / `undefined` — A value typed as **both** `T | null | undefined` shares the single `ptr null` sentinel, so it can't tell which nullish kind it holds — it compares per the statically-chosen kind, which can be wrong for the other
- `any` — Under `-compat=js`, unary `+` on an `any` isn't parsed yet (arithmetic otherwise runtime-dispatches; under `-compat=strict` it's a clean compile error)
- `any` — Widening a static object into `any` covers only a fresh `new C()` of a data-only class ([ADR-00990](adr/ADR-00990.md), [TDD-00155](tdd/TDD-00155.md) Stage 6); an *aliased* static binding (`const a: any = o`), a method-carrying class, and a cyclic object graph still reject member access
- `any` — A dynamic object has no inspect-style `{ a: 1 }` form — `console.log`/`String()` render it `[object Object]`
- `any` — `Object.defineProperties` (plural) and descriptor-form `Object.assign` are follow-ons
- `any` — Primitive members through `any` (`x.length` on a boxed string/array) read as `undefined` — no runtime primitive-method dispatch yet
- `unknown` — Same as `any` (see above)
- `symbol` — No dynamic property keys
- `symbol` — No well-known symbols as runtime values (`Symbol.iterator`, etc.); the exceptions are `[Symbol.asyncIterator]` and `[Symbol.iterator]` recognized purely syntactically as class/object-literal computed-member keys ([TDD-00089](tdd/TDD-00089.md), [ADR-00278](adr/ADR-00278.md)/[ADR-00279](adr/ADR-00279.md)), not evaluable `Symbol` values
- `bigint` — No `.toLocaleString()` (an `Intl`-shaped gap)
- Object types (interfaces / inline `{}`) — A **generic or qualified interface base** (`extends Boxed<number>`, `extends ns.Base`) parses but does not merge — its members stay missing from the derived interface
- Object types (interfaces / inline `{}`) — A bare call signature is supported only alone (desugars to the function type); mixing it with fields/an index signature is a clean rejection, for both inline `{}` types and interfaces ([ADR-00448](adr/ADR-00448.md)/[ADR-00455](adr/ADR-00455.md))
- Object types (interfaces / inline `{}`) — A same-name `interface` + `class` pair: the class wins as the binding, the interface's extra members ignored ([ADR-00466](adr/ADR-00466.md))
- Union types beyond `T \| null` — Scalar members (`number`/`string`/`boolean` + `null`/`undefined`), object/interface/class members (one object member ([TDD-00115](tdd/TDD-00115.md)), or ≥2 as a **discriminated union** sharing a first-position string-literal tag ([TDD-00116](tdd/TDD-00116.md))), and a `ReadableStream` member ([TDD-00119](tdd/TDD-00119.md)); array/`ArrayBuffer` members, number-literal tags, and a non-first-position tag aren't supported
- Union types beyond `T \| null` — Usable at the top level of a var declaration/function param/return and as an **object field** (checked + boxed at construction, [TDD-00119](tdd/TDD-00119.md)); as an **array element** it's still rejected (element-level checking not wired)
- Union types beyond `T \| null` — Flow narrowing is for a union **local** via `typeof`/truthiness/`==null` (if/else branches + early return); a field/element, discriminated-union tag, `switch (typeof x)`, and `as` casts aren't narrowed; an un-narrowed object union has no field access ([TDD-00114](tdd/TDD-00114.md))
- Intersection types — Object-type members only — a non-object member (scalar/function/array/tuple/union, all `never` in TS), or a generic-with-type-arguments member, is a clean compile error
- Intersection types — A field declared with conflicting **non-object** types across members is rejected under `-compat=strict` (default); the `-compat=js` TS-faithful `never`-field is not yet implemented, so a conflict is currently rejected in both modes (same-named **object** fields are recursively intersected instead — `{ x: A } & { x: B }` ⇒ `x: A & B`)
- Intersection types — Inherits object types' missing-field leniency: a partial object literal for an `A & B`-typed target is not rejected (a pre-existing object-literal behavior, not intersection-specific)
- Tuple types — No rest/optional/named elements
- Tuple types — Constant index only — no non-constant index; compound assignment to an element (`t[0] += x`) is a clean rejection
- Tuple types — No array methods (`.map`/`.filter`/…) on a tuple (`.length` works)
- Tuple types — Not supported nested in `any` or a union
- Mapped & utility types — `Partial`/`Required`/`Readonly` are structural no-ops — the object model already zero-fills omitted fields and does no mutation checking; the visibly-effective utilities are `Pick`/`Omit`/`Record`
- Mapped & utility types — Mapped value body handles `T[K]` (homomorphic), a bare `K`, a concrete type (uniform), or `T[K]` wrapped one level in `Array`/`Promise`/`Set`; a `T[K]` nested deeper isn't substituted
- Mapped & utility types — No key remapping (`as`) or modifier-removal (`-?`/`-readonly`); `?`/`readonly` are accepted but near-no-ops
- Mapped & utility types — String-literal types resolve to `string` — the literal value is not narrowed/enforced
- Mapped & utility types — An unrecognized `Name<Args>` (or a malformed `Pick`/`Omit`) is not yet a clean rejection — [TDD-00079](tdd/TDD-00079.md) Stage 0
- Conditional types (`T extends U ? X : Y`) — `infer` supported for `Array<infer E>`/`Promise<infer V>` and a bare `infer R`; function-signature inference (`(...) => infer R`) and deeper `infer` nesting are deferred
- Conditional types (`T extends U ? X : Y`) — `assignable` is structural width subtyping (objects), element-wise (arrays), IR-equality with numeric leniency (scalars) — not full variance
- Generics on user functions/interfaces/classes — No explicit call-site type arguments for a *function* (`identity<number>(5)`) — inference-only, blocked by the `a<b>(c)` grammar ambiguity (a generic class still takes an explicit `new Box<T>(...)` list)
- Generics on user functions/interfaces/classes — A generic function needs an inferable `T`/`T[]`-typed parameter for each of its type parameters
- Generics on user functions/interfaces/classes — Map/Set/Promise/closure/dynamic type arguments are a clean rejection (scalars, arrays, and object/interface/class types are supported)
- Generics on user functions/interfaces/classes — V2 `@erased` covers functions only (not interfaces/classes) and bare `T` positions only — `T[]` or `T` nested in an object field is a clean compile error
- Generics on user functions/interfaces/classes — Arithmetic on an erased `T` hits the same "operator on any/unknown" rejection as plain `any`
- Index signatures (`{ [k: string]: T }`) — A mixed named+index shape (`{ id: number; [k: string]: T }`) is a clean rejection; the class-body form (`class C { [n: number]: T }`) parses and is **dropped** — indexing a class instance keeps its use-site rejection ([ADR-00476](adr/ADR-00476.md))
- Index signatures (`{ [k: string]: T }`) — `JSON.stringify` of an index-signature dict is compact output only — the `space` argument isn't threaded through this path ([ADR-00482](adr/ADR-00482.md))
- `typeof` type queries (`type T = typeof x`) — A function-local binding shadowing a top-level one of the same name isn't distinguished (the top-level wins)
- `typeof` type queries (`type T = typeof x`) — An unresolvable query degrades to the `number` default rather than erroring
- Type assertions (`x as T`, `as const`, `satisfies T`, `<T>x`) — **Erased, not enforcing** — the assertion is dropped and the value keeps its *own* inferred type, so `as T` does not re-type the expression; a cast the code *relies on* for typing won't take effect (matches TS runtime erasure, not static narrowing/widening — [ADR-00371](adr/ADR-00371.md)); the general `any as T` question is [TDD-00176](tdd/TDD-00176.md)
- Template literal types (`` `a-${T}` ``) — **Erased to `string`** — the literal pattern is parsed (no-substitution, multi-substitution, and `[]`-suffixed forms) but resolves to `string`, not narrowed/enforced; the same simplification string-literal types use ([ADR-00561](adr/ADR-00561.md))
- `readonly T[]` array-type modifier — **Erased, not enforcing** — `readonly number[]` and `readonly [T, U]` parse (in variable/parameter/return/field positions), but the modifier is dropped and mutation is not prevented, the same stance as `Readonly<T>` and the mapped-type `readonly` modifier. The `ReadonlyArray<T>` alias form is not covered ([ADR-00373](adr/ADR-00373.md))
- Ambient declarations (`declare const`/`function`/`class`/`module`/`namespace`) — A runtime *use* of a declared binding this compiler doesn't itself provide is an ordinary `undefined variable` error — under whole-program AOT an ambient declaration has no external target to bind to
- Ambient declarations (`declare const`/`function`/`class`/`module`/`namespace`) — `declare class` and the brace-bodied `global`/`module`/`namespace` blocks are parsed and erased (no binding — [ADR-00388](adr/ADR-00388.md))

### Process / CLI I/O — Strict 13/32 (~41%) · 52 caveats — [Process / CLI I/O](status/PROCESS-CLI.md)
- Command-line arguments (`process.argv`) — Mirrors C's `argv` directly (`argv[0]` is the binary's own path), not Node's two-prefix convention — see [ADR-00002](adr/ADR-00002.md)
- Command-line arguments (`process.argv`) — `typeof process.argv[99]` reports `'string'` when absent (Node: `'undefined'`) — `typeof` reads the static kind
- Environment variables (`process.env.KEY` / `process.env["KEY"]`) — `typeof process.env.MISSING` is `'string'` (Node: `'undefined'`) — `typeof` reads the static kind, not the runtime absence
- `process.execFileSync(file, args?, options?)` — Options support only `{ cwd, encoding: 'utf8' }` ([ADR-00589](adr/ADR-00589.md)) — `cwd` chdir's the child before exec; `env`/`timeout`/`stdio` overrides are still deferred
- `process.execFileSync(file, args?, options?)` — stderr is inherited (visible on the terminal live), not captured
- `process.emitWarning(message, type?\|options?)` — The one-shot `--trace-warnings` hint line Node prints is deliberately omitted, and the `'warning'`-event `Error` carries only `name`/`message` (not `code`/`detail`) — the fixed error-object shape ([ADR-00580](adr/ADR-00580.md))
- `process.kill(pid, signal?)` — The thrown message carries the signal number (`kill(pid=…, signal=15)`), not the name Node prints
- `process.stdout.write(s)` / `process.stderr.write(s)` (raw write, no auto-newline) — Text-only (byte-string, not binary-safe past an embedded null — same scope narrowing `console.log`'s own string formatting already has)
- `process.stdout.write(s)` / `process.stderr.write(s)` (raw write, no auto-newline) — No stream surface beyond `.write`/`.isTTY`/`.columns`/`.rows` (no `'drain'`)
- `process.stdin.setRawMode(enabled)` (raw terminal input) — A no-op when fd 0 is not a terminal (`tcgetattr` fails; on Windows, `GetConsoleMode` fails) — guard on `process.stdin.isTTY` first, as every TUI does
- `klain:tty` `readByte()` / `readKey()` (synchronous raw reads) — Bespoke, non-Node surface under the explicit `klain:` specifier — Node has no synchronous single-key read (it uses `process.stdin.on('data')` events)
- `klain:tty` `readByte()` / `readKey()` (synchronous raw reads) — Blocking reads on fd 0; do not mix with `process.stdin.on('data')` in the same run, which puts fd 0 in non-blocking mode (a synchronous read would then see EOF-on-EAGAIN)
- Writing `process.env` (`process.env.KEY = val` / `process.env["KEY"] = val`) — Compound assignment (`process.env.X += …`) is a clean rejection
- `process.on('SIGINT'/'SIGTERM'/'SIGWINCH'/'SIGBREAK', handler)` (signal handlers) — The handler runs from ordinary control flow at the top of the event loop's own iteration, never from real async signal context (on Windows the console control callback queues the signal and the loop's next wait, at most 50 ms away, delivers it on the main thread — [ADR-00728](adr/ADR-00728.md))
- `process.on('SIGINT'/'SIGTERM'/'SIGWINCH'/'SIGBREAK', handler)` (signal handlers) — Event name must be a compile-time string literal, exactly `'SIGINT'`/`'SIGTERM'`/`'SIGWINCH'`/`'SIGBREAK'` (dynamic event names are a clean compile error, matching `Object.hasOwn`'s dynamic-key rejection)
- `process.on('SIGINT'/'SIGTERM'/'SIGWINCH'/'SIGBREAK', handler)` (signal handlers) — Windows, as in Node: `'SIGINT'` fires on Ctrl+C, `'SIGBREAK'` on Ctrl+Break, `'SIGTERM'` is accepted but never fires (`process.kill` terminates unconditionally); `'SIGBREAK'` is accepted on POSIX hosts and never raised there
- `process.on('SIGINT'/'SIGTERM'/'SIGWINCH'/'SIGBREAK', handler)` (signal handlers) — Handler must be a zero-argument `() => void` closure, same shape `setTimeout`'s own callback requires
- `process.on('SIGINT'/'SIGTERM'/'SIGWINCH'/'SIGBREAK', handler)` (signal handlers) — One handler slot per signal — registering the same signal twice overwrites the previous handler
- `process.on('SIGINT'/'SIGTERM'/'SIGWINCH'/'SIGBREAK', handler)` (signal handlers) — Only fires while a `http.listen`-driven or timer-driven event loop is actively iterating; a plain synchronous script with neither has no loop to check from
- `process.on('exit'/'uncaughtException'/'warning', ...)` + `process.exitCode` — `'uncaughtException'` runs the listener (suppressing the default `Uncaught:` print) but the process still exits (code 1) — the setjmp/longjmp exception model has already unwound to the top-level catch-all, so it can't resume execution like Node does
- `process.on('exit'/'uncaughtException'/'warning', ...)` + `process.exitCode` — `'warning'` fires (with the warning as an `Error`) when `process.emitWarning` is called; the stderr print still happens too
- `process.on('exit'/'uncaughtException'/'warning', ...)` + `process.exitCode` — No `'unhandledRejection'` yet (needs rejected-promise tracking)
- `process.on('exit'/'uncaughtException'/'warning', ...)` + `process.exitCode` — One listener slot per event, arrow-literal only
- `process.memoryUsage()` — `external`/`arrayBuffers` report 0 — V8's off-heap C++-binding accounting has no native analogue (all allocation lives in `rss` and the one heap above), so they are disclosed rather than invented
- `process.memoryUsage()` — `heapTotal`/`heapUsed` measure this compiler's object heap, not a V8 JS-object heap — a native reinterpretation (the C allocator arena, or Boehm's heap under `-mm=gc`), so the magnitudes differ from Node's even though the direction (growth on allocation) matches
- `process.nextTick(fn)` — Shares the one microtask FIFO with Promise reactions rather than draining strictly before them (Node keeps a separate nextTick queue ahead of promises); zero-argument `() => void` callback only
- `process.version` / `process.versions` — `versions` omits bundled-lib keys this compiler doesn't ship (`uv`/`undici`/`icu`/…) rather than fabricate them
- `process.version` / `process.versions` — Linked-library versions that could be reported truthfully (`openssl` under the OpenSSL backend, `zlib` when linked) are not surfaced
- `process.version` / `process.versions` — `node`/`v8` track the compatibility *ceiling* (the pinned test-corpus release); a `--node-compat` floor is deferred ([TDD-00136](tdd/TDD-00136.md))
- `process.stdin` (streamed reads, not just `readLineSync()`) — `'data'` delivers each read chunk as a UTF-8 string, not a `Buffer` — `setEncoding('utf8')` is a faithful no-op but a non-utf8 encoding is rejected (no binary mode)
- `process.stdin` (streamed reads, not just `readLineSync()`) — Flowing mode only (attaching `'data'` starts it) — no `.pause()`/`.resume()`/`.read()`/`'readable'`
- `process.stdin` (streamed reads, not just `readLineSync()`) — One listener per event, arrow/function-expression literal only; callbacks fire synchronously from the dispatch, not via a microtask
- `readline` module (`createInterface`, `'line'`/`'close'` events, `question()`, `close()`) — The listener callbacks fire synchronously from the event loop's dispatch, not deferred through a microtask queue (same as `child_process`)
- `readline` module (`createInterface`, `'line'`/`'close'` events, `question()`, `close()`) — One listener per event, an arrow/function-expression literal only; no line history/editing, no `SIGINT`/`'pause'`/`'resume'`, no completer
- `readline` module (`createInterface`, `'line'`/`'close'` events, `question()`, `close()`) — The `createInterface` options object is accepted but ignored — input is always stdin, output always stdout; one active interface at a time
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — The listener callbacks fire synchronously from the event loop's dispatch, not deferred through the microtask queue (`process.nextTick` — see the row above)
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — One listener per event, an arrow/function-expression literal only (the same posture as `Worker`); no `removeListener`
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — Only `spawn` takes an options object; `exec`/`execFile` take no options
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — `shell` must be `true` (the command as one string); a shell *path*, a dynamic value, or `shell: true` with an args array are clean rejections
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — `env` must be an object literal of string values (fully replaces the child env, Node's non-merging semantics)
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — `stdio` fd numbers, `'ipc'`, and streams are clean rejections (only `'pipe'`/`'inherit'`/`'ignore'`, as a string or a 3-element array)
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — `timeout`'s `killSignal` must be a literal name/number
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — The `'error'` Error's `.syscall`/`.path`/`.spawnargs` and Node's `spawn <file> <CODE>` message wording are unset
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — A legacy `'exit'`/`'close'` listener that declares one non-nullable `number` param sees `0` (not `null`) on a signal death — a plain `number` can't hold `null`; type the param `number | null` for the faithful `null` ([ADR-00761](adr/ADR-00761.md))
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — `child.kill` takes a signal *name* string only as a **literal** (resolved to the host's signal number at compile time) or a number; a dynamic signal-name string is a clean rejection
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — Handles and their buffered output are never freed (`-mm=manual`)
- `child_process.fork()` (self-fork + IPC channel) — Self-fork only: the path must be `__filename` or `process.argv[1]` (forking a *different* module would need a second compiled program — clean rejection); the options object must be an empty `{}`
- `child_process.fork()` (self-fork + IPC channel) — Messages are strings (`send(obj)` is a clean rejection); one `'message'` listener per side
- `child_process.fork()` (self-fork + IPC channel) — Bare `process.send` reads as a *boolean* forked-ness probe, not Node's function-or-undefined — `assert.strictEqual(process.send, undefined)`-style tests type-mismatch cleanly
- `child_process.spawnSync()` / `.execSync()` / `.execFileSync()` (blocking) — Options object supports `{ cwd, encoding: 'utf8' }` only (no `env`/`timeout`/`input`/`shell`/`stdio` — clean rejections); results are strings (the `encoding: 'utf8'` shape), not Buffers
- `child_process.spawnSync()` / `.execSync()` / `.execFileSync()` (blocking) — `spawnSync`'s result carries `status`/`stdout`/`stderr`/`pid` only — no `signal`/`error`/`output` fields (a signal death folds into `status` as `128+signal`)
- `child_process.spawnSync()` / `.execSync()` / `.execFileSync()` (blocking) — `execSync`/`execFileSync` throw `Command failed: <command>` on a non-zero exit status ([ADR-00753](adr/ADR-00753.md)), but the thrown plain `Error` does not yet carry the `.status`/`.stdout`/`.stderr` ExecException properties — use `spawnSync().status` when the exit code or captured output of a failing command is needed

### RegExp — Strict 6/14 (~43%) · 10 caveats — [RegExp](status/REGEXP.md)
- Literal syntax: `/pattern/flags` — `x in /foo/` mis-lexes the `/` as division (the lexer's regex-vs-division disambiguation gap, since `in` isn't its own token in this lexer) — a small, deliberately-accepted gap
- `.exec(str)` — An unmatched optional capture group becomes `""` rather than a true per-element `null` (no per-element-nullable-string array exists)
- `.exec(str)` — Real JS's `index`/`input`/`groups` extra properties on the result aren't built
- `str.match(regexp)` — Shares `.exec()`'s result-array narrowing (an unmatched optional capture group becomes `""`, not per-element `null`; no `index`/`input`/`groups`)
- `str.matchAll(regexp)` — Returns an eager `string[][]`, not a real lazy iterator (no lazy-iteration infrastructure exists anywhere in this compiler)
- `str.replace(regexp, replacement)` (string or callback) — Replacement template supports `$1`-`$9`/`$&`/`$$` only (`` $` ``/`$'` — pre-/post-match text — are out of scope)
- `str.replace(regexp, replacement)` (string or callback) — The callback form is invoked with a fixed `(match, offset, string)` — real JS's variadic `...capturedGroups` in the middle isn't supported (a callback's arity is fixed at compile time but a pattern's capture count is only known at runtime; a callback declaring more than 3 parameters is a compile-time error)
- `str.replaceAll(regexp, replacement)` (string or callback) — Same replacement narrowing as `.replace()` (`$1`-`$9`/`$&`/`$$` only; fixed `(match, offset, string)` callback)
- `str.split(regexp)` — Only splits on a non-zero-length match — real JS's more intricate zero-length-match handling isn't replicated (see [ADR-00119](adr/ADR-00119.md))
- `str.split(regexp)` — Captured groups in the split pattern are never spliced into the result (out of scope from the start)

### Modules — Strict 8/16 (50%) · 15 caveats — [Modules](status/MODULES.md)
- Circular imports — Supported only for the declarations-only case — a file in an import cycle can't run arbitrary top-level side-effecting code
- Imported (non-entry) files may run top-level side-effecting code — A file that genuinely participates in an import cycle keeps the declarations-only restriction (no bare executable top-level statements), and additionally requires a top-level `var`/`let`/`const` initializer to be a compile-time literal — no TDZ/live-binding modeling
- `import * as ns from '...'` (namespace import) — Compile-time-only — `ns` is never a runtime value, usable only as the object of a non-computed dotted access; assigning it to a variable, passing it as a value, or `ns["x"]` reaches codegen as a plain undefined reference rather than a dedicated error message
- `import * as ns from '...'` (namespace import) — Type-position access (`let x: ns.SomeInterface`) and `new ns.SomeClass(...)` are not supported — the latter a pre-existing, namespace-independent restriction (`new` already only accepts a single bare identifier)
- Static CommonJS `require('<literal>')` — Top-level, string-literal specifier only. A destructuring default/nested-pattern/rest on a require binding, or a dynamic `require(expr)` / a `require` nested in a function body (lazy loading), is a clean compile error — deferred to the runtime/lazy-loading capability
- Re-exports (`export { x } from './other'`, `export * from './other'`) — No namespace re-exports (`export * as ns from`) — parsed and explicitly rejected with a clear error rather than silently mis-parsed, no concrete use case yet
- Re-exports (`export { x } from './other'`, `export * from './other'`) — Re-exporting from a built-in module (`export { readFileSync } from 'fs'`) is an explicit, rejected scope cut, not an oversight
- Bare/package-style imports (`import x from 'somepackage'`) — klmpm is Stage 1 only ([TDD-00054](tdd/TDD-00054.md)) — deliberately just the resolution half; no klmpm tool exists yet to fetch/version/lock a dependency, so a `klain_modules/<name>/` directory has to be hand-constructed today
- Bare/package-style imports (`import x from 'somepackage'`) — npm/`node_modules` interop is a separate, unstarted, differently-scoped mechanism — see [TDD-00053](tdd/TDD-00053.md)
- Dynamic `import(...)` — Opt-in via `-dynamic-import=lazy` ([ADR-00515](adr/ADR-00515.md)/[TDD-00056](tdd/TDD-00056.md)); the default `-dynamic-import=eager` (TDD-00055's compile-time-merge backend) is not yet built and is a clean codegen error pointing at the flag
- Dynamic `import(...)` — String-literal specifier only — a runtime-computed `import(expr)` is a clean compile error (all imports resolve at compile time)
- Dynamic `import(...)` — Result object exposes the target's **annotated scalar/string exports** only (via dlsym'd accessors); `function`/`class`/array/object exports are omitted in V1, and an un-annotated export has no field
- Dynamic `import(...)` — Incompatible with `--static` (a static binary can't `dlopen`) — a clean mutual-exclusion rejection; the lazy build emits a sibling `<binary>.d/` island directory that must ship with the binary
- Dynamic `import(...)` — A nested dynamic `import()` inside an island compiles eagerly (islands don't recursively partition in V1)
- `import.meta.url` — `import.meta.url` is the only supported member — bare `import.meta` or any other member (`import.meta.resolve`, etc.) is a clean parse-time error

### SQLite (node:sqlite) — Strict 9/18 (50%) · 10 caveats — [SQLite (node:sqlite)](status/SQLITE.md)
- `new DatabaseSync(path, options?)` — `options` is read from an object literal (V1): `readOnly`, `open`, `enableForeignKeyConstraints` (default on), `timeout`. A non-literal options argument uses the defaults
- `db.function(name[, options], fn)` (scalar UDF) — Scalar functions only; parameter/return types must be number, integer, bigint, or string (BLOB args and `aggregate()` are later stages). A per-registration trampoline invokes the compiled closure
- `stmt.get<T>(...params)` → a row or `null` — Row shape `T` must be given as an explicit type argument (`stmt.get<{ id: number }>()`) or resolvable named type; an untyped read is a compile error — the dynamic-row mode is a later stage
- `stmt.all<T>(...params)` → `T[]` — Same explicit-row-type requirement as `get<T>`
- `stmt.iterate<T>(...params)` — Materialised eagerly (equivalent to `all<T>()`) so `for…of` works; a lazy `.next()` iterator is a later stage. Same explicit-row-type requirement
- `stmt.run(...params)` → `{ changes, lastInsertRowid }` — `changes`/`lastInsertRowid` are `number`; integers beyond 2^53 lose precision (as they do in Node's default number mode)
- `stmt.columns()` — `name` and declared `type` are populated; origin `column`/`table`/`database` read `null` (they need `SQLITE_ENABLE_COLUMN_METADATA`, absent from the system libsqlite3)
- `stmt.columns()` — The origin fields actually surface as `""`, not `null`: `columns()[0].column` → `""` (Node, on a libsqlite3 built with column metadata: `"id"`/`"main"`/`"t"`)
- `stmt.setReadBigInts()` / `stmt.setAllowBareNamedParameters()` — Accepted for API completeness but effectively no-ops: the statically-typed row field governs integer representation (declare a field `bigint` to read one), and bare named parameters are always accepted
- Parameter binding (positional + named) — Positional (`?`) and named (a single object argument → `:name`/`@name`/`$name`, or the bare key). An integral `number` binds as INTEGER, a fractional one as REAL, matching Node

### Encoding / Text — Strict 1/2 (50%) · 1 caveat — [Encoding / Text](status/ENCODING-TEXT.md)
- `TextDecoder` — UTF-8 only (V1 scope) — non-UTF-8 support (Latin-1/windows-1252, UTF-16, Greek, the rest of the WHATWG label list) is a staged, low-priority follow-on in [TDD-00034](tdd/TDD-00034.md), not started; a recognized non-UTF-8 label (`latin1`/`utf-16`/…) therefore throws a `RangeError` at construction rather than decoding ([ADR-00567](adr/ADR-00567.md))

### String Methods — Strict 17/32 (~53%) · 14 caveats — [String Methods](status/STRING-METHODS.md)
- `.length` — Byte length, not the JS UTF-16 code-unit count — `'café'.length` is `5` (Node: `4`).
- `.slice(start, end?)` — Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it (`'café'.slice(0, 4)` cuts mid-`é`), diverging from Node on non-ASCII text.
- `.substring(start, end?)` — Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it, unlike Node's code-unit indexing on non-ASCII text.
- `.substr(start, length?)` — Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it, unlike Node's code-unit indexing on non-ASCII text.
- `.indexOf(substr, fromIndex?)` — Returns a byte offset, not a UTF-16 index — `'naïve'.indexOf('ve')` is `4` (Node: `3`).
- `.lastIndexOf(substr, fromIndex?)` — Returns the LAST occurrence's byte offset (binary-safe, descending memcmp scan), not a UTF-16 index — like `.indexOf` on non-ASCII text ([ADR-00843](adr/ADR-00843.md))
- `.includes(substr, position?)` — Binary-safe but byte-space — shares the byte-offset model of `indexOf`/`slice`, operating on bytes rather than UTF-16 code units (matters only on non-ASCII text).
- `.toUpperCase()` — ASCII-only case mapping (`a`–`z`/`A`–`Z`) — `'café'.toUpperCase()` is `'CAFé'` (Node: `'CAFÉ'`); no Unicode case tables.
- `.toLowerCase()` — ASCII-only case mapping — `'Σ'.toLowerCase()` is `'Σ'` (Node: `'σ'`); non-ASCII bytes pass through unchanged.
- `.codePointAt(i)` — This compiler's strings are plain byte sequences, not real UTF-16 — no surrogate-pair/multi-byte decoding, so this is exactly `.charCodeAt(i)`'s byte value under a second name; correct only for ASCII/Latin-1 text ([ADR-00028](adr/ADR-00028.md))
- `.match()` / `.matchAll()` — `.matchAll()` returns an eager `string[][]` rather than a lazy iterator ([REGEXP.md](REGEXP.md))
- `.localeCompare(other)` — Byte-order comparison, not real Unicode collation — no locale/`Intl` infrastructure
- `String.fromCharCode(n)` — Each argument is truncated to one byte (0–255), not encoded as a UTF-16 code unit — `String.fromCharCode(0x263A)` is `':'` (Node: `'☺'`).
- `String.fromCodePoint(n)` — Shares `fromCharCode`'s one-byte truncation — no astral/surrogate encoding — a code point above `0xFF` is mangled (`String.fromCodePoint(0x263A)` → `':'`, Node: `'☺'`).

### Terminal UI — `klain:tui` — Strict 6/11 (~55%) · 5 caveats — [Terminal UI — `klain:tui`](status/TERMINAL-UI.md)
- Flexbox layout (vendored Yoga) — Percentage units, `position:absolute`, and `aspectRatio` are not yet surfaced
- `Text(text, props?)` — styled, wrapped text — Multi-code-point grapheme clusters joined by ZWJ (flag, family, and skin-tone emoji sequences) paint as their separate wide glyphs, not one cluster
- `TextInput(value, props?)` — Editing/key handling is userland (read keys via `klain:tty`, mutate state, re-render)
- `render(root)` — layout + diff paint — Immediate-mode: the whole tree is rebuilt and re-laid-out every frame (the cell diff keeps *output* minimal); a retained/memoized node tree is a deferred perf question
- `state → view → update` app loop — The loop is written in userland TypeScript over the `klain:tty` key reads + `SIGWINCH` — there is no built-in app-runner and no callback-driven loop yet (a closure→C function-pointer trampoline is TDD-00150 Stage 2)

### Object / Collections — Strict 18/32 (~56%) · 25 caveats — [Object / Collections](status/OBJECT-COLLECTIONS.md)
- `Object.values(obj)` — A heterogeneous object's values are stringified ([ADR-00492](adr/ADR-00492.md)); a homogeneous shape returns a real typed `V[]`
- `Object.entries(obj)` — A heterogeneous object's values are stringified — its value union is representable only as `any`, whose operators aren't dispatched yet ([ADR-00492](adr/ADR-00492.md)); a homogeneous shape returns real typed `[string, V]` tuples
- `Object.assign(target, ...src)` — Every field a source contributes must already exist on `target`'s struct type — a source field `target`'s type doesn't have is a clean compile error (fixed-shape heap structs), not grafted on as in real JS ([ADR-00054](adr/ADR-00054.md))
- `Object.create()` / `getPrototypeOf` / `setPrototypeOf` / `__proto__` (dynamic objects) — Prototype machinery exists on **dynamic (`any`-typed) objects only** — statically-typed structs have no prototype link, and a boxed primitive's `getPrototypeOf` answers `null` (no primitive prototype objects)
- `Object.create()` / `getPrototypeOf` / `setPrototypeOf` / `__proto__` (dynamic objects) — `Object.create`'s property-descriptors second argument is rejected until descriptors land
- `Object.create()` / `getPrototypeOf` / `setPrototypeOf` / `__proto__` (dynamic objects) — Inherited properties don't appear in `for...in` (own-only enumeration; real JS walks the chain's enumerables)
- `Object.defineProperty` / `getOwnPropertyDescriptor` / `getOwnPropertyNames` / accessors (dynamic objects) — Dynamic (`any`-typed / js-mode-literal) objects only — statically-typed structs have no descriptor table
- `Object.defineProperty` / `getOwnPropertyDescriptor` / `getOwnPropertyNames` / accessors (dynamic objects) — `Object.defineProperties` and the descriptor-aware `Object.assign` form are follow-ons
- `new Proxy(target, handler)` — Traps implemented: `get`/`set`/`has`/`deleteProperty` — dispatched in the dynamic-object runtime entry points, forwarding to the target when absent ([ADR-00630](adr/ADR-00630.md)); other traps (`ownKeys`, `getOwnPropertyDescriptor`, `apply`, `construct`, …) are not consulted (those operations forward to the target)
- `new Proxy(target, handler)` — The target must be a dynamic object (an untyped/`any` literal); statically-typed structs can't be proxied
- `Reflect.get/set/has/deleteProperty/ownKeys/getPrototypeOf/setPrototypeOf/isExtensible/preventExtensions/defineProperty` + `defineMetadata`/`getMetadata`/`hasMetadata` — `Reflect.apply` / `Reflect.construct` are missing
- `Reflect.get/set/has/deleteProperty/ownKeys/getPrototypeOf/setPrototypeOf/isExtensible/preventExtensions/defineProperty` + `defineMetadata`/`getMetadata`/`hasMetadata` — The metadata API (`defineMetadata`/`getMetadata`/`getOwnMetadata`/`hasMetadata`/`hasOwnMetadata`, TDD-00161 Stage 3) stores on a dynamic-object target and does not walk the prototype chain (so `get` == `getOwn`); `Reflect.metadata` as a decorator factory is rejected
- `Reflect.get/set/has/deleteProperty/ownKeys/getPrototypeOf/setPrototypeOf/isExtensible/preventExtensions/defineProperty` + `defineMetadata`/`getMetadata`/`hasMetadata` — Boolean-returning forms (`set`/`deleteProperty`/`setPrototypeOf`) return the success flag rather than throwing, per spec
- `Reflect.get/set/has/deleteProperty/ownKeys/getPrototypeOf/setPrototypeOf/isExtensible/preventExtensions/defineProperty` + `defineMetadata`/`getMetadata`/`hasMetadata` — Every form requires a dynamic (`any`-typed, or `-compat=js`) target: a statically-typed object operand is a clean compile-time rejection (strict mode does not widen a typed struct to a bag, [ADR-00630](adr/ADR-00630.md)), including `get`/`has`
- `Object.hasOwn()` / `.hasOwnProperty()` — The key must be a string literal — a runtime-computed key is a clean compile error (no runtime field-name table to check it against) ([ADR-00065](adr/ADR-00065.md))
- `Object.fromEntries()` — Keys must be strings (a `[string, V][]` array) — real JS stringifies any key and accepts symbol keys ([ADR-00348](adr/ADR-00348.md))
- Computed property keys `{ [expr]: value }` — `V` is inferred from the first property only
- Computed property keys `{ [expr]: value }` — `...spread` combined with a computed key isn't supported yet
- Computed property keys `{ [expr]: value }` — The declared-type form (`{ [key: string]: T }`) isn't supported yet
- Method shorthand `{ foo() {...} }` — No `this` binding at all — `this` inside a method-shorthand body is a clean compile-time rejection (an object literal has no nominal type to give `this` a shape, and no dynamic call-site binding machinery exists), not silently wrong
- Method shorthand `{ foo() {...} }` — No `async`/generator method shorthand either, matching this compiler's class methods ([ADR-00169](adr/ADR-00169.md))
- `new Map(entries)` — Accepts only a `[K, V][]` array of 2-tuples, narrowed from the spec's `Iterable<[K, V]>` — the only iterable/pair concept a general expression has here ([ADR-00347](adr/ADR-00347.md))
- `new Set(iterable)` — Accepts only an array expression, narrowed from the spec's `Iterable<T>` — the only iterable concept a general expression has here ([ADR-00159](adr/ADR-00159.md))
- `WeakMap` / `WeakSet` / `WeakRef` — Object-identity keys only (a primitive key is a clean compile error); non-iterable (no `size`/iteration — matches spec)
- `WeakMap` / `WeakSet` / `WeakRef` — Under `-mm=manual` (default) a weak reference is strong: nothing is ever collected, so `.deref()` never nulls and keys persist ("leak by design"). Real weak semantics require `-mm=gc` ([TDD-00112](tdd/TDD-00112.md)/[ADR-00349](adr/ADR-00349.md))

### Language Constructs — Strict 48/78 (~62%) · 66 caveats — [Language Constructs](status/LANGUAGE-CONSTRUCTS.md)
- `for…of` over arrays, strings, `Map`, `Set`, and a class implementing `next(): T \| null` — A bare `for (const v of map)` iterates values (use `.keys()` for keys — [ADR-00011](adr/ADR-00011.md)); the two-name pattern `for (const [k, v] of map)` decomposes entries ([ADR-00481](adr/ADR-00481.md))
- `for…of` over arrays, strings, `Map`, `Set`, and a class implementing `next(): T \| null` — A string iterates per byte, not per Unicode code point — correct for ASCII/Latin-1, the same byte-string narrowing the rest of the string layer carries ([ADR-00535](adr/ADR-00535.md))
- `try` / `catch` / `finally` — A catch pattern can only destructure the caught value's fixed `{kind, message, name}` shape (every thrown value, including a thrown non-Error primitive, is force-shaped into that one runtime layout), not an arbitrary custom object's own fields, and there's no array-pattern form ([ADR-00170](adr/ADR-00170.md))
- String literals (single/double quote) — An embedded NUL (`\x00`, `\0`) truncates the string, since strings are stored as NUL-terminated C strings
- String literals (single/double quote) — No strict-mode `SyntaxError` for a legacy octal escape (accepted unconditionally)
- Logical operators `&& \|\| !` — Under `-compat=js`, `&&`/`\|\|` are value-preserving only for **same-typed** operands; differently-typed operands (`0 \|\| "x"`) stay bool, since the value-preserving result would be a union this compiler can't represent ([ADR-00220](adr/ADR-00220.md))
- Comma / sequence operator `(a, b, c)` — A sequence whose *first* operand is a lone identifier (`(a, b)`) is instead parsed as an arrow-function parameter list — a known ambiguity; write a non-identifier first operand ([ADR-00179](adr/ADR-00179.md))
- `typeof` operator — `typeof <namespace>.<method>` answers `"function"` for the common built-in namespaces — `Promise`/`Math`/`JSON`, `console` (any member), and the static methods of `Object`/`Number`/`String`/`Array`/`Date`/`Symbol`/`Reflect`/`Boolean` ([ADR-00282](adr/ADR-00282.md)/[ADR-00596](adr/ADR-00596.md)); a non-method static (`Number.MAX_VALUE`) still answers through normal inference, and a method on a namespace outside this allow-list falls back to inference
- `typeof` operator — `typeof value.method` is `"function"` for a class instance method and the common built-in string/array methods ([ADR-00607](adr/ADR-00607.md)); a string/array method name outside that curated set falls back to inference (a wrong `"number"`)
- `const` / `let` / `var` declarations — Definite-assignment analysis is sound-not-complete: a typed `let` assigned only in a maybe-skipped loop or across correlated conditions still falls through to the `undefined`/zero default rather than raising `used before being assigned` ([TDD-00071](tdd/TDD-00071.md) Stage 2/[ADR-00213](adr/ADR-00213.md))
- `const` / `let` / `var` declarations — A top-level `const`/`let` still isn't readable from a named `function` only in the invariant's genuine edges — a `bigint`, a *scalar*-returning un-annotated call (`emitVarDecl`'s `i64` default), a `new Set([…])` typed from its initializer array, or an init depending on a runtime-local (a `{ …rest }` spread of a destructuring target); an arrow/closure still captures any of them ([TDD-00093](tdd/TDD-00093.md))
- Array destructuring `const [a, b] = arr` — In the *assignment* form (`[a, b] = expr`, not a fresh declaration), a compound operator (`[a, b] += …`) and a member target (`[obj.x, arr[i]] = e`) stay rejected ([ADR-00595](adr/ADR-00595.md))
- Object destructuring `const { x, y } = obj` — Default values (`{ x = 1 }`) require a nullable/optional field (`T \| null`, `T \| undefined`, or `key?: T`) — a nullable *scalar* field now carries a real `{ i1, T }` presence bit ([TDD-00064](tdd/TDD-00064.md)/[TDD-00187](tdd/TDD-00187.md)), so its default fires on genuine absence only (a stored 0 survives); a default on a non-nullable field stays a clean compile-time rejection ([ADR-00158](adr/ADR-00158.md)/[ADR-00779](adr/ADR-00779.md))
- Object destructuring `const { x, y } = obj` — In the *assignment* form (`({ x, y } = expr)`), a `= default` on a nested position stays rejected — matching the declaration form ([ADR-00597](adr/ADR-00597.md))
- Object destructuring `const { x, y } = obj` — `{ ...rest }` over an `any`/union/generic source (Stage 3c) and in the *assignment* form (`({ a, ...rest } = e)`) stay clean rejections
- Object destructuring `const { x, y } = obj` — A computed key (`{ [k]: v }`) is supported only for a **constant** string/number literal key (`{ ["a"]: v }`, `{ [0]: v }` — resolves to that field); a runtime-valued key on a fixed object is a clean rejection ([ADR-00609](adr/ADR-00609.md))
- Tagged template literals (`` tag`Hello ${x}` ``) — A **user** tag's `strings` argument has no `.raw` property (this compiler's arrays carry no extra properties) — the built-in `String.raw` tag itself is implemented and interleaves the raw, un-escaped quasis ([ADR-00562](adr/ADR-00562.md))
- Function declarations (top-level) — Cannot reference a sibling top-level binding whose value is a connection handle (`Worker`/`WebSocket`/`EventSource`, `BroadcastChannel`/`MessageChannel`, `XMLHttpRequest` — construction opens a thread/socket), a `Promise`, or a generic class instance with **inferred** rather than explicit type arguments (`new Box(5)`, or a nested `new Box<Box<number>>()`) — such a binding stays a `main()` local outside a named function's fresh scope, failing with `undefined variable`. Scalars, strings, arrays, `TypedArray`s, objects, `Map`/`Set`, class instances (including an explicit `new Box<number>()`), the value/event handles (`Blob`, `Date`, `Error`, `URL`, `URLSearchParams`, `URLPattern`, `RegExp`, `Headers`, `ArrayBuffer`, `DataView`, `TextEncoder`/`TextDecoder`, `Request`, `AbortController`, `Event`/`CustomEvent`, `EventTarget`), and the streams (`ReadableStream`/`WritableStream`/`TransformStream`/`CompressionStream`, the Node streams) + `EventEmitter`, and `http.createServer` handles ([ADR-00426](adr/ADR-00426.md)) are promoted to module globals and are readable ([TDD-00093](tdd/TDD-00093.md)/[ADR-00342](adr/ADR-00342.md)/[ADR-00709](adr/ADR-00709.md)); arrow functions/closures capture everything regardless ([TDD-00057](tdd/TDD-00057.md))
- Function declarations (top-level) — A genuinely circular pair of mutually-recursive unannotated functions can't converge on a return type and keeps the scalar-default fallback ([TDD-00058](tdd/TDD-00058.md))
- Function declarations (top-level) — The `arguments` object is synthesized from the declared parameters when they all share one type ([ADR-00387](adr/ADR-00387.md)) — `.length`, indexing, and `for…of` work; mixed-type parameters, a rest/destructured parameter, and an arrow function (which has no own `arguments` in JS) are clean rejections, and it reflects the declared parameters rather than growing with extra untyped arguments (there is no variadic call beyond an explicit `...rest`); class method bodies get the same synthesis ([ADR-00464](adr/ADR-00464.md))
- Function expressions (`var f = function(x) {...}`, callback arguments, IIFE bodies) — A generic function used by value is out of scope (no single monomorphized symbol to point at) ([ADR-00200](adr/ADR-00200.md))
- Default parameter values — A default expression referencing an earlier parameter works across free functions, instance/static methods, and constructors ([ADR-00598](adr/ADR-00598.md)); a rest-parameter constructor is unsupported
- Default parameter values — Filled through a first-class function value too — a closure bound to a variable/field, an object shorthand method, an IIFE ([ADR-00914](adr/ADR-00914.md)) — including a default that references a variable captured from the closure's defining scope, evaluated in the body prologue via an argument-presence mask ([ADR-00915](adr/ADR-00915.md)); this also covers a scalar/string/object/array captured-default parameter, an async closure, and `.bind` (the mask threads through the bind trampoline) ([ADR-00916](adr/ADR-00916.md)). A captured-default on a nullable-scalar or destructured (`{a}`/`[a]`) parameter stays a clean rejection (no single overwrite slot), and capturing an array *variable* into a default (vs. referencing a module-global array) hits the separate array-capture limitation
- Optional parameters (`param?`) — An array-typed optional parameter stays an empty array when omitted (an array aggregate has no spare absent state); an un-narrowed optional scalar is still lenient in arithmetic (`emitIdent` unwraps the payload before the operator, so `a + b` on an absent `b` reads `0` rather than erroring — the same residual as any un-narrowed nullable local)
- Destructured function parameters (`function f({ x, y }: T) {}` / `function f([a, b]: T[]) {}`) — No combination with `...` or a whole-parameter default value; a pattern param with no annotation and no contextual type (a plain `function f([a, b])`) is still rejected, matching TS's implicit-any error
- Destructured function parameters (`function f({ x, y }: T) {}` / `function f([a, b]: T[]) {}`) — A `= default` on a nested position is a clean rejection; per-element defaults follow the same rules as the statement-level destructuring rows
- Nested function declarations (`function outer() { function inner() {...}; return inner(); }`) — A declaration inside a lexical block (an `if`/`for`/`while`/`do-while`/bare block, a `try`/`catch`/`finally` block, or a `switch` case) is supported and block-scoped ([TDD-00152](tdd/TDD-00152.md)/[ADR-00602](adr/ADR-00602.md)); the residual is the specialized `for…of`/`for…in` iterator variants (generator/stream/`Symbol.iterator` paths) that iterate their body directly (no miscompile — capturing such a loop variable is a clean rejection)
- Nested function declarations (`function outer() { function inner() {...}; return inner(); }`) — A block-nested declaration capturing a **C-style `for` loop variable** is a clean rejection — the loop variable is a single per-iteration cell; copy it to a `const` inside the loop body and capture that ([ADR-00602](adr/ADR-00602.md))
- Nested function declarations (`function outer() { function inner() {...}; return inner(); }`) — Captures enclosing locals/parameters (by reference, emitted as a closure value — [TDD-00129](tdd/TDD-00129.md) Stage 1/[ADR-00386](adr/ADR-00386.md)), but only for use **at or after** the declaration point — a capturing nested function called before its declaration is a clean error (full pre-declaration hoisting is Stage 2); a non-capturing one keeps full hoisting/forward-reference. Capturing an array variable, and a capturing nested *generator*, stay clean rejections
- Nested function declarations (`function outer() { function inner() {...}; return inner(); }`) — Generic (`<T>`) nested declarations and same-scope duplicate names are clean compile errors ([TDD-00057](tdd/TDD-00057.md)/[ADR-00149](adr/ADR-00149.md))
- `Function.prototype.call` / `.apply` / `.bind` — `thisArg` is evaluated then **ignored** — a closure has no rebindable `this`, so method-borrowing (calling a method value against a different receiver) is unsupported; only the arguments are forwarded
- `Function.prototype.call` / `.apply` / `.bind` — Only on a first-class function **value** (a closure, function parameter, arrow/function expression, or named function) — call/apply/bind on a built-in (`net.connect.apply(…)`) isn't supported, since built-ins aren't function values
- `Function.prototype.call` / `.apply` / `.bind` — `.apply` takes either a literal array (`f.apply(null, [a, b])`) or a runtime array spread into a **rest** parameter (`f.apply(null, arr)` where `f` is `(...xs) => …`); a runtime array into a fixed-arity function is the usual spread-into-fixed-arity rejection
- `Function.prototype.call` / `.apply` / `.bind` — `.bind` is V1-scoped to functions whose parameters are plain scalar/string/pointer types with no rest slot — an array/nullable-scalar/rest parameter is a clean compile error
- Function overload signatures (`function f(x: number): number;` + impl) — Signatures are parsed and **erased** — call sites type-check against the implementation's parameter list only, with no per-signature arity/type narrowing (an ill-formed group — a signature with no implementation, interrupted, or name-mismatched — is still a clean rejection)
- Generator functions (`function* f(): T { yield x; }`, `.next(value)`, `for...of`) — A free `function*` may be top-level **or nested** (a nested `function*` captures enclosing state by reference — an enclosing `let` mutated after the generator is created is seen by a later `.next()` — reusing the closure-boxing on the instance's `__env`; an *array* capture stays a clean rejection); a generator *expression* is supported only as a top-level `const/let/var G = function* ...` binding (rewritten to a named declaration, [TDD-00096](tdd/TDD-00096.md)/[ADR-00293](adr/ADR-00293.md)) — an argument/nested/IIFE use is a clean rejection. Instance generator methods — sync and `async *m()` — are supported separately (rows below), but `static`/`abstract` generator methods stay clean rejections ([TDD-00094](tdd/TDD-00094.md))
- Generator functions (`function* f(): T { yield x; }`, `.next(value)`, `for...of`) — The return-type annotation is optional — the element type is inferred from the body's yields (numeric join, `yield*` delegation, return fallback; only a genuinely non-joinable mix still requires the annotation — [ADR-00293](adr/ADR-00293.md)); still requires a plain non-destructured parameter list and a non-array parameter type (an array *element* type is supported — yielded/sent arrays round-trip through every generator slot, `for...of`/`.next()`/`yield*`/`for await` alike, [ADR-00676](adr/ADR-00676.md); a tuple/object element type also works)
- Generator functions (`function* f(): T { yield x; }`, `.next(value)`, `for...of`) — When present, the annotation may be the element type written directly (`function* f(): number`) or the idiomatic-TS wrapper around it — `Generator<T>`, `IterableIterator<T>`, `Iterator<T>`, `Iterable<T>` and their `Async` forms, plus the three-arg `Generator<T, TReturn, TNext>` (TReturn/TNext ignored in V1) — which unwraps to `T` ([ADR-00814](adr/ADR-00814.md))
- Generator functions (`function* f(): T { yield x; }`, `.next(value)`, `for...of`) — Calling `.next()` again after completion returns `{value: <T's zero value>, done: true}`, not `undefined` ([TDD-00061](tdd/TDD-00061.md)/[ADR-00173](adr/ADR-00173.md)) — deliberately kept bare even with the `T \| undefined` sentinel available: tsc types the result's `value` as `any`, so the typed `T` here is already stricter than TS, and flipping it would break every `.next().value` consumer for no faithfulness gain ([ADR-00781](adr/ADR-00781.md))
- `Promise.all` / `.race` / `.allSettled` — `Promise.allSettled` reports a rejected element's `.reason` as a wrapped `Error` (stringified message), not the original rejected value — `(await Promise.allSettled([Promise.reject(42)]))[0].reason.message` is `'42'`, where Node gives `.reason === 42`; the always-present `value`/`reason` fields also aren't omitted (a faithful reject-value model is [TDD-00169](tdd/TDD-00169.md)).
- Namespaces (`namespace X {}` / `module X {}`, function merging) — Top-level declarations only (a namespace inside a function body is not supported); no `declare namespace`, no cross-module `export namespace`; the same namespace member declared in two files is a link-time duplicate-symbol error, not file-private ([TDD-00095](tdd/TDD-00095.md)/[TDD-00148](tdd/TDD-00148.md))
- Namespaces (`namespace X {}` / `module X {}`, function merging) — Type members (class/interface/type/enum) desugar to *bare-name* top-level declarations — two namespaces declaring the same class name collide, ([ADR-00450](adr/ADR-00450.md)); outside `X.Enum.Member` / `X.Class.static` chains resolve through the qualifier strip ([ADR-00480](adr/ADR-00480.md))
- Namespaces (`namespace X {}` / `module X {}`, function merging) — A top-level namespace `const` initializer can't reference a sibling member (it evaluates outside the namespace context); sibling references *inside member function bodies* work, with consts subject to the [ADR-00342](adr/ADR-00342.md) promotion type limits
- Interfaces (structural) — An optional field whose type is an array or tuple stays bare — an omitted `tags?: string[]` reads as an empty array, not `undefined` (the `{ptr,i64}` slot has no distinct absent state — [ADR-00246](adr/ADR-00246.md))
- Object literals `{ key: value }` — No duplicate-`__proto__`-key detection (Annex B legacy `SyntaxError` rule)
- Getters / setters (`get x() {}` / `set x(v) {}`) on classes and object literals — An accessor-bearing **object literal** can't be assigned to a *differently-shaped* structural object type (`const p: { x: number } = objWithGetter`) — its accessors are methods, not fields; a clean rejection, used directly it works ([ADR-00603](adr/ADR-00603.md))
- Getters / setters (`get x() {}` / `set x(v) {}`) on classes and object literals — `console.log` of an accessor object prints only its data fields, not `x: [Getter]` (an inspect-fidelity gap)
- Getters / setters (`get x() {}` / `set x(v) {}`) on classes and object literals — Static accessors, and logical assignment (`&&=`/`\|\|=`/`??=`) on an accessor, remain out of scope for V1 ([TDD-00030](tdd/TDD-00030.md)/[ADR-00110](adr/ADR-00110.md))
- Built-in `Error` subtypes (`new TypeError(msg)`, `RangeError`, `SyntaxError`, `EvalError`, `URIError`, `ReferenceError`, `DOMException`) and `instanceof` against them — `class X extends Error` works one level deep ([ADR-00630](adr/ADR-00630.md)): construction, `super(msg)`, `.message`/`.name` (default `"Error"` unless assigned), extra fields/methods, throw/catch with `instanceof` discrimination against sibling subclasses, `Error.prototype.toString`; extending an Error *subclass* is a clean rejection, and `.stack`/`Error.captureStackTrace` don't exist
- Built-in `Error` subtypes (`new TypeError(msg)`, `RangeError`, `SyntaxError`, `EvalError`, `URIError`, `ReferenceError`, `DOMException`) and `instanceof` against them — `DOMException` carries no legacy numeric `.code`
- `new Array<T>(n?)` — A preallocated `new Array<T>(n)` fills real zero-valued slots, not holes — `new Array<number>(3)[0]` is `0` (Node: `undefined`), and `map`/`forEach` visit those slots instead of skipping holes.
- `class` (fields, constructor, methods, `this`, `new ClassName(args)`) — Instance-field initializers (`x = expr`) work, and a class with fields needs **no** explicit constructor — a bare declared field (`x: number`) reads as its calloc'd deterministic-zero value (0/false/null, the [ADR-00157](adr/ADR-00157.md) convention), any initializers that exist run in a synthesized constructor ([ADR-00374](adr/ADR-00374.md)); the one remaining rejection is a **derived** class adding fields when its base has a rest-parameter constructor (write an explicit `super(...)`). static field initializers (`static x = 5`) work — lowered to assignments run in declaration order in the class's static-init, ahead of any `static {}` block, with an unannotated one typed by inference ([ADR-00375](adr/ADR-00375.md)). An instance-field initializer lowered into the constructor can currently see the constructor's own parameters (which real JS's separate initializer scope forbids), and an unannotated initializer of an expression shape the compiler doesn't recognize falls back to `i64` ([TDD-00063](tdd/TDD-00063.md)/[ADR-00180](adr/ADR-00180.md))
- `class` (fields, constructor, methods, `this`, `new ClassName(args)`) — A computed/dynamic member name (identifier, call, or interpolation) is a clean rejection; the recognized well-known-symbol keys are `[Symbol.asyncIterator]` and `[Symbol.iterator]`, desugared to the async-/sync-iteration methods ([TDD-00089](tdd/TDD-00089.md), [ADR-00278](adr/ADR-00278.md))
- `class` (fields, constructor, methods, `this`, `new ClassName(args)`) — A class expression used as a runtime value (argument, return, nested binding) or a named self-reference (`class D {...}` whose body references `D`) is a clean rejection; `extends <expression>` is out of scope
- `class` (fields, constructor, methods, `this`, `new ClassName(args)`) — Class early-error gaps (deferred, no valid program miscompiles): a field initializer containing `arguments` or a `super()` call, a `super()` call in a method parameter default, field-definition ASI on the same line (`field = 1 method(){}`), and the `#constructor` private-name ban all compile instead of raising the spec's `SyntaxError`
- `class` (fields, constructor, methods, `this`, `new ClassName(args)`) — A bare field (`x;`) defaults to `number` (the unannotated-parameter convention; JSDoc `@type` overrides) — TS infers implicit `any`, so a non-numeric use is a shifted typed error ([ADR-00474](adr/ADR-00474.md))
- Real JS/TS `#x` runtime-private field syntax — No class-field-initializer syntax (`#x = 1;` — a pre-existing gap for every field, not `#`-specific)
- Real JS/TS `#x` runtime-private field syntax — No early-error check for a `#m`/`static #m` name collision in the same class, or for the `#constructor`-is-always-banned rule ([ADR-00155](adr/ADR-00155.md))
- `instanceof` (against user-defined classes) — `x instanceof Object` on a value of an *undecidable* static type (`any`, a union, or a nullable `T | null`) is a clean rejection — its runtime primitive-vs-object identity isn't statically known ([ADR-00605](adr/ADR-00605.md))
- Experimental decorators — class, property, parameter & method `@decorator` — A class decorator that *returns a replacement constructor* is refused at runtime (a loud throw, not a silent drop) — the static-class model has no constructor-replacement routing yet ([TDD-00161](tdd/TDD-00161.md) Stage 4b); observe-only class decorators (the registration pattern) run faithfully. A decorator on a generic class is a clean compile-time rejection
- Experimental decorators — class, property, parameter & method `@decorator` — Accessor (get/set), static, and generator method decorators, and method decorators on a generic class, are a clean rejection; a method whose parameter/return types can't be marshalled through the decorator ABI (array/nullable/rest parameters) is also rejected
- Experimental decorators — class, property, parameter & method `@decorator` — Factory-call decorators (`@dec(...)`) work across placements (the factory runs, its returned function decorates) when the returned decorator is a named function or a typed closure; a returned **`any`-typed closure that captures** the factory's arguments hits the separate dynamic-prototype-method capture limitation — annotate the factory's return type to route it through the typed-closure path
- Experimental decorators — class, property, parameter & method `@decorator` — The standard (TC39) `(value, context)` dialect (`-decorators=standard`, default is experimental) covers class (observe), method, and instance-field decorators (the field initializer + `context.addInitializer` callbacks run per-instance in the constructor), with `context = { kind, name, static, private, addInitializer, metadata }`; getter/setter and `accessor x` auto-field decorators run (a decorated `accessor` desugars to a backing field + generated get/set, and the decorator follows the TC39 `{ get, set, init }` protocol — a returned get/set replaces the accessor, a returned init transforms the backing field's initial value). Static-field decorators are still a clean rejection; TC39 has no parameter decorators. A returned *replacement* that reads typed `this.field` hits the general replacement-`this` limitation below
- Experimental decorators — class, property, parameter & method `@decorator` — A decorator *replacement* function (method, getter, or setter) runs as a dynamic function whose `this` is a boxed receiver, so it cannot do typed `this.field` access on a statically-typed instance — an observe-only decorator (returns the original) is unaffected, and a replacement that doesn't touch typed `this` fields works
- Experimental decorators — class, property, parameter & method `@decorator` — A dynamic function (any closure boxed into an `any`/decorator position) now binds its **declared parameter types** (so a typed body like a `(initial: number)` field initializer works) and **captures enclosing locals** (by shared heap cell, into the tag-12 record's env — so factory decorators, wrapping method decorators, and capturing `addInitializer` callbacks all work) ([ADR-00647](adr/ADR-00647.md))
- Experimental decorators — class, property, parameter & method `@decorator` — `emitDecoratorMetadata` design types are name-carrying descriptor objects (`{ name: "Number" }`, the class name for a class type) — correct for `.name` inspection, but not the real runtime constructors (no first-class class-constructor value), so identity checks fail; `Reflect.metadata` used manually as a decorator factory is rejected; a metadata target must be a real dynamic object (a boxed static class instance is rejected at runtime)

### Array Methods — Strict 23/37 (~62%) · 14 caveats — [Array Methods](status/ARRAY-METHODS.md)
- `new Array<T>(n?)` — A preallocated `new Array<T>(n)` fills real zero-valued slots, not holes — `new Array<number>(3)[0]` is `0` (Node: `undefined`), and `map`/`forEach` visit those slots instead of skipping holes.
- `.length` — `a.length = 2` (real JS's array-truncation idiom) hard compile-errors with "field assignment on non-object" — length is read-only in practice ([ADR-00166](adr/ADR-00166.md))
- `.push(...items)` — An array passed as an **object field** or **array element** (`obj.items`, `grid[i]`), or as a higher-order-callback element, still crosses as a copy, so a length change through those is not seen by the caller ([TDD-00127](tdd/TDD-00127.md))
- `.pop()` — When the array is passed as an **object field**/**array element** (`obj.items`, `grid[i]`) or a HOF-callback element, a length change in a callee is not seen by the caller — those still pass a copy ([TDD-00127](tdd/TDD-00127.md))
- `.shift()` — When the array is passed as an **object field**/**array element** (`obj.items`, `grid[i]`) or a HOF-callback element, a length change in a callee is not seen by the caller — those still pass a copy ([TDD-00127](tdd/TDD-00127.md))
- `.unshift(...items)` — When the array is passed as an **object field**/**array element** (`obj.items`, `grid[i]`) or a HOF-callback element, a length change in a callee is not seen by the caller — those still pass a copy ([TDD-00127](tdd/TDD-00127.md))
- `.splice(start, delete?, ...items)` — When the array is passed as an **object field**/**array element** (`obj.items`, `grid[i]`) or a HOF-callback element, a length change in a callee is not seen by the caller — those still pass a copy ([TDD-00127](tdd/TDD-00127.md))
- `.indexOf(item, fromIndex?)` — Rejects a nested-array element (`number[][]`) — compares a bare register, no callback ([ADR-00152](adr/ADR-00152.md))
- `.lastIndexOf(item)` — Rejects a nested-array element (`number[][]`) — compares a bare register, like `.indexOf` ([ADR-00152](adr/ADR-00152.md)/[ADR-00843](adr/ADR-00843.md))
- `.includes(item)` — Rejects a nested-array element (`number[][]`) — compares a bare register, no callback ([ADR-00152](adr/ADR-00152.md))
- `.sort(fn?)` — Rejects a nested-array element (`number[][]`) — the custom comparator is a C-ABI `qsort()` trampoline with one fixed variant per element kind ([ADR-00152](adr/ADR-00152.md))
- `.flat(depth?)` — `depth` must be a compile-time constant integer or `Infinity` — this compiler's arrays have a fixed nesting depth at the type level, so the result's element type has to be known at compile time ([TDD-00029](tdd/TDD-00029.md)/[ADR-00107](adr/ADR-00107.md))
- `.keys()` / `.values()` / `.entries()` — All return materialized arrays, not lazy iterators — this compiler has no general iterator protocol (the same convention `Map`/`Set`'s own `.keys()`/`.values()`/`Map.entries()` use) ([ADR-00057](adr/ADR-00057.md))
- `Array.from(iterable, mapFn?)` — Generators, `thisArg`, and indexed array-like properties (`{ length: 2, 0: 'a' }`) are not supported

### Global Functions & Constants — Strict 14/21 (~67%) · 7 caveats — [Global Functions & Constants](status/GLOBAL-FUNCTIONS.md)
- `NaN` (global constant) — `let NaN = 99;` is a reserved-name collision under the default `-compat=strict`; `-compat=js` allows the shadow real JS permits
- `Infinity` (global constant) — Same shadowing caveat as `NaN` above — needs `-compat=js`, not unconditional
- `globalThis` — Only member access resolves, and only to *known* globals — an unknown `globalThis.foo` is a compile error (there is no dynamic global record); a bare `globalThis` used as a standalone object value, computed access (`globalThis["x"]`), and assigning a new global (`globalThis.x = …`) are unsupported
- `structuredClone(obj)` — `EventEmitter`/`URL`/`URLSearchParams`/functions/class instances/`Promise`/`any`/`unknown` are rejected at compile time rather than silently aliased (`URL` matches Node, which throws `DataCloneError` for it); a `Map`/`Set` with an **array/Map/Set** key or value element type is also rejected (only scalar/string/object elements clone — [ADR-00574](adr/ADR-00574.md))
- `structuredClone(obj)` — A cloned `AggregateError`'s `.errors` degrades to empty
- `queueMicrotask(fn)` — Drained at the reachable checkpoints (end of the top-level script, each scheduler step); a program with neither timers nor async tasks drains once at exit
- `gc()` — A no-op under `-mm=manual` (the default) — nothing is ever collected; only meaningful under `-mm=gc`, where it forces a full Boehm collection

### JSON — Strict 10/15 (~67%) · 5 caveats — [JSON](status/JSON.md)
- `JSON.stringify(value, null, space)` (pretty-printing) — `space` must be a literal number (N spaces, capped at 10) or literal string — a runtime `space` value is a clean compile error ([ADR-00222](adr/ADR-00222.md))
- `JSON.stringify(value, null, space)` (pretty-printing) — The `replacer` (2nd) argument is supported only as `null`/undefined — a function/array replacer is a clean compile error, not silently ignored
- `JSON.parse(s)` → top-level `T[]` (incl. object & nested arrays) — A bare reassignment into a **member or element** target (`obj.items = JSON.parse(...)`, `grid[i] = JSON.parse(...)`) isn't projected from declaration context — write the target type on the call (`obj.items = JSON.parse(...) as Item[]`) to project anywhere ([ADR-00715](adr/ADR-00715.md))
- `JSON.parse(s)` validates input (throws `SyntaxError` on malformed JSON) — The `SyntaxError` message is position-based (`Unexpected token in JSON at position N`), not Node/V8's exact per-token wording
- `JSON.parse(s)` → `any`/`unknown` (dynamic shape) — The result is a dynamic tree — statically-typed operations on it (arithmetic on elements, passing into typed slots) hit the normal `any` limits until narrowed; `JSON.parse(s) as T` narrows at the source, routing through the typed projection instead ([ADR-00715](adr/ADR-00715.md))

### console — Strict 8/12 (~67%) · 4 caveats — [console](status/CONSOLE.md)
- `console.log(...)` — A string with an embedded null byte truncates *on display* at the first `\0` (`printf` `"%s"`); the stored string is intact. No binary-safe print — use `process.stdout.write` for binary output
- `console.trace(...)` — Prints `"Trace: <message>"` and nothing else; real Node's entire point of `.trace()` is the call stack it prints below the message, which this never generates at all
- `console.table()` — An array of objects (columns = the shared fields) or an array of primitives (a single `Values` column) is tabulated; a `columns` filter argument, a plain-object argument, and an array-of-arrays shape aren't tabulated (they fall back to `console.log`, as a non-tabular value does)
- `console.dir(obj, { depth?, colors? })` — The `colors` option is accepted but ignored — inspected output carries no ANSI here

### path — Strict 8/10 (80%) · 3 caveats — [path](status/PATH.md)
- `path.resolve(...segments)` — `path.win32.resolve()` on a non-Windows host resolves against a POSIX `cwd` (`/home/me` → `\home\me\foo`), exactly as Node does there; only meaningful on Windows.
- `path.posix` / `path.win32` — The flavour objects are dispatch targets, not values: `const p = path.win32` and then `p.join(...)` is not supported — call through `path.win32.join(...)` or a named import (`import { win32 } from 'path'`).
- `path.posix` / `path.win32` — `matchesGlob` is missing from both flavours (it needs Node's glob matcher).

### HTTP Server — Strict 14/16 (~88%) · 7 caveats — [HTTP Server](status/HTTP-SERVER.md)
- `http.createServer((req, res) => …).listen(port[, cb])` — real Node shape — No HTTP pipelining, and `Keep-Alive: timeout=5` is advertised but not enforced by an idle timer
- `http.createServer((req, res) => …).listen(port[, cb])` — real Node shape — An additional concurrent server can't combine with `new Worker()` threads (fork + threads is unsafe), needs an inline listener (no `createServer()`+`.on('request')` split), and a non-primary `ws`/`upgrade` handler needs a `const x = http.createServer(...)` binding
- `const server = http.createServer(cb?)` handle — `.listen(port?, cb?)`/`.close(cb?)`/`.closeAllConnections()`/`.address()`/`.on('request'/'upgrade', cb)` — The `'listening'`/ready callback fires synchronously at `listen()` time, not on a later tick, so a `close()` before that tick doesn't cancel it (edge divergence)
- `const server = http.createServer(cb?)` handle — `.listen(port?, cb?)`/`.close(cb?)`/`.closeAllConnections()`/`.address()`/`.on('request'/'upgrade', cb)` — `'close'`, `'clientError'`, `'error'` and the connection-timeout options are primary-server-only; `'clientError'` exposes a small honest `.code` subset (the parser produces no llhttp `HPE_*` codes); a `'connection'` socket's own `'data'`/`'close'` listeners aren't driven (the HTTP parser owns the byte stream)
- `const server = http.createServer(cb?)` handle — `.listen(port?, cb?)`/`.close(cb?)`/`.closeAllConnections()`/`.address()`/`.on('request'/'upgrade', cb)` — Connection timeouts close silently with no `408` body (V1) and don't cover HTTP/2
- `const server = http.createServer(cb?)` handle — `.listen(port?, cb?)`/`.close(cb?)`/`.closeAllConnections()`/`.address()`/`.on('request'/'upgrade', cb)` — A handler wrapped in any call other than the `test` `mustCall(...)` counting wrapper isn't contextually typed
- `const server = http.createServer(cb?)` handle — `.listen(port?, cb?)`/`.close(cb?)`/`.closeAllConnections()`/`.address()`/`.on('request'/'upgrade', cb)` — Rejected: a behavior-changing value of a no-op option (e.g. `noDelay: false`), a custom `IncomingMessage`/`ServerResponse` class (needs the D1 dynamic object model), and `maxHeaderSize`

### Number / Math — Strict 32/35 (~91%) · 3 caveats — [Number / Math](status/NUMBER-MATH.md)
- `Number.prototype.toFixed(n)` — Exact-halfway values round half-to-even (C `printf`), not JS's round-half-up — `(2.5).toFixed(0)` is `'2'` (Node: `'3'`) and `(8.5).toFixed(0)` is `'8'` (Node: `'9'`).
- `Number.prototype.toString(radix?)` — For a **non-power-of-two** base a repeating fractional expansion is capped at 1100 digits and the double-precision multiply can differ from V8's exact-bignum result in the trailing digits
- `Number.prototype.toPrecision(n)` — Chooses exponential vs fixed notation at C `%g`'s threshold (exponent < -4) rather than JS's (exponent < -6), so a small magnitude like `0.0000123` renders `"1.23e-5"` where real JS gives `"0.0000123"` ([ADR-00065](adr/ADR-00065.md))

### JavaScript built-in objects (completeness index) — completeness index (not parity-counted) · 31 caveats — [JavaScript built-in objects (completeness index)](status/JAVASCRIPT-BUILTINS.md)
- `Error` + subtypes (`TypeError`/`RangeError`/`SyntaxError`/`EvalError`/`URIError`/`ReferenceError`/`AggregateError`/`DOMException`), `class X extends Error` (1 level) — No error-options second argument — `new Error(m, { cause })` fails to parse, so `.cause` is unavailable
- `Error` + subtypes (`TypeError`/`RangeError`/`SyntaxError`/`EvalError`/`URIError`/`ReferenceError`/`AggregateError`/`DOMException`), `class X extends Error` (1 level) — `.stack` is typed a number, not a string (`typeof err.stack` is `'number'`, Node: `'string'`)
- `Promise` (`all`/`race`/`allSettled`/`any`/`resolve`/`reject`, executor, `then`/`catch`/`finally`) — `Promise.allSettled` reports a rejected element's `.reason` as a wrapped `Error` (stringified message), not the original rejected value — `.reason.message` is `'42'`, Node: `.reason === 42` (faithful reject-value model is [TDD-00169](tdd/TDD-00169.md))
- `Promise` (`all`/`race`/`allSettled`/`any`/`resolve`/`reject`, executor, `then`/`catch`/`finally`) — The always-present `value`/`reason` fields aren't omitted on the opposite outcome
- `globalThis` — `globalThis` exists only inside `typeof globalThis` — used as a value (property access, assignment, identity), it fails compilation with `undefined variable 'globalThis'`.
- `performance` (`now`/`mark`/`measure`) + `Date` (+`now`/`parse`, setters, arithmetic) — `Date` is a plain i64 epoch with no `NaN` — an invalid date is a `-1` sentinel (`new Date('bad').getTime()` is `-1`, Node: `NaN`)
- `performance` (`now`/`mark`/`measure`) + `Date` (+`now`/`parse`, setters, arithmetic) — Out-of-range date fields wrap rather than invalidating (`Date.parse('2020-13-45')` is a valid timestamp, Node: `NaN`)
- `performance` (`now`/`mark`/`measure`) + `Date` (+`now`/`parse`, setters, arithmetic) — No `Date.UTC` static, `getTimezoneOffset`, or `Date.prototype.toString` — each is a compile error
- `FinalizationRegistry` — `cleanupSome` (non-standard) rejected; aggregate held types (arrays, nullable scalars) rejected
- `FinalizationRegistry` — Same-thread V1: a worker's registrations are flushed only by its own thread, not the process exit hook
- `FinalizationRegistry` — Under `-mm=gc`, firing depends on the target actually being collected (conservative scanning can pin a stack-reachable pointer — same posture as the `WeakRef` tests)
- `String` — `.normalize()` **missing** (no Unicode tables); `.at()` OOB returns `""` not `undefined`; `.codePointAt()` == `.charCodeAt()` (byte strings, correct only ASCII/Latin-1); `.matchAll()` eager not lazy; `.localeCompare()` is byte-order → [String methods](STRING-METHODS.md)
- `Array` — length-mutating methods propagate to caller only for plain **variable** params (not object-field/array-element receivers); `a.length = n` truncation compile-errors; `.keys`/`values`/`entries` materialized not lazy; `.flat(depth)` needs a constant depth → [Array methods](ARRAY-METHODS.md)
- `Object` / dynamic model — prototype machinery, descriptors, accessors exist on **`any`-typed / js-mode dynamic objects only**; static structs are fixed-shape (no dynamic add/delete, no prototype); `Object.assign` can't graft new fields; `hasOwn` needs string-literal keys → [Object, Map & Set](OBJECT-COLLECTIONS.md)
- `Reflect` — missing `apply`/`construct`; requires a dynamic target → [Object, Map & Set](OBJECT-COLLECTIONS.md)
- `Proxy` — only `get`/`set`/`has`/`deleteProperty` traps; dynamic target only → [Object, Map & Set](OBJECT-COLLECTIONS.md)
- `Number` — `toString(radix)` non-power-of-two fractional trailing-digit divergence; `toPrecision` fixed/exp threshold differs → [Number & Math](NUMBER-MATH.md)
- `Function` `.call`/`.apply`/`.bind` — forward args but **ignore `thisArg`** (no method-borrowing); `.bind` scalar-param only; first-class function values, not built-ins → [Language constructs](LANGUAGE-CONSTRUCTS.md)
- `Symbol` — no well-known symbols as runtime values; only `[Symbol.iterator]`/`[Symbol.asyncIterator]` recognized syntactically → [Type system](TYPE-SYSTEM.md)
- `RegExp` — `u`/`y`/`d` flags **missing** (accepted, not implemented); `exec` result lacks `index`/`input`/`groups`; unmatched groups become `""` not `null` → [RegExp](REGEXP.md)
- `JSON` — statically-typed heterogeneous-array `stringify` **missing** (use a tuple/`any`); function/array `replacer` rejected; `space` must be literal → [JSON](JSON.md)
- TypedArrays / `ArrayBuffer` — no `.buffer` back-ref; `resize`/`grow` need `{maxByteLength}`; views don't length-track resize → [Binary data & typed arrays](BINARY-DATA-TYPED-ARRAYS.md)
- `TextDecoder` — UTF-8 only; non-UTF-8 labels throw `RangeError` at construction → [Encoding & text](ENCODING-TEXT.md)
- `URLSearchParams` / `URLPattern` — `URLSearchParams` keeps one value per key; `URLPattern` is object-init only with a reduced grammar and a merged-`Map` `.exec()` result → [URL](URL.md)
- `EventTarget` / `Event` / `AbortSignal` — single-target dispatch (no capture/bubble/propagation); reduced Event property set; `AbortSignal` custom `abort(reason)` surfaces as `AbortError`; wired into `fetch` not `setTimeout` → [Events & cancellation](EVENTS-CANCELLATION.md)
- `crypto.subtle` — literal-only algorithm dispatch; ops throw synchronously rather than rejecting; jwk as `Map<string,string>`; `CryptoKey.algorithm`/`.usages` unimplemented → [Web Crypto](WEB-CRYPTO.md)
- `Date` — UTC-only everywhere (deliberate); `parse` returns a `-1` sentinel not `NaN`; setters need a named-variable receiver; no locale/Intl formatting → [Performance timing](PERFORMANCE-TIMING.md)
- `performance` — `mark`/`measure` last-write-wins; `measure` returns a plain number; no `PerformanceObserver`/entries → [Performance timing](PERFORMANCE-TIMING.md)
- `eval` — general/dynamic eval **missing**; only a compile-time-constant `eval("<expression>")` static subset works → [Global functions](GLOBAL-FUNCTIONS.md)
- `globalThis` — member access to known globals only; no bare-value use, computed access, or new-global assignment → [Global functions](GLOBAL-FUNCTIONS.md)
- `globalThis` — `globalThis` exists only inside `typeof globalThis` — used as a value (property access, assignment, identity), it fails compilation with `undefined variable 'globalThis'`.

### TypeScript language features (completeness index) — completeness index (not parity-counted) · 16 caveats — [TypeScript language features (completeness index)](status/TYPESCRIPT-FEATURES.md)
- `any` / `unknown` — Full only under `-compat=js` (NaN-boxed D1 dynamic objects/prototypes/descriptors); under `-compat=strict` arithmetic on `any` is a compile error. Gaps: primitive-member dispatch through `any`, `Object.values/entries` on dynamic objects, allocation-site widening of a boxed static object beyond a fresh `new C()` (aliased binding / method class / cyclic shape — [ADR-00990](adr/ADR-00990.md)) → [Type system](TYPE-SYSTEM.md)
- Union types — Scalars, single-object, and first-position string-literal discriminated unions only; no array-element unions, non-first-position/number-literal tags; narrowing is local (`typeof`/truthiness/`==null`) — no `switch(typeof)`, `as`-narrowing, or tag narrowing → [Type system](TYPE-SYSTEM.md)
- Intersection types — Object-type members only; conflicting non-object fields rejected (TS `never`-field not modeled) → [Type system](TYPE-SYSTEM.md)
- Tuple types — No rest/optional/named elements; constant index only; no array methods; not nestable in `any`/union → [Type system](TYPE-SYSTEM.md)
- Mapped & utility types — Effective ones are `Pick`/`Omit`/`Record`; `Partial`/`Required`/`Readonly` are erased structural no-ops. No key remapping (`as`), no `-?`/`-readonly` modifier removal; compile-time-only → [Type system](TYPE-SYSTEM.md)
- Conditional types + `infer` — `infer` limited to `Array`/`Promise<infer>` and bare `infer R`; no `(...) => infer R`; assignability is structural width, not full variance → [Type system](TYPE-SYSTEM.md)
- Generics — Monomorphization-based; **no call-site type args for functions** (generic *classes* take `new Box<T>()`); each type param needs an inferable `T`/`T[]` param; generic functions aren't first-class values → [Type system](TYPE-SYSTEM.md)
- Type assertions — `as T` / `as const` / `satisfies` / `<T>x` — **Erased, not enforcing** — the value keeps its inferred type; a cast relied on for re-typing does not take effect (matches TS runtime erasure, not static narrowing) → [Type system](TYPE-SYSTEM.md)
- Template-literal types & string-literal types — Parsed but **erased/widened to `string`**, not narrowed or enforced → [Type system](TYPE-SYSTEM.md)
- `readonly T[]` modifier — Erased, not enforced; `ReadonlyArray<T>` alias not covered → [Type system](TYPE-SYSTEM.md)
- Ambient declarations (`declare var`/`function`/`enum`/`class`/`module`/`namespace`/`global`) — `declare var`/`function`/`enum` are real bindings; brace-bodied ambient forms parsed and erased (no external link target under whole-program AOT) → [Type system](TYPE-SYSTEM.md)
- Namespaces — Top-level only; no `declare namespace`; members desugar to bare-name top-level decls (cross-namespace same-name class collides) → [Language constructs](LANGUAGE-CONSTRUCTS.md)
- Function overloads — Signatures parsed and **erased**; call sites check the implementation only (no per-signature narrowing) → [Language constructs](LANGUAGE-CONSTRUCTS.md)
- `Function.prototype.call`/`apply`/`bind` — `thisArg` evaluated then ignored (no rebindable `this`, so no method-borrowing); first-class function values only, not builtins → [Language constructs](LANGUAGE-CONSTRUCTS.md)
- Decorators — Class-decorator **replacement** is a documented static-model divergence (refused at runtime), and standard static-field decorators are rejected → [Language constructs](LANGUAGE-CONSTRUCTS.md)
- Symbols — V1 opaque unique values (`Symbol()`, `===`, `typeof`, `.description`, `Symbol.for`/`keyFor`); no dynamic property keys; only `[Symbol.iterator]`/`[Symbol.asyncIterator]` recognized as computed keys → [Type system](TYPE-SYSTEM.md)

### Node.js built-in modules (completeness index) — completeness index (not parity-counted) · 5 caveats — [Node.js built-in modules (completeness index)](status/NODE-MODULES.md)
- `timers` — Pending: `timers/promises`
- `url` — IDN conversion is libcurl-backend-gated
- `url` — Lenient relative parsing deferred
- `perf_hooks` — `PerformanceObserver` is synchronous V1 (no async-batched observer delivery)
- `perf_hooks` — Pending: `monitorEventLoopDelay`/`createHistogram`
