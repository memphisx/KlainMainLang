# Notifications & Misc (Low priority / browser-specific)

> Part of the [Implementation Status](README.md) index. Mostly browser-specific and unlikely to be useful in a native CLI context. Tracked here for completeness, not because any of it is planned.

**Coverage**: Not tracked (out of scope by design, not a gap).

| API | Notes |
|---|---|
| Notifications API | Browser-only desktop notifications; `node-notifier` equivalent not in scope |
| Push API | Requires Service Worker and browser push infrastructure |
| Service Worker API | Browser-only background script; N/A for native |
| Storage API (`localStorage` / `sessionStorage`) | Browser session concept; N/A for native |
| IndexedDB | Browser embedded database; out of scope as a *browser* API — but see [MEMORY-MANAGEMENT.md](MEMORY-MANAGEMENT.md)'s sibling idea of an IndexedDB-*shaped* native storage API, tracked separately in [TDD-00011](../tdd/TDD-00011.md) |
| Clipboard API | Requires desktop GUI; N/A for native |
| Geolocation API | Hardware sensor; N/A for native CLI |
| Canvas / WebGL / WebGPU | Graphics; N/A for native CLI |
