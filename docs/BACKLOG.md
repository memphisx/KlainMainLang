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

- Conformance headless DOM shim (TDD-00204 Track 5): jsdom tier — element tree,
  events, parser — unlocks the ~20–25K WPT testharness `.html` documents; own TDD
  before code. Plus TS script-mode global merging (top multi-file false-reject).

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
- URL/URLSearchParams (TDD-00203): (b) non-special-scheme URLs
  (`new URL('foo:bar')`) — libcurl rejects them, needs a hand-written WHATWG
  parser; (c) IDN of a non-ASCII domain on Mac needs libcurl+libidn2 (platform).
- **Other WPT-surfaced Web-API gaps** (`-suite wpt`, ADR-00871): no `.onabort`
  event-handler property on `AbortSignal`; `instanceof` against the ambient
  `AbortSignal` class is unsupported.
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
- `libbf` (MIT) as a third selectable `-bigint` backend alongside
  libtommath/gmp.
- **WebCrypto `crypto.subtle`** — ~7 algorithm ops still "not implemented"; heavy
  format/curve restrictions.
- **Cross-cutting roots (high leverage):** array length-mutation doesn't
  propagate through object-field / element / HOF-callback arrays (TDD-00127);
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
