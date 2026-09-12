# Backlog

A prioritised list of what's **left to be done** — nothing else. Not a changelog,
not a status page, not a trophy cabinet for finished work.

**House rules — please keep them. Ignoring them has cost us real bugs:**

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

The exhaustive list of every unfinished TDD is the generated table in
[docs/status/README.md](status/README.md); this file is the prioritized shortlist.

---

## 1. Highest leverage — do these first

- **Conformance denominator honesty — Track 5 residue** (TDD-00204, Tracks 1–4
  shipped: ADR-00875–00879, per-platform report folders + live TUI dashboard
  included). Remaining: the **headless DOM shim** (jsdom tier — element tree,
  events, parser) that unlocks the ~20–25K WPT testharness `.html` documents;
  own TDD before code. Adjacent follow-ups the new reports surfaced: the async
  lane's `then`-handler `5e-324` boxed-value corruption (real bug, high value);
  TS script-mode global merging (top multi-file false-reject shape).

- **Fully-incremental `res` `Writable`** (TDD-00195 Stage 2 remainder) — server
  `req` is a Node `Readable` and the response flush is now driven by `res.end()`
  (fire-and-forget handlers work), but `res` is still buffered (one flush at
  `res.end`). Incremental `res.write` streaming to the socket mid-handler with
  real backpressure/`'drain'` and `req.pipe(res)` remain.
- **`http.createServer` edges** — `createServer` options object mostly rejected;
  `Connection: close` on HTTPS/1.1 streaming; the `'error'`/`'clientError'`
  server events (`'close'`/`'connection'` done, primary-only); HTTP/2 has no
  request bodies; multi-server can't combine with cluster.
- **D1 dynamic object model** (TDD-00155) — runtime property add/delete on typed
  structs, full `Proxy` trap set, well-known symbols as dispatch. Adjacent:
  TDD-00068, TDD-00176.
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

- **fs options residue** — no write `mode`; no non-`utf8` encodings;
  `readFileSync` with no encoding returns a string, not a `Buffer`; no
  `readdirSync` `recursive`+`withFileTypes` together nor `encoding:'buffer'`; no
  `Dirent.path`; `COPYFILE_FICLONE` a no-op; `fs.constants` lacks `O_*`.
  Genuinely-open edges with a reason: `rmSync` retries, `birthtimeMs` on Linux,
  the `fdatasync` distinction.
- **Streams** (TDD-00132) — no BYOB/byte controllers; string chunks default; no
  options-form `transform`; 3-arg `write` rejected; `extends Transform<In,Out>`
  parse error; `ReadableStream.from()` arrays only.
- **WebSocket** — no binary `.send()`; binary `ev.data` NUL-truncates (use
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
- **URL/URLSearchParams overhaul (TDD-00203) — mostly shipped, residue:**
  `URLSearchParams` is now a faithful ordered pair-list (ADR-00873) and `URL` got
  default-port stripping, host lowercasing, `URL.parse`/`canParse` statics, and
  `JSON.stringify`→`href` (ADR-00874). Remaining: (a) `url.searchParams` is a
  **snapshot**, not live — mutations don't write back to `url.search` (needs the
  URL object to hold the pair-list handle and re-serialize on mutation); (b)
  **non-special-scheme** URLs (`new URL('foo:bar')`) — libcurl rejects them, needs
  a hand-written WHATWG parser instead of libcurl; (c) IDN of a non-ASCII domain
  on the Mac build needs libcurl+libidn2 (platform, not code).
- **Other WPT-surfaced Web-API gaps** (`-suite wpt`, ADR-00871): `EventTarget`
  listeners must be exactly a 1-arg function so the `.onabort`-property /
  0-arg-listener `dom/abort` tests don't compile; `instanceof` against the ambient
  `AbortSignal` class is unsupported.
