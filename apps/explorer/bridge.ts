// explorer — the native bridge: the four functions the web UI calls over the
// klain:webview boundary, each `(args: JSON string) -> JSON string`, backed by
// real `fs`/`path`. Kept apart from the window wiring (main.ts) so the file-IO
// half is readable and testable on its own.

import { readdirSync, readFileSync, readFileSyncBytes, statSync } from "fs";
import { join, extname } from "path";
import { Entry } from "./types";

// home(): the directory the explorer opens in — the process's working dir.
export function home(args: string): string {
  return JSON.stringify({ path: process.cwd() });
}

// listDir(path): the directory's entries as { name, path, isDir, size }.
export function listDir(args: string): string {
  const parsed: string[] = JSON.parse(args);
  const dir = parsed[0];
  const names = readdirSync(dir);
  const out: Entry[] = [];
  for (let i = 0; i < names.length; i++) {
    const name = names[i];
    const full = join(dir, name);
    let isDir = false;
    let size = 0;
    try {
      const st = statSync(full);
      isDir = st.isDirectory();
      size = st.size;
    } catch (e) {
      // Unreadable entry (permissions, broken symlink): list it as a 0-byte file.
    }
    out.push({ name, path: full, isDir, size });
  }
  return JSON.stringify(out);
}

// readText(path): a text file's contents, or an { error } message.
export function readText(args: string): string {
  const parsed: string[] = JSON.parse(args);
  const path = parsed[0];
  try {
    return JSON.stringify({ text: readFileSync(path) });
  } catch (e) {
    return JSON.stringify({ error: "could not read " + path });
  }
}

// readImage(path): the image as a base64 data URL the <img> can show directly.
// readFileSyncBytes is the binary-safe read (a NUL byte won't truncate it);
// Buffer base64-encodes the bytes.
export function readImage(args: string): string {
  const parsed: string[] = JSON.parse(args);
  const path = parsed[0];
  const ext = extname(path).toLowerCase();
  let mime = "image/jpeg";
  if (ext === ".png") mime = "image/png";
  else if (ext === ".gif") mime = "image/gif";
  else if (ext === ".svg") mime = "image/svg+xml";
  else if (ext === ".webp") mime = "image/webp";
  else if (ext === ".bmp") mime = "image/bmp";
  else if (ext === ".ico") mime = "image/x-icon";
  try {
    const bytes = readFileSyncBytes(path);
    const b64: string = Buffer.from(bytes).toString("base64");
    return JSON.stringify({ dataUrl: "data:" + mime + ";base64," + b64 });
  } catch (e) {
    return JSON.stringify({ error: "could not read " + path });
  }
}
