<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">klain: namespace</span>
    <h1><code>klain:webview</code> — desktop applications</h1>
    <p class="km-doc__lede">
      A compiled binary that opens a real window, renders an HTML/CSS/JS UI, and whose buttons
      call straight into your typed native code. This is the Tauri architecture — the system
      browser engine (WKWebView on macOS, WebKitGTK on Linux) for the UI, this compiler for the
      backend — but <strong>same-process</strong>: no separate renderer, no IPC serialization
      beyond a JSON string. Any SPA that builds to static assets (React, Vue, Svelte, vanilla)
      can be the frontend.
    </p>
    <p>
      <strong>macOS needs zero dependencies</strong> (WebKit ships with the OS). Linux needs the
      WebKitGTK dev packages at build time.
    </p>
    <CodeBlock filename="app.ts" :code="webviewCode" />

    <h2>How the page talks to native code</h2>
    <p>
      Yes — it works like Electron/Tauri's exposed functions, but simpler. <code>w.bind(name, cb)</code>
      injects a <code>window.name(...)</code> function into <em>every</em> page. Calling it from
      page JS returns a <strong>Promise</strong> and invokes your native callback on the GUI
      thread. There is no <code>contextBridge</code> and no preload script — the binding is direct.
    </p>
    <ul>
      <li><strong>Page → native:</strong> <code>await window.myFn(...)</code>. The callback receives
        the call's arguments as a JSON-array <em>string</em>; it returns a JSON string (or nothing),
        which resolves the page-side promise. Throwing rejects it. <code>JSON.parse</code>/
        <code>JSON.stringify</code> are the whole contract — explicit, no hidden marshaling.</li>
      <li><strong>Native → page:</strong> <code>w.eval(js)</code> fires JavaScript into the page
        (thread-safe — a Worker can push UI updates), and every bound call's return value flows
        back as its promise result.</li>
    </ul>
    <p>
      Inside a bound callback you have the full synchronous runtime — <code>fs</code>,
      <code>spawnSync</code>, <code>crypto</code>, the <code>fetch</code> client, everything already
      shipped. And callbacks can be <strong>async</strong>: a callback returning
      <code>Promise&lt;string&gt;</code> settles the page-side promise when it resolves.
    </p>

    <h2>Typed bindings — no hand-written JSON</h2>
    <p>
      Pass a <code>bindings</code> object to the constructor and the compiler does the marshaling:
      the page's arguments are decoded into your callback's <em>declared</em> parameter types and
      the return value is JSON-encoded automatically. The object's keys are the entire exposed
      surface — nothing reaches <code>window</code> unless you put it there, which is the security
      boundary. (<code>w.bind()</code> stays the raw string-in/out escape hatch;
      <code>w.bindTyped(name, fn)</code> is the imperative typed form.)
    </p>
    <CodeBlock filename="typed.ts" :code="typedCode" />
    <p>
      <code>setTimeout</code>, <code>setInterval</code>, and promise reactions run
      <strong>on the GUI thread</strong> under <code>run()</code> — a lightweight page-driven tick
      pump drives this runtime's timer/microtask queues, so timed UI updates and async bind work
      with no <code>Worker</code>. Servers and other fd-driven loops still belong in a Worker.
    </p>

    <h2>Serving a full SPA</h2>
    <p>Two patterns, both shipped:</p>
    <ul>
      <li><strong>Inline</strong> — <code>w.html(...)</code> with an HTML string (a single-file SPA
        build works here). The string is compiled <em>into the binary</em> as a constant, so this
        pattern is self-contained — no external files.</li>
      <li><strong>Embedded (single-file)</strong> — <code>new Webview({ serve: './dist' })</code>
        compiles the whole built directory <em>into the binary</em> and serves it from an in-binary
        static server; the result is one self-contained executable with no <code>dist/</code> beside
        it. This is the recommended way to ship a multi-asset SPA/SSG.</li>
      <li><strong>Served (external)</strong> — or run your own <code>http.createServer</code> in a
        <code>Worker</code> and navigate to it, if you'd rather serve from disk or a remote.</li>
    </ul>
    <CodeBlock filename="embedded.ts" :code="embedCode" />
    <p class="km-note">
      Any framework build works — <code>quasar build</code> (SPA or SSG), <code>vite build</code>, …
      Binary assets (fonts/images) are served byte-exact. For lower-level control,
      <code>import { embedDir } from 'klain:assets'</code> gives an <code>EmbeddedAssets</code>
      handle with <code>.get(path): ArrayBuffer</code>.
    </p>

    <h2>The V1 surface</h2>
    <table>
      <thead><tr><th>Call</th><th>Effect</th></tr></thead>
      <tbody>
        <tr><td><code>new Webview({ title, width, height, debug })</code></td><td>Create the window (one per process in V1). <code>debug: true</code> enables devtools.</td></tr>
        <tr><td><code>w.navigate(url)</code> / <code>w.html(doc)</code></td><td>Load a URL / an inline document</td></tr>
        <tr><td><code>w.bind(name, cb)</code> / <code>w.unbind(name)</code></td><td>Expose / remove a <code>window.name(...)</code> native function</td></tr>
        <tr><td><code>w.eval(js)</code> / <code>w.init(js)</code></td><td>Run JS now / before every page load</td></tr>
        <tr><td><code>w.setTitle(s)</code> / <code>w.setSize(w, h)</code></td><td>Update the window chrome</td></tr>
        <tr><td><code>w.run()</code></td><td>Enter the GUI loop — <strong>blocks</strong>; statements after it are the shutdown path</td></tr>
        <tr><td><code>w.terminate()</code> / <code>w.destroy()</code></td><td>End <code>run()</code> / tear the window down</td></tr>
      </tbody>
    </table>
    <p>
      Typed bindings can be <strong>async</strong> too — a callback returning
      <code>Promise&lt;T&gt;</code> settles the page promise with the JSON-encoded <code>T</code>.
      And <code>klainmain --emit-window-dts</code> writes a <code>Window</code>
      <code>.d.ts</code> from your bindings, so the page-side SPA gets autocomplete and
      typechecking on <code>window.*</code> (and you get a machine-checkable record of the
      exact contract).
    </p>
    <p class="km-note">
      <strong>Not yet:</strong> one window per process (multi-window is a later stage).
    </p>

    <h2>Packaging — a double-clickable app</h2>
    <p>
      Add <code>--package</code> and the compiler wraps the binary into a desktop app for the
      host platform: a <code>.app</code> bundle on macOS (with an <code>Info.plist</code> and
      optional icon) or a <code>.desktop</code> launcher on Linux. On macOS the bundle is what
      gives the window proper foreground activation.
    </p>
    <CodeBlock filename="build.sh" :code="packageCode" />
    <p class="km-note">
      Metadata comes from flags: <code>--app-name</code>, <code>--app-id</code>,
      <code>--app-version</code>, and <code>--app-icon</code> (a <code>.icns</code> or
      <code>.png</code> on macOS; a <code>.png</code>/<code>.svg</code> on Linux). Bare
      <code>--package</code> uses sensible defaults. It bundles the executable — self-contained
      inline-HTML apps package perfectly; bundling a served SPA's <code>dist/</code> folder is a
      follow-up.
    </p>

    <div class="km-doc__nextrow">
      <router-link to="/docs/klain" class="km-btn">← klain: namespace</router-link>
      <router-link to="/docs/klain/http" class="km-btn km-btn--gold">klain:http →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'

