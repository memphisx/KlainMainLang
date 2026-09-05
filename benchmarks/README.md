# Memory-management benchmarks

Stress programs and a runner for comparing this compiler's memory-management
**methods** (`-mm=manual` / `gc` / `auto`, and `-optimize-memory`) against each
other on identical workloads. Kept **separate from `examples/`** on purpose:
these are not correctness fixtures wired into `make examples`, they run longer,
and they carry a cross-engine axis: Node, Bun, and PerryTS run the same sources when installed.

The primary axis is **memory** (peak resident set, and — once the live tool
exists — allocation/consumption over time), with wall-clock time as a secondary
signal. Each program deliberately hammers a *different* allocator path, so a
method's behaviour can be read per-shape rather than as one blended number.

## Layout

```
benchmarks/
  run.sh            the runner (bash MVP)
  programs/         one folder per SOURCE, one .ts per workload
    klain/          home-grown allocator-shape stressors
    clbg/           ports from the Computer Language Benchmarks Game (nbody, binary_trees)
    perceus/        ports of Perceus/Koka benchmarks (rbtree)
    perry/          fetched verbatim from PerryTS's own benchmarks/suite (MIT), self-timing print disabled
  build/            compiled binaries + intermediates (gitignored)
  results/          every run's raw output auto-appends to results/YYYY-MM-DD.txt (gitignored)
```

Programs are grouped **by source** and the runner reports each category under
its own heading, so numbers from different suites never mix in one table.
Select one category with `./run.sh perry/`, or by name across all categories
with `./run.sh map`.

## Programs

| Program | Allocator path it stresses |
|---|---|
| `binary_trees` | many small, short-lived heap objects (tree nodes) built and discarded in waves — the classic GC stressor |
| `rbtree` | red-black-tree insertion — the canonical Perceus benchmark; every insert rebuilds the path nodes, the textbook reuse-in-place target |
| `list_churn` | functional array pipelines (`map`/`filter`/`reduce`) — a fresh backing buffer per stage, per iteration; the workload a Perceus reuse pass targets most directly |
| `map_churn` | hash-table (`Map`) backing storage built up and thrown away each round |
| `string_churn` | string concatenation — quadratic allocate-and-copy pressure |
| `closure_churn` | closure `{fn, env}` environments that escape into an array, then are discarded each round |
| `json_churn` | `JSON.parse`/`stringify` round-trips — dynamic-object tree + string allocators together, a common real-workload shape |
| `nbody` | (almost) no allocation — float-heavy integrator from the Computer Language Benchmarks Game; the fair raw-compute baseline where a GC has nothing to do |

Every program prints a checksum. The runner compares checksums across every
engine/mode: a mismatch is a correctness bug (same workload must yield the same
result), so the suite doubles as a differential oracle — across this compiler's
own modes *and* against Node. Each program scales its workload by the
`BENCH_SCALE` environment variable (default `1`), read at run time so it is both
identical across engines and opaque to the optimizer (the loops can't be
constant-folded away).

### Two variants: naive vs. hand-managed

`auto`/`gc`/`node` run the plain `<name>.ts`. The `manual` engine instead prefers
a `programs/<name>_manual.ts` variant when one exists — the same workload written
the way manual mode is *meant* to be: explicit `Memory.free`, or `@free`/`@owned`
annotations. So manual is measured as disciplined code rather than as the naive
program leaking. (These `_manual` variants are being added incrementally; where
one is absent, manual falls back to the naive program — the memory cap below
keeps that safe.)

## Running

```sh
make build                       # at the repo root, produces ./klainmain
cd benchmarks
./run.sh                         # all programs, all engines, best-of-3
./run.sh list map                # only programs matching these name globs
ITER=5 ./run.sh                  # more iterations (best run wins)
ENGINES="manual auto node" ./run.sh
BENCH_SCALE=2 ./run.sh           # scale every workload up (mind the cap)
```

Engines: `manual gc auto auto-opt node bun perry`. `gc`, `bun`, and `perry` are
skipped automatically if their toolchain isn't installed; `node` and `bun` run
the `.ts` directly (Node ≥ 22 / Bun strip types natively — this box has the
latest via the `node`/`bun` on `PATH`; nvm-managed versions could be added as
extra engines later), and `perry` compiles via `perry compile` before running.

## Safety limits

This runner is built so it **cannot** run the machine out of memory:

- **One child process at a time** — never parallel.
- **Hard RSS ceiling** (`MEM_CAP_MB`, default **512**) — a watchdog samples the
  child's resident memory every 50 ms and kills it the instant it crosses the
  cap. Healthy runs peak around 150 MB, so 512 MB leaves ~3× headroom while still
  catching a runaway early.
- **Hard wall-clock ceiling** (`TIMEOUT_S`, default **60**) — kills a hung or
  infinite-looping child.
- **Kernel address-space backstop** for the klain binaries (`ulimit -v`, enforced
  on Linux, a no-op on macOS where the RSS watchdog is the guard).
- **Interrupt-safe** — a trap kills the running child if the script is stopped, so
  nothing is orphaned.

Lower the ceilings on a busy shared machine:

```sh
MEM_CAP_MB=256 TIMEOUT_S=30 ./run.sh
```

A killed run is reported as `MEM-KILLED` / `TIMEOUT` in its row, not silently.

## Results