- **Any-equality: a null value compares unequal to `null`.** A null string boxed
  as `any` (`URLSearchParams.get(missing)` passed to an `any` parameter) is not
  `=== null` — surfaced by the WPT harness's `assert_equals(x, null)`, blocks a
  couple of `url/` files. In the D1 NaN-box any-eq subsystem (TDD-00155), not
  URL-specific.
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
- `Array.from({ length: n }, fn)` / `Array.from({ length: n })` — the array-like
  overload (object `length` protocol + undefined fill) is unbuilt; the
  `(iterable, mapFn)` and string/Map/Set forms work.
- Heterogeneous / union-element arrays (TDD-00200) — boxed-element `any[]`
  representation + full method surface shipped (ADR-00887, `-compat=js` boxes
  inferred mixed literals / heterogeneous `[]`, strict rejects recognizably).
  Residue: a *constrained union* element (`(A | B)[]`) is still rejected
  (element-level union checking unwired, TDD-00043); a statically-typed
  object/array *value* boxed into an element is type-erased — deep
  `JSON.stringify` of it throws (object *literals* are fine).
- Honor `--unhandled-rejections=none`: an unhandled rejection still stringifies
  the rejection value (Node never touches it) — don't eagerly coerce it.
- A thrown plain object's own fields aren't readable after catch (`throw {x:1}`;
  `e.x`) — needs D1 runtime object shape (TDD-00155 Stage 6). Primitives + Errors
  are faithful.
- Invalid-IR backlog — a function / non-primitive operand under `-compat=js`
  (`{} & function(){}`, `f - 1`) truncates a raw pointer — the broad
  operator-on-any compat=js gap, not bitwise-specific (strict rejects cleanly).
- Dynamic `import()` beyond the eager V1 (TDD-00055); the `-compat`
  per-divergence flags (TDD-00075); `any` residues (TDD-00162).
- `libbf` (MIT) as a third selectable `-bigint` backend alongside
  libtommath/gmp.
- **WebCrypto `crypto.subtle`** — ~7 algorithm ops still "not implemented"; heavy
  format/curve restrictions.
- **Cross-cutting roots (high leverage):** array length-mutation doesn't
  propagate through object-field / element / HOF-callback arrays (TDD-00127);
  nested-array element rejection in `.sort`/`.indexOf`/`.includes`/
  `Object.groupBy` (ADR-00152); `.buffer` absent on TypedArrays, views don't
  track `resize` (ADR-00494/00564).

## 5. Perf / GC / infra

- Escape analysis / stack allocation (TDD-00134; default-gate TDD-00174) — **the
  dominant native perf gap**: every object literal heap-allocates today.
- Deep reclamation under `-mm=auto` (TDD-00175); precise/moving Immix GC
  (TDD-00135); array amortized-growth `cap` (ADR-00517).
- DWARF debug symbols (TDD-00073); richer diagnostics / strict error-matching
  (TDD-00072); `@readonly`/`@pure` enforcement (TDD-00126/00128).
- Conformance infra: the WPT slice (TDD-00082); statusgen deriving the website
  reference badges from the status source instead of hand-mirroring (TDD-00145).

## 6. Deliberately deprioritized (later)

- Leaf pseudo-APIs as end-game conformance-unblockers: IndexedDB (TDD-00011);
  Notifications / `localStorage` / Clipboard / Geolocation (TDD-00171); Gamepad
  (TDD-00170); Canvas-2D; the File API family (TDD-00172).
- The TDD-00147 Android port.
- Native desktop GUI — Qt/QML for KDE-Linux + Sailfish Silica (TDD-00192,
  superseding the TDD-00032 placeholder).
- The alt-webview CEF/Qt shims (TDD-00144) + multi-window from TDD-00142.
- The TUI-framework roadmap (TDD-00150).
- `TextDecoder` non-UTF-8 (TDD-00034).
- The `klmpm` package manager (TDD-00054); npm/`node_modules` interop (TDD-00053).
- An alt Go `fetch` backend (TDD-00003).
- Self-hosting (TDD-00124) with its `klain:` module set (TDD-00189).
- `klain:ffi` beyond-Node FFI (TDD-00190).

Reference docs, not queued work: the WASM target (TDD-00048), the Pico (TDD-00036)
and Raspberry Pi (TDD-00045) targets, Immix (TDD-00135).
