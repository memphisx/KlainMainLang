// examples/webview/inline.ts — a self-contained desktop app (TDD-00142): an
// inline-HTML UI whose buttons call straight into native KlainMainLang code.
// Two native bindings: a counter kept in native state, and a directory listing
// read with fs.readdirSync. Native pushes results back into the page with eval.
//
// Build:  klainmain examples/webview/inline.ts && ./examples/webview/inline
// (macOS: zero extra deps; Linux: WebKitGTK dev packages — see README.)

import { Webview } from 'klain:webview'
import { readdirSync } from 'fs'

let count = 0

const w = new Webview({ title: "Klain Desktop", width: 640, height: 480, debug: true })

// Native counter: page calls window.bump(), gets the new value back as JSON.
w.bind("bump", (args: string): string => {
  count = count + 1
  return JSON.stringify({ count: count })
})

// Native file browser: page calls window.listDir(JSON.stringify([path])),
// native reads the directory and returns the entries as a JSON array.
w.bind("listDir", (args: string): string => {
  const parsed: string[] = JSON.parse(args)
  const path = parsed[0]
  const entries = readdirSync(path)
  return JSON.stringify(entries)
})

w.html(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>Klain Desktop</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; margin: 2rem; }
  button { font-size: 1rem; padding: .5rem 1rem; margin-right: .5rem; }
  #count { font-size: 2rem; font-weight: 600; }
  ul { max-height: 12rem; overflow: auto; border: 1px solid #ccc; padding: .5rem 1.5rem; }
</style></head>
<body>
  <h1>Counter</h1>
  <p><span id="count">0</span></p>
  <button onclick="bump()">Increment (native)</button>

  <h1>Directory</h1>
  <input id="path" value="." style="width: 60%; padding: .4rem;">
  <button onclick="browse()">List (native fs)</button>
  <ul id="entries"></ul>

  <script>
    async function bump() {
      const r = await window.bump()
      document.getElementById('count').textContent = r.count
    }
    async function browse() {
      const path = document.getElementById('path').value
      const entries = await window.listDir(JSON.stringify([path]))
      const ul = document.getElementById('entries')
      ul.innerHTML = ''
      for (const name of entries) {
        const li = document.createElement('li')
        li.textContent = name
        ul.appendChild(li)
      }
    }
  </script>
</body>
</html>`)

// Native → page push: set an initial status line after the window is up.
w.init("window.addEventListener('load', () => console.log('inline app loaded'))")

w.run()
console.log("window closed")
