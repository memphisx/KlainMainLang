// examples/webview/spa.ts — the "pack any SPA" pattern (TDD-00142, Stage 2).
//
// A multi-asset SPA (anything that builds to a static dist/ — React/Vue/
// Svelte/vanilla) is served by http.createServer running in a Worker thread
// (which legitimately owns its own event loop), while the main thread owns the
// GUI loop and navigates to it. Here dist/ is a tiny hand-written page; swap it
// for your framework's build output.
//
// Build: klainmain examples/webview/spa.ts && ./examples/webview/spa
//
// Production note: a robust app posts an *ephemeral* port (listen on 0) back
// from the worker via postMessage before navigating. This example uses a fixed
// port for clarity, with a page-reload guard injected via w.init() to cover the
// brief window before the server socket is bound.

import { Webview } from 'klain:webview'
import { Worker } from 'worker_threads'

const PORT = 8137

// The server runs in its own thread — its listen() loop and the GUI loop each
// own a thread, exactly the coexistence contract the design calls for.
const server = new Worker('./spa_server.ts', { workerData: "" + PORT })

const w = new Webview({ title: "Klain SPA", width: 800, height: 600, debug: true })

// init() runs before every page load: if the first navigation lands before the
// server socket is up (a blank page), reload shortly after. The real page sets
// document.body.dataset.ready, which stops the reload loop.
w.init(`
  window.addEventListener('load', () => {
    setTimeout(() => {
      if (!document.body || document.body.dataset.ready !== '1') location.reload()
    }, 400)
  })
`)

w.navigate("http://127.0.0.1:" + PORT + "/")
w.run()

server.terminate()
console.log("window closed")
