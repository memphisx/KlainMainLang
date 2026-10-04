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

## Restructure first — [TDD-00230](tdd/TDD-00230.md)

Type, representation, builtin-dispatch and event-loop bugs below are fixed by the
phase that owns them, not at the symptom site.

- P3.2: the klain: host objects, array/object arguments to a C entry point,
  `@lowerType`.
- When TDD-00230 closes: run every `apps/` gallery app by hand (`make apps`
  only builds them) and check each still behaves as the website shows it.

## 0. Open bugs — before anything else, in this order

Wrong behaviour in something that claims to work. A bug found and not fixed the
same day lands here, at the top. One bug per line.

- Unannotated parameter (`function f(n)`) is `i64` (ADR-00042), not tsc's implicit `any`: truncates `f(0.5)` in strict, rejects `(f) => f()` in js; `staging/sm/Date/timeclip.js` strict.

### CI (Release run on `7b399d0`: every test job red)

The macOS tests below pass on the M4 with the staged tree (2026-09-25);
recheck on the next CI run.

1. **`TestE2EFsStatfsSyncSameAsNode`** differs from Node on all five platforms.
2. **`TestE2EHTTPClientRefusedConnectExitsCleanly`** (macos-arm64, linux-x64,
   linux-arm64): the program's own line is missing before the uncaught
   transport error.
3. **An interval starves during a fetch await** (macos-arm64, macos-x64): the
   `…AwaitFetch…`, `…TimerCallbackIsACoroutine` and `…FetchThenChainRunsTheLoop`
   tests see 5–7 ticks where they want 8 → TDD-00230 phase 4. They count ticks.
4. **The macOS jobs hit the 50 min timeout**: `TestE2ENetConnectPortOnly`
   hangs (arm64), `TestE2EHTTPClientVariableOptionsObject` hangs (x64).
5. **`TestE2EHTTPCreateServerAsyncHandlerInProcessClient`** fails (macos-x64).
7. **`-mm=gc` on Linux CI**: `TestE2EWorkerModuleTaskGCMode` (x64),
   `TestE2EGCModeFetchFromResolverThread` (arm64).

### Runtime

8. **HTTP examples hang ~1 run in 15 on the M4**, at HEAD too:
   `examples/http/async_handler_inproc_client`, `client_server`,
   `req_readable`, `chained_listen` → TDD-00230 phase 4.
9. **`TestE2EHTTPClientMethodAndHeaders` hangs under `make test-par`** (one
   shard hit 20 min, 2026-09-25); passes alone.
10. **Event loop** (→ TDD-00230 phase 4): `fd_set` overflow at fd ≥ 1024; h2
   and the code-generated TLS paths (wss, h2) block the loop; a handler `await` can
   `nanosleep` the server; the timer array never shrinks; one due timer per
   iteration; a signal before `select()` is lost; fetch-await in a callback spins.
11. **`-mm=gc` on macOS: thread-local roots are not registered** (dyld
    allocates `_Thread_local` lazily; the hook is a no-op).
    `TestE2ETaskStackReclaimedGC`, `…ThreadLocalRootsSurviveChurn`,
    `…CoroutineChainSurvivesChurn`, `…GeneratorFromModuleTask` segfault. Next:
    the C reproduction from ADR-01049, then the `tlv` descriptors.
12. **`-mm=gc` segfaults on macOS in `make mode-lanes`**: `async_generator`
    (after its output), `read_stream_backpressure`, `res_backpressure_drain`,
    `res_stream_backpressure`, `http_upload`. Linux unchecked.
13. **`-mm=gc` on Linux x86-64: TLS examples segfault** (`crypto/crypto`,
    `fetch/wttr_weather`, `http/https_client`, `http/multi_server_https`,
    `http2/secure_server`, `https/https_server`); Windows runs them clean.
14. **`-mm=gc` on Linux: host-name lookups under churn segfault**
    (`TestE2EGCModeFetchFromResolverThread` at 25 rounds; clean with
    `GC_DONT_GC=1`). Get a backtrace first (loop the CLI binary; gdb hides it).
15. **One-off:** `TestE2EHTTPCreateServerResStreamLargeBodyIntact` — "server
    never started listening", once in 274 runs. Chase it when it recurs.

### Values and representation (→ TDD-00230 P3.3 unless noted)

149. **An Error's `writable`/`configurable` are ignored** (`defineProperty`
    on a field), and `getOwnPropertyDescriptor(e, 'cause')` is undefined.
151. **A RegExp follows PCRE2 where ECMAScript differs**: an empty optional
    group iteration is `""` (`/^(.*)(.*)?$/` group 2), `v`-flag set
    operations never match, invalid `v`-mode syntax is accepted.
146. **Primitive wrapper objects** (`new Number(3)`) do not exist.
20. **A declared-type slot drops `undefined`**: `const s: string = a[5]` prints
    `null`, `s === undefined` is false (also `spawnSync(...).stdout` of a child
    that never started).
21. **An optional field can't tell explicit from omitted**: an omitted
    `s?: string | null` reads `null` and is enumerated; `{ a: undefined }`
    reads as omitted.
24. **An `any` Map/Set cast to other element types misreads entries**
    (`box as Map<any, any>` over a `Map<string, number>`); a channel can't
    be held in `any`.
25. **Strings are byte-indexed** (`'café'.length` is 5, ADR-00028) → TDD-scale.
26. **EventSource data strings are unchecked.**
28. **An array write past the end throws** (`a[3] = 4` on `[1]`); Node extends
    with holes → [TDD-00226](tdd/TDD-00226.md), not started.
29. **A computed-key object literal is a Map inside**: its methods can't be
    called (`q[k]()`, `q.name()`) and it can't be held in `any`.
140. **A method call on a value typed as a class it is not an instance of
    runs the typed class's method**: `(b as any as A).m()` with an
    unrelated `class B { m() }` calls `A.m`; Node calls `B.m` (by name).

### Front end and typing

147. **A member missing from an interface only `@types/node` declares
    (`Buffer`) is not TS2339**: its files do not parse whole for the
    member list (ADR-01342).
148. **Builtin prototypes are not values**: `Map.prototype.get === m.get`.

31. **`arguments.length` counts declared parameters, not passed ones**
    (`f(1, 2)` of `f(a, b?, c?)` reads 3); needs a hidden argument count.
142. **Per-evaluation class records are never freed**: the record and instance
    tables and static areas grow per evaluation ([ADR-01338](adr/ADR-01338.md)).
44. **Function objects** (rest of [TDD-00229](tdd/TDD-00229.md) Stage A): a
    function unboxed from `any` into a typed slot is a new thunk, so identity
    is lost (`promisify(p) === p` is false); no `String(f)` source text; no
    `[class X]` rendering.
48. **EventEmitter's typed map is checked by codegen, not the checker**:
    the lib does not declare @types/node's `Key`/`Args`/`Listener`.

### Node API

56. **`cluster.worker` in a worker exposes `.id` only**; its `.process`,
    `.send` and events are missing.

### `-compat=js`

59. **An unassigned typed `let` reads its zero value**; Node reads
    `undefined` → P2.5.
60. **A `(T | undefined)[]` element has no storage** (`[1, undefined]`,
    `flatMap` with a fall-off callback).
62. **Assigning to a method on an instance fails** (`this._read = fn`:
    "no field '_read'"); JS gives the instance an own property.
64. **An absent value in a declared `number` slot reads 0** (`const c: number
    = process.stdout.columns` off a TTY, `xs.pop()! + 1` on an empty array);
    Node keeps `undefined` (and `NaN` in arithmetic) → P3.3.
65. **An omitted `Error | null` argument reads `null`**; Node reads `undefined`
    (`cb()` into `(err?: Error | null) => …`, stream callbacks) → P3.3.
66. **Loop phases**: a thread-pool completion runs before a due 0 ms timer, and
    a promise-form fs op settles before an earlier callback-form one
    (`examples/fs/fs_async_pool` prints `non-blocking: false`,
    `examples/fs_async` reorders its first line) → TDD-00230 phase 4.
70. **A namespace-qualified generic alias's defaults are not applied** in
    the checker (`N.A` for `type A<T = string>` inside `namespace N`).
72. **Host classes have no layout row** except `AbortSignal`/`AbortController`:
    `(msgEvent as any).data` misreads (ADR-01181).
75. **Sloppy-mode `this` in a plain call is `undefined`, not `globalThis`**
    (`globalThis` is not a value).
78. **`new Response(stream)` takes only a `ReadableStream<Uint8Array>`**; a
    stream of `any` chunks (Uint8Array at run time) is rejected.
90. **A Symbol-keyed class member** (`[k]: T`, `[k]() {}`) is a parse
    error; only constant string/number computed names work.
91. **Added properties are invisible to `for…in` and method calls on the
    statically typed binding** (`for (k in p)`, `p.m()` after `(p as
    any).m = f`); console.log/JSON/Object.keys see them (ADR-01212).
92. **`delete` of a declared field of a static object leaves it in place.**
94. **An optional field set to `undefined` reads as omitted**: `{ a: 1, b:
    undefined }` prints `{ a: 1 }` and `Object.keys` lists `a` only.
97. **An omitted optional `T | null` field reads `null`** (`{ b: 1 }` as
    `{ k?: K | null; b: number }` prints `k: null`), and an omitted optional
    union field prints `a: undefined`; Node lists neither → P3.3.
98. **A value of another kind written through `any` or a structural type
    into a field** (`o.x = "s"` for a `number` field) shadows the field: its
    statically typed binding still reads the old value → P3.3.
99. **The checker accepts any value as `BodyInit`/`HeadersInit`**
    (`fetch(u, { body: 5 })`, `headers: new Map<string, number>()`; tsc
    TS2769): `Iterable`, `AsyncIterable` and `FormData` are undeclared and a
    number satisfies `Record<string, string>` → P3.1.
102. **A dictionary boxed into `any` is opaque**: `console.log` prints
    `[Object]`, `Object.keys` is `[]`, `JSON.stringify` throws; the box
    doesn't record how the map stores its values (ADR-01225) → P3.3.
103. **An object or bag converted to a dictionary type is a copy**: writes
    through the dictionary don't reach the source (ADR-01225) → P3.3.
108. **Reading `.buffer` on a `TypedArray | DataView` union** is a codegen
    error ("un-narrowed union").
111. **`Object.assign` onto an array target** (`Object.assign([], xs)`) is a
    compile error ("target must be an object").
112. **Symbol keys through `any`**: `Object.assign` and
    `Object.defineProperties` skip them.
115. **`events`' typed event map is unchecked**: `emit("undeclared")` on an
    `EventEmitter<{…}>` compiles; tsc rejects it (@types/node's conditional
    listener types).
117. **After the restructure: delete `klain:tty`** (bespoke `readByte`/
    `readKey`; `klain:tui` reads keys from `process.stdin` `'data'`). Not a
    restructure step.
124. **`Date` is UTC-only**: local-time getters, `new Date(y, m, …)`,
    `getTimezoneOffset`, `toString` ignore the time zone (Node: local).
123. **`url.parse` lacks `slashes`** and the `parseQueryString` object
    (ADR-00669).
122. **`(a, b)` comma expression parses as arrow params** (ADR-00179);
    parse as expression, reinterpret on `=>`.
121. **`fs.watch` on macOS reports the watched path**, not the changed
    entry's name (ADR-00758); FSEvents gives it.
118. **`klain:sync` async preemption (TDD-00143 Stage 4)**: no `SIGURG`
    preemption; C helpers/ffi calls run unpreemptible; back-edge polls
    remain. After phase 4, with Stage 5 (precise stack maps).
116. **Constructor arguments are not type-checked**: `new A('x')` for
    `constructor(n: number)` compiles in strict (TS2345/TS2559/TS2554).
126. **`.constructor` is not a property** (`x.constructor.name`,
    `x.constructor === C`) on any object: a compile error.
127. **A dict boxed into `any` is a copy**: writes through the `any` don't
    reach the original object (identity lost).
128. **`const x: T[] = null as any`** is a compile error.
129. **TypedArray `.buffer`/`.byteOffset` are missing**.
131. **An `any` argument is converted to a typed parameter's type**:
    `path.join(5 as any)` passes `"5"` (`...args: string[]`), so the
    callee's `typeof` validation sees a string where Node throws
    `ERR_INVALID_ARG_TYPE`.
132. **`(s as any)[Symbol.iterator]` is `undefined` for a string** (Map,
    Set and arrays answer a function).
134. **Ubuntu 26.04 lane**: CI and the Linux test boxes use 24.04
    (clang 18); run the suites on 26.04 (newer clang, glibc, OpenSSL)
    and move CI's Linux runners to it.
137. **`new C(x)` of a generic class needs explicit type arguments** in
    codegen ("inference isn't supported for class construction"); the
    checker already infers them.
136. **Conformance runner**: the TS suite runs its cases serially on one
    core (`runTSLane`); the memory cap assumes ~1.5 GiB per worker, but a
    library-heavy WPT/Node file's clang now peaks at 3–4 GiB (~60 GB with
    14 workers on Node304). The TS js lane's process peaks at 46 GB (one
    case or accumulated state; not yet narrowed).
135. **An `any` object passed for an interface-typed parameter is copied
    into its layout**: a member of another kind (a string for `string[]`)
    becomes garbage where JS passes the object itself.
138. **An instance method read as a value fails to compile**: `const f =
    k.greet` and `k.greet.name` give "no field 'greet'" (unbound `this`,
    one function identity per method, `.bind`/`.call` all open). Blocks
    TDD-00230 P2.7; fix right after Phase 5's closure header and method
    table.
139. **A namespace import is not a value**: `typeof path` and `const p =
    path` (after `import * as path`) fail with "cannot find name";
    `typeof crypto` is `undefined` (Node: a module namespace object).
    Checker half with TDD-00230 P2's namespace types; the object after
    Phase 5 and TDD-00238 Stage 4.
## 1. Highest leverage — do these first

- After TDD-00230 and the open bugs/caveats, ahead of most open TDDs:
  [TDD-00239](tdd/TDD-00239.md) `klain:test` (terminal, HTTP, webview apps).


- Conformance headless DOM shim (TDD-00204 Track 5): jsdom tier — element tree,
  events, parser — unlocks the ~20–25K WPT testharness `.html` documents; own TDD
  before code. Plus TS script-mode global merging (top multi-file false-reject).

