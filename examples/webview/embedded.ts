// examples/webview/embedded.ts — a SINGLE-FILE desktop app (TDD-00142 Stage 7).
//
// `serve` embeds the built SPA directory into the compiled binary at compile
// time and serves it from an in-binary static server (an ephemeral loopback
// port), then navigates the window to it. The result is one self-contained
// executable — no `dist/` folder beside it at runtime. Any framework build that
// produces static assets works: `quasar build` (SPA or SSG), `vite build`, etc.
//
// Build:  klainmain examples/webview/embedded.ts && ./examples/webview/embedded
// (move or delete examples/webview/dist/ afterward to prove it's embedded.)
//
// For lower-level control, `import { embedDir } from 'klain:assets'` gives an
// EmbeddedAssets handle with `.get(path): ArrayBuffer` over the embedded bytes.

import { Webview } from 'klain:webview'

const w = new Webview({
  title: "Klain (embedded)",
  width: 800,
  height: 600,
  serve: "./examples/webview/dist",
})

w.run()
console.log("window closed")
