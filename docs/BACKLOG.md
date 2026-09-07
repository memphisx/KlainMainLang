# Remaining-Work Backlog

A reconciled, whole-project audit of what is actually left, distinct from the
per-feature status pages (`docs/status/`, generated from `docs/status/data/*.json`)
and the design docs (`docs/tdd/`). This is a hand-maintained planning doc: when an
item ships, delete it here and update the status JSON / ADR / TDD in the same
commit. Snapshot date: **2026-09-07**.

Reconciled against the status JSON (authoritative for *current* shipped state),
the TDD index, and a code-level sweep — so items that older design docs still
list as "to do" but which have since shipped are **excluded** here (notably most
of TDD-00180 §2–§6, closed by ADR-00736–00769).

Legend: **[X-plat]** cross-platform · **[Win]** Windows-specific ·
**Enabler** (unblocks a cluster) · **Cluster** (broad) · **Leaf** (isolated).

---

## 1. Highest-leverage clusters (attack these first)

1. **Undefined sentinel for scalar types** — [X-plat] Enabler. ADR-00157.
   No `undefined` representation for concrete scalar types is the root of dozens
   of caveats at once: `.pop()/.shift()/.find()/.at()` zero-returns, optional
   field / destructuring zero-reads, `process.env.MISSING` → `null` (not
   `undefined`), out-of-range `argv[i]` → `""`, `stdout.columns` off-TTY,
   closed-stream reads. One representation fix retires a whole caveat family.

2. **`err.errno` numeric field (libuv-negative)** — [X-plat] Cluster. ADR-00768,
   TDD-00180 OQ2. The *only* remaining fs / child_process error-shape gap now
   that `code` / `syscall` / `path` ship. Decide whether Linux/macOS also expose
   the libuv-negative numbering. Small, and finishes the error-shape cluster.

3. **Real async I/O + Node streams handed out by `fs`/`http`** — [X-plat] Cluster.
   `fs.promises` is fake-async (blocking syscall inline, Promise pre-settled, no
   thread pool); `fetch` / `Response.text()`/`.json()`/`.arrayBuffer()` are
   synchronous (a WHATWG divergence); `fs`/`http` do not hand out real Node
   `Readable`/`Writable`. Core to the microservice / REST-server target.

4. **`http.createServer(...).listen()` is single-instance, once-per-program** —
   [X-plat] Cluster. TDD-00131 Stage 1. `http.close()` doesn't lift it; options
   object mostly rejected; keep-alive only on buffered `res.end` (chunked /
   streaming forces `Connection: close`); HTTP/2 has no request bodies
   (`req.end()` is a no-op). A real limit for the server-side priority.

5. **D1 dynamic object model** — [X-plat] Cluster/Enabler. TDD-00155.
   ~30% of all documented caveats: runtime property add/delete on
   statically-typed structs, the full `Proxy` trap set, well-known symbols as
   runtime dispatch (only `Symbol.iterator`/`asyncIterator` honored today).

6. **General C FFI (AOT `node:ffi`)** — [X-plat] Enabler. TDD-00164.
   Foundational pivot: unblocks `node:sqlite` (TDD-00151), the `opentui` TUI
   backend, GTK/Qt/SDL/imgui GUI, ncurses, native DB clients.

---

## 2. Windows port

The **incremental** port is effectively complete — the remaining leaves are
either untestable from a dev box or deliberate divergences. The only substantial
Windows work is structural, and by policy is built **from Mac/Linux** (Docker +
CI), never from the Windows box.

### Self-contained binaries / distribution (the axis the fidelity audit missed)
Whether a compiled `.exe` runs on a stock Windows box with no MSYS2 install — first-order
for a "one self-contained binary" compiler, and not covered by TDD-00180's behavior audit.
- **C++ runtime (klain:tui / klain:webview) — done** (ADR-00771): statically linked.
- **Networking feature libs (`curl`/`ssl`/`nghttp2`/`zlib`) — open**: any `fetch`/`http`/`tls`
  program (incl. the `loadtest` showcase app) still needs `libcurl-4.dll` + its chain beside
  the `.exe`. Static libcurl on mingw drags krb5/gssapi (the hard `--static` problem). Options:
  static-link the whole curl chain on Windows, or bundle the DLLs app-local / via the installer.
  This is real, valuable Windows work — the flagship networking app isn't single-binary without it.
