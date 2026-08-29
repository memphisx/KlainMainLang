<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/memory-management.json; edit the JSON, then run `make status`. -->

# Memory Management

> Part of the [Implementation Status](README.md) index. Selected once per compiled binary via the `-mm=manual|gc|auto` CLI flag (default `manual`). Full design: [TDD-00001](../tdd/TDD-00001.md).

**Coverage**: 2/3 modes (`manual`, `gc`); `auto` is design-only.

**Strict Coverage**: 0/3 — both implemented modes carry a real, disclosed caveat (a non-empty Caveats cell), so neither counts.

Format: [Status page format](README.md#status-page-format).

## The three modes

| Mode | Status | Caveats | Notes |
|---|---|---|---|
| `manual` (default) | ✅ | • Never frees on its own — a program's footprint grows monotonically with runtime unless the programmer calls `Memory.free(x)` by hand<br>• Same footguns as C, including that a string *literal* is a compile-time global constant, not `malloc`'d, so freeing one crashes exactly like C's `free("literal")` | • Freed by the programmer via explicit `Memory.free(x)`, which resolves `x`'s underlying heap pointer (array data pointer, object struct pointer, closure header + environment, Map/Set backing buffers) and frees it — shallow only, never anything reachable *through* it<br>• [ADR-00030](../adr/ADR-00030.md) (Stage 1 of the staged plan in [TDD-00001](../tdd/TDD-00001.md)) |
| `gc` | ✅ | • Requires `bdw-gc`/`libgc` installed at build time (`brew install bdw-gc` / `apt-get install libgc-dev` / `apk add gc-dev`) — this compiler's only non-libc external dependency, and only for binaries compiled with `-mm=gc`<br>• Strictly safer for long-running processes than `manual`, but the default hasn't been flipped to it | • Boehm–Demers–Weiser collector (`libgc`): swaps the declared `@malloc`/`@realloc` for `@GC_malloc`/`@GC_realloc` and links `-lgc` — no per-feature codegen change beyond the allocation call sites, since the collector conservatively scans the native stack/registers/managed heap for live pointers<br>• `Memory.free(x)` stays legal (lowers to `GC_free`, an always-safe early-release optimization Boehm documents)<br>• See [ADR-00071](../adr/ADR-00071.md), [ADR-00080](../adr/ADR-00080.md), [ADR-00093](../adr/ADR-00093.md) |
| `auto` | ❌ | | • Not started. Compiler-inserted frees at compile-time-proven-safe points, no runtime collector — sequenced last because a wrong escape analysis is a use-after-free, not just a delayed free<br>• Staged: Stage 2 is a `/** @free */` annotation plus a conservative local escape check (insert `free()` at every block exit path, reject if the value might escape); Stage 3 is an `@owned` linear-value annotation with real last-use liveness analysis<br>• `Memory.free(x)` would be a compile error in this mode (the compiler's own inserted frees can't account for a manual free it didn't plan for)<br>• See [TDD-00001](../tdd/TDD-00001.md)'s Design section for the full staging rationale and why a Rust-style borrow checker was ruled out as disproportionate |

## Why this matters

Every heap allocation this compiler emits (arrays growing/`push`ing, object literals, closure environments, string concatenation/slicing/template literals, `Map`/`Set` backing tables, JSON/Date formatting scratch buffers, boxed `any`/`unknown` payloads, …) goes through a plain `malloc`/`realloc` call. In `manual` mode that's never freed except by an explicit `Memory.free(x)` — a complete non-issue for the short-lived CLI processes every example/test compiles to today, but a hard blocker for this project's other stated direction: long-running microservice-style processes fronting `http.listen`. `-mm=gc` is a one-flag opt-in fix for exactly that gap; see the [Roadmap](README.md#roadmap)'s Structural priorities for the current sequencing read.
