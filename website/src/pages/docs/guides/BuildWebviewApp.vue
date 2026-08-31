<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guides · Desktop</span>
    <h1>Build a desktop app with <code>klain:webview</code></h1>
    <p class="km-doc__lede">
      We'll build a real <strong>desktop file explorer</strong>: a native window whose UI is an
      ordinary <a href="https://quasar.dev" target="_blank" rel="noopener">Quasar</a> single-page
      app, backed by real <code>fs</code> calls in compiled native code. The left pane lists a
      directory; the right pane previews the selected entry — text files as text, images inline. It
      compiles to one self-contained binary with the whole UI embedded; no browser, no Electron, no
      <code>node_modules</code> at runtime.
    </p>

    <Shot :src="listingImg" alt="A native window listing a directory with file icons and sizes"
      caption="explorer — a Quasar UI in a native window, listing a directory over native fs. Folders first, then files with sizes." />

    <h2>The mental model</h2>
    <p>
      A <code>klain:webview</code> app is two halves that meet at a tiny bridge:
    </p>
    <ul>
      <li><strong>The page</strong> — a normal web UI (here a Quasar SPA) rendered by the OS's own
        webview (WebKit on macOS, WebKitGTK on Linux). It knows nothing about the filesystem.</li>
      <li><strong>The native side</strong> — your compiled TypeScript. You expose functions to the
        page with <code>w.bind(name, fn)</code>; the page calls them as <code>window.name(...)</code>
        and <code>await</code>s the result. That's the only channel between them.</li>
    </ul>
    <p>
      So the UI is web, but every privileged action — reading a directory, opening a file — is a
      native call. This guide's app exposes four: <code>home</code>, <code>listDir</code>,
      <code>readText</code>, and <code>readImage</code>.
    </p>

    <h2>1 · The window</h2>
    <p>
      The whole native program is small. <code>serve</code> points at a folder of static assets
      (our built SPA); at <em>compile</em> time those bytes are embedded into the binary and served
      from an in-binary loopback server, so the finished executable needs no <code>dist/</code>
      beside it. We vendor Quasar's and Vue's UMD builds into that folder so the app is fully
      offline — no CDN.
    </p>
    <CodeBlock filename="explorer.ts" :code="windowCode" />
    <p class="km-note">
      Two ways to supply the page: <code>w.html(&#96;…&#96;)</code> for a single inline HTML string, or
      <code>serve</code> for a whole asset folder (what we use, since Quasar is several files). Both
      expose the same <code>w.bind</code> bridge.
    </p>

    <h2>2 · The native bridge</h2>
    <p>
      Each <code>w.bind</code> registers a function the page can call. The contract is simple and
      worth getting exactly right: the page's call arguments arrive as <strong>one JSON-array
      string</strong>, and your handler returns a <strong>JSON string</strong> that the page
      receives already parsed. So the pattern is <em>parse the args, do the work, stringify the
      result</em>:
    </p>
    <CodeBlock filename="explorer.ts" :code="bindCode" />
    <p class="km-note">
      <code>JSON.parse</code> here is type-directed — it only projects into a
      <strong>type-annotated</strong> target. Write <code>const a: string[] = JSON.parse(args)</code>,
      not a bare <code>const a = JSON.parse(args)</code> (which yields nothing to index). This is the
      one easy-to-miss rule of the bridge.
    </p>

    <h2>3 · Previewing files</h2>
    <p>
      Text is the easy case — <code>readFileSync</code> hands the page a string. Images need a
      little more: read the raw bytes with the binary-safe <code>readFileSyncBytes</code>,
      base64-encode them with <code>Buffer</code>, and return a <code>data:</code> URL the page can
      drop straight into an <code>&lt;img&gt;</code>. No temp files, no static-file plumbing:
    </p>
    <CodeBlock filename="explorer.ts" :code="imageCode" />
    <p>
      On the page side, a click decides which native call to make from the file's extension, and the
      result drives a `v-if` between a text pane and an image pane:
    </p>
    <div class="km-doc__shots">
      <Shot :src="textImg" alt="The explorer showing a markdown file's contents in the right pane"
        caption="A text file → readText → shown in a scrollable pane." />
      <Shot :src="imageImg" alt="The explorer showing an image rendered in the right pane"
        caption="An image → readImage → an inline data-URL &lt;img&gt;." />
    </div>

    <h2>4 · The UI is just Quasar</h2>
    <p>
      Nothing about the front-end is special to <code>klain:webview</code> — it's a stock Quasar app
      (<code>QLayout</code>, <code>QList</code>, <code>QScrollArea</code>, dark mode) driving those
      four native calls through one small helper. Because the page is plain web tech, you author and
      style it exactly as you would any Quasar app; the native binary just gives it a window and a
      filesystem.
    </p>
    <CodeBlock filename="index.html" :code="pageCode" />

    <h2>Good to know</h2>
    <ul>
      <li><strong>Offline &amp; self-contained.</strong> Vendoring the UMD builds and using
        <code>serve</code> means the shipped binary embeds the entire UI — it runs with no network
        and no files beside it. <code>serve</code>'s path is resolved at compile time.</li>
      <li><strong>The bridge is the boundary.</strong> The page can only do what your binds let it;
        the exposed functions are the whole trust surface. This explorer is deliberately read-only.</li>
      <li><strong>One window per process</strong> in this release; the app loop (timers, promises)
        runs on the GUI thread, so servers or other fd-driven loops still belong in a Worker.</li>
      <li><strong>Platform.</strong> macOS needs zero extra dependencies; Linux needs the WebKitGTK
        dev packages at build time.</li>
    </ul>

    <div class="km-doc__nextrow">
      <router-link to="/docs/guides" class="km-btn">← All guides</router-link>
      <router-link to="/docs/examples/webview/explorer" class="km-btn km-btn--gold">See explorer.ts →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'
import Shot from 'components/docs/Shot.vue'
import listingImg from 'src/assets/webview/listing.png'
import textImg from 'src/assets/webview/text.png'
import imageImg from 'src/assets/webview/image.png'

const windowCode = `import { Webview } from 'klain:webview'
import { readdirSync, readFileSync, readFileSyncBytes, statSync } from 'fs'
import { join, extname } from 'path'

const w = new Webview({
  title: 'Klain Files',
  width: 1040,
  height: 680,
  serve: './file-explorer/dist',   // embedded into the binary at compile time
})

// ...binds go here...

w.run()`

const bindCode = `type Entry = { name: string; path: string; isDir: boolean; size: number }

// listDir(path): a directory's entries as { name, path, isDir, size }.
w.bind('listDir', (args: string): string => {
  const parsed: string[] = JSON.parse(args)   // typed target — required
  const dir = parsed[0]
  const names = readdirSync(dir)
  const out: Entry[] = []
  for (let i = 0; i < names.length; i++) {
    const full = join(dir, names[i])
    let isDir = false, size = 0
    try { const st = statSync(full); isDir = st.isDirectory(); size = st.size } catch (e) {}
    out.push({ name: names[i], path: full, isDir, size })
  }
  return JSON.stringify(out)
})

// readText(path): a text file's contents.
w.bind('readText', (args: string): string => {
  const parsed: string[] = JSON.parse(args)
  return JSON.stringify({ text: readFileSync(parsed[0]) })
})`

const imageCode = `// readImage(path): the file as a base64 data URL the <img> can show directly.
w.bind('readImage', (args: string): string => {
  const parsed: string[] = JSON.parse(args)
  const path = parsed[0]
  const ext = extname(path).toLowerCase()
  const mime = ext === '.png' ? 'image/png'
             : ext === '.gif' ? 'image/gif'
             : ext === '.svg' ? 'image/svg+xml' : 'image/jpeg'
  const bytes = readFileSyncBytes(path)                 // binary-safe read
  const b64: string = Buffer.from(bytes).toString('base64')
  return JSON.stringify({ dataUrl: 'data:' + mime + ';base64,' + b64 })
})`

const pageCode = `<!-- the page calls native binds through one helper -->
const native = async (fn, ...args) => {
  const r = await window[fn](...args)          // args are passed straight through
  return typeof r === 'string' ? JSON.parse(r) : r
}

async function open (entry) {
  if (entry.isDir) return load(entry.path)
  const ext = entry.name.split('.').pop().toLowerCase()
  if (['png','jpg','jpeg','gif','svg'].includes(ext)) {
    preview.value = { kind: 'image', src: (await native('readImage', entry.path)).dataUrl }
  } else {
    preview.value = { kind: 'text', text: (await native('readText', entry.path)).text }
  }
}
// ...rendered with QList on the left and a QScrollArea / <img> on the right.`
</script>

<style scoped>
.km-doc__shots {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 1rem;
  margin: 1.5rem 0;
}
.km-doc__shots :deep(.km-shot) { margin: 0; }
</style>
