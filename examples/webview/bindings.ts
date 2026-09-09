// examples/webview/bindings.ts — typed bindings (TDD-00142 Stage 5/6): pass a
// plain object of functions to the constructor and every key becomes a typed
// window.* function in the page. No hand-written JSON — the page's arguments
// decode into each callback's declared parameter types and the return value
// JSON-encodes automatically, async included. The object's keys are the entire
// exposed surface (the allowlist): nothing reaches window unless it's here.
//
// The bindings value can be an inline object literal or, as here, a variable —
// handy when the API grows or is assembled elsewhere. `w.bind(name, cb)` stays
// the raw string-JSON escape hatch and `w.bindTyped(name, fn)` the imperative
// typed form; add --emit-window-dts to generate the page-side Window typing.
//
// Build:  klainmain examples/webview/bindings.ts && ./examples/webview/bindings
// Windows: klainmain examples/webview/bindings.ts && examples/webview/bindings.exe (WebView2; see README)
// (macOS: zero extra deps; Linux: WebKitGTK dev packages — see README.)

import { Webview } from 'klain:webview'
import { readdirSync } from 'fs'

interface Stats { calls: number; last: string }

let calls = 0
let last = ""

const api = {
  // scalar in, scalar out — page: await window.add(40, 2)
  add: (a: number, b: number): number => a + b,
  // string in, string out
  greet: (name: string): string => {
    calls = calls + 1
    last = name
    return `Hello from native code, ${name}!`
  },
  // object return — JSON-encoded for the page
  stats: (): Stats => ({ calls: calls, last: last }),
  // real native work: list a directory with fs
  listDir: (path: string): string[] => readdirSync(path),
  // async — the page's promise settles with the encoded value
  slowDouble: async (n: number): Promise<number> => {
    await new Promise<number>((resolve) => { setTimeout(() => resolve(0), 300) })
    return n * 2
  },
}

const w = new Webview({ title: "Typed Bindings", width: 700, height: 520, debug: true, bindings: api })

w.html(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>Typed Bindings</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; margin: 2rem; }
  button { font-size: 1rem; padding: .5rem 1rem; margin: .25rem .5rem .25rem 0; }
  pre { background: #f4f4f4; padding: .75rem; min-height: 6rem; }
</style></head>
<body>
  <h1>Typed window.* bindings</h1>
  <button onclick="run('add', window.add(40, 2))">add(40, 2)</button>
  <button onclick="run('greet', window.greet('Thessaloniki'))">greet('Thessaloniki')</button>
  <button onclick="run('stats', window.stats())">stats()</button>
  <button onclick="run('listDir', window.listDir('.'))">listDir('.')</button>
  <button onclick="run('slowDouble', window.slowDouble(21))">await slowDouble(21)</button>
  <pre id="out"></pre>
  <script>
    async function run(label, promise) {
      const value = await promise
      document.getElementById('out').textContent = label + ' -> ' + JSON.stringify(value, null, 2)
    }
  </script>
</body>
</html>`)

w.run()
console.log("window closed")
