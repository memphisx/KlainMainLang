# Events & Cancellation

> Part of the [Implementation Status](README.md) index.

**Coverage**: 0/3 (0%) · **Strict Coverage**: 0/3 (0%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `EventTarget` / `addEventListener` / `dispatchEvent` | ❌ | | • Generic event bus; prerequisite for many APIs (a general `AbortController` among them) |
| `Event` / `CustomEvent` | ❌ | | • Base event types |
| `AbortController` / `AbortSignal` | ❌ | | • Cancellation token for fetch, streams, timers; the general version depends on `EventTarget`<br>• A *fetch-specific* cancellation token is lower effort than the general version: the multi-interface machinery in [ADR-00050](../adr/ADR-00050.md) already tracks each in-flight transfer, and `curl_multi_remove_handle` + `curl_easy_cleanup` can cancel one mid-transfer |
