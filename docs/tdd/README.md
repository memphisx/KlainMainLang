# Technical Design Documents (TDDs)

This folder tracks scoping/design work done *before* a feature is implemented — the problem, the design options considered, tradeoffs, and prerequisites. `STATUS.md` was growing a "Design Notes"/"Scoping" section directly inline for every not-yet-built feature, which made it harder to scan for actual implementation status; those sections now live here instead, with `STATUS.md` linking to them.

## Numbering

- Files are named `TDD-NNNNN.md`, zero-padded to 5 digits, starting at [TDD-00001](TDD-00001.md).
- Numbers are assigned sequentially and never reused, the same convention `docs/adr/` uses.
- Before creating a new one, check the Index below for the last number used.

## Cross-referencing

Every mention of another TDD or an ADR — in prose, in the `Status` line, in `STATUS.md`, anywhere — is a real markdown link to that file (`[TDD-00006](TDD-00006.md)` from within `docs/tdd/`, `[ADR-00048](../adr/ADR-00048.md)` from outside it), not a bare `TDD-00006` or a backtick-quoted path. See `docs/adr/README.md`'s own Cross-referencing section for the one exception (a document referring to itself).

## Relationship to ADRs

A TDD **does** carry a status field, unlike an ADR: `Not Started | In Progress | Partially Implemented | Implemented | Superseded`, kept current as work actually happens — this is the quick-reference layer for "what's actually done" that `STATUS.md`'s own summary and this folder's Index both draw from. That's the opposite of an ADR, which has no status field at all, because an ADR is only ever written *after* something is finished (see `docs/adr/README.md`) — there's nothing to track.

The **design content itself** (Context, Design, Prerequisites, Open questions) still isn't edited to match what actually shipped, even as the status field moves. Once a TDD's feature is actually implemented (fully or in part):

- Write an ADR documenting what was actually built — this is the existing standing rule for every feature/bugfix, unchanged.
- Cross-reference the TDD from the ADR's `Relations` field (`Implements TDD-NNNNN`), and update the TDD's own `Status` line to point back at the ADR.
- If the real implementation diverged from the original design, that divergence belongs in the ADR ("here's what was planned, here's what was actually built and why"), not retrofitted into the TDD's Design section — the TDD stays the honest historical record of the thinking at the time. Only the `Status` line (and, for a genuinely abandoned/replaced design, a note that it was superseded) is ever touched after the fact.

## Format

Copy [`TEMPLATE.md`](TEMPLATE.md) as a starting point. At minimum, a TDD should cover:

- **Context** — the problem or need, and why it's being scoped now.
- **Design** — the approach(es) considered, in enough detail that implementation could start directly from it. Lay out tradeoffs if multiple options were weighed; note a recommended direction if one exists, and why.
- **Prerequisites** — what's already built and reusable vs. what's still missing, so the work can be picked off incrementally rather than discovered mid-implementation.

## Index

| # | Title | Status |
|---|---|---|
| [00001](TDD-00001.md) | Memory Management: three mutually-exclusive compilation modes (`manual`/`gc`/`auto`) | Partially Implemented ([ADR-00030](../adr/ADR-00030.md), [ADR-00071](../adr/ADR-00071.md)) |
| [00002](TDD-00002.md) | Timers (setTimeout/setInterval) | Implemented ([ADR-00031](../adr/ADR-00031.md)) |
| [00003](TDD-00003.md) | Alternative fetch Backend: a Go helper instead of libcurl | Not Started |
| [00004](TDD-00004.md) | HTTP Server | Implemented ([ADR-00048](../adr/ADR-00048.md), [ADR-00049](../adr/ADR-00049.md), [ADR-00072](../adr/ADR-00072.md)) |
| [00005](TDD-00005.md) | Unannotated parameter typing | Partially Implemented ([ADR-00042](../adr/ADR-00042.md)) |
| [00006](TDD-00006.md) | Event Loop | Partially Implemented ([ADR-00048](../adr/ADR-00048.md), [ADR-00049](../adr/ADR-00049.md), [ADR-00050](../adr/ADR-00050.md)) |
| [00007](TDD-00007.md) | Coerce object literal fields against their declared type | Not Started |
| [00008](TDD-00008.md) | External conformance suites (TypeScript + Test262) as a test-coverage benchmark | Partially Implemented ([ADR-00047](../adr/ADR-00047.md)) |
| [00009](TDD-00009.md) | Classes / OOP (methods, constructors, inheritance) | Partially Implemented ([ADR-00062](../adr/ADR-00062.md), [ADR-00063](../adr/ADR-00063.md), [ADR-00064](../adr/ADR-00064.md), [ADR-00067](../adr/ADR-00067.md)) |
| [00010](TDD-00010.md) | Generics on user-defined functions and interfaces | Not Started |
| [00011](TDD-00011.md) | IndexedDB-Compatible Storage API (pluggable embedded/proxy backends) | Not Started |
| [00012](TDD-00012.md) | Computed property keys (`{ [expr]: value }`) | Implemented ([ADR-00066](../adr/ADR-00066.md)) |
| [00013](TDD-00013.md) | Error subtypes / tagged errors | Not Started |
| [00014](TDD-00014.md) | Full-pipeline (codegen-through-binary) fuzz testing | Implemented ([ADR-00070](../adr/ADR-00070.md)) |
| [00015](TDD-00015.md) | `JSON.parse` into nested object fields | Not Started |
