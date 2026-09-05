#!/usr/bin/env bash
#
# Memory-management benchmark runner (MVP, bash).
#
# Compiles every program in programs/ under each engine/mode, runs it a few
# times, and reports best wall time + peak resident memory. Compares this
# compiler's memory-management methods against each other and (when installed)
# against Node and PerryTS on identical workloads.
#
# SAFETY (this runner deliberately cannot run the machine out of memory):
#   * exactly ONE child process runs at a time (never parallel),
#   * every child is wrapped by a watchdog that KILLS it the instant its RSS
#     exceeds MEM_CAP_MB or its wall time exceeds TIMEOUT_S,
#   * a trap kills the running child if the script itself is interrupted.
# Defaults are conservative; lower them further on a shared machine, e.g.
#   MEM_CAP_MB=512 TIMEOUT_S=30 ./run.sh
#
# Programs live in per-source category folders: programs/klain/ (home-grown
# allocation-path stressors), programs/perry/ (fetched from PerryTS's own
# benchmark suite), ... — results are reported per category so numbers from
# different suites never mix in one table.
#
# Usage:
#   ./run.sh                       # all categories, all engines, best-of-3
#   ./run.sh list map              # only programs matching these name globs
#   ./run.sh klain/                # one whole category
#   ITER=5 ./run.sh                # more iterations (best run wins)
#   ENGINES="manual auto node" ./run.sh
#   BENCH_SCALE=2 ./run.sh         # scale every workload up (watch the cap!)
#
# Engines: manual gc auto auto-opt node bun perry.
#   * gc / perry are skipped automatically if their toolchain isn't installed.
#   * node/bun run the .ts directly (Node >= 22 strips types natively).
#   * Two-variant convention: for the `manual` engine, if programs/<name>_manual.ts
#     exists it is used instead of <name>.ts — the hand-managed (Memory.free /
#     @free / @owned) version, so manual is measured as it is meant to be written,
#     not leaking the naive program.

set -uo pipefail
cd "$(dirname "$0")"

ROOT=".."
BIN="$ROOT/klainmain"
BUILD="build"
mkdir -p "$BUILD" results

# Every run's full output is also appended to a dated file under results/
# (gitignored), so results survive the terminal scrollback.
exec > >(tee -a "results/$(date +%Y-%m-%d).txt") 2>&1
echo "--- run: $(date '+%Y-%m-%d %H:%M:%S') argv: $* ---"

ITER="${ITER:-3}"
ENGINES="${ENGINES:-manual gc auto auto-opt node bun perry}"
MEM_CAP_MB="${MEM_CAP_MB:-512}"      # hard per-process RSS ceiling (healthy runs peak ~150MB)
TIMEOUT_S="${TIMEOUT_S:-60}"         # hard per-process wall-clock ceiling
export BENCH_SCALE="${BENCH_SCALE:-1}"

CAP_KB=$(( MEM_CAP_MB * 1024 ))
POLL=0.05                            # watchdog sample interval (s) — small so RSS can't overshoot far
# Kernel-enforced address-space backstop for klain binaries (Linux; a no-op on
# macOS, where the RSS watchdog is the guard). Generous so a healthy run never
# trips it, but a runaway is killed by the kernel long before it can wedge the box.
VSZ_BACKSTOP_MB=$(( MEM_CAP_MB * 4 > 4096 ? MEM_CAP_MB * 4 : 4096 ))
VSZ_BACKSTOP_KB=$(( VSZ_BACKSTOP_MB * 1024 ))

if [[ ! -x "$BIN" ]]; then
  echo "error: $BIN not found — run 'make build' at the repo root first." >&2
  exit 1
fi

# --- watchdog ---------------------------------------------------------------
# Kill the running child if the script is interrupted, so nothing is orphaned.
CURRENT_PID=""
cleanup() { [[ -n "$CURRENT_PID" ]] && kill -9 "$CURRENT_PID" 2>/dev/null; }
trap 'cleanup; exit 130' INT TERM
trap cleanup EXIT

hires() { perl -MTime::HiRes=time -e 'printf "%.4f", time' 2>/dev/null || date +%s; }

