#!/usr/bin/env bash
# Regenerate the webview guide screenshots in src/assets/webview/ from the real
# examples/webview/explorer app. Unlike the TUI shots (VHS, headless), a webview
# is a native GUI window, so this drives the actual window on a macOS desktop:
# it launches the app, reads the window bounds, and screenshots (and clicks)
# via macOS tooling.
#
# Requirements (macOS): `brew install cliclick`, plus Accessibility AND Screen
# Recording permission for the terminal running this. Not reproducible headless
# or in CI — the committed PNGs are the source of truth; rerun only on a Mac when
# the app's look changes.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
out="$repo/website/src/assets/webview"
demo="/tmp/klain-demo/gallery"
bin="$(mktemp -d)/explorer"
trap 'kill "${pid:-0}" 2>/dev/null || true; rm -rf "/tmp/klain-demo" "$(dirname "$bin")"' EXIT

command -v cliclick >/dev/null || { echo "cliclick not found — brew install cliclick" >&2; exit 1; }
mkdir -p "$out"

# A neutral demo project with a text file, a markdown file, an image, and a dir.
mkdir -p "$demo/src"
printf '# Gallery\n\nA folder of mixed files to preview.\n\n- notes.txt\n- logo.png\n- src/\n' > "$demo/README.md"
printf 'meeting notes\n- ship v0.11\n- write docs\n' > "$demo/notes.txt"
cp "$repo/website/src/assets/tui/todo.png" "$demo/logo.png"   # any PNG works as the demo image

echo "building explorer…"
( cd "$repo" && go build -o klainmain . && ./klainmain -o "$bin" examples/webview/explorer.ts >/dev/null 2>&1 )

echo "launching…"
( cd "$demo" && exec "$bin" >/dev/null 2>&1 ) &
pid=$!
sleep 3
osascript -e 'tell application "System Events" to set frontmost of (first process whose name contains "explorer") to true' 2>/dev/null
sleep 1

bounds="$(osascript -e 'tell application "System Events" to tell (first process whose name contains "explorer") to get {position, size} of front window')"
wx=$(echo "$bounds" | cut -d, -f1 | tr -d ' '); wy=$(echo "$bounds" | cut -d, -f2 | tr -d ' ')
ww=$(echo "$bounds" | cut -d, -f3 | tr -d ' '); wh=$(echo "$bounds" | cut -d, -f4 | tr -d ' ')

shoot() { screencapture -x -R "${wx},${wy},${ww},${wh}" "$out/$1"; echo "  wrote $1"; }
# Window-relative list-row centres (points): dirs first, then files by name.
click_row() { cliclick "c:$((wx + 150)),$((wy + $1))"; sleep 0.7; }

shoot listing.png            # initial directory listing, nothing selected
click_row 194; shoot text.png    # README.md → text preview
click_row 130; shoot image.png   # logo.png  → image preview

echo "done → $out"; ls -la "$out"
