# Remaining-Work Backlog

A reconciled, whole-project audit of what is actually left, distinct from the
per-feature status pages (`docs/status/`, generated from `docs/status/data/*.json`)
and the design docs (`docs/tdd/`). This is a hand-maintained planning doc: when an
item ships, delete it here and update the status JSON / ADR / TDD in the same
commit. Snapshot date: **2026-09-08**.

Reconciled against the status JSON (authoritative for *current* shipped state),
the TDD index, and a code-level sweep — items an older design doc still lists
as "to do" but which have since shipped are excluded here.

Legend: **[X-plat]** cross-platform · **[Win]** Windows-specific ·
**Enabler** (unblocks a cluster) · **Cluster** (broad) · **Leaf** (isolated).

Completeness: the *mechanically exhaustive* list of every not-yet-done TDD is
the generated table in [docs/status/README.md](status/README.md)'s Design
Documents section; this file is the **prioritized** view, and every unfinished
TDD is placed in one of the sections below. Guiding principle (carried from the
old status-README roadmap): most caveats here are deferred shortcuts, not
permanent design decisions — where a divergence from real JS/TS is fixable and
moves conformance, fix it on sight rather than documenting it as intentional;
only genuine scope narrowings get to stay.

---

## 1. Highest-leverage clusters (attack these first)

