// examples/webview/explorer.ts — a read-only desktop file explorer: a Quasar
// UI in a native window, backed by real `fs` calls over the klain:webview
// bridge (TDD-00142). The left pane lists a directory; the right pane previews
// the selected entry — text files as text, images as an inline data URL.
//
// The UI is a single-page Quasar app (Vue 3 + Quasar UMD, vendored into dist/
// so the app is fully offline — no CDN). We add no framework code beyond one
// small component; everything visible is stock Quasar (QLayout, QList, QItem,
// QScrollArea, QImg). The native side exposes four functions the page calls:
// `home`, `listDir`, `readText`, `readImage`.
//
// Build:  klainmain examples/webview/explorer.ts && ./examples/webview/explorer
// (macOS: zero extra deps; Linux: WebKitGTK dev packages — see README.)
//
// `serve` embeds ./file-explorer/dist into the binary at compile time and
// serves it from an in-binary loopback server, so the finished executable is
// self-contained — no dist/ folder needs to sit beside it at runtime.

import { Webview } from 'klain:webview'
import { readdirSync, readFileSync, readFileSyncBytes, statSync } from 'fs'
import { join, extname } from 'path'

type Entry = { name: string; path: string; isDir: boolean; size: number };

const w = new Webview({
  title: "Klain Files",
  width: 1040,
  height: 680,
  serve: "./examples/webview/file-explorer/dist",
})

// home(): the directory the explorer opens in — the process's working dir.
w.bind("home", (args: string): string => {
  return JSON.stringify({ path: process.cwd() });
});

// listDir(path): the directory's entries as { name, path, isDir, size }.
w.bind("listDir", (args: string): string => {
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
});

// readText(path): a text file's contents, or an { error } message.
w.bind("readText", (args: string): string => {
  const parsed: string[] = JSON.parse(args);
  const path = parsed[0];
  try {
    return JSON.stringify({ text: readFileSync(path) });
  } catch (e) {
    return JSON.stringify({ error: "could not read " + path });
  }
});

// readImage(path): the image as a base64 data URL the <img> can show directly.
// readFileSyncBytes is the binary-safe read (a NUL byte won't truncate it);
// Buffer base64-encodes the bytes.
w.bind("readImage", (args: string): string => {
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
});

w.run();
console.log("window closed");