- **winpthread via goroutines**: a pure `klain:sync` (goroutines) program that isn't TUI/webview
  would still pull `libwinpthread-1.dll` (the static group is on the tui/webview path); fold the
  static-winpthread group into the concurrency/worker link path too.

### Structural (the real remaining Windows debt)
- **Event-loop reactor / owned handle table** — TDD-00182 (Option A, 6 stages)
  → TDD-00183 (IOCP, Option B, committed follow-on). Dissolves the whole §1
  cluster: blocking `stdin`/pipe reads, ~10 ms idle poll-spin, 384-socket
  `select()` cap, `dup2` socket aliasing, synchronous `CreatePipe` child stdio.
  The one shared primitive (a loop wakeup channel) already exists (ADR-00757).
- **`cluster` round-robin** — 3 skipped tests. TDD-00177 OQ7 / TDD-00105.
  Workers share one listening socket; `http.close()` from a worker doesn't reach
  siblings. Reactor-adjacent.

### Self-contained binary (single-file `.exe`)
- **`klain:tui` — done** (ADR-00771): TUI terminal apps link the mingw C++ runtime
  and pcre2 statically (Yoga compiled with g++ so the gcc static `libstdc++.a`
  matches), so they import only stock Windows DLLs. Verified on `klaintop`/`files`/
  `todo`.
- **`klain:webview` — follow-up**: desktop apps still link `libstdc++` dynamically
  (their binding is a single clang-compiled `.cc` on the shared clang line, not a
  g++ prebuilt object like Yoga). A webview app also needs the per-machine Edge
  WebView2 runtime, so it is a different deployment shape; making its `.cc` a g++
  prebuilt object would close the C++-runtime gap. [X-plat concern is moot —
  POSIX C++ runtime is a system lib.]
- **Networking feature libs** (`curl`/`nghttp2`/`ssl`) remain dynamic DLLs on
  Windows by design (static libcurl on mingw drags krb5/gssapi). A TUI+TLS program
  is therefore not fully single-file; the common TUI+regex case is.

### Deferred edges (low value / untestable from a dev box)
- Broken-pipe write nuances: `WSAESHUTDOWN`→`EPIPE` (not observably reachable —
  the JS `Writable` layer intercepts write-after-`end()`); and the TLS-via-BIO
  error code on long-lived pub/sub connections (gRPC/HTTP2/`wss`), unverified vs
  Node. A persistent-connection probe would settle the latter. TDD-00180 §5.
- Named pipes `\\.\pipe\…` for `net.connect({path})`/`listen(path)`. TDD-00180 §5.
- Non-ASCII console **input** (`ReadConsoleW`→UTF-8; the output/code-page side
  ships). TDD-00180 §2.
- `readlink` UNC / junction PrintName edge; `chdir` hidden `=X:` drive vars.
  TDD-00180 §4.
- `process.stdout.columns`/`.rows` off a TTY returns 80×24 — a **deliberate,
  tested divergence** (no clean undefined-number; 80×24 is the common substitute).
  Folds into item 1 above if/when the undefined sentinel lands.

### CI-lane-pending verification (implemented, needs the runner)
- `lstat(symlink).size` on Windows (ADR-00769) — the dev box lacks symlink
  privilege (Developer Mode), so the shim compiles but the symlink assertion
  skips; the CI runner creates symlinks.
- `fs.watch` macOS kqueue backend (ADR-00758) — no macOS host yet.

### Out of scope by design
`--static` full-static on Windows, `-crypto=commoncrypto` (macOS-only), ASan/UBSan
(no mingw runtime). `-mm=gc` works (the fork path is skipped).

---

## 2b. Showcase apps — relocation + proper testing (X-plat) — Enabler for delivery quality

The landing-page gallery apps (`examples/tui/{klaintop,files,todo,menu}.ts`,
`examples/webview/*`, `examples/goroutines/loadtest/`) are *real applications*, not
`console.log` fixtures, and are under-tested:

