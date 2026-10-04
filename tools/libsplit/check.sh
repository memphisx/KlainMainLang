#!/bin/sh
# check.sh — for each example: build it whole and with -lib-objects
# (TDD-00238 Stage 4), run both, compare; then count distinct texts per
# library module unit.
# Usage: tools/libsplit/check.sh <workdir> [example...]
set -u
W=$1; shift
mkdir -p "$W/units"
go build -o "$W/libsplit" ./tools/libsplit || exit 1
K=$(pwd)/klainmain
pass=0; fail=0
for f in "$@"; do
  n=$(echo "$f" | tr / _ | sed 's/\.ts$//')
  d="$W/$n"; mkdir -p "$d"
  "$K" -lib-objects=false -o "$d/whole" "$f" > "$d/build.log" 2>&1 || { rm -rf "$d"; continue; }
  if ! "$K" -lib-objects -o "$d/split" "$f" > "$d/split.log" 2>&1; then echo "SPLITFAIL $f"; fail=$((fail+1)); continue; fi
  dir=$(dirname "$f")
  (cd "$dir" && perl -e "alarm 30; exec @ARGV" "$d/whole" a b > "$d/whole.out" 2>&1; echo "rc=$?" >> "$d/whole.out")
  (cd "$dir" && perl -e "alarm 30; exec @ARGV" "$d/split" a b > "$d/split.out" 2>&1; echo "rc=$?" >> "$d/split.out")
  # Mask what legitimately differs between two runs or two binary paths.
  sed -i.bak -e 's/(node:[0-9]*)/(node:N)/' -e "s#$d/whole#BIN#g;s#$d/split#BIN#g" -e 's/[0-9][0-9.e-]*ms/Nms/g' -e 's/port [0-9]*/port N/g' "$d/whole.out" "$d/split.out"
  "$K" -emit-llvm "$f" > "$d/prog.ll" 2>/dev/null && "$W/libsplit" -o "$d/u" "$d/prog.ll" > /dev/null 2>&1 &&
    for u in "$d"/u/lib_*.ll; do
      [ -f "$u" ] && echo "$(basename "$u") $(shasum "$u" | cut -c1-16)" >> "$W/units/hashes"
    done
  if cmp -s "$d/whole.out" "$d/split.out"; then pass=$((pass+1)); rm -rf "$d"; else echo "DIFF $f"; fail=$((fail+1)); rm -rf "$d/u" "$d/prog.ll"; fi
done
echo "pass=$pass fail=$fail"
echo "module units: $(cut -d' ' -f1 "$W/units/hashes" | sort -u | wc -l) modules, $(sort -u "$W/units/hashes" | wc -l) distinct texts"
sort -u "$W/units/hashes" | cut -d' ' -f1 | uniq -c | awk '$1>1' | sort -rn | head -20