# bounded <outfile> <cmd...> : run cmd with stdout to outfile, guarded by the
# global MEM_CAP_MB / TIMEOUT_S. Sets BOUNDED_STATUS (0 ok, 201 mem-kill,
# 202 timeout, else child exit), BOUNDED_PEAK_KB, BOUNDED_WALL.
bounded() {
  local out="$1"; shift
  BOUNDED_PEAK_KB=0; BOUNDED_STATUS=0; BOUNDED_WALL=0
  local start; start=$(hires)
  "$@" >"$out" 2>/dev/null &
  CURRENT_PID=$!
  local ticks=0 rss
  while kill -0 "$CURRENT_PID" 2>/dev/null; do
    rss=$(ps -o rss= -p "$CURRENT_PID" 2>/dev/null | tr -d ' ')
    if [[ -n "$rss" ]]; then
      (( rss > BOUNDED_PEAK_KB )) && BOUNDED_PEAK_KB=$rss
      if (( rss > CAP_KB )); then
        kill -9 "$CURRENT_PID" 2>/dev/null; wait "$CURRENT_PID" 2>/dev/null
        BOUNDED_STATUS=201; CURRENT_PID=""; return
      fi
    fi
    ticks=$(( ticks + 1 ))
    if (( ticks > TIMEOUT_S * 20 )); then     # POLL=0.05s → 20 ticks/s
      kill -9 "$CURRENT_PID" 2>/dev/null; wait "$CURRENT_PID" 2>/dev/null
      BOUNDED_STATUS=202; CURRENT_PID=""; return
    fi
    sleep "$POLL"
  done
  wait "$CURRENT_PID"; BOUNDED_STATUS=$?
  CURRENT_PID=""
  BOUNDED_WALL=$(awk "BEGIN{printf \"%.3f\", $(hires) - $start}")
}

# --- engine helpers ---------------------------------------------------------
klain_flags() {
  case "$1" in
    manual)   echo "-mm=manual" ;;
    gc)       echo "-mm=gc" ;;
    auto)     echo "-mm=auto" ;;
    auto-opt) echo "-mm=auto -optimize-memory" ;;
  esac
}

# Source file for (engine, category, name): the manual engine prefers a
# hand-managed <name>_manual.ts variant when present.
source_for() {
  local engine="$1" cat="$2" name="$3"
  if [[ "$engine" == "manual" && -f "programs/${cat}/${name}_manual.ts" ]]; then
    echo "programs/${cat}/${name}_manual.ts"
  else
    echo "programs/${cat}/${name}.ts"
  fi
}

# Detect optional toolchains once.
gc_ok=1
echo 'console.log("probe");' > "$BUILD/_probe.ts"
"$BIN" -mm=gc -o "$BUILD/_probe" "$BUILD/_probe.ts" >/dev/null 2>&1 || gc_ok=0
rm -f "$BUILD"/_probe* 2>/dev/null
node_ok=0; command -v node >/dev/null 2>&1 && node_ok=1
bun_ok=0; command -v bun >/dev/null 2>&1 && bun_ok=1
perry_ok=0; command -v perry >/dev/null 2>&1 && perry_ok=1

