// files — the filesystem layer: directory listings and entry previews over real
// `fs`/`path` calls. No `klain:tui`; the view (view.ts) renders whatever this
// returns and the loop (main.ts) navigates with it.

import { readdirSync, readFileSync, statSync } from "fs";
import { join } from "path";

export function isDir(p: string): boolean {
  return statSync(p).isDirectory();
}

// Drop a trailing "/" directory marker to recover the real entry name.
export function stripSlash(name: string): string {
  if (name.length > 0 && name.charAt(name.length - 1) === "/") {
    return name.slice(0, name.length - 1);
  }
  return name;
}

// The current directory's entries, ".." first, directories marked with a "/".
export function listing(dir: string): string[] {
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
export function preview(dir: string, entry: string): string {
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
