# Notifications & Misc (Low priority / browser-specific)

> Part of the [Implementation Status](README.md) index. Most of this is genuinely browser-specific and N/A in a native CLI context. A few entries, though, are browser API *shapes* with a real native reinterpretation — the same idea as the IndexedDB-shaped native storage API in [TDD-00011](../tdd/TDD-00011.md): keep the familiar browser surface, back it with an OS-native implementation. Those are marked **Differentiator (deferred)** below — deliberately not a V1 target and not yet scoped in a TDD, but not ignored either.

**Coverage**: Not tracked — these are browser APIs, out of scope *as such*, not a gap, so this page carries no Coverage/Strict fraction. Every row is ❌ (nothing implemented natively; the **Differentiator (deferred)** rows note where a native reinterpretation would be genuinely useful, none a committed target yet). Status is still shown per the shared [Status page format](README.md#status-page-format); the Caveats column is omitted because nothing here is implemented for a caveat to describe.

| API | Status | Notes |
|---|---|---|
| Notifications API | ❌ | **Differentiator (deferred).** The `Notification` shape maps cleanly onto real OS notifications — macOS Notification Center, Linux `libnotify`/`notify-send` — a genuinely useful CLI capability (`node-notifier`-style). Browser desktop-notification semantics (permission prompts, service-worker delivery) are the part that stays out of scope. Not scoped in a TDD yet. |
| Storage API (`localStorage` / `sessionStorage`) | ❌ | **Differentiator (deferred).** `localStorage`'s synchronous `getItem`/`setItem`/`removeItem` shape backs naturally onto a file-persisted native key/value store (and `sessionStorage` onto an in-process one) — a lighter-weight sibling of the IndexedDB-shaped idea in [TDD-00011](../tdd/TDD-00011.md), handy for CLI config/state persistence. Not scoped in a TDD yet. |
| Clipboard API | ❌ | **Differentiator (deferred).** `navigator.clipboard` read/write maps onto the OS clipboard (macOS `pbcopy`/`pbpaste`, Linux `xclip`/`wl-copy`) — useful for CLI tooling. The async-permission model is the browser-specific part left behind. Not scoped in a TDD yet. |
| Push API | ❌ | Requires a Service Worker plus browser push infrastructure — no meaningful native reinterpretation. Out of scope. |
| Service Worker API | ❌ | Browser-only background-script/lifecycle model; N/A for native. |
| Geolocation API | ❌ | Hardware sensor tied to a browser permission model; N/A for native CLI. |
| Canvas / WebGL / WebGPU | ❌ | Graphics; N/A for native CLI. Direct framebuffer/hardware output is a separate track — see [TDD-00033](../tdd/TDD-00033.md). |
