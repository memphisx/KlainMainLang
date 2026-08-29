# Node.js parity — honest gap analysis

Hand-written companion to the generated
[CONFORMANCE-RESULTS-NODE.md](CONFORMANCE-RESULTS-NODE.md) (TDD-00121 Track B).
That file is regenerated per run; this one interprets it: what the numbers
actually mean, what was previously mislabeled, and the ranked list of work that
would move them. Update this file whenever the oracle's failure histogram
shifts materially.

Last reconciled: 2026-08-28 (Mac), against the pinned `nodejs/node` checkout.

## The two numbers are different claims

- **`docs/status/` coverage (~95% of the Node.js API area)** measures: "of the
  features this compiler *chose to target*, how many work for their core typed
  use case, with caveats disclosed per row." It is a claim about the targeted
  subset, not about Node.
- **The Node oracle pass rate (37 of 2451 attempted files, ~1.5%)** measures:
  "how much of Node's *own* behavioral suite runs verbatim after a mechanical
  CommonJS→typed-ESM transform." It is a floor, and it is low.

Both are real. Quoting the first without the second overstates Node parity —
a reader who hears "~95% Node coverage" and then watches `node`'s own tests
fail will reasonably conclude the docs are inflated. Public-facing claims
should either quote both numbers or say "targeted subset" explicitly.

## Classification contract (ADR-00407 / ADR-00409)

- A require of a **real Node core module** the compiler doesn't implement is
  a **FAIL** (`MODULE_NOT_IMPLEMENTED`) — an in-scope gap. `http2`, `https`,
  `vm`, `async_hooks`, `domain`, the `node:test` runner: all in scope, all
  counted against the failure column until implemented.
- **SKIP** is reserved for Node-repo-internal harness surface only: unshimmed
  `../common/*` helpers, `internal/*`, dynamic/inline `require`,
  `.call`/`.apply` dynamic dispatch, inspector-gated files (which real Node
  also skips when the inspector is compiled out).
- A module `docs/status/` marks ✅ may never be skipped as unsupported — that
  mismatch is a runner bug. A skip reason naming a one-line helper is a shim
  waiting to be written. **A skip label must name the concrete blocker** (the
  unhandled-require reasons now quote the offending statement) — an
  aggregate label like "dynamic require" hid ~180 static-form files (fixtures, tmpdir, countdown, and the `common` probe
  values are already shimmed, including the destructured
  `const { mustCall } = require('../common')` form).

## Current totals

3478 files: **37 passed, 2414 failed, 1027 skipped**. Earlier runs reported
26–28 passed with ~2100 skipped — the shift is mostly reclassification
(shims + unimplemented-module FAILs; the old skip column hid ~1000 files
of in-scope work), plus the incremental passes from the idiom-closure
batches (ADR-00406–00435).

## The module-level gap map (from MODULE_NOT_IMPLEMENTED counts)

| Module | Files blocked | Notes |
|---|---|---|
| ~~`http2`~~ | — | All four TDD-00139 stages shipped (ADR-00414–00417): server (compat + core streams), client sessions, constants, settings helpers — over h2c. Remaining: request bodies, `createSecureServer` (TLS), multi-server files |
| `vm` | 77 | Not started — now the largest module gap |
| ~~`https`~~ | — | Closed (ADR-00410): `https.get`/`request` ride the libcurl client; `https.createServer` stays a clean rejection until the accept loop is TLS-wrapped |
| ~~`test` (`node:test` runner)~~ | — | Implemented (TDD-00140/ADR-00419): test/it/describe/suite, TestContext, hooks, TAP output, nonzero exit — 5 new verbatim passes incl. Node's own subtest/after-hook runner test |
| `async_hooks` | 52 | Not started |
| `domain` | 35 | Deprecated in Node; lowest priority of this table |
| ~~`diagnostics_channel`~~ | — | Implemented V1 (ADR-00420): channel/subscribe/publish with string messages; `tracingChannel` remains a clean rejection |
| `v8` | 18 | Not started (`stream/web` closed — ADR-00410) |
| smaller: `perf_hooks`, `repl`, `inspector`, `wasi`, `sqlite`, … | <15 each | See the generated failure table |

