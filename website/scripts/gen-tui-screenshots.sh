#!/usr/bin/env bash
# Regenerate the TUI guide screenshots in src/assets/tui/ from the real
# examples/tui/* programs. These are genuine captures: each example is compiled
# to a native binary, run inside a real terminal by VHS (charmbracelet/vhs),
# driven to a representative state with keystrokes, and screenshotted to PNG.
#
# Requirements (macOS): `brew install vhs`. Run from anywhere:
#   website/scripts/gen-tui-screenshots.sh
#
# The PNGs are committed so the site builds without VHS; rerun this only when an
# example's output changes.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
out="$repo/website/src/assets/tui"
# A SHORT work dir on purpose: the compiled binary's path is typed into the
# terminal, so a blank/early-exited capture shows that command line. A long
# mktemp path (…/var/folders/…/tmp.XXXX/) makes a blank frame ~25 KB — big
# enough to pass a size check; a short path keeps a blank tiny (~2 KB) so the
# retry loop reliably detects and re-shoots it.
work="/tmp/kmshot"
rm -rf "$work"; mkdir -p "$work"
trap 'rm -rf "$work" /tmp/klain-demo' EXIT

command -v vhs >/dev/null || { echo "vhs not found — brew install vhs" >&2; exit 1; }

echo "building compiler…"
( cd "$repo" && go build -o klainmain . )

for ex in todo klaintop files menu; do
  echo "compiling ${ex}…"
  ( cd "$repo" && ./klainmain -o "$work/$ex" "examples/tui/$ex.ts" >/dev/null 2>&1 )
done
mkdir -p "$out"

# Screenshot-only mocking: keep a personal machine name / home path out of the
# published images. The real examples are untouched — we only compile/serve
# lightly-edited copies for the capture.

# klaintop: swap the real hostname() for a generic name.
sed 's/hostname()/"klaindev-pro"/' "$repo/examples/tui/klaintop.ts" > "$work/klaintop.mock.ts"
( cd "$repo" && ./klainmain -o "$work/klaintop" "$work/klaintop.mock.ts" >/dev/null 2>&1 )

# files: browse a neutral demo project rather than the real repo under $HOME.
demo="/tmp/klain-demo/acme-cli"
mkdir -p "$demo/src"
cat > "$demo/README.md" <<'MD'
# acme-cli

A small command-line tool. Build with `make`, run with `./acme`.

## Layout
- src/     source
- README   this file
MD
cat > "$demo/src/main.ts" <<'TS'
import { parseArgs } from './args'

function main(argv: string[]): number {
  const opts = parseArgs(argv)
  if (opts.help) { printUsage(); return 0 }
  return run(opts)
}

main(process.argv.slice(2))
TS
printf 'db.host = localhost\ndb.port = 5432\nlog.level = info\n' > "$demo/config.ini"
printf 'v0.3.0 — initial public release\nv0.2.0 — internal beta\n' > "$demo/CHANGELOG.txt"

# Shared VHS preamble: a crisp, dark, high-DPI terminal with a mono font that
# carries box-drawing, block, and braille glyphs.
preamble() {
  cat <<EOF
Output "$work/$3.gif"
Set FontSize 20
Set Padding 24
Set Width $1
Set Height $2
EOF
}

# todo — the to-do list. Default frame already shows a checked item, the
# progress bar, and the help line; snap it straight away.
{
  preamble 720 430 todo
  cat <<EOF
Hide
Type "$work/todo" Enter
Sleep 1.2s
Show
Sleep 400ms
Screenshot "$out/todo.png"
Type "q" Sleep 300ms
EOF
} > "$work/todo.tape"

# menu — the fruit picker. Move down and pick a couple of fruits so the shot
# shows selection, checkmarks, the chosen counter, and the progress bar.
{
  preamble 680 430 menu
  cat <<EOF
Hide
Type "$work/menu" Enter
Sleep 750ms
Space
Down Down
Space
Down
Show
Sleep 250ms
Screenshot "$out/menu.png"
Ctrl+C Sleep 200ms
EOF
} > "$work/menu.tape"

# klaintop — the live monitor. Snap it early (~0.9s): VHS/ttyd injects a
# phantom Ctrl-C a second or two into a capture, and klaintop (correctly) quits
# on Ctrl-C, so a late screenshot catches an already-exited program. The first
# frame is fully painted well before then.
{
  preamble 720 560 klaintop
  cat <<EOF
Hide
Type "$work/klaintop" Enter
Sleep 900ms
Down
Down
Down
Show
Sleep 250ms
Screenshot "$out/klaintop.png"
Ctrl+C Sleep 200ms
EOF
} > "$work/klaintop.tape"

# files — the two-pane browser. Run it in a directory whose entry count fits
# the pane (the docs pages), then move onto a source file to fill the preview
# pane with real text.
{
  preamble 1080 620 files
  cat <<EOF
Hide
Type "cd /tmp/klain-demo/acme-cli" Enter
Sleep 250ms
Type "$work/files" Enter
Sleep 800ms
Down
Show
Sleep 200ms
Screenshot "$out/files.png"
Ctrl+C Sleep 200ms
EOF
} > "$work/files.tape"

# Capture, retrying on a blank frame. menu and klaintop quit on Ctrl-C, and
# VHS/ttyd injects a phantom Ctrl-C a beat into a recording — so an unlucky run
# catches an already-exited program (a tiny, near-black PNG). Retry until the
# PNG is a real frame.
# Per-example minimum PNG size below which the frame is treated as blank and
# retried. A blank menu still carries the (long) shell command line, so it lands
# around 25 KB — well above the tiny truly-black blanks but below a real, busy
# menu frame (~55 KB+); it needs a higher floor than the others.
for ex in todo menu klaintop files; do
  # A blank frame that still shows the (long) shell command lands ~25–32 KB, so
  # each example needs a floor below its real, busy frame but above its blank.
  case "$ex" in
    todo) min=10000 ;;      # real ~24–46 KB, blank ~2 KB
    menu) min=45000 ;;      # real ~55–69 KB, blank ~25 KB
    klaintop) min=60000 ;;  # real ~120 KB,   blank ~28 KB
    files) min=40000 ;;     # real ~55 KB+,   blank ~32 KB
    *) min=6000 ;;
  esac
  ok=0
  for attempt in 1 2 3 4 5 6 7 8; do
    ( cd "$work" && vhs "$work/$ex.tape" >/dev/null 2>&1 )
    sz=$(stat -f%z "$out/$ex.png" 2>/dev/null || echo 0)
    if [ "$sz" -gt "$min" ]; then echo "captured ${ex} (${sz}b)"; ok=1; break; fi
    echo "  ${ex}: blank frame (${sz}b < ${min}), retry ${attempt}…"
  done
  [ "$ok" = 1 ] || echo "WARNING: ${ex} never captured a non-blank frame"
done

# VHS insists on an --output GIF; we only want the screenshots. Drop the GIFs.
rm -f "$work"/*.gif
echo "done → $out"
ls -la "$out"
