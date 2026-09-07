// A two-pane file browser — navigate the filesystem with a live preview,
// showing nested `klain:tui` layout (a row split into a list and a preview
// pane) driven by real `fs`/`path` calls.
//
//   ./files              # interactive: ↑/↓ move · Enter open dir · q quit
//   ./files </dev/null   # non-TTY: paints the current directory and exits
//
// Each keypress rebuilds the directory listing and re-reads the selected entry
// for the preview — the immediate-mode `state → view → update` loop, split by
// concern: fs.ts (listings/preview), view.ts (the two-pane layout), and this
// file (the navigation loop; the state is "where am I and what's selected").

import { render, enter, leave } from "klain:tui";
import { readKey } from "klain:tty";
import { join } from "path";
import { isDir, stripSlash, listing } from "./fs";
import { view } from "./view";

let cwd = process.cwd();
let entries = listing(cwd);
let cursor = 0;

enter();
render(view(cwd, entries, cursor));

if (!process.stdin.isTTY) {
  leave();
} else {
  process.stdin.setRawMode(true);
  let running = true;
  while (running) {
    const key: string = readKey();
    if (key === "q" || key.charCodeAt(0) === 3) {
      running = false;
    } else if (key === "\x1b[A") {
      cursor = (cursor + entries.length - 1) % entries.length;
    } else if (key === "\x1b[B") {
      cursor = (cursor + 1) % entries.length;
    } else if (key === "\r" || key === "\n") {
      const sel = entries[cursor];
      let target = cwd;
      if (sel !== "..") target = join(cwd, stripSlash(sel));
      if (isDir(target)) {
        if (sel === "..") cwd = join(cwd, "..");
        else cwd = target;
        entries = listing(cwd);
        cursor = 0;
      }
    }
    render(view(cwd, entries, cursor));
  }
  leave();
}