1. **Remaining `T | undefined` adopters** — [X-plat] Leaf cluster
   (TDD-00187's last per-site adoption):
   - `ReadableStream.read()` after close — needs the discriminated
     `{done:false, value:T} | {done:true, value:undefined}` record design
     (plus `desiredSize` `0`-vs-`null`), not a plain field flip.
   Deliberate non-goal: a generator's `.next().value` stays bare — tsc types
   it `any` there, so the current typed `T` is already stricter than TS;
   flipping it would be less faithful, not more (ADR-00781).

2. **Real async streaming leftovers** — [X-plat]. TDD-00186 (+ TDD-00185,
   the thread-pool substrate it rides on). Left: the fetch body promises
   buffer eagerly at the accessor call rather than lazily off the fetch
   reactor (so intervening work doesn't overlap the download, and a `.json()`
   parse error surfaces at the call, not as a rejection); consumer
   backpressure on the pooled read streams; the legacy `fs.readFile(path, cb)`
   callback form and binary-data async writes still inline on the loop thread;
   the pool itself Windows-pending (folds into the reactor work, §2); and
   `http` request/response bodies as first-class Node `Readable`/`Writable`.
   Core to the microservice / REST-server target.

3. **`http.createServer(...).listen()` is single-instance, once-per-program** —
   [X-plat] Cluster. TDD-00131 Stage 1. `http.close()` doesn't lift it; options
   object mostly rejected; keep-alive only on buffered `res.end` (chunked /
   streaming forces `Connection: close`); HTTP/2 has no request bodies
   (`req.end()` is a no-op). A real limit for the server-side priority.

4. **D1 dynamic object model** — [X-plat] Cluster/Enabler. TDD-00155.
   ~30% of all documented caveats: runtime property add/delete on
   statically-typed structs, the full `Proxy` trap set, well-known symbols as
   runtime dispatch (only `Symbol.iterator`/`asyncIterator` honored today).
   Adjacent design decisions: the static-vs-dynamic object-model axes
   (TDD-00068, direction decided) and general `as T` on dynamic values
   (TDD-00176, open).

5. **General C FFI (AOT `node:ffi`)** — [X-plat] Enabler. TDD-00164.
   Foundational pivot: unblocks `node:sqlite` (TDD-00151), the `opentui` TUI
   backend, GTK/Qt/SDL/imgui GUI, ncurses, native DB clients.

---

## 2. Windows port

The **incremental** port is effectively complete — the remaining leaves are
either untestable from a dev box or deliberate divergences. The only substantial
Windows work is structural, and by policy is built **from Mac/Linux** (Docker +
CI), never from the Windows box.

### Structural (the real remaining Windows debt)
- **Event-loop reactor / owned handle table** — TDD-00182 (Option A, 6 stages)
  → TDD-00183 (IOCP, Option B, committed follow-on). Dissolves the whole §1
  cluster: blocking `stdin`/pipe reads, ~10 ms idle poll-spin, 384-socket
  `select()` cap, `dup2` socket aliasing, synchronous `CreatePipe` child stdio —
  and is where the fs thread pool (POSIX-only today) folds in for Windows.
- **`cluster` round-robin** — 3 skipped tests. TDD-00177 OQ7 / TDD-00105.
  Workers share one listening socket; `http.close()` from a worker doesn't reach
  siblings. Reactor-adjacent.

### Deferred edges (low value / untestable from a dev box)
- Broken-pipe write nuances: `WSAESHUTDOWN`→`EPIPE` (not observably reachable —
  the JS `Writable` layer intercepts write-after-`end()`); and the TLS-via-BIO
  error code on long-lived pub/sub connections (gRPC/HTTP2/`wss`), unverified vs
  Node. A persistent-connection probe would settle the latter. TDD-00180 §5.
- Named pipes `\\.\pipe\…` for `net.connect({path})`/`listen(path)`. TDD-00180 §5.
- Non-ASCII console **input** — the fully-reliable wide `ReadConsoleW`→UTF-8
  path. TDD-00180 §2.
- `readlink` UNC / junction PrintName edge; `chdir` hidden `=X:` drive vars.
  TDD-00180 §4.

### CI-lane-pending verification (implemented, needs the runner)
- `lstat(symlink).size` on Windows (ADR-00769) — the dev box lacks symlink
  privilege (Developer Mode), so the shim compiles but the symlink assertion
  skips; the CI runner creates symlinks.

### Out of scope by design
`--static` full-static on Windows, `-crypto=commoncrypto` (macOS-only), ASan/UBSan
(no mingw runtime). `-mm=gc` works (the fork path is skipped).

---

## 3. Node / runtime surface (mostly leaf, incremental)

- **`fs` options-argument residue**: no write `mode` arg; no non-`'utf8'`
  encodings; `readFileSync` with no encoding returns a string, not a `Buffer`;
  no `readdirSync` `recursive`+`withFileTypes` together, nor
  `encoding:'buffer'`; no `Dirent.path` alias; the `COPYFILE_FICLONE` bits are
  a best-effort no-op; `fs.constants` lacks the host-specific `O_*` open flags
  (no API consumes them yet). Still genuinely open,
  each for a stated reason: `rmSync` `maxRetries`/`retryDelay` (Windows-centric
  retry; needs a catchable recursive `rm`), `birthtimeMs`=0 on Linux (glibc;
  needs `statx`; Linux-only), and `fdatasyncSync`==`fsyncSync` (already faithful
  on macOS, which has no `fdatasync`; the Linux data-only distinction is
  non-observable).
- **Streams (WHATWG + Node)** — TDD-00132. No BYOB/byte controllers; string
  chunks default; no options-form `transform`; `write(chunk,enc,cb)` 3-arg
  rejected; `extends Transform<In,Out>` parse error; `ReadableStream.from()`
  arrays only. Enabler for binary/fetch-body streaming.
- **WebSocket**: no binary `.send()`; binary `ev.data` NUL-truncates (use
  `ev.dataBytes()`); `.close()` doesn't await the peer echo. Blocks binary
  pub/sub use.
- **`net.Socket`** `setEncoding`/`ref`/`unref`/`pause`/`resume`/`setTimeout` are
  no-ops. **`dgram`** udp4-only, `.on('message')` only. **`dns`** absent.
- **child_process**: listeners fire synchronously from dispatch (not microtask),
  one listener per event, arrow-literal only, no `removeListener`; `fork` is
  self-fork only; `env`/`timeout`/`stdio`-array override breadth.
- **process/os/console**: no `'unhandledRejection'`; `memoryUsage()`'s
  `external`/`arrayBuffers` stay 0 (no native off-heap accounting);
  `process.stdin`
  flowing-mode only (no `.pause()`/`.resume()`/`.read()`/`'readable'`) and
  string-chunk only (`setEncoding('utf8')` is a no-op, non-utf8 rejected —
  ADR-00793); `console.trace()` prints no
  stack and `Error.stack` is absent — both blocked on a runtime call-frame
  representation (a per-fiber shadow call stack), scoped in TDD-00188 (distinct
  from TDD-00073's DWARF-for-debuggers); `node:tty` module surface absent.
- **Partially-implemented module residues**: `perf_hooks` `PerformanceObserver`
  beyond the shipped entry types (TDD-00166); `async_hooks` context propagation
  into `.then`/`.catch` reactions and EventEmitter listeners, `snapshot()`,
  exception-safe `run` restore (TDD-00168); the remaining importable specifiers
  for Web-global-backed modules (TDD-00165); `klain:webview` multi-window
  (TDD-00142, deferred with the alt backends in §6).
- **Not started modules**: `vm` (~77 conformance files, gated on an embedded JS
  engine — TDD-00046; only a compile-time string-literal subset is in reach
  without it), `domain`, `string_decoder`, `util/types`, `assert/strict`,
  `dns/promises`, `readline/promises`, `timers/promises`, `stream/consumers`,
  `node:sqlite` (TDD-00151), `node:ffi` (TDD-00164, see item 6).

---

## 4. Core language / TypeScript (mostly leaf)

- Dynamic `eval` / `Function(string)` / `vm` / `repl` — opt-in embedded engine
  (quickjs-ng), TDD-00046. Nothing built.
- General lazy Iterator/AsyncIterator protocol (`Iterator.prototype.map/filter/…`,
  `Array.keys/values/entries`, `matchAll`, Map/Set iterators all materialize) —
  ADR-00057. Pervasive "materialized not lazy" caveats trace here.
- RegExp `u`/`v`/`y`/`d` flags accepted but not implemented; no `\u{…}`/`\p{…}`;
  `.exec`/`.match` missing `index`/`input`/`groups`; `.matchAll` eager.
- `Intl.*` (no ICU — bare `Intl` is a compile error; blocks `localeCompare`/
  `toLocaleString` fidelity); `Temporal` absent; `String.prototype.normalize()`;
  `Reflect.apply`/`construct`.
- Spread into fixed-arity fn / variadic builtins (`f(...arr)`, `Math.hypot(...a)`)
  — TDD-00106. `setImmediate` == `setTimeout(0)` (flat timer queue, no phases).
- Faithful async rejection values (`allSettled` `.reason`, `throw <non-Error>`) —
  TDD-00169. Closure capture for nested fn decls hoisting/generators — TDD-00129.
  Generalized destructuring (object rest + assignment-form) — TDD-00065.
  Decorators class-replacement + static-field — TDD-00161.
- Dynamic `import(...)` beyond the eager V1 (real lazy loading needs a module
  runtime the whole-program merge doesn't have) — TDD-00055. The `-compat`
  axis's third bucket (per-divergence flags) — TDD-00075. `any`/implicit-`any`
  residues — TDD-00162 (Partially Implemented) carries the buckets the D1 work
  didn't close.
- **WebCrypto `crypto.subtle`** — the largest half-built subsystem: ~7 algorithm
  ops (`importKey`/`generateKey`/`deriveBits`/`deriveKey`/`sign`/`verify`) return
  "not implemented yet"; heavy format/curve restrictions.

### Cross-cutting root causes (high leverage, like item 1)
- Array reference semantics: length-mutation propagates for plain-variable params
  but not object-field/array-element/HOF-callback arrays. TDD-00127 / ADR-00517.
- Nested-array element rejection in `.sort()`/`.indexOf()`/`.includes()`/
  `Object.groupBy()`. ADR-00152.
- `.buffer` absent on all TypedArrays; views don't length-track `resize`.
  ADR-00494 / ADR-00564. Binary interop.

---

## 5. Perf / GC / infra (enablers; mostly multi-stage plans with Stage 1 shipped)

- Stack-allocation escape analysis (`-optimize-memory`, Stage 1 shipped) —
  TDD-00134; maturity gate to make it default — TDD-00174. **The dominant native
  perf gap** (every object literal is heap-allocated today — ADR-00702).
- Deep reclamation under `-mm=auto` (Stage 1 shipped) — TDD-00175.
  Precise/moving GC (Immix) — TDD-00135. Array amortized-growth (`cap`) —
  ADR-00517.
- DWARF debug symbols (TDD-00073); enriched diagnostics / strict error-matching
  (TDD-00072); `@readonly`/`@pure` enforcement (TDD-00126/00128).
- Conformance infra: the WPT slice (TDD-00082's remaining bucket).
  statusgen's last phase — deriving the website reference badges/differences
  from the status source instead of hand-mirroring them (TDD-00145's
  Reconciliation section).

---

## 6. Deliberately deprioritized (Later tier)

Per the project's priority ranking, intrinsically-useless leaf APIs are end-game
conformance-unblockers: IndexedDB (TDD-00011), Notifications / `localStorage` /
Clipboard / Geolocation (TDD-00171) / Gamepad (TDD-00170) / Canvas-2D as
native-reinterpreted APIs, the File API family (TDD-00172), mobile targets
(Sailfish TDD-00146, Android TDD-00147), alternative webview backends
(TDD-00144, plus `klain:webview` multi-window from TDD-00142),
the TUI-framework roadmap (TDD-00150), `TextDecoder` non-UTF-8 (TDD-00034),
the `klmpm` package manager (TDD-00054), npm/`node_modules` interop
(TDD-00053, bottlenecked on TDD-00022's scope), an alternative Go-helper
`fetch` backend (TDD-00003), self-hosting (TDD-00124).

Feasibility/reference documents, not queued work: the WebAssembly target
(TDD-00048), the freestanding Pico (TDD-00036) and Raspberry Pi (TDD-00045)
targets, Immix (TDD-00135, listed in §5). TDD-00032/TDD-00033 are bootstrapping
placeholders superseded in practice by `klain:webview` (TDD-00142) and the FFI
plan (TDD-00164).
