// Shared content: code samples (verbatim from the repo's examples/) and the
// coverage figures (mirrored from docs/status/README.md). Keeping them here
// means the marketing page and the docs never drift apart.

export const GITHUB_URL = 'https://github.com/memphisx/KlainMainLang'

export const samples = {
  generics: {
    filename: 'generics.ts',
    code: `function identity<T>(x: T): T {
  return x;
}

console.log(identity(42));       // 42
console.log(identity("hello"));  // hello

interface Box<T> {
  value: T;
}

const boxedNumber: Box<number> = { value: 7 };
const boxedString: Box<string> = { value: "seven" };`
  },

  server: {
    filename: 'http_server.ts',
    code: `import http from 'http'

interface Res {
  status: number
  body: string
  headers: Map<string, string>
}

http.listen(8080, (req: HttpRequest): Res => {
  let respHeaders: Map<string, string> = new Map<string, string>()
  respHeaders.set('Content-Type', 'text/plain')

  if (req.path === '/hello') {
    let name: string = req.query.has('name') ? req.query.get('name') : 'stranger'
    return { status: 200, body: 'hello, ' + name, headers: respHeaders }
  }

  return { status: 404, body: 'not found: ' + req.path, headers: respHeaders }
})`
  },

  fetch: {
    filename: 'fetch.ts',
    code: `const r = await fetch('http://127.0.0.1:8765/get')

console.log(r.status)          // 200
console.log(r.ok)              // true

interface Ip { origin: string }

// .json() parses the body straight into a declared type
const data = r.json() as Ip
console.log(data.origin)`
  }
}

export const terminal = `$ make build           # → ./klainmain
$ ./klainmain app.ts   # → native binary
$ ./app
hello, native world`

// Coverage figures — source: docs/status/README.md (2026-08-25).
export const coverage = [
  { area: 'Async / Promise', pct: 100, group: 'Language' },
  { area: 'Classes / OOP', pct: 100, group: 'Language' },
  { area: 'Array methods', pct: 100, group: 'Language' },
  { area: 'Number / Math', pct: 100, group: 'Language' },
  { area: 'Type primitives', pct: 100, group: 'Language' },
  { area: 'Object & collections', pct: 97, group: 'Language' },
  { area: 'Modules', pct: 94, group: 'Language' },
  { area: 'String methods', pct: 93, group: 'Language' },
  { area: 'JSON', pct: 87, group: 'Language' },
  { area: 'Type system features', pct: 82, group: 'Language' },

  { area: 'Networking (fetch, WS, SSE)', pct: 100, group: 'Web platform' },
  { area: 'Streams', pct: 100, group: 'Web platform' },
  { area: 'Web Crypto', pct: 100, group: 'Web platform' },
  { area: 'Workers / Concurrency', pct: 100, group: 'Web platform' },
  { area: 'Binary data & Typed Arrays', pct: 100, group: 'Web platform' },
  { area: 'URL', pct: 100, group: 'Web platform' },
  { area: 'Timers', pct: 100, group: 'Web platform' },

  { area: 'HTTP Server', pct: 100, group: 'Node.js' },
  { area: 'events (EventEmitter)', pct: 100, group: 'Node.js' },
  { area: 'path', pct: 100, group: 'Node.js' },
  { area: 'os', pct: 100, group: 'Node.js' },
  { area: 'File System (fs)', pct: 93, group: 'Node.js' },
  { area: 'Process / CLI I/O', pct: 92, group: 'Node.js' },
  { area: 'Other core modules', pct: 92, group: 'Node.js' }
]

// Headline area figures (docs/status/README.md section totals). These are
// curated feature-area checklists — "does the core case work?" — NOT external
// conformance. See `conformance` below for the honest, unflattering numbers.
export const headline = [
  { label: 'TypeScript core language', value: '~95%', sub: '295 / 309 targeted features' },
  { label: 'Web Platform APIs', value: '100%', sub: '55 / 55 targeted features' },
  { label: 'Node.js APIs', value: '~95%', sub: '81 / 85 targeted features' }
]

// External conformance — full public test suites, run unfiltered.
// Source: docs/testing/CONFORMANCE-RESULTS*.md. Deliberately kept visible: the
// feature numbers above measure the paths this compiler targets; these measure
// it against everything, most of which is out of scope by design.
export const conformance = [
  { label: 'Test262 (in-scope subset)', value: '14.8%', sub: '5,067 / 34,334 · 10.9% over the full corpus' },
  { label: 'TypeScript accept/reject', value: '51.4%', sub: '4,755 / 9,256 cases agree with tsc' },
  { label: 'Node.js test/parallel', value: '1.8%', sub: '21 / 1,195 runnable files pass' }
]