## Failure histogram → ranked work list (non-module buckets)

1. **Server/handle "a number has no method X" decay** (~110 remaining):
   the http side is largely closed (ADR-00412 — contextual typing through
   `mustCall` wrappers for arrows *and* function expressions, options-object
   `http.get`, post-loop reaction flush); what's left is dynamic-`this`
   `function()` handlers (`this.address()`), `net`/`tls`/`dgram` variants,
   and multi-server files blocked by the single-server V1. The raw fail list
   (`NODE_RAW_REASONS=1 NODE_FAILLIST=…`) names the exact file and line.
2. ~~`child_process.spawnSync`/`execFileSync`~~ closed (ADR-00411 — embedded C
   core; the residual bucket is their unsupported options-object argument,
   ~22 files).
3. **`new Worker(...)` with a computed path** (~50): compile-time-literal
   restriction is architectural (worker code is compiled in); files using
   fixtures paths could be unblocked by constant-folding `fixtures.path(...)`.
4. ~~`tls.createServer` options literals~~ / ~~`net.connect(port)` arity~~ /
   ~~dgram `.address()`~~ closed (ADR-00413).
5. ~~`expected {, got .`~~ closed — it was `class X extends
   events.EventEmitter`, the qualified-base twin of qualified `new`
   (ADR-00408).
6. **`worker_threads.MessageChannel`**, **`crypto.generateKeyPair`**
   (~15 each). ~~`stream` `PassThrough`/`pipeline`/`finished`/`duplexPair`
   named exports~~ closed (ADR-00422) — the affected files now surface
   their *next* blockers, per-reason across the stream corpus: method-
   shorthand stream callbacks (`new Readable({ read() {} })` — the option
   must currently be an arrow/function expression; ~17), `mustCall`'s
   0-2-simple-params ABI bound (~4 here, more corpus-wide), the `Duplex`
   *class* itself (`new Duplex`/`instanceof Duplex`; ~6),
   `.destroy()`/`.read()`/`.setEncoding()` methods, `encoding`/`final`
   options, and `_readableState`/`_writableState` internals (out of
   scope). Aggregate pass count unmoved (35) — expected: these files
   stack multiple blockers.

Recently closed (same session, closing batches): `new http.Agent(...)` as
an inert pool-config token (18→1 — ADR-00432); spawn options — `cwd`
wired through, `shell`/`stdio: 'pipe'` tolerated (28→0 — ADR-00433); the
Node `crypto` **module** — `generateKeyPair(Sync)` in PEM over the
existing keygen ABI, `randomBytes`, module-named re-exports, mustCall
arity 2→3 for `(err, a, b)` callbacks (ADR-00434 — the 16 remaining
generateKeyPair files now name their real blockers: `rsa-pss`/`dsa`/`dh`
types, `cipher`/`der` encodings); `const { subtle } = globalThis.crypto`
binds a compile-time subtle alias (ADR-00435).

Recently closed (earlier same-session batches — aggregate reached **37**):
cluster workers ride the fork IPC channel (`worker.send`/`.on('message'/
'online'/'exit')`, in-worker `process.send` — ADR-00427);
`assert.match`/`doesNotMatch` (~13) + `process.getuid` family +
unhandled-builtin-member diagnostics now name the module instead of
leaking the internal marker (ADR-00428); http client: **variable-bound
options objects** (previously a silent-garbage-URL *hang* — the worst
kind of wrongness) + the `agent` key (ADR-00429); the **ClientRequest
handle** — `http.request(...).end()`, bound `req.end()`, `abort`/
`destroy`, `on('response'/'error')` (~30 first-blockers — ADR-00430);
`worker_threads` re-exports `MessageChannel`/`MessagePort`/
`BroadcastChannel` (~13, one resolver line — ADR-00431). Remaining
ranked next: multi-server-per-program (V1 single-server, ~14 direct),
`crypto.generateKeyPair` (13), spawn options objects (28, mostly
`process.execPath` re-spawns), `new http.Agent` as a value (18).