const webviewCode = `import { Webview } from 'klain:webview'
import { readdirSync } from 'fs'

const w = new Webview({ title: 'My App', width: 900, height: 600 })

// Expose a native function to the page: window.listDir(...)
w.bind('listDir', (args: string): string => {
  const req = JSON.parse(args)          // the page's argument list, as JSON
  const entries = readdirSync(req[0])   // real native fs call
  return JSON.stringify(entries)        // resolves the page-side promise
})

w.html(\`<!doctype html><button onclick="show()">List</button>
  <ul id="out"></ul>
  <script>
    async function show() {
      const names = await window.listDir(JSON.stringify(['.']))
      out.innerHTML = names.map(n => '<li>' + n + '</li>').join('')
    }
  <\\/script>\`)

w.run()   // blocks — the window is the program now`

const spaCode = `import { Webview } from 'klain:webview'
import { Worker } from 'worker_threads'

// A Worker owns the server's event loop; the main thread owns the GUI loop.
const server = new Worker('./spa_server.ts', { workerData: '8137' })

const w = new Webview({ title: 'Klain SPA', width: 800, height: 600 })
w.navigate('http://127.0.0.1:8137/')
w.run()`

const typedCode = `import { Webview } from 'klain:webview'

interface Point { x: number; y: number }

const w = new Webview({
  title: 'My App', width: 900, height: 600,
  bindings: {
    // args decode into (number, number); the number return JSON-encodes
    add: (a: number, b: number): number => a + b,
    // returns an object — encoded to JSON for the page
    mkPoint: (x: number, y: number): Point => ({ x, y }),
  },
})

// page:  const p = await window.mkPoint(4, 5)   // -> { x: 4, y: 5 }
w.run()`

const embedCode = `import { Webview } from 'klain:webview'

// Embeds ./dist into the binary at compile time and serves it — one file, no
// external assets at runtime.
const w = new Webview({ title: 'My App', width: 900, height: 600, serve: './dist' })
w.run()`

const packageCode = `# compile + wrap into "Klain Demo.app" (double-clickable on macOS)
klainmain --package --app-name "Klain Demo" \\
  --app-icon icon.png app.ts

open "Klain Demo.app"`
</script>

<style scoped>
.km-note {
  border-left: 3px solid var(--km-gold, #c6a03c);
  padding: 8px 0 8px 16px;
  opacity: 0.85;
}
</style>
