# Conformance results

Generated conformance reports, **one folder per platform, one subfolder per
`-compat` flag** (`<platform>/<flag>/`), so each (platform, lane) pair's
history is independently diffable: a regression in one lane on one platform is
a clean per-file git delta, running conformance on one machine never clobbers
another platform's numbers, and an error that surfaces on only one platform or
in only one lane is obvious. The platform name (`macos-arm64`, `linux-x64`,
`windows-x64`, …) is derived by the runner from the host — never passed by
hand. The layout extends to a new platform or flag by adding a folder, not a
column.

Per platform folder:

| Flag | Folder | Test262 | Node-core | TypeScript oracle | Web Platform |
|---|---|---|---|---|---|
| `-compat=strict` | `<platform>/strict/` | `CONFORMANCE-RESULTS.md` | `CONFORMANCE-RESULTS-NODE.md` | `CONFORMANCE-RESULTS-TS.md` | `CONFORMANCE-RESULTS-WPT.md` |
| `-compat=js` | `<platform>/js/` | `CONFORMANCE-RESULTS.md` | `CONFORMANCE-RESULTS-NODE.md` | `CONFORMANCE-RESULTS-TS.md` | `CONFORMANCE-RESULTS-WPT.md` |

plus `<platform>/conformance-summary.json` — the machine-readable projection
of that platform's headline numbers (the website publishes the primary dev
platform's copy via `website/scripts/gen-conformance.mjs`).

Platforms with committed results:

- [`macos-arm64/`](macos-arm64/) — the primary dev machine (Apple Silicon).

Every `*.md` under the platform folders is **generated** — regenerate with the
suite's `make` target (`make conformance`, `make conformance-node`,
`make conformance-ts`, `make conformance-wpt`), which run both lanes
(`-compat=both`) and write into the current host's platform folder. Do not
hand-edit; re-run instead.

Companion (hand-written, not generated):

- [`PROGRESS-DASHBOARD.md`](PROGRESS-DASHBOARD.md) — the live-progress
  emitter (`progress-<suite>.txt` lines in the scratch dir) and the
  `klainconf` TUI dashboard that watches a running conformance sweep.

- [`NODE-GAP-ANALYSIS.md`](NODE-GAP-ANALYSIS.md) — interpretation + ranked
  remaining-work list for the Node-core oracle.
- [`CONFORMANCE-COVERAGE.md`](CONFORMANCE-COVERAGE.md) — retired V1 scan tracker,
  kept for history.
