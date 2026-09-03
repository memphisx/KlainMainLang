// examples/webview/spa_server.ts — the static-file server worker for spa.ts.
// Walks dist/ for the requested path, falls back to index.html (SPA routing),
// and sets a content-type by extension. The dist/-walking helper lives here,
// in the example, not in the compiler (per TDD-00142).

import http from 'klain:http'  // bespoke handler⇒response server (Node's faithful shape is http.createServer)
import { readFileSync, existsSync } from 'fs'
import { parentPort, workerData } from 'worker_threads'

interface Res {
  status: number
  body: string
  headers: Map<string, string>
}

const cfg: string = workerData
const port = parseInt(cfg, 10)
const root = "./examples/webview/dist"

function contentType(path: string): string {
  if (path.endsWith(".html")) return "text/html; charset=utf-8"
  if (path.endsWith(".js")) return "text/javascript; charset=utf-8"
  if (path.endsWith(".css")) return "text/css; charset=utf-8"
  if (path.endsWith(".json")) return "application/json"
  if (path.endsWith(".svg")) return "image/svg+xml"
  return "application/octet-stream"
}

http.listen(port, (req: HttpRequest): Res => {
  let rel: string = req.path
  if (rel === "/") {
    rel = "/index.html"
  }
  let file: string = root + rel
  // SPA fallback: unknown routes serve index.html so client-side routing works.
  if (!existsSync(file)) {
    file = root + "/index.html"
  }
  const headers: Map<string, string> = new Map<string, string>()
  headers.set("Content-Type", contentType(file))
  return { status: 200, body: readFileSync(file), headers: headers }
})