# --- program selection ------------------------------------------------------
# A pattern matches against the category-qualified path (so "klain/" selects a
# whole category and "map" any program with map in its name, in any category).
progs=()
if [[ $# -gt 0 ]]; then
  for pat in "$@"; do
    for f in programs/*/*"${pat#*/}"*.ts; do
      [[ -e "$f" && "$f" != *_manual.ts ]] || continue
      [[ "$pat" == */* && "$f" != programs/"${pat%%/*}"/* ]] && continue
      progs+=("$f")
    done
  done
else
  for f in programs/*/*.ts; do
    [[ -e "$f" && "$f" != *_manual.ts ]] && progs+=("$f")
  done
fi
[[ ${#progs[@]} -eq 0 ]] && { echo "no matching programs" >&2; exit 1; }

printf 'benchmarks: %d program(s), %d iter, scale %s, engines: %s\n' \
  "${#progs[@]}" "$ITER" "$BENCH_SCALE" "$ENGINES"
printf 'safety: mem cap %d MB, timeout %d s, one process at a time\n\n' \
  "$MEM_CAP_MB" "$TIMEOUT_S"

lastcat=""
for src in "${progs[@]}"; do
  name="$(basename "$src" .ts)"
  cat="$(basename "$(dirname "$src")")"
  if [[ "$cat" != "$lastcat" ]]; then
    printf '########## category: %s ##########\n\n' "$cat"
    lastcat="$cat"
  fi
  echo "=== [$cat] $name ==="
  printf '  %-10s %10s %12s   %s\n' "engine" "best(s)" "peakRSS(MB)" "output"
  ref_out=""
  for engine in $ENGINES; do
    # availability gates
    case "$engine" in
      gc)    (( gc_ok ))    || { printf '  %-10s %10s %12s   %s\n' "$engine" "-" "-" "(skipped: libgc not installed)"; continue; } ;;
      node)  (( node_ok ))  || { printf '  %-10s %10s %12s   %s\n' "$engine" "-" "-" "(skipped: node not installed)"; continue; } ;;
      bun)   (( bun_ok ))   || { printf '  %-10s %10s %12s   %s\n' "$engine" "-" "-" "(skipped: bun not installed)"; continue; } ;;
      perry) (( perry_ok )) || { printf '  %-10s %10s %12s   %s\n' "$engine" "-" "-" "(skipped: perry not installed)"; continue; } ;;
    esac

    psrc="$(source_for "$engine" "$cat" "$name")"
    variant=""
    [[ "$psrc" == *_manual.ts ]] && variant=" [_manual]"

    # Build the run command per engine (compile step for klain, direct for node).
    runbin=""
    if [[ "$engine" == "node" ]]; then
      runcmd=(node "$psrc")
    elif [[ "$engine" == "bun" ]]; then
      runcmd=(bun run "$psrc")
    elif [[ "$engine" == "perry" ]]; then
      runbin="$BUILD/${cat}.${name}.perry"
      bounded /dev/null perry compile -o "$runbin" "$psrc"
      if (( BOUNDED_STATUS != 0 )) || [[ ! -x "$runbin" ]]; then
        printf '  %-10s %10s %12s   %s\n' "$engine" "-" "-" "(compile failed/killed: $BOUNDED_STATUS)"
        continue
      fi
      runcmd=(bash -c 'ulimit -v '"$VSZ_BACKSTOP_KB"' 2>/dev/null; exec "$1"' _ "$runbin")
    else
      runbin="$BUILD/${cat}.${name}.${engine}"
      # Compilation is low-memory but still guarded against a hang.
      bounded /dev/null "$BIN" $(klain_flags "$engine") -o "$runbin" "$psrc"
      if (( BOUNDED_STATUS != 0 )); then
        printf '  %-10s %10s %12s   %s\n' "$engine" "-" "-" "(compile failed/killed: $BOUNDED_STATUS)"
        continue
      fi
      # Kernel address-space backstop (enforced on Linux, no-op on macOS). exec keeps
      # the same pid so the RSS watchdog still tracks it.
      runcmd=(bash -c 'ulimit -v '"$VSZ_BACKSTOP_KB"' 2>/dev/null; exec "$1"' _ "$runbin")
    fi

    best_wall=""; peak_kb=0; out=""; failed=""
    for (( i = 0; i < ITER; i++ )); do
      of="$BUILD/.out"
      bounded "$of" "${runcmd[@]}"
      case "$BOUNDED_STATUS" in
        0)   : ;;
        201) failed="MEM-KILLED (>${MEM_CAP_MB}MB)"; break ;;
        202) failed="TIMEOUT (>${TIMEOUT_S}s)"; break ;;
        *)   failed="EXIT $BOUNDED_STATUS"; break ;;
      esac
      out="$(cat "$of" 2>/dev/null)"
      (( BOUNDED_PEAK_KB > peak_kb )) && peak_kb=$BOUNDED_PEAK_KB
      if [[ -z "$best_wall" ]] || awk "BEGIN{exit !($BOUNDED_WALL < $best_wall)}"; then best_wall="$BOUNDED_WALL"; fi
    done

    if [[ -n "$failed" ]]; then
      printf '  %-10s %10s %12s   %s\n' "$engine" "-" "-" "$failed$variant"
      continue
    fi
    # Correctness oracle: output must match across engines/modes.
    tag="$out$variant"
    if [[ -z "$ref_out" ]]; then ref_out="$out"; elif [[ "$out" != "$ref_out" ]]; then tag="!! MISMATCH: $out$variant"; fi
    rss_mb="$(awk "BEGIN{printf \"%.1f\", $peak_kb/1024}")"
    printf '  %-10s %10s %12s   %s\n' "$engine" "$best_wall" "$rss_mb" "$tag"
  done
  echo
done
