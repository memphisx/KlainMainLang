<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/desktop-webview.json; edit the JSON, then run `make status`. -->

# Desktop — `klain:webview`

Native desktop windows over the **system** browser engine (WKWebView on macOS,
WebKitGTK on Linux) via the vendored [webview/webview](https://github.com/webview/webview)
C++ library — the Tauri architecture with this compiler as the backend. A
compiled binary opens a window, renders an HTML/CSS/JS UI (any SPA that builds
to static assets), and its buttons call straight into typed native functions.

Design: [TDD-00142](../tdd/TDD-00142.md). Implementation:
[ADR-00436](../adr/ADR-00436.md) (Stage 0 — C++ build plumbing),
[ADR-00437](../adr/ADR-00437.md) (Stage 1 — the window),
[ADR-00438](../adr/ADR-00438.md) (Stage 2 — bind/IPC + served SPA),
[ADR-00439](../adr/ADR-00439.md) (Stage 3 — loop fusion + async bind),
[ADR-00440](../adr/ADR-00440.md) (Stage 4 — packaging),
[ADR-00441](../adr/ADR-00441.md)/[ADR-00442](../adr/ADR-00442.md) (Stage 5–6 —
typed bind, `bindings`, `--emit-window-dts`),
[ADR-00443](../adr/ADR-00443.md) (Stage 7 — SPA embedding).

`import { Webview } from 'klain:webview'`

**Coverage: 20/20 surface, 100%. Strict: 0/20** — the module-wide V1 caveats
below exclude every row from Strict.

## Surface

| Feature | Status | Notes |
|---|---|---|
| `new Webview({ title, width, height, debug })` | ✅ | Parse-time constructor; ptr-slot handle. All options optional |
| `.navigate(url)` | ✅ | `http(s)://…` and `data:` URIs |
| `.html(doc)` | ✅ | Inline document (small UIs / single-file SPA builds) |
| `.setTitle(s)` | ✅ | |
| `.setSize(w, h)` | ✅ | `WEBVIEW_HINT_NONE` |
| `.init(js)` | ✅ | Runs before every page load |
| `.eval(js)` | ✅ | Fire-and-forget; routed through `webview_dispatch` (thread-safe from a Worker) |
| `.bind(name, cb)` | ✅ | `window.name(...)` in the page → native `cb(reqJSON)`; returns a page-side Promise |
| `.bind` async (`cb` returns `Promise<string>`) | ✅ | Page promise settles when the native promise settles — driven on the GUI thread by the page-tick pump ([ADR-00439](../adr/ADR-00439.md)) |
| `.unbind(name)` | ✅ | |
| `.run()` | ✅ | Blocks — owns the GUI main loop; statements after it are the shutdown path |
| Timers/microtasks on the GUI thread | ✅ | `setTimeout`/`setInterval`/promise reactions run under `run()` without a Worker, via a page-driven tick pump ([ADR-00439](../adr/ADR-00439.md)) |
| `.terminate()` | ✅ | Ends `run()` (from a bound callback or another thread) |
| `.destroy()` | ✅ | |
| bind throw → page-promise reject | ✅ | Via the setjmp catch scaffold |
| `bindings: { … }` + `.bindTyped(name, fn)` (typed) | ✅ | Auto-decode the page's args into the callback's declared parameter types, auto-JSON-encode the return, incl. **async** (`Promise<T>`, settled value encoded) and nested-tuple params; the `bindings` keys are the exposed `window.*` allowlist ([ADR-00441](../adr/ADR-00441.md)/[ADR-00442](../adr/ADR-00442.md)) |
| `--emit-window-dts` | ✅ | Writes `<output>.window.d.ts` declaring `interface Window { … }` from the typed bindings — page-side autocomplete + the audit surface for the allowlist ([ADR-00442](../adr/ADR-00442.md)) |
| `new Webview({ serve: "./dist" })` | ✅ | Embeds a built SPA/SSG directory into the binary at compile time (`.incbin`) and serves it from an in-binary static server on an ephemeral loopback port, then navigates — a **single-file** desktop app, no external `dist/` ([ADR-00443](../adr/ADR-00443.md)) |
| `klain:assets` `embedDir(path)` + `.get(path)` | ✅ | Embed a directory at compile time; `.get(path): ArrayBuffer` reads an embedded file byte-exact (binary-safe) over the static blob, no copy ([ADR-00443](../adr/ADR-00443.md)) |
| `klainmain -package` → `.app` / `.desktop` | ✅ | Wraps the binary into a double-clickable macOS `.app` bundle (Info.plist + optional `.icns` icon) or a Linux `.desktop` launcher; `-app-name`/`-app-id`/`-app-version`/`-app-icon` ([ADR-00440](../adr/ADR-00440.md)) |

## Loading patterns

- **Inline** — `.html(...)` (see `examples/webview/inline.ts`: a counter kept in
  native state + a `fs.readdirSync` file browser).
- **Served SPA** — `http.createServer` serves `dist/` in a Worker thread; the
  main thread `.navigate`s to it (see `examples/webview/spa.ts` +
  `spa_server.ts`).
- **Embedded (single-file)** — `new Webview({ serve: "./dist" })` embeds the build
  into the binary and serves it from an in-binary server; no `dist/` at runtime
  (see `examples/webview/embedded.ts`).
- **External URL** — `.navigate("https://…")`.

## Caveats (what's left)

- **One window per process** (V1). A second `new Webview` is a clean
  compile-time rejection. Multi-window is deferred — the codegen guard is one
  line, but true multi-window needs reworking Cocoa run-loop / app-delegate
  ownership in the vendored engine.
- **Two bind contracts.** `w.bind(name, cb)` is the raw escape hatch — the page's
  argument list arrives as one JSON-array string and the callback returns a JSON
  string (sync, or `async` returning `Promise<string>` settled later,
  [ADR-00439](../adr/ADR-00439.md)). **Typed** bind (`bindings: {…}` on the
  constructor, or `w.bindTyped(name, fn)`) auto-decodes the args into the
  callback's declared parameter types and JSON-encodes the return
  ([ADR-00441](../adr/ADR-00441.md)/[ADR-00442](../adr/ADR-00442.md)); the
  `bindings` keys are the exposed `window.*` allowlist. **Async** typed bind (a
  callback returning `Promise<T>` settles the page promise with the JSON-encoded
  `T`) and nested-tuple params work; `--emit-window-dts` writes the page-side
  `Window` typing. A rejection carries a fixed message, not `error.message`.
- **Loop fusion is page-driven.** `setTimeout`/`setInterval`/microtasks/promise
  reactions run on the GUI thread under `run()` via a page-injected tick pump
  (16ms) — no Worker needed for timers or async `bind`. Servers and other
  fd-driven loops (stream pumps, fork IPC) still belong in a Worker; the pump
  drives this runtime's timer/microtask/task queues, not `select()`.
- **Single-file apps.** `new Webview({ serve: "./dist" })` embeds the built SPA
  into the binary and serves it from an in-binary server, so a packaged `.app`
  needs no external `dist/` (this closes the earlier packaging gap). The `serve`
  path is resolved at *compile* time (relative to the compiler's CWD).
- **Platform**: macOS needs zero extra deps (WebKit.framework ships with the
  OS); Linux needs the WebKitGTK dev packages at build time — see README
  Requirements. Windows is out of scope.
- **Verified on macOS (Apple Silicon M4)** — window creation, method surface,
  the page→native→terminate roundtrip, both example apps, the Stage 3 page-tick
  pump + async-bind roundtrips (gated windowed smoke tests), Stage 4 packaging
  (`-package` → a `.app` that `plutil` lints clean and `open` launches as a
  foreground GUI app; `.png`→`.icns` icon pipeline), Stage 5 typed bind (a
  page calling typed `window.add`/`mkPoint` round-trips scalar + object), Stage 6 async-typed + nested-tuple round-trips + `--emit-window-dts`, and Stage 7
  embedding (`embedDir`/`get` byte-exact incl. a binary asset; `Webview({ serve })`
  renders the embedded page with no `dist/` on disk). **Linux:
  compile-tier only** — the vendored binding compiles and links against
  `webkit2gtk-4.1` (Docker `ubuntu:24.04`, no display; CI links it too), but a
  windowed run on Linux is not yet exercised.
