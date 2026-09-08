// Minimal, dependency-free static server for the SSG build (dist/ssg), used by
// the Playwright e2e suite. Mirrors how a static host (GitHub Pages) serves the
// prerendered site: every route is its own <route>/index.html, unknown paths
// fall back to 404.html. No SPA history rewrite — deep links must resolve to a
// real prerendered file, which is exactly what we want to test.
import { createServer } from 'node:http'
import { readFile, stat } from 'node:fs/promises'
import { join, extname, normalize } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = fileURLToPath(new URL('../dist/ssg', import.meta.url))
const PORT = Number(process.env.SSG_PORT || 9200)

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.webp': 'image/webp',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.map': 'application/json; charset=utf-8'
}

async function tryFile (p) {
  try {
    const s = await stat(p)
    if (s.isFile()) return p
  } catch { /* not a file */ }
  return null
}

// Resolve a URL path to a file on disk, honouring the per-route index.html
// layout SSG emits.
async function resolve (urlPath) {
  const clean = normalize(decodeURIComponent(urlPath.split('?')[0])).replace(/^(\.\.[/\\])+/, '')
  const abs = join(ROOT, clean)
  return (
    (extname(abs) ? await tryFile(abs) : null) ||
    (await tryFile(join(abs, 'index.html'))) ||
    (await tryFile(abs))
  )
}

const server = createServer(async (req, res) => {
  const file = (await resolve(req.url)) || (await tryFile(join(ROOT, '404.html')))
  if (!file) {
    res.writeHead(404, { 'content-type': 'text/plain' })
    res.end('not found')
    return
  }
  const body = await readFile(file)
  const isFallback = file.endsWith('404.html') && !req.url.startsWith('/404')
  res.writeHead(isFallback ? 404 : 200, { 'content-type': TYPES[extname(file)] || 'application/octet-stream' })
  res.end(body)
})

server.listen(PORT, () => {
  console.log(`ssg static server on http://localhost:${PORT}/ (root: ${ROOT})`)
})
