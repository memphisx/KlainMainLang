# Live conformance progress + the klainconf dashboard

Hand-written companion (tooling design note, not a compiler TDD). A
full-denominator conformance run is a multi-hour, multi-suite affair whose
only native signal was a stderr line every N files — invisible when the run
is backgrounded. This describes the live-progress mechanism and its viewer.

## Emitter (Go, `tools/conformance/progress.go`)

Each suite's result loop writes `.conformance-out/progress-<suite>.txt` every
25 files (and at lane start/end) — one line, replaced atomically
(tmp + rename) so a reader never sees a torn write:

```
suite|lane|done|total|pass|fail|skip|startedUnix|updatedUnix
```

A flat `|`-separated line, not JSON, on purpose: the viewer is written in
this compiler's own typed subset, where `JSON.parse` lands in `any` and
member-access-on-`any` is still D1 work — `split("|")` + `Number()` is fully
in-language today. The file lives in the gitignored scratch dir; it is
runtime telemetry, never a committed report. For the TS oracle, `pass` means
agreement with tsc and `fail` disagreement.

## Viewer (`apps/klainconf/`, a showcase TUI app in the language itself)

`klain:tui` + `klain:tty`, the klaintop pattern (`readKey(1000)` as the
refresh tick). One row per suite: lane, progress bar, done/total,
pass/fail/skip, files/sec and ETA derived from `done / (updated − started)`,
and a staleness marker when `updatedUnix` stops advancing (>15s: the run is
chewing a slow file or wedged — the "is it stuck?" question answered at a
glance). Non-TTY: prints one snapshot and exits. `q`/Ctrl-C quits.

The dashboard is read-only and decoupled from the run — start it before,
during, or after, in any terminal or tmux pane; several viewers can watch one
run. Typical use:

```
tmux split-window ./klainconf     # pane 2: watch
make conformance                  # pane 1: run
```

## Deliberate non-features

- No auto-launch from `make conformance*` — composition over magic; tmux
  does this better.
- Current-file name in the progress line (nice for spotting a pathological
  input) — deferred; costs a field and per-file write traffic.