- **`make examples`** runs each app as `app </dev/null >/dev/null` and checks only
  **exit 0** — so a TUI app is verified to *start and cleanly exit on a non-tty*,
  nothing about its actual behavior. It runs with `ucrt64/bin` on PATH, so it
  **cannot catch a missing-DLL / non-self-contained binary** (the ADR-00771 class
  of bug slipped through exactly here).
- **Webview apps are excluded entirely** (`! -path 'examples/webview/*'` in the
  `EXAMPLES` glob) — never compiled or run by CI, beyond the one `-package` GUI
  test.
- **No test builds the real gallery apps** (`klaintop.ts` etc.); the conpty/tty
  tests exercise *synthetic* TUI programs, and the new self-contained guards
  (ADR-00771) are synthetic too.

Proposed: move these apps to a dedicated top-level location (e.g. `apps/` or
`showcase/`) with their own test harness — compile-check every app on every
platform (catches build breaks + the self-containment regression via an
objdump/imports assertion on Windows), drive the TUI ones through the conpty
harness for real behavior, and a headless compile-check for webview. Update the
Makefile, the website gallery/`ExampleTree` paths, and the guide pages in the
same change. Structural + cross-platform + website-coupled → do it from
Mac/Linux with the full CI matrix, not from the Windows box.

## 3. Node / runtime surface (mostly leaf, incremental)

- **`fs`**: no `options`/`encoding`/`mode`/`flag` arg on write/append/read;
  `rmSync` no `maxRetries`/`retryDelay`; `readlinkSync` 4096-byte cap;
  `readdirSync` no `recursive`/`encoding:'buffer'`; `Dirent` missing
  `.parentPath`/device predicates; `copyFileSync` no `mode`; `birthtimeMs`=0 on
  Linux (glibc; needs `statx`); no `futimesSync`; `fdatasyncSync`==`fsyncSync`.
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
- **process/os/console**: no `'unhandledRejection'`; `memoryUsage()` only `rss`
  real; `process.stdin` flowing-mode/string-only; `console.trace()` prints no
  stack; `node:tty` module surface absent; `os.homedir()` throws without `$HOME`
  (no passwd fallback); `os.tmpdir()` ignores `$TMP`/`$TEMP`.
- **Not started modules**: `vm` (~77 conformance files, gated on an embedded JS
  engine — TDD-00046), `domain`, `string_decoder`, `util/types`, `assert/strict`,
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
  `Array.prototype.reduceRight()`; `Reflect.apply`/`construct`.
- Spread into fixed-arity fn / variadic builtins (`f(...arr)`, `Math.hypot(...a)`)
  — TDD-00106. `setImmediate` == `setTimeout(0)` (flat timer queue, no phases).
- Faithful async rejection values (`allSettled` `.reason`, `throw <non-Error>`) —
  TDD-00169. Closure capture for nested fn decls hoisting/generators — TDD-00129.
  Generalized destructuring (object rest + assignment-form) — TDD-00065.
  Decorators class-replacement + static-field — TDD-00161.
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

## 5. Perf / GC / infra (enablers, mostly staged)

- Stack-allocation escape analysis (`-optimize-memory`, Stage 1 shipped) —
  TDD-00134; maturity gate to make it default — TDD-00174. **The dominant native
  perf gap** (every object literal is heap-allocated today — ADR-00702).
- Deep reclamation under `-mm=auto` (Stage 1 shipped) — TDD-00175.
  Precise/moving GC (Immix) — TDD-00135. Array amortized-growth (`cap`) —
  ADR-00517.
- DWARF debug symbols (TDD-00073); enriched diagnostics / strict error-matching
  (TDD-00072); `@readonly`/`@pure` enforcement (TDD-00126/00128).

---

## 6. Deliberately deprioritized (Later tier)

Per the project's priority ranking, intrinsically-useless leaf APIs are end-game
conformance-unblockers: IndexedDB (TDD-00011), Notifications / `localStorage` /
Clipboard / Geolocation (TDD-00171) / Gamepad (TDD-00170) / Canvas-2D as
native-reinterpreted APIs, mobile targets (Sailfish TDD-00146, Android TDD-00147),
alternative webview backends (TDD-00144), the TUI-framework roadmap (TDD-00150),
`TextDecoder` non-UTF-8 (TDD-00034), the `klmpm` package manager (TDD-00054),
self-hosting (TDD-00124).
