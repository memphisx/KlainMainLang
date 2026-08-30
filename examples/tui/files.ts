// A two-pane file browser — navigate the filesystem with a live preview,
// showing nested `klain:tui` layout (a row split into a list and a preview
// pane) driven by real `fs`/`path` calls.
//
//   ./files              # interactive: ↑/↓ move · Enter open dir · q quit
//   ./files </dev/null   # non-TTY: paints the current directory and exits
//
// Each keypress rebuilds the directory listing and re-reads the selected entry
// for the preview — the immediate-mode `state → view → update` loop again, this
// time with the state being "where am I in the tree and what's selected".

import { Box, Text, List, render, enter, leave } from "klain:tui";
import { readKey } from "klain:tty";
import { readdirSync, readFileSync, statSync } from "fs";
import { join, basename } from "path";

function isDir(p: string): boolean {
  return statSync(p).isDirectory();
}

// Drop a trailing "/" directory marker to recover the real entry name.
function stripSlash(name: string): string {
  if (name.length > 0 && name.charAt(name.length - 1) === "/") {
    return name.slice(0, name.length - 1);
  }
  return name;
}

// The current directory's entries, ".." first, directories marked with a "/".
function listing(dir: string): string[] {
  const names = readdirSync(dir);
  const out: string[] = [".."];
  for (let i = 0; i < names.length; i++) {
    const full = join(dir, names[i]);
    out.push(isDir(full) ? names[i] + "/" : names[i]);
  }
  return out;
}

// Preview of the selected entry: a directory's own listing, or the head of a
// text file (capped so a huge or binary file can't flood the pane).
function preview(dir: string, entry: string): string {
  let full = dir;
  if (entry !== "..") full = join(dir, stripSlash(entry));
  if (isDir(full)) {
    const inner = readdirSync(full);
    let s = full + "\n\n" + inner.length + " entries\n";
    for (let i = 0; i < inner.length && i < 12; i++) s = s + "  " + inner[i] + "\n";
    return s;
  }
  const body = readFileSync(full);
  return body.slice(0, 600);
}

function view(dir: string, entries: string[], cursor: number) {
  return Box(
    { flexDirection: "column", width: 72, height: 22, border: "round", borderColor: "cyan" },
    [
      Text(" " + dir, { color: "green", bold: true }),
      Box({ flexDirection: "row", flexGrow: 1 }, [
        Box({ width: 28, padding: 1, border: "single", borderColor: "blue" }, [
          List(entries, { selected: cursor }),
        ]),
        Box({ flexGrow: 1, padding: 1 }, [
          Text(preview(dir, entries[cursor]), { color: "gray" }),
        ]),
      ]),
      Text(" ↑/↓ move · Enter open · q quit", { color: "gray", dim: true }),
    ],
  );
}

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
