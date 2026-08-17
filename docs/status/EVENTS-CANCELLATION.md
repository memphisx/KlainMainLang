# Events & Cancellation

> Part of the [Implementation Status](README.md) index.

**Coverage**: 0% (0/5) — not started.

**Caveats**:

- `EventTarget`/`Event`/`CustomEvent` are not implemented — a prerequisite for many other APIs (a general `AbortController` among them).
- A *fetch-specific* cancellation token is lower effort than the general version: the multi-interface machinery in [ADR-00050](../adr/ADR-00050.md) already tracks each in-flight transfer, and `curl_multi_remove_handle` + `curl_easy_cleanup` can cancel one mid-transfer.

| API | Notes |
|---|---|
| `EventTarget` / `addEventListener` / `dispatchEvent` | Generic event bus; prerequisite for many APIs |
| `Event` / `CustomEvent` | Base event types |
| `AbortController` / `AbortSignal` | Cancellation token for fetch, streams, timers |
