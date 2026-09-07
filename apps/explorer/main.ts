// explorer — a read-only desktop file explorer: a Quasar UI in a native window,
// backed by real `fs` calls over the klain:webview bridge (TDD-00142). The left
// pane lists a directory; the right pane previews the selected entry — text
// files as text, images as an inline data URL.
//
// The UI is a single-page Quasar app (Vue 3 + Quasar UMD, vendored into dist/
// so the app is fully offline — no CDN). Everything visible is stock Quasar
// (QLayout, QList, QItem, QScrollArea, QImg); the native side (bridge.ts)
// exposes four functions the page calls: `home`, `listDir`, `readText`,
// `readImage`. This file is just the window: create it, wire the bridge, run.
//
// Build:  klainmain --static apps/explorer/main.ts && ./apps/explorer/main
// Windows: klainmain --static apps/explorer/main.ts && apps/explorer/main.exe (WebView2; see README)
// (macOS: zero extra deps; Linux: WebKitGTK dev packages — see README.)
//
// `serve` embeds ./dist into the binary at compile time and serves it from an
// in-binary loopback server, so the finished executable is self-contained — no
// dist/ folder needs to sit beside it at runtime.

import { Webview } from "klain:webview";
import { home, listDir, readText, readImage } from "./bridge";

const w = new Webview({
  title: "Klain Files",
  width: 1040,
  height: 680,
  serve: "./apps/explorer/dist",
});

w.bind("home", home);
w.bind("listDir", listDir);
w.bind("readText", readText);
w.bind("readImage", readImage);

w.run();
console.log("window closed");