Recently closed (same session, middle batches — aggregate 35→36, ~500
reason-changes per batch as stacked blockers surface): `process.<stdio>.isTTY`
+ zero-param `createServer(mustNotCall())` listeners (ADR-00424);
**`child_process.fork` self-fork with a real NODE_CHANNEL_FD IPC channel**
(TDD-00141/ADR-00425 — `fork(__filename)`/`fork(process.argv[1])`, string
messages, both `process.send`-detection and argv-branching idioms; forking a
*different* module remains a clean rejection, ~23 files); http server-handle
bindings promoted to module globals so top-level helper functions can
reference them (ADR-00426 — 'address' residue 60→54, remainder is dgram/net
`this.address()` in `function()` callbacks). `cluster.fork()` messaging can
now reuse the IPC channel (next).

Recently closed before that: the chained-binding idiom `const server =
http.createServer(cb).listen(0, readyCb)` (~61 'address' + ~34 'close'
member errors — listen() now returns the handle, stored before the ready
callback; ADR-00423) and function-less `mustCall()`/`mustCall(n)` (~19
incl. the bound-wrapper form; same ADR) — 278 files moved to new reasons,
aggregate still 35 (stacked blockers). The residue of the 'address'
cluster (~59) is different shapes: `this.address()` inside `function()`
callbacks (dgram/net `.bind(0, function() { this... })`), and a top-level
`function doRequest()` referencing the sibling server-handle binding
(the ADR-00342 promotion excludes these handles).

Recently closed before that: variable-bound `http.createServer` handle +
named import + contextual `(req, res)` typing (ADR-00406); qualified `new mod.Class(...)`
and `extends mod.Class` parsing (~168 parse failures, ADR-00408);
`https` client + `stream/web` (ADR-00410); the `spawnSync` family incl.
`{ cwd, encoding }` options (ADR-00411); the full Node test idiom —
wrapper contextual typing, options-object client, post-loop reaction flush
(ADR-00412); `str.toString()` identity; and a runner bug where embedded C
runtime files were never linked, mislabeling those files CLANG_ERROR
(fixed in ADR-00411).

## Remaining skip buckets, with verdicts

| Bucket | Files | Verdict |
|---|---|---|
| unhandled `require()` forms | ~157 | **The old "~336 dynamic requires" label was wrong** — most were static forms the transform didn't parse (`require('m').member`, side-effect bare requires, requires inside embedded worker-source *strings*), now handled (ADR-00418); each remaining skip names its exact form. What's left: computed specifiers (`require(fixtures.path(…))` — real dynamic loading), `util.debuglog(…)` bindings, `require(m)()` module-as-function, relative test-file requires |
| `common.skipIfInspectorDisabled` | ~117 | Genuine skip: no inspector exists; real Node skips these too when the inspector is compiled out |
| `internal/test/binding` + other `internal/*` | ~90 | Node-internal harness, out of scope |
| `common.invalidArgTypeHelper` | ~61 | Borderline: these assert Node's exact dynamic-misuse error strings, which a typed compiler rejects at compile time instead — tests of dynamic error behavior |
| `.call(`/`.apply(` | ~58 | Out of scope (dynamic dispatch) |
| `../common/child_process`, `arraystream`, other unshimmed helpers | ~60 | Shim candidates — each needs a per-helper look; `child_process` helper (~27) is next |
| `common.escapePOSIXShell`, `skipIfEslintMissing`, other env helpers | ~55 | Mixed; eslint files are lint-infra, POSIX-shell helper is shimmable |