- **`http.createServer` remaining edges** — custom `IncomingMessage`/
  `ServerResponse` subclass rejected (wants Streams synthetic-root `extends`,
  ADR-00393). HTTP/2 request/response body streaming — body arrives as one chunk
  at `END_STREAM`, no `res.write` flow control (TDD-00218).
- **D1 dynamic object model** (TDD-00155) — Stage 6 residue: aliased-binding widening (`const a: any = o`),
  `<`/`>` on `any`, `Object.values`/`entries` on a *static* array; array
  methods on an `any` holding an array of objects (element kind the box
  can't describe). Broader: property add/delete on typed structs, full `Proxy` traps,
  well-known symbols as dispatch. Perf follow-up: TDD-00220.
- **C FFI residue** (TDD-00164) — `using`/`[Symbol.dispose]` disposal,
  `close()`-invalidates-callbacks. Beyond-Node: TDD-00190.

## 2. Windows port

MacOS/Ubuntu are the main dev machines; the aim is to minimize return trips to the Windows
laptop. Cross-platform work is driven from Mac/Linux (Docker + CI), but anything
that genuinely needs a native Windows box to complete or verify should be
finished while on it — don't defer such work into another comeback.

Cross-platform features that block a Windows row (build on every host; the
Windows half is named):

- **`server.listen(path)` / `listen({ path, host })`** — no host has it. POSIX
  AF_UNIX; Windows a named-pipe server (`CreateNamedPipeW` overlapped +
  `ConnectNamedPipe` as the accept op, a fresh instance per client; the client
  half exists, ADR-01034).
- **child_process residue** (ADR-01080 did the rest) — `fork()` options
  (`cwd`/`env`/`silent`/`stdio`/`timeout`/`execArgv`: its runtime is a separate
  fork/exec path on both hosts, `runtime_ipc.go` + `runtime_spawn_win.go`);
  numeric-fd `stdio` entries; the exec callback Error's numeric `code`/`cmd`.
- **Local-time `Date`** (UTC-only on every host today; in scope) — Windows:
  `GetTimeZoneInformationForYear`/`TZ`.

Windows-only:

- **`TestE2EWinRealpathWalksLinks`** (CI windows-x64): `realpath` returns the
  link's path, not its target.
- **node:ffi `unordered_map` order** (MSVC STL port) unverified against a
  Windows Node ≥ 26.10.0.
- **`-dynamic-import=lazy`** (ADR-01330) refuses Windows: a DLL binding to the
  executable's symbols needs an export library for the `.exe`.
- **The C runtimes from the IR → C port (TDD-00240)**: ~40 `_WIN32` branches
  (task ucontext/fibers, fs errno, child_process, net, ipc, timers) are
  compile-checked only — build and run the corpus natively.
- **`--static` = "self-contained"** (depends on nothing the OS doesn't ship), not
  "Linux full-static": Windows already links the optional libraries statically —
  audit per library and prove it with an import-table check per feature
  (generalise `tests/tui_selfcontained_windows_test.go`); macOS links the Homebrew
  `.a` archives and leaves system libraries dynamic (`otool -L` check). Decide
  webview (WebView2 loader is a DLL) and whether `-package` defaults to it. TDD
  first; then fix the "Linux only" help text and the stale out-of-scope notes.
- **Non-ASCII `%TEMP%`**: mingw `ld` opens intermediates through the narrow API
  (ADR-01035 covers only the output path) — stage the build dir under an ASCII root.
- **Writes to a redirected stdout/stderr pipe block the loop** (non-overlapped
  inherited pipe). Check what Node does on Windows first; then a writer thread +
  flush at exit. Same spot: the 10 ms re-probe for unarmable pipe handles.
- **512-descriptor ceiling** — the IR speaks a 1024-bit `fd_set` (same limit on
  Linux). A loop-interface change (poll- or registration-shaped); TDD.
- Child IPC channel → named pipe (today a peer-verified loopback TCP pair).
- Spawn markers carry raw handle values in the environment → `STARTUPINFO.lpReserved2`.
- **`-crypto=cng`** (TDD-00177 Stage 5) — BCrypt/NCrypt backend; removes OpenSSL
  from non-TLS programs.
- TLS write-error probe: client `destroy()`s a `wss`/h2 connection while the
  server writes; compare `err.code` with Node on Windows.
- **Deferred edges (low value / untestable from a dev box):** broken-pipe write
  codes + the TLS-BIO error on long-lived pub/sub (TDD-00180 §5).
- **Out of scope:** `-crypto=commoncrypto`, ASan/UBSan.
- `tools/ramdisk/run.sh` is unverified on macOS/Linux.

## 3. Node / runtime surface (mostly small, incremental)

- **Math last-digit precision** — `log`/`trig`/`hyperbolic`/`expm1`/`log1p`
  come from the platform libm, which differs from V8's fdlibm in the last
  binary digit (`Math.tan(1)`). Port fdlibm as `cbrt` was; each function's
  `@lower` in `lib/es.d.ts` then names the port.
- **Web streams** — no BYOB/byte controllers; `ReadableStream.from()` arrays
  only.
- **WebSocket** — client offers no `permessage-deflate`; klain:ws's
  server `.close()` doesn't await the peer's close frame.
- **dgram / dns** — `dgram` udp4-only +
  `.on('message')` only; `dns` absent.
- **child_process** — sync listener dispatch, one listener per event, arrow-only,
  no `removeListener`; `fork` self-fork only; narrow `env`/`timeout`/`stdio`.
- **process / os / console** — `memoryUsage().external`/`arrayBuffers` stay 0;
  `process.stdin` is flowing-mode + string-chunk only (no `.pause`/`.resume`/`.read`/`'readable'`);
  `console.trace()` / `Error.stack` need a runtime call-stack (TDD-00188);
  `node:tty` module surface absent.
- **Partially-done modules** — `async_hooks` `.then`/listener propagation, `snapshot()`,
  exception-safe `run` (TDD-00168); the remaining Web-global module specifiers
  (TDD-00165); `node:sqlite` dynamic-row `SELECT *`, `db.aggregate()`, error
  `.code`, lazy `iterate()` (TDD-00151); `klain:webview` multi-window
  (TDD-00142, see §6).
- URL/URLSearchParams (TDD-00203): (b) non-special-scheme URLs
  (`new URL('foo:bar')`) — libcurl rejects them, needs a hand-written WHATWG
  parser; (c) IDN of a non-ASCII domain on Mac needs libcurl+libidn2 (platform).
- **Other WPT-surfaced Web-API gaps** (`-suite wpt`, ADR-00871): `instanceof`
  against the ambient `AbortSignal` class is unsupported.
- **Node interpreter re-exec** — `spawnSync(process.execPath, ['-p'/'-e', …])`
  and Node-only CLI flags can't pass (a compiled binary can't eval source);
  needs per-flag support and argv-dispatched `-p`/`-e` entry points (TDD-scale).
- **Not started** — `vm` (TDD-00046, gated on an embedded JS engine), `domain`,
  `string_decoder`, `util/types`, `assert/strict`, `dns/promises`,
  `readline/promises`, `timers/promises`, `stream/consumers`.

## 4. Core language / TypeScript (mostly small)

- `eval` / `Function(string)` / `vm` / `repl` — embedded engine, TDD-00046.
  Nothing built.
- RegExp `u`/`v`/`y`/`d` accepted-not-implemented; no `\u{…}`/`\p{…}`.
- `Intl.*` (no ICU); `Temporal`; `String.normalize()`; `Reflect.apply`/`construct`.
- Spread into fixed-arity / variadic builtins (TDD-00106); `setImmediate` ==
  `setTimeout(0)`.
- Generalized destructuring (TDD-00065); decorator class-replacement +
  static-field (TDD-00161).
- Union-element arrays (TDD-00200): a *constrained union* element (`(A | B)[]`)
  is rejected (element-level union checking unwired, TDD-00043); a statically-typed
  object/array *value* boxed into an `any[]` element is type-erased — deep
  `JSON.stringify` of it throws (object *literals* are fine).
- Honor `--unhandled-rejections=none`: an unhandled rejection still stringifies
  the rejection value (Node never touches it) — don't eagerly coerce it.
- A thrown plain object's own fields aren't readable after catch (`throw {x:1}`;
  `e.x`) — needs D1 runtime object shape (TDD-00155 Stage 6). Primitives + Errors
  are faithful.
- Module namespace objects (TDD first): `import * as ns` as a value, every
  export in an `import()` result (functions, classes, objects), `import('node:…')`.
- The `-compat` per-divergence flags (TDD-00075); `any` residues (TDD-00162).
- `--no-any` strict-lane flag (TDD-00209): Stages 1–2 ship (annotation + inferred
  var-decl `any` rejected); left is value-level `any` in other positions
  (inferred-`any` return, nested any sub-expression).
- `libbf` (MIT) as a third selectable `-bigint` backend alongside
  libtommath/gmp.
- **Cross-cutting roots (high leverage):** nested-array element rejection in
  `.sort`/`.indexOf`/`.includes`/`Object.groupBy` (ADR-00152); `.buffer` absent
  on TypedArrays, views don't track `resize` (ADR-00494/00564).

## 5. Perf / GC / infra

- **Hand-written runtime IR** → [TDD-00240](tdd/TDD-00240.md): 319 IR
  text definitions left (`TestIRTextOnlyShrinks`): HTTP and the event
  loop with its per-subsystem no-op stubs (after the klain:http redesign);
  the rest is generated per program.
- **Cross-language inlining**: the C runtime links as separate objects, so
  hot leaf helpers (`__kml_nb_*`, `__kml_str_len`/`_alloc`/`_free`) stay IR;
  ThinLTO did not inline `__kml_str_cmp` into its caller.
- **Layout jitter in the test harness**: run each program with a
  seeded-length padding variable, printing the seed, so a read of
  uninitialized memory fails reproducibly instead of at one path length.
- **Sanitizer lane in CI**: a Linux ASan + UBSan (or MSan) run over a
  subset of the E2E tests.
- **Two deciders for a top-level binding's type** (`reliableGlobalType`,
  `emitVarDecl`); merge into one `declaredTypeOf(v)`.
- **A user function named `main` collides with the emitted entry point**
  without the resolver (the test harness path).
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
- `TextDecoder`: the CJK encodings (TDD-00034 Stage 4).
- The `klmpm` package manager (TDD-00054); npm/`node_modules` interop (TDD-00053).
- Self-hosting (TDD-00124) with its `klain:` module set (TDD-00189).
- `klain:ffi` beyond-Node FFI (TDD-00190).

Reference docs, not queued work: the WASM target (TDD-00048), the Pico (TDD-00036)
and Raspberry Pi (TDD-00045) targets, Immix (TDD-00135).