Numbers are produced fresh by a run; none are baked into this file (the earlier
illustrative table was removed when the default workloads were resized down for
safety). The qualitative story the suite is built to show: `auto` reclaims
provably-local per-iteration buffers as well as `gc` does (`list_churn`,
`map_churn`); it stays near `manual` where a value escapes or is aliased across a
loop (`string_churn`, `closure_churn`) — the exact gap a reuse/reference-count
pass would close; `nbody` is flat across modes (no allocation). Re-run to see the
current figures on your machine.

The bash bootstrap only ever writes the raw per-run log (`results/<date>.txt`,
auto-appended). A curated `results/<date>.md` — cross-scale tables plus ranked
findings and fix links — is written **by hand** today, so it drifts from the raw
log the moment a later run or fix changes the numbers. Regenerating that summary
from a structured results model is a **Stage 1** job (below): the headless
presenter emits the versioned JSON artifact and derives the table from it, so the
prose findings stay hand-authored while the numbers are never stale. The bash
script is deliberately left as-is — it is the bootstrap, not the destination.

## Design: the self-hosting tri-mode tool

The `bash run.sh` above is a bootstrap. The intended tool is written **in
KlainMainLang itself** — dogfooding that exercises `child_process`, both UI
frameworks, and the very memory subsystem it measures. It lives and is designed
here, not in `docs/tdd`/`docs/adr` (those track *compiler* work; this is an
application built with the compiler).

Feasibility is already confirmed against the current compiler: `child_process`
`spawnSync`/`execFileSync` can drive `klainmain` to compile and run each
benchmark; `process.memoryUsage().rss` self-reports; async `spawn` keeps the
fiber live so a `setInterval` can sample a child's resource use over time; and a
single binary can carry both `klain:tui` and `klain:webview` and pick one at
runtime (verified: flag > TTY/`DISPLAY` autodetect > headless fallback).

### One engine, three presenters

- **Engine** (pure TS, no UI): compiles each program × mode via `spawn`, runs it
  under sampling, and produces one structured results model per run (wall time,
  peak RSS, the sample series, checksum, pass/fail). It emits the same data
  regardless of who is watching — so it is simultaneously the CI artifact and
  the feed for both UIs.
- **Headless presenter**: prints the table and writes the JSON. The CI mode, and
  the base the other two decorate.
- **TUI presenter** (`klain:tui`): live dashboard — per-benchmark bars and a
  memory-over-time sparkline drawn as samples arrive.
- **Webview presenter** (`klain:webview`): the rich version — native `bind`
  pushes each sample into the page for real charts. This is the intended
  landing-page gallery artifact.

Mode selection is the adaptive branch, display-gated so a headless/no-`DISPLAY`
run never tries to open a window: explicit flag first; else no TTY → headless;
else Linux `DISPLAY`/`WAYLAND_DISPLAY` (macOS: logged-in session) → GUI, else TUI.

### Design decisions to settle before building

1. **Live-sampling contract** — external `ps -o rss,%cpu -p <pid>` sampling vs.
   the child self-reporting via `memoryUsage()` on a tick. Recommendation:
   external `ps` sampling (works without instrumenting the benchmark, and lets
   cross-engine children be sampled the same way), with the peak cross-checked
   against `/usr/bin/time` as today.
2. **Results schema** — one shape shared by all three presenters *and* stable
   enough to be the CI artifact and the landing-page data feed. Version it.
3. **Cross-engine adapter** — a Node/PerryTS row should be just another "runner"
   behind the same engine interface (each compiles/runs differently; the
   measurement wrapper abstracts that). Design the runner interface for this up
   front even before the non-klain runners exist.
4. **Webview live feed** — native `bind` pushing samples into the page vs. the
   page polling, without stalling the GUI thread (servers/fd-loops under a
   webview still need a Worker — the sampler likely lives there).

### Staging

- **Stage 0 (done)**: the benchmark programs + `run.sh` bootstrap.
- **Stage 1**: the TS engine + headless presenter — replaces `run.sh` for CI,
  and is the spine both UIs sit on.
- **Stage 2**: the TUI presenter (live bars + sparkline).
- **Stage 3**: the webview presenter (the gallery artifact).
- **Stage 4**: cross-engine runners (Node, Bun, PerryTS) behind the runner
  interface (the `run.sh` bootstrap already runs all three; this stage is
  folding them into the self-hosted tool's runner abstraction).
- **Ongoing**: a leak/soak variant asserting bounded RSS after warmup, feeding
  the `-mm=auto` default-flip maturity gate (TDD-00174).
- **Deferred — the rest of the Perceus suite**: `perceus/` currently ports only
  `rbtree`, the flagship reuse-in-place benchmark. The canonical Perceus/Koka
  set is five — `rbtree`, `rbtree-ck` (shared subtrees), `deriv`, `nqueens`,
  `cfold` (`binarytrees` is already covered under `clbg/`). The remaining four
  are deliberately deferred to land **with the TDD-00175 Stage 3/4 work**
  (scoped reference counting + reuse-in-place): today they would only show the
  auto-mode class-graph leak, and `rbtree-ck`'s whole point (subtree sharing) is
  unmeasurable until scoped RC exists. Port them (deterministic checksum +
  `BENCH_SCALE`, matching the existing programs) as part of that deliverable.
  Sources: Perceus paper (xnning.github.io/papers/perceus.pdf) and Koka's
  `test/bench/koka/`.
