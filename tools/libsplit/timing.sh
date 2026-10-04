#!/bin/sh
# timing.sh — compile every example whole and with -lib-objects (warm
# object cache), report the total wall time of each.
# Usage: tools/libsplit/timing.sh <workdir> [example...]
set -u
W=$1; shift
mkdir -p "$W"
K=$(pwd)/klainmain
for f in "$@"; do "$K" -lib-objects -o "$W/p" "$f" > /dev/null 2>&1; done
now() { perl -MTime::HiRes=time -e 'printf "%.3f\n", time'; }
for f in "$@"; do
  a=$(now); "$K" -lib-objects=false -o "$W/p" "$f" > /dev/null 2>&1 || continue; b=$(now)
  "$K" -lib-objects -o "$W/p" "$f" > /dev/null 2>&1; c=$(now)
  echo "$a $b $c $f" | awk '{printf "%.3f %.3f %s\n", $2-$1, $3-$2, $4}'
done | tee "$W/times"
awk '{w+=$1; s+=$2} END {printf "total whole=%.1fs split=%.1fs (%d examples)\n", w, s, NR}' "$W/times"