<!-- STATUS-CAVEATS:BEGIN — generated by `make status` from docs/status/data/*.json; do not edit below this line -->

## Every open issue — the full caveat index

Generated by `make status` from `docs/status/data/*.json`. **Every line here is an issue to tackle.** A missing feature and a behavioural divergence both break code ported from Node/TS/JS, so both count. **Strict Coverage** of an area = the ✅ features carrying zero caveats; the number rises only when a caveat is *fixed and deleted*, never by rewording. Fix a caveat in the status data and it disappears from here; do not edit below the marker by hand.

**Total: 462 caveats · overall Strict Coverage 386/644 (~60%).** Areas below are ordered worst Strict Coverage first.

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

### Networking — Strict 1/7 (~14%) · 15 caveats — [Networking](status/NETWORKING.md)
- `fetch(url)` / `fetch(url, init)` / `fetch(request)` — A network failure rejects with a `TypeError` whose message is libcurl's (`Couldn't connect to server`); Node's is `fetch failed`, with the reason as its `cause`
- `Response` (`.status`, `.ok`, `.headers`, `.body`, `.text()`, `.json()`, `.arrayBuffer()`) — `.text()`/`.json()` are null-terminated strings — a binary body truncates at the first embedded null byte; use `.arrayBuffer()` for a binary body
- `new Response(body, init)` / `Response.json()` / `Response.redirect()` / `Response.error()` — A stream body must be a `ReadableStream<Uint8Array>`: a stream of `any` chunks is rejected at compile time
- `Request` / `Headers` objects — A `Headers` method called with too few arguments (through `any`) does not throw webidl's "N argument(s) required" TypeError: `arguments.length` counts every declared parameter
- `WebSocket` — The client offers no `permessage-deflate` extension (Node's does), so `extensions` is always `''`
- `WebSocket` — klain:ws's server `.close()` ends the connection without waiting for the peer's close frame
- `XMLHttpRequest` — No default-async, callback-interleaved mode — `.send()`/`.send(body)` runs to completion then fires every registered callback once, synchronously (no re-entrant-into-already-returned-caller callback machinery to support async)
- `XMLHttpRequest` — `.open(..., async?)`'s `async === true` also blocks — no main-thread event loop to defer `.onload` onto (a documented narrowing, not fake async — [ADR-00615](adr/ADR-00615.md))
- `XMLHttpRequest` — The optional `user`/`password` arguments to `.open()` are accepted (and evaluated) but not yet wired to HTTP auth
- `XMLHttpRequest` — `.setRequestHeader(name, value)` accumulates headers, but names are **not** lowercased here (unlike `Headers`' own methods) — a separate V1 narrowing
- `XMLHttpRequest` — `.readyState` is 0/1/4 only (UNSENT/OPENED/DONE); the real spec's HEADERS_RECEIVED/LOADING have no meaning for a transfer that always runs to completion in one call
- `XMLHttpRequest` — `.responseText`/`.response` are identical values — only a text `responseType` is supported; no `responseType` other than the default
- `XMLHttpRequest` — `.onreadystatechange`/`.onload`/`.onerror` callbacks are zero-argument (deliberately — a payload-carrying callback isn't representable for this particular type, see [ADR-00131](adr/ADR-00131.md))
- `XMLHttpRequest` — `.abort()` is a best-effort `readyState` reset — there's nothing to actually interrupt once a synchronous `.send()` has returned
- `XMLHttpRequest` — `.getAllResponseHeaders()` returns headers in arrival order, not the spec’s lowercase-sorted order ([ADR-00490](adr/ADR-00490.md))

### Cryptography (Web Crypto API) — Strict 3/18 (~17%) · 15 caveats — [Cryptography (Web Crypto API)](status/WEB-CRYPTO.md)
- `crypto.getRandomValues(view)` — A whole `ArrayBuffer` is accepted as a deliberate extension (real JS throws `TypeMismatchError` — only integer TypedArrays are spec-legal) ([ADR-00554](adr/ADR-00554.md))
- `crypto.subtle.encrypt` / `.decrypt` — On `-crypto=commoncrypto` the asymmetric algorithms (RSA, EC, Ed25519, X25519) fail: they run over `node:crypto`'s KeyObject, which that backend does not provide
- `crypto.subtle.sign` / `.verify` — On `-crypto=commoncrypto` the asymmetric algorithms (RSA, EC, Ed25519, X25519) fail: they run over `node:crypto`'s KeyObject, which that backend does not provide
- `crypto.subtle.generateKey` — On `-crypto=commoncrypto` the asymmetric algorithms (RSA, EC, Ed25519, X25519) fail: they run over `node:crypto`'s KeyObject, which that backend does not provide
- `crypto.subtle.importKey` / `.exportKey` — On `-crypto=commoncrypto` the asymmetric algorithms (RSA, EC, Ed25519, X25519) fail: they run over `node:crypto`'s KeyObject, which that backend does not provide
- Node `crypto` module: `createHash`/`createHmac` (`Hash`/`Hmac`: `update`/`digest`/`copy`), `hash`, `getHashes` — On `-crypto=commoncrypto`: `md5`/`sha1`/`sha224`/`sha256`/`sha384`/`sha512` only — SHAKE, SHA-3, SHA-512/t, RIPEMD-160 and BLAKE2 throw `Digest method not supported`
- Node `crypto` module: `createCipheriv`/`createDecipheriv` (`Cipheriv`/`Decipheriv`), `getCiphers` — On `-crypto=commoncrypto`: AES-128/192/256 in CBC, ECB, CTR and GCM only — other ciphers throw `ERR_CRYPTO_UNKNOWN_CIPHER`
- Node `crypto` module: `pbkdf2`/`scrypt`/`hkdf` (+ `Sync`) — On `-crypto=commoncrypto`: the digests are the six `createHash` has there
- Node `crypto` module: `generateKeyPair`/`generateKeyPairSync`, `createSign`/`createVerify`, `sign`/`verify` — `dh` key pairs are not generated (`rsa`, `rsa-pss`, `dsa`, `ec`, `ed25519`, `ed448`, `x25519`, `x448` are)
- Node `crypto` module: `generateKeyPair`/`generateKeyPairSync`, `createSign`/`createVerify`, `sign`/`verify` — On `-crypto=commoncrypto`: RSA keys of 1024 bits or more with exponent 65537, EC keys on P-256/P-384/P-521, `ed25519` and `x25519` only; encodings other than SPKI/PKCS#8 PEM and the `padding`/`saltLength`/`dsaEncoding` sign options fail
- Node `crypto` module: `KeyObject`, `createPrivateKey`/`createPublicKey`/`createSecretKey` — A `secp256k1` JWK cannot be imported (P-256/P-384/P-521 can; export covers all four)
- Node `crypto` module: `KeyObject`, `createPrivateKey`/`createPublicKey`/`createSecretKey` — On `-crypto=commoncrypto`, creating or exporting an asymmetric key fails
- Node `crypto` module: `createECDH`/`ECDH`, `diffieHellman`, `publicEncrypt`/`privateDecrypt`/`privateEncrypt`/`publicDecrypt`, `getCurves` — On `-crypto=commoncrypto` each of these fails
- `crypto.subtle.deriveKey` / `.deriveBits` — On `-crypto=commoncrypto` the asymmetric algorithms (RSA, EC, Ed25519, X25519) fail: they run over `node:crypto`'s KeyObject, which that backend does not provide
- `crypto.subtle.wrapKey` / `.unwrapKey` — On `-crypto=commoncrypto` the asymmetric algorithms (RSA, EC, Ed25519, X25519) fail: they run over `node:crypto`'s KeyObject, which that backend does not provide

### Other Node.js Core Modules — Strict 3/17 (~18%) · 36 caveats — [Other Node.js Core Modules](status/NODE-CORE-MODULES.md)
- `assert` — No `assert.CallTracker` (deprecated in Node, DEP0173)
- `assert` — Deep equality does not compare symbol-keyed properties, and a failure diff does not invoke getters (Node's `getters: true`)
- `assert` — An uncaught `AssertionError` prints `Uncaught: <message>`; Node prints `AssertionError [ERR_ASSERTION]: <message>`, its stack and its properties
- `test` / `node:test` — A failing test's details omit Node's `test at <file>:<line>:<col>` line and the stack: the error prints as `Name [code]: message`
- `test` / `node:test` — No `mock.*`, `run()`, snapshots, concurrency, timeouts, `t.signal`/`t.assert`, or `only` filtering
- `test` / `node:test` — The spec reporter prints without colors on a terminal (Node colors it); no other reporter
- `util` — `promisify(f) === f` is false for an already-promisified `f`: a function unboxed from `any` is a new thunk
- `util` — `.inspect` renders through the compiler's inspector: `depth`, `compact` (a number or `false`; `true` is treated as 3), `sorted` (not a comparator), `breakLength`, `maxArrayLength` and `[inspect.custom]` methods apply; `colors`, `showHidden`, `getters`, `maxStringLength` and `numericSeparator` do not, so `%o` shows no hidden properties; a custom method's `depth` argument is `Infinity`, not `null`, when the depth is unlimited
- `util` — No `inherits`, `MIMEType`, `getCallSites`, `transferableAbortSignal`, `parseEnv`; `debuglog`'s logger is typed `any`
- `net` — BlockList, SocketAddress and happy-eyeballs address selection (`autoSelectFamily`) are not ported
- `net` — On Windows the TCP handles are compile-checked, not yet run
- `dgram` — Sends are synchronous (a full send buffer is waited out), so `getSendQueueSize()`/`getSendQueueCount()` are always 0
- `dgram` — The `lookup`, `receiveBlockList`/`sendBlockList` options, `bind({ fd })` and cluster-shared sockets are not ported
- `dgram` — Windows: the TTL and multicast options and membership apply to `udp4` sockets only, and that path is compile-checked, not run
- `tls` — OpenSSL-only (libssl) — a `-crypto=commoncrypto` build using `tls` is a clean compile error (Mac's CommonCrypto has no TLS API)
- `tls` — Session resumption, OCSP, renegotiation, PSK and `getPeerCertificate()`'s certificate object are not ported
- `tls` — On Windows the TCP handles are compile-checked, not yet run
- `dns` — A c-ares error has no `errno` key (Node's has `errno: undefined`): the error layout's `errno` slot is numeric
- `dns` — The Windows server discovery and client are compile-checked only
- `zlib` — Brotli and Zstd load the system libbrotli/libzstd at run time (Node bundles them): where they are missing, making a Brotli or Zstd stream is `ERR_ZLIB_INITIALIZATION_FAILED`
- `zlib` — Compressed bytes are the system libz's, which can differ from Node's bundled zlib at the same level (each inflates the other's)
- `zlib` — A `*Sync` call with `info: true` is typed `{ buffer, engine }` by an extra overload (@types types it `Buffer`)
- `http` client — `http.request(new URL(…))` is not accepted — a `string | URL | options` parameter is a union code generation cannot tell apart ([BACKLOG](../BACKLOG.md))
- `https` — Same client limit as `http` for a `URL` argument ([BACKLOG](../BACKLOG.md))
- `cluster` — `setupPrimary({ exec })` naming another file fails at `fork()` with `ERR_CHILD_PROCESS_FORK_MODULE`: a worker is this compiled program, so there is no other script for it to run (a genuine impossibility of native compilation, [ADR-01015](adr/ADR-01015.md))
- `cluster` — `inspectPort` and the inspector flags are ignored: a compiled program has no inspector
- `cluster` — A `dgram` socket bound in a worker binds on its own, not through the primary
- `cluster` — Windows: a worker's `listen` has no handle to receive — descriptor passing over the fork channel is POSIX-only
- `http2` **module** — `respondWithFD`/`respondWithFile` read the whole file into memory before sending it, where Node streams it
- `http2` **module** — `session.altsvc()`/`origin()` validate their arguments but send no frame
- `http2` **module** — `setNextStreamID`/`setLocalWindowSize` validate their argument only
- `http2` **module** — `session.socket` is the raw socket, not Node's Proxy that rejects the socket methods an h2 session owns
- `http2` **module** — The `Http2ServerRequest`/`Http2ServerResponse` server options (subclasses) are ignored
- `http2` **module** — `stream.write` has no backpressure: it always returns `true` and buffers
- `http2` **module** — Most session tuning options (`maxSessionMemory`, `maxHeaderListPairs`, `paddingStrategy`, `peerMaxConcurrentStreams`, …) are accepted and ignored
- `async_hooks` — `createHook`, `executionAsyncId`, `triggerAsyncId` and `executionAsyncResource` are not exported; an `AsyncResource`'s `asyncId()` numbers resources in creation order, with no init/before/after/destroy lifecycle behind it

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

### Streams API — Strict 3/9 (~33%) · 23 caveats — [Streams API](status/STREAMS.md)
- `ReadableStream` — No BYOB readers / byte controllers (`type: 'bytes'` rejected at compile time)
- `ReadableStream` — `ReadableStream.from()` accepts arrays only, not async iterables
- `ReadableStream` — Source callbacks must be arrow/function-expression/method-shorthand literals; a cancel callback can't take a reason parameter
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
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — A callback that declares its error parameter as an object type (`(err?: Error | null)`) sees `null` where Node passes `undefined` (`finished`, `pipeline`): both are its null pointer. One declared `any` sees Node's value
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — The HTTP server's `req`/`res` are separate runtime handles, not instances of these classes: `instanceof Readable`, a subclass's methods and piping into one of these classes do not apply to them
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — `Readable.toWeb` enqueues each chunk as it arrives, without the web stream's pull backpressure; `Duplex.toWeb`/`Duplex.fromWeb` are missing
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — Not ported: `Readable.wrap`, `stream.isReadable`/`isErrored`/`setDefaultHighWaterMark`
- `stream.Readable` / `.Writable` / `.Duplex` / `.Transform` / `.PassThrough` — A method replaced on an instance (`this._read = fn`) is not supported; pass it in the options

### Type System — Strict 13/32 (~41%) · 40 caveats — [Type System](status/TYPE-SYSTEM.md)
- `number` → `double` — Mixing an explicit integer type with a bare literal promotes to double (`int32 / 2` is `3.5`); use two integer-typed operands for integer division
- `null` / `undefined` — A value typed as **both** `T | null | undefined` shares the single `ptr null` sentinel, so it can't tell which nullish kind it holds — it compares per the statically-chosen kind, which can be wrong for the other
- `any` — `Object.assign` through `any` and `Object.defineProperties` copy and define string keys only, not symbol keys
- `any` — A non-array held in `any` (a string, a number, an object) passed where an array is declared throws a TypeError; Node passes the value as it is ([ADR-01361](adr/ADR-01361.md))
- `unknown` — Same as `any` (see above)
- `symbol` — Of the well-known symbols the runtime honors `Symbol.iterator`, `Symbol.asyncIterator` and `Symbol.toPrimitive` (own properties, class and object-literal members); `toStringTag`, `hasInstance`, `species` and the rest are values only
- `bigint` — No `.toLocaleString()` (an `Intl`-shaped gap)
- Object types (interfaces / inline `{}`) — A **generic or qualified interface base** (`extends Boxed<number>`, `extends ns.Base`) parses but does not merge — its members stay missing from the derived interface
- Object types (interfaces / inline `{}`) — A bare call signature is supported only alone (desugars to the function type); mixing it with fields/an index signature is a clean rejection, for both inline `{}` types and interfaces ([ADR-00448](adr/ADR-00448.md)/[ADR-00455](adr/ADR-00455.md))
- Object types (interfaces / inline `{}`) — A same-name `interface` + `class` pair: the class wins as the binding, the interface's extra members ignored ([ADR-00466](adr/ADR-00466.md))
- Union types beyond `T \| null` — Scalar members (`number`/`string`/`boolean` + `null`/`undefined`), object/interface/class members (any number of classes and plain object types, at most one headerless host object such as `URL`, or a **discriminated union** ([TDD-00116](tdd/TDD-00116.md)); [ADR-01219](adr/ADR-01219.md)), and a `ReadableStream` member ([TDD-00119](tdd/TDD-00119.md)); two array members of one kind, a `Headers`-like host handle beside another of its kind, number-literal tags, and a non-first-position tag aren't supported
- Union types beyond `T \| null` — Flow narrowing is for a union **local** via `typeof`/truthiness/`==null`/`instanceof`/`in`/a discriminant/a type guard (if/else branches + early return); a field/element, `switch (typeof x)`, and `as` casts aren't narrowed; a conditional expression narrows its branches too; before narrowing, an object union reads only a member all its object members declare ([TDD-00114](tdd/TDD-00114.md))
- Intersection types — Object-type members only — a non-object member (scalar/function/array/tuple/union, all `never` in TS), or a generic-with-type-arguments member, is a clean compile error
- Intersection types — A field declared with conflicting **non-object** types across members is rejected under `-compat=strict` (default); the `-compat=js` TS-faithful `never`-field is not yet implemented, so a conflict is currently rejected in both modes (same-named **object** fields are recursively intersected instead — `{ x: A } & { x: B }` ⇒ `x: A & B`)
- Tuple types — No rest/optional elements
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
- Index signatures (`{ [k: string]: T }`) — The class-body form (`class C { [n: number]: T }`) parses and is **dropped** — indexing a class instance keeps its use-site rejection ([ADR-00476](adr/ADR-00476.md))
- Type assertions (`x as T`, `as const`, `satisfies T`, `<T>x`) — **Erased, not enforcing** — the assertion is dropped and the value keeps its *own* inferred type, so `as T` does not re-type the expression; a cast the code *relies on* for typing won't take effect (matches TS runtime erasure, not static narrowing/widening — [ADR-00371](adr/ADR-00371.md)); the general `any as T` question is [TDD-00176](tdd/TDD-00176.md)
- Template literal types (`` `a-${T}` ``) — **Erased to `string`** — the literal pattern is parsed (no-substitution, multi-substitution, and `[]`-suffixed forms) but resolves to `string`, not narrowed/enforced; the same simplification string-literal types use ([ADR-00561](adr/ADR-00561.md))
- `readonly T[]` array-type modifier — **Erased, not enforcing** — `readonly number[]` and `readonly [T, U]` parse (in variable/parameter/return/field positions), but the modifier is dropped and mutation is not prevented, the same stance as `Readonly<T>` and the mapped-type `readonly` modifier. The `ReadonlyArray<T>` alias form is not covered ([ADR-00373](adr/ADR-00373.md))
- Ambient declarations (`declare const`/`function`/`class`/`module`/`namespace`) — A runtime *use* of a declared binding this compiler doesn't itself provide is an ordinary `undefined variable` error — under whole-program AOT an ambient declaration has no external target to bind to
- Ambient declarations (`declare const`/`function`/`class`/`module`/`namespace`) — `declare class` and the brace-bodied `global`/`module`/`namespace` blocks are parsed and erased (no binding — [ADR-00388](adr/ADR-00388.md))
- TypeScript type errors (assignability, calls, operators, properties, assignment targets) — Reported: `TS2322`, `TS2345`, `TS2554`/`TS2555`, `TS2362`/`TS2363`/`TS2365`, `TS2367`, `TS2339`, `TS2353`, `TS2741`/`TS2739`/`TS2740`, `TS2588`/`TS2628`/`TS2629`/`TS2630`, `TS2769`, `TS18046`–`TS18050`, `TS2531`–`TS2533`, `TS2571`, `TS2721`–`TS2723`, `TS2304` and the forms tsc gives it for a value (`TS2552`, `TS2662`/`TS2663`, `TS2693`, `TS2708`, `TS2301`, `TS18004`, `TS2592`/`TS2593`), `TS2558`, `TS2754`, the class-member accessibility codes (`TS2341`, `TS2445`, `TS2446`, `TS2673`, `TS2674`, `TS2855`), a call's `this` context (`TS2684`). Not yet: a near-miss type name (`TS2552`), a type name out of its scope, a partly assigned variable read (`TS2454`), iterating a possibly-`undefined` value (`TS2488`), a call no overload takes by arity (`TS2575`), and more
- TypeScript type errors (assignability, calls, operators, properties, assignment targets) — A property is checked only on a type whose members the checker knows whole: not on a library global's name (an `interface Element` merges with the library's) or a class merged with an interface
- TypeScript type errors (assignability, calls, operators, properties, assignment targets) — A call is checked only against a function type the checker models whole: calls to generic functions and to the builtin library's undeclared parts are not checked, and a tagged template's call not at all
- TypeScript type errors (assignability, calls, operators, properties, assignment targets) — A function type in a message names its parameters `p0`, `p1`, … where tsc prints their declared names

### Binary Data & Typed Arrays — Strict 4/9 (~44%) · 9 caveats — [Binary Data & Typed Arrays](status/BINARY-DATA-TYPED-ARRAYS.md)
- `ArrayBuffer` — `resize(n)` still requires the `{maxByteLength}` construction option — compile-time rejection otherwise ([ADR-00494](adr/ADR-00494.md), [ADR-00564](adr/ADR-00564.md))
- `ArrayBuffer` — A TypedArray view captures its length at construction and does not length-track a later `resize` — re-`new` the view after resizing (spec auto-tracks a no-explicit-length view)
- `BigInt64Array` / `BigUint64Array` — Only an explicit method allow-list is supported — indexing r/w, `.length`/`.byteLength`, `.at`, `.set`, `.subarray`, `.slice`, `.fill`, `.reverse`, for-of, `Atomics.*`; everything else (`.map`/`.filter`/`.reduce`/`.indexOf`/`.sort`/`.join`/iterator objects/…) is a compile-time rejection, never a raw-scalar leak
- `BigInt64Array` / `BigUint64Array` — `Atomics.wait`/`notify` stay `Int32Array`-only
- `SharedArrayBuffer` — `grow(n)` requires the `{maxByteLength}` construction option (compile-time rejection otherwise, vs the spec's runtime TypeError); the maximum is reserved upfront ([ADR-00494](adr/ADR-00494.md))
- `Atomics` — `wait` blocks the whole calling thread (event loop and fibers included) — allowed on the main thread (Node posture); no `waitAsync`
- `Atomics` — `wait`/`notify` are `Int32Array`-only (spec minus the BigInt64 half)
- `Atomics` — Index is bounds-unchecked, matching ordinary TypedArray indexing
- Node `Buffer` (`Buffer.from`/`.alloc`/`.toString(encoding)`/`.write`/etc.) — String-valued `.indexOf`/`.includes`/`.lastIndexOf` search the needle's UTF-8 bytes ([ADR-00558](adr/ADR-00558.md)), and `.fill(string, offset?, end?)` repeats the needle's bytes ([ADR-00559](adr/ADR-00559.md)) — all single-argument-plus-range forms (no `byteOffset`/`encoding`); no 4-arg `.write(string, offset, length, encoding)` form; no `.swap16/32/64`, `.toJSON`, `Buffer.of`, `Buffer.isAscii`/`Buffer.isUtf8`, arbitrary-width `readIntLE(offset, byteLength)`, `poolSize`

### Modules — Strict 9/18 (50%) · 15 caveats — [Modules](status/MODULES.md)
- Circular imports — Supported only for the declarations-only case — a file in an import cycle can't run arbitrary top-level side-effecting code
- Imported (non-entry) files may run top-level side-effecting code — A file that genuinely participates in an import cycle keeps the declarations-only restriction (no bare executable top-level statements), and additionally requires a top-level `var`/`let`/`const` initializer to be a compile-time literal — no TDZ/live-binding modeling
- Type-only imports and exports (`import type`, `{ type X }`, `export type { X }`) — A Node class of a module code generation implements (`Hash`, `Http2ServerRequest`, …) is a type only: a value use is a compile error; the classes of the modules written in TypeScript (`net`, `tls`, `http`, `https`, `stream`, `events`, `child_process`, `tty`, `readline`, `string_decoder`) are values
- `import * as ns from '...'` (namespace import) — Compile-time-only — `ns` is never a runtime value, usable only as the object of a non-computed dotted access; assigning it to a variable, passing it as a value, or `ns["x"]` reaches codegen as a plain undefined reference rather than a dedicated error message
- Static CommonJS `require('<literal>')` — Top-level, string-literal specifier only. A destructuring default/nested-pattern/rest on a require binding, or a dynamic `require(expr)` / a `require` nested in a function body (lazy loading), is a clean compile error — deferred to the runtime/lazy-loading capability
- Re-exports (`export { x } from './other'`, `export * from './other'`) — No namespace re-exports (`export * as ns from`) — parsed and explicitly rejected with a clear error rather than silently mis-parsed, no concrete use case yet
- Re-exports (`export { x } from './other'`, `export * from './other'`) — Re-exporting from a built-in module (`export { readFileSync } from 'fs'`) is an explicit, rejected scope cut, not an oversight
- Bare/package-style imports (`import x from 'somepackage'`) — klmpm is Stage 1 only ([TDD-00054](tdd/TDD-00054.md)) — deliberately just the resolution half; no klmpm tool exists yet to fetch/version/lock a dependency, so a `klain_modules/<name>/` directory has to be hand-constructed today
- Bare/package-style imports (`import x from 'somepackage'`) — npm/`node_modules` interop is a separate, unstarted, differently-scoped mechanism — see [TDD-00053](tdd/TDD-00053.md)
- Dynamic `import(...)` — String-literal specifier only — a runtime-computed `import(expr)` is a clean compile error (all imports resolve at compile time)
- Dynamic `import(...)` — The result object exposes the target's **annotated scalar/string exports** only; `function`/`class`/array/object exports are omitted, and an un-annotated export has no field (module namespace objects are missing for static `import * as ns` too)
- Dynamic `import(...)` — `import('node:…')` of a builtin module is a clean compile error
- Dynamic `import(...)` — `-dynamic-import=lazy` and `-dynamic-import=isolated` refuse `--static` (a static binary can't `dlopen`), and `lazy` is not available on Windows
- Dynamic `import(...)` — Under `-dynamic-import=isolated`, an island's sockets, pipes and children are invisible to the importer's `select()`: while such an island's top-level `await` is pending, the importer re-polls it every 10 ms instead of waking on the island's fd ([TDD-00225](tdd/TDD-00225.md)); the island's timers are folded exactly
- `import.meta.url` — `import.meta.url` is the only supported member — bare `import.meta` or any other member (`import.meta.resolve`, etc.) is a clean parse-time error

### Concurrency (Workers) — Strict 2/4 (50%) · 13 caveats — [Concurrency (Workers)](status/CONCURRENCY-WORKERS.md)
- `Worker` — A worker's file is one of the program's own modules, compiled in: `new Worker` takes a path literal (resolved relative to the file it is written in, where Node resolves it against the working directory) or `__filename`; a computed path or `eval: true` code fails at `new Worker` (a genuine impossibility of native compilation)
- `Worker` — `terminate()` takes effect at the worker's next event-loop turn: a worker busy in synchronous code runs on until it yields, where Node interrupts it
- `Worker` — A transferred `ArrayBuffer` is copied, not detached: the sender's stays usable
- `Worker` — A worker shares `process.env` with its parent (Node gives it a copy) and writes its `stdout`/`stderr` directly; the `stdin`/`stdout`/`stderr`/`env`/`resourceLimits`/`argv`/`execArgv` options, `worker.stdout`/`stdin`/`stderr` and `getHeapSnapshot()` are not ported
- `Worker` — An `'uncaughtException'` listener in a worker runs but the worker still ends with code 1
- `Worker` — No combining with `http.listen({workers:N})`
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — Channel ops are synchronous blocking (`ch.send(v)`/`ch.receive()`), not `await`; element is a fixed 8-byte slot (number/string/object/boolean)
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — A `select` recv handler receives the value but not the closed flag (use a `for (const v of ch)` range, which ends on close, to detect it)
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — Fixed goroutine stacks (default 256 KiB, tunable via `KLAINSYNC_STACK_KB`), no overflow guard — deep recursion is UB; growable/moving stacks gated on precise stack maps
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — Blocking-syscall P-handoff is a bounded sysmon-driven rescue-M approximation, not full `entersyscall`/`exitsyscall`
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — A nil (never-constructed) channel isn't expressible — channels are always `new Channel`
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — Per-iteration capture of a loop `let` directly in a goroutine closure is a pre-existing general closure limitation — pass the value as a function parameter (a `spawn(work)` helper) instead
- `klain:sync` goroutines, channels, `select` (`go`, `Channel<T>`, `select`) — `send` after `close` and `close` of a closed channel abort the process (Go-panic parity); native-only (compile error under plain `tsc`/Node)

### Performance & Timing — Strict 7/14 (50%) · 6 caveats — [Performance & Timing](status/PERFORMANCE-TIMING.md)
- `Date` — `toString`'s zone name comes from a built-in table of the common zones' abbreviations; a zone outside it prints the C library's abbreviation (`GMT-0300 (-03)` where Node says `(Brasilia Standard Time)`)
- `Date` setters (`setFullYear`, `setMonth`, `setDate`, `setHours`, `setMinutes`, `setSeconds`, `setMilliseconds`, `setTime`) — Requires a named-variable receiver (not a field access or call result — this compiler's Date is a plain number, not a reference object, so there's no heap location to mutate otherwise)
- `Date` arithmetic (`date ± durationMs`, `date - date`, `date += durationMs`) — Under `-compat=js`, a compound `date += n` / `date -= n` keeps the variable a Date; JavaScript turns it into a string / a number (the plain `+`/`-` do)
- `Date.prototype.toLocaleDateString()` — One fixed `"M/D/YYYY"` format (the default en-US shape) in local time; no locale argument or full `Intl`-style locale support
- `Date.prototype.toLocaleTimeString()` / `toLocaleString()` — One fixed en-US shape (`"1:04:09 PM"`, `"1/5/2020, 1:04:09 PM"`) in local time; no locale argument or full `Intl`-style locale support
- `createHistogram` / `monitorEventLoopDelay` — A histogram keeps exact samples, not Node's HDR histogram (3 significant figures): a percentile of large values can differ in its last digits

### URL — Strict 5/10 (50%) · 5 caveats — [URL](status/URL.md)
- `URL` — A non-ASCII hostname is case-folded and punycoded, without the rest of UTS 46's mapping: a full-width `ＥＸＡＭＰＬＥ.com` punycodes where Node reads `example.com`
- `URLPattern` — Component regexes run on PCRE2, so a regexp group's ECMAScript-only semantics differ: an empty optional group iteration reads `""` (JavaScript: `undefined`), `v`-flag set operations (`[[a-z]--a]`) never match, and invalid `v`-mode syntax is accepted (3 of the 369 WPT `urlpatterntestdata.json` cases)
- `url.parse(urlString[, parseQueryString[, slashesDenoteHost]])` (legacy) — The `parseQueryString` object form of `query` is a dictionary held in a union field: reading it after narrowing works, but `console.log`/`JSON.stringify` of it through `any` do not see its keys ([ADR-01225](adr/ADR-01225.md))
- `url.urlToHttpOptions(url)` — Expects a WHATWG `URL`; a legacy `Url`/plain object isn't accepted ([ADR-00672](adr/ADR-00672.md))
- `url.domainToASCII(domain)` / `url.domainToUnicode(domain)` — A non-ASCII hostname is case-folded and punycoded, without the rest of UTS 46's mapping: a full-width `ＥＸＡＭＰＬＥ.com` punycodes where Node reads `example.com`

### Encoding / Text — Strict 1/2 (50%) · 1 caveat — [Encoding / Text](status/ENCODING-TEXT.md)
- `TextDecoder` — The CJK encodings (GBK, GB18030, Big5, EUC-JP, EUC-KR, ISO-2022-JP, Shift_JIS), which Node decodes through ICU, throw `ERR_ENCODING_NOT_SUPPORTED`

### File System (fs) — Strict 11/21 (~52%) · 9 caveats — [File System (fs)](status/FILE-SYSTEM.md)
- `fs.statSync` / `lstatSync` / `fstatSync` → `Stats` — `{ bigint: true }` (`BigIntStats`) is not implemented: the option is ignored
- `fs.statfsSync(path)` → `StatFs` — `{ bigint: true }` is ignored: the fields are numbers
- `fs.mkdirSync` / `mkdtempSync` / `rmdirSync` / `rmSync` / `unlinkSync` — `rmSync`'s `maxRetries`/`retryDelay` are validated and unused (Node retries `EBUSY`/`ENOTEMPTY`/`EPERM`)
- `fs.readdirSync(path[, options])` → names or `Dirent[]` — No `.path` alias on `Dirent` (Node v24 removed it): reading `dirent.path` is a compile-time error, not `undefined`
- `fs.renameSync` / `copyFileSync` / `linkSync` / `symlinkSync` / `readlinkSync` / `realpathSync` — `COPYFILE_FICLONE_FORCE` fails with `ENOTSUP`: there is no copy-on-write clone (`COPYFILE_FICLONE` copies)
- `fs.open` / `close` / `read` / `write` / `writev` / `readv` / `fsync` (callback form) — On Windows a positioned read or write seeks, then transfers — not atomic against another operation on the same descriptor; not verified on Windows
- `fs.promises.open` → `FileHandle` — Not ported: `chown`, `readv`, `readLines`, `readableWebStream`, `[Symbol.asyncDispose]`
- `fs.watch(path[, options][, listener])` → `FSWatcher` — On macOS a directory's event names the directory, not the changed entry: kqueue watches a descriptor, not entries ([ADR-00758](adr/ADR-00758.md))
- `fs.watch(path[, options][, listener])` → `FSWatcher` — `recursive` and `persistent: false` are validated and not applied

### Terminal UI — `klain:tui` — Strict 6/11 (~55%) · 5 caveats — [Terminal UI — `klain:tui`](status/TERMINAL-UI.md)
- Flexbox layout (vendored Yoga) — Percentage units, `position:absolute`, and `aspectRatio` are not yet surfaced
- `Text(text, props?)` — styled, wrapped text — Multi-code-point grapheme clusters joined by ZWJ (flag, family, and skin-tone emoji sequences) paint as their separate wide glyphs, not one cluster
- `TextInput(value, props?)` — Editing/key handling is userland (read keys via `klain:tty`, mutate state, re-render)
- `render(root)` — layout + diff paint — Immediate-mode: the whole tree is rebuilt and re-laid-out every frame (the cell diff keeps *output* minimal); a retained/memoized node tree is a deferred perf question
- `state → view → update` app loop — The loop is written in userland TypeScript over the `klain:tty` key reads + `SIGWINCH` — there is no built-in app-runner and no callback-driven loop yet (a closure→C function-pointer trampoline is TDD-00150 Stage 2)

### Process / CLI I/O — Strict 19/33 (~58%) · 24 caveats — [Process / CLI I/O](status/PROCESS-CLI.md)
- `process.emitWarning(warning, type?\|options?, code?, ctor?)` — The one-shot `(Use \`node --trace-warnings ...\`)` hint Node prints is omitted: a compiled program has no `--trace-warnings` flag
- `process.stdout.write(s)` / `process.stderr.write(s)` (raw write, no auto-newline) — `process.stdin`/`stdout`/`stderr` are typed `any` to the checker until `process` itself is typed from its module ([TDD-00230](tdd/TDD-00230.md) P3.2), so a wrong call (`process.stdout.write()` with no argument) is not reported as a TypeScript error
- `klain:tty` `readByte()` / `readKey()` (synchronous raw reads) — Bespoke, non-Node surface under the explicit `klain:` specifier — Node has no synchronous single-key read (it uses `process.stdin.on('data')` events)
- `klain:tty` `readByte()` / `readKey()` (synchronous raw reads) — Blocking reads on fd 0; do not mix with `process.stdin.on('data')` in the same run, which puts fd 0 in non-blocking mode (a synchronous read would then see EOF-on-EAGAIN)
- `process.env` as a value (`Object.keys`/`values`/`entries`, `for…in`, `{ ...process.env }`, `JSON.stringify`, `const env = process.env`) — The object is a snapshot taken when the bare `process.env` expression is evaluated: keyed reads/writes (`process.env.X`) stay live, but a write through an alias (`const env = process.env; env.X = '1'`) lands in the copy, not the environment — Node's object is a live proxy
- `process.on(signal, listener)` (signal listeners) — Windows delivery through the signal table ([ADR-01250](adr/ADR-01250.md)) is compile-checked only; the watcher path was run on macOS
- `process` as an `EventEmitter` (`on`/`once`/`off`/`emit`/…, `'exit'`/`'uncaughtException'`/`'unhandledRejection'`/`'warning'`) + `process.exitCode` — `'uncaughtException'` runs the listener (suppressing the default `Uncaught:` print) but, after a synchronous throw, the process still exits (code 1) — the setjmp/longjmp exception model has already unwound to the top-level catch-all, so it can't resume execution like Node does (a rejection reported to it does go on)
- `process.memoryUsage()` — `external`/`arrayBuffers` report 0 — V8's off-heap C++-binding accounting has no native analogue (all allocation lives in `rss` and the one heap above), so they are disclosed rather than invented
- `process.memoryUsage()` — `heapTotal`/`heapUsed` measure this compiler's object heap, not a V8 JS-object heap — a native reinterpretation (the C allocator arena, or Boehm's heap under `-mm=gc`), so the magnitudes differ from Node's even though the direction (growth on allocation) matches
- `process.nextTick(fn)` — Shares the one microtask FIFO with Promise reactions rather than draining strictly before them (Node keeps a separate nextTick queue ahead of promises)
- `process.version` / `process.versions` — `versions` omits bundled-lib keys this compiler doesn't ship (`uv`/`undici`/`icu`/…) rather than fabricate them
- `process.version` / `process.versions` — Linked-library versions that could be reported truthfully (`openssl` under the OpenSSL backend, `zlib` when linked) are not surfaced
- `process.version` / `process.versions` — `node`/`v8` track the compatibility *ceiling* (the pinned test-corpus release); a `--node-compat` floor is deferred ([TDD-00136](tdd/TDD-00136.md))
- `process.stdin` (streamed reads) — `process.stdin`/`stdout`/`stderr` are typed `any` to the checker until `process` itself is typed from its module ([TDD-00230](tdd/TDD-00230.md) P3.2), so a wrong call (`process.stdout.write()` with no argument) is not reported as a TypeScript error
- `readline` module (`createInterface`, `'line'`/`'close'` events, `question()`, `close()`) — `readline.promises` holds `createInterface` only; its `Interface` and `Readline` classes are imported from `readline/promises` (a class is not yet a value)
- `readline` module (`createInterface`, `'line'`/`'close'` events, `question()`, `close()`) — `util.promisify(rl.question)` is not the promise form: `question` has no `util.promisify.custom` (a class's `prototype` is not reachable yet); `readline/promises`' `question` is
- `readline` module (`createInterface`, `'line'`/`'close'` events, `question()`, `close()`) — Display widths use Node's non-ICU `getStringWidth` table, so a few wide or emoji code points can take a different column count than an ICU build of Node
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — Handle passing over the IPC channel and `serialization: 'advanced'` are not ported
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — On Windows the process handles are compile-checked, not yet run
- `child_process.spawn()` / `.exec()` / `.execFile()` (async, event-driven) — `-mm=manual`: a process handle's table slot is never reused
- `child_process.fork()` (self-fork + IPC channel) — Self-fork only: the module must be this program's entry (a different module is a second compiled program: `ERR_CHILD_PROCESS_FORK_MODULE`)
- `child_process.fork()` (self-fork + IPC channel) — `serialization: 'advanced'` is not ported: messages always cross as json
- `child_process.fork()` (self-fork + IPC channel) — A `dgram` socket cannot be sent as a handle; on Windows no handle can (descriptor passing is POSIX-only)
- `child_process.spawnSync()` / `.execSync()` / `.execFileSync()` (blocking) — On Windows the blocking spawn is compile-checked, not yet run

### Object / Collections — Strict 19/32 (~59%) · 26 caveats — [Object / Collections](status/OBJECT-COLLECTIONS.md)
- `Object.keys(obj)` — `Object.values`/`entries`/`for...in` still list an omitted optional field (as `undefined`); only `keys` filters
- `Object.values(obj)` — On a *static* array (`Object.values([7, 8])`) it is a clean rejection; a bare `any` holding an array answers its elements
- `Object.entries(obj)` — On a *static* array (`Object.entries([7, 8])`) it is a clean rejection; a bare `any` holding an array answers `["0", v0], …`
- `Object.assign(target, ...src)` — Between statically typed objects, every field a source contributes must already exist on `target`'s struct type — a source field `target`'s type doesn't have is a clean compile error (fixed-shape heap structs), not grafted on as in real JS ([ADR-00054](adr/ADR-00054.md))
- `Object.assign(target, ...src)` — Through `any`, symbol keys are not copied; an array target is a compile error
- `Object.create()` / `getPrototypeOf` / `setPrototypeOf` / `__proto__` (dynamic objects) — Prototype machinery exists on **dynamic (`any`-typed) objects only** — statically-typed structs have no prototype link, and a boxed primitive's `getPrototypeOf` answers `null` (no primitive prototype objects)
- `Object.create()` / `getPrototypeOf` / `setPrototypeOf` / `__proto__` (dynamic objects) — `Object.create`'s property-descriptors second argument is rejected until descriptors land
- `Object.create()` / `getPrototypeOf` / `setPrototypeOf` / `__proto__` (dynamic objects) — Inherited properties don't appear in `for...in` (own-only enumeration; real JS walks the chain's enumerables)
- `Object.defineProperty` / `getOwnPropertyDescriptor` / `getOwnPropertyNames` / accessors (dynamic objects) — `Object.defineProperty` and `defineProperties` take dynamic (`any`-typed / js-mode-literal) objects only — a statically typed struct has no descriptor table
- `Object.defineProperty` / `getOwnPropertyDescriptor` / `getOwnPropertyNames` / accessors (dynamic objects) — `Object.defineProperties` defines string keys only, not symbol keys
- `new Proxy(target, handler)` — Traps implemented: `get`/`set`/`has`/`deleteProperty` — dispatched in the dynamic-object runtime entry points, forwarding to the target when absent ([ADR-00630](adr/ADR-00630.md)); other traps (`ownKeys`, `getOwnPropertyDescriptor`, `apply`, `construct`, …) are not consulted (those operations forward to the target)
- `new Proxy(target, handler)` — The target must be a dynamic object (an untyped/`any` literal); statically-typed structs can't be proxied
- `Reflect.get/set/has/deleteProperty/ownKeys/getPrototypeOf/setPrototypeOf/isExtensible/preventExtensions/defineProperty` + `defineMetadata`/`getMetadata`/`hasMetadata` — `Reflect.construct` with a `newTarget` other than the target throws a TypeError rather than constructing with that prototype
- `Reflect.get/set/has/deleteProperty/ownKeys/getPrototypeOf/setPrototypeOf/isExtensible/preventExtensions/defineProperty` + `defineMetadata`/`getMetadata`/`hasMetadata` — The metadata API (`defineMetadata`/`getMetadata`/`getOwnMetadata`/`hasMetadata`/`hasOwnMetadata`, TDD-00161 Stage 3) stores on a dynamic-object target and does not walk the prototype chain (so `get` == `getOwn`); `Reflect.metadata` as a decorator factory is rejected
- `Reflect.get/set/has/deleteProperty/ownKeys/getPrototypeOf/setPrototypeOf/isExtensible/preventExtensions/defineProperty` + `defineMetadata`/`getMetadata`/`hasMetadata` — Boolean-returning forms (`set`/`deleteProperty`/`setPrototypeOf`) return the success flag rather than throwing, per spec
- `Reflect.get/set/has/deleteProperty/ownKeys/getPrototypeOf/setPrototypeOf/isExtensible/preventExtensions/defineProperty` + `defineMetadata`/`getMetadata`/`hasMetadata` — Every form requires a dynamic (`any`-typed, or `-compat=js`) target: a statically-typed object operand is a clean compile-time rejection (strict mode does not widen a typed struct to a bag, [ADR-00630](adr/ADR-00630.md)), including `get`/`has`
- `Object.hasOwn()` / `.hasOwnProperty()` — The key must be a string literal — a runtime-computed key is a clean compile error (no runtime field-name table to check it against) ([ADR-00065](adr/ADR-00065.md))
- `Object.fromEntries()` — Keys must be strings (a `[string, V][]` array) — real JS stringifies any key and accepts symbol keys ([ADR-00348](adr/ADR-00348.md))
- Computed property keys `{ [expr]: value }` — `V` is inferred from the first property only
- Computed property keys `{ [expr]: value }` — `...spread` combined with a computed key isn't supported yet
- Computed property keys `{ [expr]: value }` — The declared-type form (`{ [key: string]: T }`) isn't supported yet
- Computed property keys `{ [expr]: value }` — A literal with a non-constant key is a `Map` inside: a function-valued property can't be called as a method, and the literal can't be held in `any`
- Method shorthand `{ foo() {...} }` — `this` in a method-shorthand body is bound only when the literal's contextual type gives the method a `this: T` parameter (`{ read() { this.push(x) } }` against `read: (this: S, n: number) => void`, [ADR-01158](adr/ADR-01158.md)); otherwise it is a clean compile-time rejection, where TypeScript types it as the object literal
- Method shorthand `{ foo() {...} }` — No generator method shorthand (`*g() {}`): rejected like a generator expression used as a value ([ADR-00169](adr/ADR-00169.md))
- `WeakMap` / `WeakSet` / `WeakRef` — Object-identity keys only (a primitive key is a clean compile error); non-iterable (no `size`/iteration — matches spec)
- `WeakMap` / `WeakSet` / `WeakRef` — Under `-mm=manual` (default) a weak reference is strong: nothing is ever collected, so `.deref()` never becomes `undefined` and keys persist ("leak by design"). Real weak semantics require `-mm=gc` ([TDD-00112](tdd/TDD-00112.md)/[ADR-00349](adr/ADR-00349.md))

### Timers — Strict 3/5 (60%) · 3 caveats — [Timers](status/TIMERS.md)
- `setTimeout(fn, ms)` / `clearTimeout(id)` — The handle is the timer's numeric id, not Node's `Timeout` object: `typeof` reads `"number"`, and `ref`/`unref`/`hasRef`/`refresh`/`close` work only on a typed handle, not through `any`
- `setImmediate(fn)` / `clearImmediate(id)` — Real Node guarantees `setImmediate` fires before a same-tick `setTimeout(fn, 0)` when scheduled from inside an I/O callback, because its event loop has distinct phases (check vs. timers); this compiler's `__kml_timer_drain` is a single flat fire-time-ordered queue with no phase concept, so the two are genuinely indistinguishable here (both fire at "now")
- `setImmediate(fn)` / `clearImmediate(id)` — The handle is the timer's numeric id, not Node's `Immediate` object: `typeof` reads `"number"`; `ref`/`unref`/`hasRef` work only on a typed handle, not through `any`

### Language Constructs — Strict 50/81 (~62%) · 63 caveats — [Language Constructs](status/LANGUAGE-CONSTRUCTS.md)
- `for…of` over arrays, strings, `Map`, `Set`, a bare `any`, and a class implementing `next(): T \| null` — Over a bare `any`, each element is read through a numeric-string key (a per-element `sprintf` + dynamic get) — a direct per-tag element walk is the perf follow-up
- `try` / `catch` / `finally` — A thrown plain object's own fields read `undefined` through the caught value (`throw { code: 1 }` then `e.code` or `catch ({ code }: any)`): a static object has no runtime shape once boxed
- `try` / `catch` / `finally` — A nested or array catch pattern destructures only the Error shape (`{ kind, message, name }`) ([ADR-00170](adr/ADR-00170.md))
- `TypeError` on a property access off `undefined`/`null` (`s.length`, `r.name`, `p.m()`, `s[i]`, `p.x = v`) — In `p.x = f()` the base is checked before the right-hand side is evaluated, so `f()` does not run ahead of the `TypeError` (JS evaluates it first)
- String literals (single/double quote) — An embedded NUL (`\x00`, `\0`) truncates the string, since strings are stored as NUL-terminated C strings
- String literals (single/double quote) — No strict-mode `SyntaxError` for a legacy octal escape (accepted unconditionally)
- Ternary `cond ? a : b` — A pointer branch mixed with a scalar one that is not a scalar union (`c ? 1 : someObject`), and an array branch mixed with a non-array one, are compile-time rejections unless one branch is `any` (then the result is `any`, [ADR-01159](adr/ADR-01159.md)) — narrow first, or assign each branch separately
- Comma / sequence operator `(a, b, c)` — A sequence whose *first* operand is a lone identifier (`(a, b)`) is instead parsed as an arrow-function parameter list — a known ambiguity; write a non-identifier first operand ([ADR-00179](adr/ADR-00179.md))
- `typeof` operator — `typeof <namespace>.<method>` answers `"function"` for the common built-in namespaces — `Promise`/`Math`/`JSON`, `console` (any member), and the static methods of `Object`/`Number`/`String`/`Array`/`Date`/`Symbol`/`Reflect`/`Boolean` ([ADR-00282](adr/ADR-00282.md)/[ADR-00596](adr/ADR-00596.md)); a non-method static (`Number.MAX_VALUE`) still answers through normal inference, and a method on a namespace outside this allow-list falls back to inference
- `typeof` operator — `typeof value.method` is `"function"` for a class instance method and the common built-in string/array methods ([ADR-00607](adr/ADR-00607.md)); a string/array method name outside that curated set falls back to inference (a wrong `"number"`)
- `const` / `let` / `var` declarations — Under `-compat=js`, a function declared in a block is not visible after the block: Annex B hoists it to the enclosing function as a `var` (`if (c) { function g() {} } g()` runs in Node), here it is a clean rejection
- `const` / `let` / `var` declarations — A top-level `const`/`let` still isn't readable from a named `function` only in the invariant's genuine edges — a `bigint`, an un-annotated call *through another top-level binding* (`const f = () => …; const x = f()` — its result type isn't known before `main()` runs), a `new Set([…])` typed from its initializer array, or an init depending on a runtime-local (a `{ …rest }` spread of a destructuring target); an arrow/closure still captures any of them ([TDD-00093](tdd/TDD-00093.md)). An annotated `any`/`unknown`/union binding is a module global like the rest ([ADR-01059](adr/ADR-01059.md))
- Array destructuring `const [a, b] = arr` — In the *assignment* form (`[a, b] = expr`, not a fresh declaration), a compound operator (`[a, b] += …`) and a member target (`[obj.x, arr[i]] = e`) stay rejected ([ADR-00595](adr/ADR-00595.md))
- Object destructuring `const { x, y } = obj` — A default (`{ x = 1 }`) on a `T \| null \| undefined` reference field (a string, array or object) also fires on `null`: one null pointer holds both ([ADR-01307](adr/ADR-01307.md))
- Object destructuring `const { x, y } = obj` — In the *assignment* form over a statically-typed source (`({ x, y } = obj)`), a `= default` on a nested position stays rejected — matching the declaration form ([ADR-00597](adr/ADR-00597.md))
- Object destructuring `const { x, y } = obj` — `{ ...rest }` over an `any`/union/generic source (Stage 3c) and in the *assignment* form (`({ a, ...rest } = e)`) stay clean rejections
- Object destructuring `const { x, y } = obj` — In a declaration, a computed key (`{ [k]: v }`) is supported only for a **constant** string/number literal key (`{ ["a"]: v }`, `{ [0]: v }` — resolves to that field); a runtime-valued key on a fixed object is a clean rejection ([ADR-00609](adr/ADR-00609.md))
- Function declarations (top-level) — Cannot reference a sibling top-level binding whose value is a connection handle (`Worker`, `BroadcastChannel`/`MessageChannel`, `XMLHttpRequest` — construction opens a thread/socket), a `Promise`, or a generic class instance with **inferred** rather than explicit type arguments (`new Box(5)`, or a nested `new Box<Box<number>>()`) — such a binding stays a `main()` local outside a named function's fresh scope, failing with `undefined variable`. Scalars, strings, arrays, `TypedArray`s, objects, `Map`/`Set`, class instances (including an explicit `new Box<number>()`), the value/event handles (`Blob`, `Date`, `Error`, `URL`, `URLSearchParams`, `URLPattern`, `RegExp`, `Headers`, `ArrayBuffer`, `DataView`, `TextEncoder`/`TextDecoder`, `Request`, `AbortController`, `Event`/`CustomEvent`, `EventTarget`), and the streams (`ReadableStream`/`WritableStream`/`TransformStream`/`CompressionStream`, the Node streams) + `EventEmitter`, and `http.createServer` handles ([ADR-00426](adr/ADR-00426.md)) are promoted to module globals and are readable ([TDD-00093](tdd/TDD-00093.md)/[ADR-00342](adr/ADR-00342.md)/[ADR-00709](adr/ADR-00709.md)); arrow functions/closures capture everything regardless ([TDD-00057](tdd/TDD-00057.md))
- Function declarations (top-level) — A genuinely circular pair of mutually-recursive unannotated functions can't converge on a return type and keeps the scalar-default fallback ([TDD-00058](tdd/TDD-00058.md))
- Function declarations (top-level) — The `arguments` object is synthesized from the declared parameters when they all share one type ([ADR-00387](adr/ADR-00387.md)) — `.length`, indexing, and `for…of` work; mixed-type parameters, a rest/destructured parameter, and an arrow function (which has no own `arguments` in JS) are clean rejections, and it reflects the declared parameters rather than growing with extra untyped arguments (there is no variadic call beyond an explicit `...rest`); class method bodies get the same synthesis ([ADR-00464](adr/ADR-00464.md))
- Function expressions (`var f = function(x) {...}`, callback arguments, IIFE bodies) — A generic function used by value is out of scope (no single monomorphized symbol to point at) ([ADR-00200](adr/ADR-00200.md))
- Default parameter values — A default expression referencing an earlier parameter works across free functions, instance/static methods, and constructors ([ADR-00598](adr/ADR-00598.md)); a rest-parameter constructor is unsupported
- Default parameter values — Filled through a first-class function value too — a closure bound to a variable/field, an object shorthand method, an IIFE ([ADR-00914](adr/ADR-00914.md)) — including a default that references a variable captured from the closure's defining scope, evaluated in the body prologue via an argument-presence mask ([ADR-00915](adr/ADR-00915.md)); this also covers a scalar/string/object/array captured-default parameter, an async closure, and `.bind` (the mask threads through the bind trampoline) ([ADR-00916](adr/ADR-00916.md)). A captured-default on a nullable-scalar or destructured (`{a}`/`[a]`) parameter stays a clean rejection (no single overwrite slot), and capturing an array *variable* into a default (vs. referencing a module-global array) hits the separate array-capture limitation
- Optional parameters (`param?`) — In strict mode an un-narrowed optional scalar is still lenient in arithmetic (`emitIdent` unwraps the payload before the operator, so `a + b` on an absent `b` reads `0` rather than tsc's `possibly undefined` error — the same residual as any un-narrowed nullable local); under `-compat=js` the absent operand is `NaN`, as `ToNumber(undefined)` ([ADR-01070](adr/ADR-01070.md))
- Destructured function parameters (`function f({ x, y }: T) {}` / `function f([a, b]: T[]) {}`) — No combination with `...` or a whole-parameter default value; a pattern param with no annotation and no contextual type (a plain `function f([a, b])`) is still rejected, matching TS's implicit-any error
- Destructured function parameters (`function f({ x, y }: T) {}` / `function f([a, b]: T[]) {}`) — A `= default` on a nested position is a clean rejection; per-element defaults follow the same rules as the statement-level destructuring rows
- Nested function declarations (`function outer() { function inner() {...}; return inner(); }`) — Generic (`<T>`) nested declarations and same-scope duplicate names are clean compile errors ([TDD-00057](tdd/TDD-00057.md)/[ADR-00149](adr/ADR-00149.md)); a hoisted call that reads a captured variable declared *between* the call and the declaration is a clean compile error (Node crashes with a runtime TDZ error there — the [TDD-00070](tdd/TDD-00070.md) posture)
- Nested function declarations (`function outer() { function inner() {...}; return inner(); }`) — Two mutually-recursive **capturing** nested siblings (`f` calls `g`, `g` calls `f`, both closing over an enclosing local) are a clean `undefined function or closure` compile error — cross-sibling letrec needs a shared self-capture cell ([ADR-01016](adr/ADR-01016.md)); self-recursion, including unannotated with the recursive call as the first return, works
- `Function.prototype.call` / `.apply` / `.bind` — `thisArg` is the receiver only of a function with a `this: T` parameter, declared or from its contextual type ([ADR-01158](adr/ADR-01158.md)); for any other function it is evaluated then ignored, so borrowing a class method against another receiver (`obj.m.call(other)`) is unsupported
- `Function.prototype.call` / `.apply` / `.bind` — call/apply/bind on a Node module's function (`net.connect.apply(…)`) isn't supported — it isn't a function value; the ECMAScript builtins (`parseInt`, `Math.max`, `encodeURIComponent`, …) are ([ADR-01307](adr/ADR-01307.md))
- `Function.prototype.call` / `.apply` / `.bind` — `.apply` takes either a literal array (`f.apply(null, [a, b])`) or a runtime array spread into a **rest** parameter (`f.apply(null, arr)` where `f` is `(...xs) => …`); a runtime array into a fixed-arity function is the usual spread-into-fixed-arity rejection
- Function overload signatures (`function f(x: number): number;` + impl) — Signatures are parsed and **erased** — call sites type-check against the implementation's parameter list only, with no per-signature arity/type narrowing (an ill-formed group — a signature with no implementation, interrupted, or name-mismatched — is still a clean rejection)
- Generator functions (`function* f(): T { yield x; }`, `.next(value)`, `for...of`) — A free `function*` may be top-level **or nested** (a nested `function*` captures enclosing state by reference — an enclosing `let` mutated after the generator is created is seen by a later `.next()` — reusing the closure-boxing on the instance's `__env`; an *array* capture stays a clean rejection); a generator *expression* is supported only as a top-level `const/let/var G = function* ...` binding (rewritten to a named declaration, [TDD-00096](tdd/TDD-00096.md)/[ADR-00293](adr/ADR-00293.md)) — an argument/nested/IIFE use is a clean rejection. Instance generator methods — sync and `async *m()` — are supported separately (rows below), but `static`/`abstract` generator methods stay clean rejections ([TDD-00094](tdd/TDD-00094.md))
- Generator functions (`function* f(): T { yield x; }`, `.next(value)`, `for...of`) — The return-type annotation is optional — the element type is inferred from the body's yields (numeric join, `yield*` delegation, return fallback; only a genuinely non-joinable mix still requires the annotation — [ADR-00293](adr/ADR-00293.md)); still requires a plain non-destructured parameter list and a non-array parameter type (an array *element* type is supported — yielded/sent arrays round-trip through every generator slot, `for...of`/`.next()`/`yield*`/`for await` alike, [ADR-00676](adr/ADR-00676.md); a tuple/object element type also works)
- Generator functions (`function* f(): T { yield x; }`, `.next(value)`, `for...of`) — When present, the annotation may be the element type written directly (`function* f(): number`) or the idiomatic-TS wrapper around it — `Generator<T>`, `IterableIterator<T>`, `Iterator<T>`, `Iterable<T>` and their `Async` forms, plus the three-arg `Generator<T, TReturn, TNext>` (TReturn is ignored; a `yield` expression is the value `.next(v)` sent, typed `any` whatever TNext declares) — which unwraps to `T` ([ADR-00814](adr/ADR-00814.md))
- Generator functions (`function* f(): T { yield x; }`, `.next(value)`, `for...of`) — Calling `.next()` again after completion returns `{value: <T's zero value>, done: true}`, not `undefined` ([TDD-00061](tdd/TDD-00061.md)/[ADR-00173](adr/ADR-00173.md)) — deliberately kept bare even with the `T \| undefined` sentinel available: tsc types the result's `value` as `any`, so the typed `T` here is already stricter than TS, and flipping it would break every `.next().value` consumer for no faithfulness gain ([ADR-00781](adr/ADR-00781.md))
- `Promise.all` / `.race` / `.allSettled` — An inline list mixing promises and values (`[p, 4]`) is a heterogeneous array literal, rejected in the strict lane — an `any[]` list works
- Namespaces (`namespace X {}` / `module X {}`, function merging) — Top-level declarations only (a namespace inside a function body is not supported); no `declare namespace`, no cross-module `export namespace`; the same namespace member declared in two files is a link-time duplicate-symbol error, not file-private ([TDD-00095](tdd/TDD-00095.md)/[TDD-00148](tdd/TDD-00148.md))
- Namespaces (`namespace X {}` / `module X {}`, function merging) — Type members (class/interface/type/enum) desugar to *bare-name* top-level declarations — two namespaces declaring the same class name collide, ([ADR-00450](adr/ADR-00450.md)); outside `X.Enum.Member` / `X.Class.static` chains resolve through the qualifier strip ([ADR-00480](adr/ADR-00480.md))
- Namespaces (`namespace X {}` / `module X {}`, function merging) — A top-level namespace `const` initializer can't reference a sibling member (it evaluates outside the namespace context); sibling references *inside member function bodies* work, with consts subject to the [ADR-00342](adr/ADR-00342.md) promotion type limits
- Interfaces (structural) — An explicit `undefined` stored into an optional member (`{ a: undefined }` against `a?: T`) reads as omitted: `console.log` drops the key and `Object.keys`/`in` report it absent (Node keeps the key, `{ a: undefined }`)
- Object literals `{ key: value }` — No duplicate-`__proto__`-key detection (Annex B legacy `SyntaxError` rule)
- Getters / setters (`get x() {}` / `set x(v) {}`) on classes and object literals — An accessor-bearing **object literal** can't be assigned to a *differently-shaped* structural object type (`const p: { x: number } = objWithGetter`) — its accessors are methods, not fields; a clean rejection, used directly it works ([ADR-00603](adr/ADR-00603.md))
- Getters / setters (`get x() {}` / `set x(v) {}`) on classes and object literals — `console.log` of an accessor object prints only its data fields, not `x: [Getter]` (an inspect-fidelity gap)
- Getters / setters (`get x() {}` / `set x(v) {}`) on classes and object literals — Static accessors, and logical assignment (`&&=`/`\|\|=`/`??=`) on an accessor, remain out of scope for V1 ([TDD-00030](tdd/TDD-00030.md)/[ADR-00110](adr/ADR-00110.md))
- Built-in `Error` subtypes (`new TypeError(msg)`, `RangeError`, `SyntaxError`, `EvalError`, `URIError`, `ReferenceError`, `DOMException`) and `instanceof` against them — `.stack` and `Error.captureStackTrace` don't exist: util.inspect shows an error as Node does without the stack's frames (`Name: message`, `X [Error]` for a class not named by its `name`, then its own properties)
- `new Array<T>(n?)` — A preallocated `new Array<T>(n)` fills real zero-valued slots, not holes — `new Array<number>(3)[0]` is `0` (Node: `undefined`), and `map`/`forEach` visit those slots instead of skipping holes.
- `class` (fields, constructor, methods, `this`, `new ClassName(args)`) — Instance-field initializers (`x = expr`) work, and a class with fields needs **no** explicit constructor — a bare declared field (`x: number`) reads as its calloc'd deterministic-zero value (0/false/null, the [ADR-00157](adr/ADR-00157.md) convention), any initializers that exist run in a synthesized constructor ([ADR-00374](adr/ADR-00374.md)); the one remaining rejection is a **derived** class adding fields when its base has a rest-parameter constructor (write an explicit `super(...)`). static field initializers (`static x = 5`) work — lowered to assignments run in declaration order in the class's static-init, ahead of any `static {}` block, with an unannotated one typed by inference ([ADR-00375](adr/ADR-00375.md)). An instance-field initializer lowered into the constructor can currently see the constructor's own parameters (which real JS's separate initializer scope forbids), and an unannotated initializer of an expression shape the compiler doesn't recognize falls back to `i64` ([TDD-00063](tdd/TDD-00063.md)/[ADR-00180](adr/ADR-00180.md))
- `class` (fields, constructor, methods, `this`, `new ClassName(args)`) — A computed class member name must be a literal or one of the well-known symbols `iterator`, `asyncIterator`, `toPrimitive`, `dispose` and `asyncDispose` (desugared to their protocol methods, [TDD-00089](tdd/TDD-00089.md), [ADR-00278](adr/ADR-00278.md)); any other (an identifier, a call, `Symbol.toStringTag`) is a clean rejection
- `class` (fields, constructor, methods, `this`, `new ClassName(args)`) — A class that reads a local of its enclosing function from a nested `function` is a clean rejection; an inherited static method called on a subclass reads `this` as the class declaring it; a class expression that reads none is hoisted to one top-level class, so evaluating it twice yields the same class (identity and statics shared, where Node creates a new class per evaluation); `extends <expression>` is out of scope
- `class` (fields, constructor, methods, `this`, `new ClassName(args)`) — Class early-error gaps (deferred, no valid program miscompiles): a field initializer containing `arguments` or a `super()` call, a `super()` call in a method parameter default, field-definition ASI on the same line (`field = 1 method(){}`), and the `#constructor` private-name ban all compile instead of raising the spec's `SyntaxError`
- `class` (fields, constructor, methods, `this`, `new ClassName(args)`) — A bare field (`x;`) defaults to `number` (the unannotated-parameter convention; JSDoc `@type` overrides) — TS infers implicit `any`, so a non-numeric use is a shifted typed error ([ADR-00474](adr/ADR-00474.md))
- `class` `static` members/`static {}` blocks, `private`/`protected` visibility, `abstract` classes/methods, `implements` — A value of another kind written through an interface-typed reference or `any` into an object's field (`o.x = "s"` for a `number` field) shadows the field: the object's own statically typed binding still reads the old value ([ADR-01215](adr/ADR-01215.md))
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

### String Methods — Strict 22/33 (~67%) · 10 caveats — [String Methods](status/STRING-METHODS.md)
- `.length` — Byte length, not the JS UTF-16 code-unit count — `'café'.length` is `5` (Node: `4`).
- `.slice(start?, end?)` — Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it (`'café'.slice(0, 4)` cuts mid-`é`), diverging from Node on non-ASCII text.
- `.substring(start, end?)` — Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it, unlike Node's code-unit indexing on non-ASCII text.
- `.substr(start, length?)` — Byte offsets, not UTF-16 indices — a bound inside a multi-byte character splits it, unlike Node's code-unit indexing on non-ASCII text.
- `.indexOf(substr, fromIndex?)` — Returns a byte offset, not a UTF-16 index — `'naïve'.indexOf('ve')` is `4` (Node: `3`).
- `.lastIndexOf(substr, fromIndex?)` — Returns the LAST occurrence's byte offset (binary-safe, descending memcmp scan), not a UTF-16 index — like `.indexOf` on non-ASCII text ([ADR-00843](adr/ADR-00843.md))
- `.includes(substr, position?)` — Binary-safe but byte-space — shares the byte-offset model of `indexOf`/`slice`, operating on bytes rather than UTF-16 code units (matters only on non-ASCII text).
- `.codePointAt(i)` — Positions are byte offsets in this compiler's UTF-8 strings: at the first byte of a character it reads that character's code point, at a continuation byte that byte; Node indexes UTF-16 code units, so an index past a non-ASCII character differs and a surrogate half is never returned ([ADR-01224](adr/ADR-01224.md), [ADR-00028](adr/ADR-00028.md))
- `.localeCompare(other)` — Byte-order comparison, not real Unicode collation — no locale/`Intl` infrastructure
- `String.fromCharCode(n)` — An unpaired surrogate is encoded on its own, so two calls' halves concatenated (`fromCharCode(0xD83D) + fromCharCode(0xDE00)`) do not join into one character as Node's UTF-16 strings do ([ADR-01224](adr/ADR-01224.md))

### RegExp — Strict 10/15 (~67%) · 5 caveats — [RegExp](status/REGEXP.md)
- Literal syntax: `/pattern/flags` — `x in /foo/` mis-lexes the `/` as division (the lexer's regex-vs-division disambiguation gap, since `in` isn't its own token in this lexer) — a small, deliberately-accepted gap
- `str.match(regexp)` — With a regex held in a variable (not a literal), the result has no `index`/`input`/`groups`; a non-global literal's result carries them, as `.exec()`'s does
- `str.replace(regexp, replacement)` (string or callback) — Replacement template supports `$1`-`$9`/`$&`/`$$` only (`` $` ``/`$'` — pre-/post-match text — are out of scope)
- `str.replace(regexp, replacement)` (string or callback) — The callback form is invoked with a fixed `(match, offset, string)` — real JS's variadic `...capturedGroups` in the middle isn't supported (a callback's arity is fixed at compile time but a pattern's capture count is only known at runtime; a callback declaring more than 3 parameters is a compile-time error)
- `str.replaceAll(regexp, replacement)` (string or callback) — Same replacement narrowing as `.replace()` (`$1`-`$9`/`$&`/`$$` only; fixed `(match, offset, string)` callback)

### FFI (node:ffi) — Strict 8/12 (~67%) · 4 caveats — [FFI (node:ffi)](status/FFI.md)
- `new DynamicLibrary(path)` / `lib.close()` / `ffi.dlclose(lib)` — `console.log(lib)` shows `DynamicLibrary {}` where Node shows its own accessors (`{ path: [Getter], symbols: [Getter] }`): a class instance here cannot carry own accessor properties
- `new DynamicLibrary(path)` / `lib.close()` / `ffi.dlclose(lib)` — `using` declarations are not parsed yet; `lib[Symbol.dispose]()` and `close()` dispose
- `library.functions` / `library.symbols` / no-arg `getFunctions()` / `getSymbols()` — Windows: the order is an MSVC-STL port (Node's Windows build uses the MSVC STL, this toolchain mingw) not yet checked against a Windows `node:ffi`
- `library.registerCallback([sig,] cb)` / `unregisterCallback(ptr)` / `refCallback` / `unrefCallback` — `refCallback`/`unrefCallback` are validated no-ops (a registered callback is always strongly referenced here), and `library.close()` does not invalidate callbacks — unregister explicitly; an exception a callback throws is reported as uncaught once the foreign call returns

### console — Strict 8/12 (~67%) · 4 caveats — [console](status/CONSOLE.md)
- `console.log(...)` — A bare string argument with an embedded null byte truncates *on display* at the first `\0` (`printf` `"%s"`); the stored string is intact, and inside a container it renders as `'a\x00b'`. A length-driven write needs every runtime string to carry its length header first — the URL/crypto sidecars do not yet ([ADR-01075](adr/ADR-01075.md)). Use `process.stdout.write` for binary output
- `console.trace(...)` — Prints `"Trace: <message>"` and nothing else; real Node's entire point of `.trace()` is the call stack it prints below the message, which this never generates at all
- `console.table()` — An array of objects (columns = the shared fields) or an array of primitives (a single `Values` column) is tabulated; a `columns` filter argument, a plain-object argument, and an array-of-arrays shape aren't tabulated (they fall back to `console.log`, as a non-tabular value does)
- `console.dir(obj, { depth?, colors? })` — The `colors` option is accepted but ignored — inspected output carries no ANSI here

### path — Strict 7/10 (70%) · 3 caveats — [path](status/PATH.md)
- `path.join(...segments)` — A non-string argument held in `any` is converted to a string at the call instead of throwing Node's `ERR_INVALID_ARG_TYPE` (the parameters are typed `string`)
- `path.resolve(...segments)` — `path.win32.resolve()` on a non-Windows host resolves against a POSIX `cwd` (`/home/me` → `\home\me\foo`), exactly as Node does there; only meaningful on Windows.
- `path.posix` / `path.win32` — `matchesGlob` is missing from both flavours (it needs Node's glob matcher).

### JSON — Strict 11/15 (~73%) · 4 caveats — [JSON](status/JSON.md)
- `JSON.stringify(mixedTypeArray)` — Strict lane: a literal mixing two object types, or an untyped `[]` grown with values of different types, is rejected (annotate it, or `-compat=js`)
- `JSON.parse(s)` → top-level `T[]` (incl. object & nested arrays) — A bare reassignment into a **member or element** target (`obj.items = JSON.parse(...)`, `grid[i] = JSON.parse(...)`) isn't projected from declaration context — write the target type on the call (`obj.items = JSON.parse(...) as Item[]`) to project anywhere ([ADR-00715](adr/ADR-00715.md))
- `JSON.parse(s)` validates input (throws `SyntaxError` on malformed JSON) — The `SyntaxError` message is position-based (`Unexpected token in JSON at position N`), not Node/V8's exact per-token wording
- `JSON.parse(s)` → `any`/`unknown` (dynamic shape) — The result is a dynamic tree — statically-typed operations on it (arithmetic on elements, passing into typed slots) hit the normal `any` limits until narrowed; `JSON.parse(s) as T` narrows at the source, routing through the typed projection instead ([ADR-00715](adr/ADR-00715.md))

### events (EventEmitter) — Strict 7/9 (~78%) · 3 caveats — [events (EventEmitter)](status/EVENT-EMITTER.md)
- `new EventEmitter<T>()` / extending it via `class X extends EventEmitter<T>` — An override's declared signature isn't checked for compatibility against the method it replaces (tsc's TS2416)
- `new EventEmitter<T>()` / extending it via `class X extends EventEmitter<T>` — The checker types every event as taking `...args: any[]`: `lib/node/events.ts` declares its methods that way, not through @types/node's conditional types (`Key`/`Args`/`Listener`), so a typed map's event names and argument types are not checked
- `.listenerCount(event)` / `.eventNames()` — `eventNames()` leaves out symbol event names (`Reflect.ownKeys` omits an object's symbol keys); `on`/`emit`/`listenerCount` with a symbol work

### Array Methods — Strict 29/37 (~78%) · 8 caveats — [Array Methods](status/ARRAY-METHODS.md)
- `new Array<T>(n?)` — A preallocated `new Array<T>(n)` fills real zero-valued slots, not holes — `new Array<number>(3)[0]` is `0` (Node: `undefined`), and `map`/`forEach` visit those slots instead of skipping holes.
- `.length` — Growing `a.length` fills zero-valued slots in a typed-element array, not holes (a boxed-element `any[]` reads `undefined`, as Node)
- `.indexOf(item, fromIndex?)` — Rejects a nested-array element (`number[][]`) — compares a bare register, no callback ([ADR-00152](adr/ADR-00152.md))
- `.lastIndexOf(item)` — Rejects a nested-array element (`number[][]`) — compares a bare register, like `.indexOf` ([ADR-00152](adr/ADR-00152.md)/[ADR-00843](adr/ADR-00843.md))
- `.includes(item)` — Rejects a nested-array element (`number[][]`) — compares a bare register, no callback ([ADR-00152](adr/ADR-00152.md))
- `.sort(fn?)` — Rejects a nested-array element (`number[][]`) — the custom comparator is a C-ABI `qsort()` trampoline with one fixed variant per element kind ([ADR-00152](adr/ADR-00152.md))
- `.flat(depth?)` — `depth` must be a compile-time constant integer or `Infinity` — this compiler's arrays have a fixed nesting depth at the type level, so the result's element type has to be known at compile time ([TDD-00029](tdd/TDD-00029.md)/[ADR-00107](adr/ADR-00107.md))
- `Array.from(iterable, mapFn?)` — `thisArg` (the third argument) is not supported

### Global Functions & Constants — Strict 17/21 (~81%) · 4 caveats — [Global Functions & Constants](status/GLOBAL-FUNCTIONS.md)
- `structuredClone(obj)` — Statically typed `EventEmitter`/`URL`/`URLSearchParams`/functions/class instances/`Promise` are rejected at compile time rather than silently aliased (`URL` matches Node, which throws `DataCloneError` for it); a `Map`/`Set` with an **array/Map/Set** key or value element type is also rejected (only scalar/string/object elements clone — [ADR-00574](adr/ADR-00574.md))
- `structuredClone(obj)` — A cloned `AggregateError`'s `.errors` degrades to empty
- `queueMicrotask(fn)` — Drained at the reachable checkpoints (end of the top-level script, each scheduler step); a program with neither timers nor async tasks drains once at exit
- `gc()` — A no-op under `-mm=manual` (the default) — nothing is ever collected; only meaningful under `-mm=gc`, where it forces a full Boehm collection

### Number / Math — Strict 30/36 (~83%) · 6 caveats — [Number / Math](status/NUMBER-MATH.md)
- `Math.log/log2/log10` — Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS)
- `Math.sin/cos/tan` — Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS)
- `Math.cbrt/expm1/log1p` — Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS)
- `Math.asin/acos/atan/atan2` — Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS)
- `Math.sinh/cosh/tanh` — Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS)
- `Math.exp` and `Math.asinh/acosh/atanh` — Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS)

### HTTP Server — Strict 14/16 (~88%) · 3 caveats — [HTTP Server](status/HTTP-SERVER.md)
- `http.createServer((req, res) => …).listen(port[, cb])` — real Node shape — On Windows the TCP handles are compile-checked, not yet run
- `const server = http.createServer(cb?)` handle — `.listen(port?, cb?)`/`.close(cb?)`/`.closeAllConnections()`/`.address()`/`.on('request'/'upgrade', cb)` — The `IncomingMessage`/`ServerResponse` options (custom classes) are not accepted
- `const server = http.createServer(cb?)` handle — `.listen(port?, cb?)`/`.close(cb?)`/`.closeAllConnections()`/`.address()`/`.on('request'/'upgrade', cb)` — `res.setHeaders` takes a `Map` only — a `Headers` argument is a union code generation cannot tell apart ([BACKLOG](../BACKLOG.md))

### SQLite (node:sqlite) — Strict 16/18 (~89%) · 2 caveats — [SQLite (node:sqlite)](status/SQLITE.md)
- `db.createSession()` / `db.applyChangeset()` / `Session` — Available only where the system `libsqlite3` is built with sessions (macOS's is); elsewhere `ERR_INVALID_STATE` names the omission — Node bundles its own SQLite
- `db.enableLoadExtension()` / `db.loadExtension()` — The system `libsqlite3` on macOS omits extension loading, so loading an extension there is `ERR_INVALID_STATE`; Node bundles its own SQLite

### os — Strict 13/14 (~93%) · 2 caveats — [os](status/OS.md)
- `os.getPriority()` / `os.setPriority()` — The error's `errno`/`syscall` are copies of `info`'s, not accessors over it
- `os.getPriority()` / `os.setPriority()` — Windows' priority-class mapping is compile-checked only

### JavaScript built-in objects (completeness index) — completeness index (not parity-counted) · 28 caveats — [JavaScript built-in objects (completeness index)](status/JAVASCRIPT-BUILTINS.md)
- `Function` instances — `name`, `length`, util.inspect form (`[Function: f]`, `[AsyncFunction: f]`, `bound f`, …) — `toString()` / `String(f)` has no source text (a compile error on a statically-typed function)
- `Function` instances — `name`, `length`, util.inspect form (`[Function: f]`, `[AsyncFunction: f]`, `bound f`, …) — A function unboxed from `any` into a typed slot is a new function (`promisify(p) === p` is false)
- `Function` instances — `name`, `length`, util.inspect form (`[Function: f]`, `[AsyncFunction: f]`, `bound f`, …) — A class value has no `[class X]` rendering
- `Error` + subtypes (`TypeError`/`RangeError`/`SyntaxError`/`EvalError`/`URIError`/`ReferenceError`/`AggregateError`/`DOMException`), `class X extends Error` — The error-options second argument must be a `{ cause: <expr> }` object **literal** — a variable/computed options bag is a clean rejection ([ADR-01007](adr/ADR-01007.md)); `AggregateError` takes no options (its `.cause` reads `undefined`)
- `Error` + subtypes (`TypeError`/`RangeError`/`SyntaxError`/`EvalError`/`URIError`/`ReferenceError`/`AggregateError`/`DOMException`), `class X extends Error` — `.stack` is typed a number, not a string (`typeof err.stack` is `'number'`, Node: `'string'`)
- `FinalizationRegistry` — `cleanupSome` (non-standard) rejected; aggregate held types (arrays, nullable scalars) rejected
- `FinalizationRegistry` — Same-thread V1: a worker's registrations are flushed only by its own thread, not the process exit hook
- `FinalizationRegistry` — Under `-mm=gc`, firing depends on the target actually being collected (conservative scanning can pin a stack-reachable pointer — same posture as the `WeakRef` tests)
- `String` — `.normalize()` **missing** (no Unicode tables); `.at()` OOB returns `""` not `undefined`; `.codePointAt()` == `.charCodeAt()` (byte strings, correct only ASCII/Latin-1); `.matchAll()` eager not lazy; `.localeCompare()` is byte-order → [String methods](STRING-METHODS.md)
- `Array` — length-mutating methods propagate to caller only for plain **variable** params (not object-field/array-element receivers); `.flat(depth)` needs a constant depth → [Array methods](ARRAY-METHODS.md)
- `Object` / dynamic model — prototype machinery, descriptors, accessors exist on **`any`-typed / js-mode dynamic objects only**; static structs are fixed-shape (no dynamic add/delete, no prototype); `Object.assign` between static objects can't graft new fields; `hasOwn` needs string-literal keys → [Object, Map & Set](OBJECT-COLLECTIONS.md)
- `Reflect` — missing `construct`; the object methods require a dynamic target → [Object, Map & Set](OBJECT-COLLECTIONS.md)
- `Proxy` — only `get`/`set`/`has`/`deleteProperty` traps; dynamic target only → [Object, Map & Set](OBJECT-COLLECTIONS.md)
- `Number` — `toString(radix)` non-power-of-two fractional trailing-digit divergence; `toPrecision` fixed/exp threshold differs → [Number & Math](NUMBER-MATH.md)
- `Function` `.call`/`.apply`/`.bind` — `thisArg` binds only a `this: T` parameter (no method-borrowing of a class method); not on a Node module's functions → [Language constructs](LANGUAGE-CONSTRUCTS.md)
- `Symbol` — Of the well-known symbols the runtime honors `Symbol.iterator`, `Symbol.asyncIterator` and `Symbol.toPrimitive`; `toStringTag`, `hasInstance`, `species` and the rest are values only → [Type system](TYPE-SYSTEM.md)
- `RegExp` — `u`/`d` flags **missing** (accepted, not implemented) → [RegExp](REGEXP.md)
- `JSON` — a strict-lane literal mixing two object types is rejected → [JSON](JSON.md)
- TypedArrays / `ArrayBuffer` — no `.buffer` back-ref; `resize`/`grow` need `{maxByteLength}`; views don't length-track resize → [Binary data & typed arrays](BINARY-DATA-TYPED-ARRAYS.md)
- `TextDecoder` — UTF-8 only; non-UTF-8 labels throw `RangeError` at construction → [Encoding & text](ENCODING-TEXT.md)
- `URLSearchParams` / `URLPattern` — `URLPattern`'s regexp groups follow PCRE2 where ECMAScript differs → [URL](URL.md)
- `EventTarget` / `Event` / `AbortSignal` — `AbortSignal` isn't wired into `setTimeout` → [Events & cancellation](EVENTS-CANCELLATION.md)
- `crypto.subtle` — literal-only algorithm dispatch; ops throw synchronously rather than rejecting; `CryptoKey.algorithm`/`.usages` unimplemented → [Web Crypto](WEB-CRYPTO.md)
- `Date` — Setters need a named-variable receiver; no locale/`Intl` formatting; `toString`'s zone name covers the common zones only → [Performance timing](PERFORMANCE-TIMING.md)
- `performance` — `eventLoopUtilization`/`nodeTiming` missing → [Performance timing](PERFORMANCE-TIMING.md)
- `eval` — general/dynamic eval **missing**; only a compile-time-constant `eval("<expression>")` static subset works → [Global functions](GLOBAL-FUNCTIONS.md)
- `Iterator` / `AsyncIterator` helpers (`Iterator.prototype.map`/`filter`/`take`/`drop`/…) — `Iterator.from` and the global `Iterator` constructor (`instanceof Iterator`, `extends Iterator`) are missing
- `Reflect.construct` — a `newTarget` other than the target throws a TypeError rather than constructing with that prototype → [Object, Map & Set](OBJECT-COLLECTIONS.md)

### TypeScript language features (completeness index) — completeness index (not parity-counted) · 16 caveats — [TypeScript language features (completeness index)](status/TYPESCRIPT-FEATURES.md)
- `any` / `unknown` — `Object.assign` through `any` and `Object.defineProperties` copy and define string keys only, not symbol keys → [Type system](TYPE-SYSTEM.md)
- Union types — Scalars, objects (classes, plain object types, one headerless host object such as `URL`) and discriminated unions; no number-literal or non-first-position tags; narrowing is local (`typeof`/truthiness/`==null`/`instanceof`/`in`/a tag) — no `switch(typeof)` or `as`-narrowing → [Type system](TYPE-SYSTEM.md)
- Intersection types — Object-type members only; conflicting non-object fields rejected (TS `never`-field not modeled) → [Type system](TYPE-SYSTEM.md)
- Tuple types — No rest/optional elements; constant index only; no array methods; not nestable in `any`/union → [Type system](TYPE-SYSTEM.md)
- Mapped & utility types — Effective ones are `Pick`/`Omit`/`Record`; `Partial`/`Required`/`Readonly` are erased structural no-ops. No key remapping (`as`), no `-?`/`-readonly` modifier removal; compile-time-only → [Type system](TYPE-SYSTEM.md)
- Conditional types + `infer` — `infer` limited to `Array`/`Promise<infer>` and bare `infer R`; no `(...) => infer R`; assignability is structural width, not full variance → [Type system](TYPE-SYSTEM.md)
- Generics — Monomorphization-based; **no call-site type args for functions** (generic *classes* take `new Box<T>()`); each type param needs an inferable `T`/`T[]` param; generic functions aren't first-class values → [Type system](TYPE-SYSTEM.md)
- Type assertions — `as T` / `as const` / `satisfies` / `<T>x` — **Erased, not enforcing** — the value keeps its inferred type; a cast relied on for re-typing does not take effect (matches TS runtime erasure, not static narrowing) → [Type system](TYPE-SYSTEM.md)
- Template-literal types & string-literal types — Parsed but **erased/widened to `string`**, not narrowed or enforced → [Type system](TYPE-SYSTEM.md)
- `readonly T[]` modifier — Erased, not enforced; `ReadonlyArray<T>` alias not covered → [Type system](TYPE-SYSTEM.md)
- Ambient declarations (`declare var`/`function`/`enum`/`class`/`module`/`namespace`/`global`) — `declare var`/`function`/`enum` are real bindings; brace-bodied ambient forms parsed and erased (no external link target under whole-program AOT) → [Type system](TYPE-SYSTEM.md)
- Namespaces — Top-level only; no `declare namespace`; members desugar to bare-name top-level decls (cross-namespace same-name class collides) → [Language constructs](LANGUAGE-CONSTRUCTS.md)
- Function overloads — Signatures parsed and **erased**; call sites check the implementation only (no per-signature narrowing) → [Language constructs](LANGUAGE-CONSTRUCTS.md)
- `Function.prototype.call`/`apply`/`bind` — `thisArg` binds only a `this: T` parameter (no method-borrowing of a class method) → [Language constructs](LANGUAGE-CONSTRUCTS.md)
- Decorators — Class-decorator **replacement** is a documented static-model divergence (refused at runtime), and standard static-field decorators are rejected → [Language constructs](LANGUAGE-CONSTRUCTS.md)
- Symbols — V1 opaque unique values (`Symbol()`, `===`, `typeof`, `.description`, `Symbol.for`/`keyFor`); no dynamic property keys; only `[Symbol.iterator]`/`[Symbol.asyncIterator]` recognized as computed keys → [Type system](TYPE-SYSTEM.md)

### Node.js built-in modules (completeness index) — completeness index (not parity-counted) · 4 caveats — [Node.js built-in modules (completeness index)](status/NODE-MODULES.md)
- `buffer` (`Buffer`) — `File` and `resolveObjectURL` are not exported (no `File` global, no object-URL registry)
- `timers` — The module's timer functions wrap the globals rather than being them: `timers.setTimeout === setTimeout` is `false` (Node: `true`)
- `url` — Lenient relative parsing deferred
- `perf_hooks` — Histograms keep exact samples (not HDR); `eventLoopUtilization`/`nodeTiming` missing
