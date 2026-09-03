# Conformance results

Generated conformance reports, **one folder per `-compat` flag** so each flag's
history is independently diffable (a regression in one lane is a clean per-file
git delta, and an error that surfaces in only one lane is obvious). The layout
extends to a future flag by adding a folder, not a column.

| Flag | Folder | Test262 | Node-core | TypeScript oracle |
|---|---|---|---|---|
| `-compat=strict` | [`strict/`](strict/) | [Test262](strict/CONFORMANCE-RESULTS.md) | [Node](strict/CONFORMANCE-RESULTS-NODE.md) | [TS](strict/CONFORMANCE-RESULTS-TS.md) |
| `-compat=js` | [`js/`](js/) | [Test262](js/CONFORMANCE-RESULTS.md) | [Node](js/CONFORMANCE-RESULTS-NODE.md) | [TS](js/CONFORMANCE-RESULTS-TS.md) |

Every `*.md` under the flag folders is **generated** — regenerate with the
suite's `make` target (`make conformance`, `make conformance-node`,
`make conformance-ts`), which run both lanes (`-compat=both`) and write into
these folders. Do not hand-edit; re-run instead.

Companion (hand-written, not generated):

- [`NODE-GAP-ANALYSIS.md`](NODE-GAP-ANALYSIS.md) — interpretation + ranked
  remaining-work list for the Node-core oracle.
- [`CONFORMANCE-COVERAGE.md`](CONFORMANCE-COVERAGE.md) — retired V1 scan tracker,
  kept for history.
