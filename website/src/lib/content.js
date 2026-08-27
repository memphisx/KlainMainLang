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

  numbers: {
    filename: 'jsdoc-widths.ts',
    code: `// Valid TypeScript — the JSDoc @type is erasable, so tsc still accepts it.
// This compiler reads the width and gives 'number' real machine-int semantics.

// An 8-bit unsigned integer wraps at its width, like C / a typed array.
/** @type {uint8} */
let r = 255
r = r + 1
console.log(r)              // 0  (wrapped at 8 bits)

// Single-precision float, narrower than the default IEEE-754 double.
/** @type {float32} */
let ratio = 1 / 3
console.log(ratio)         // 0.3333333432674408

// A bare 'number' stays a JS-faithful double — same 2**53 ceiling as JS.
let big = 9007199254740993 // 2**53 + 1
console.log(big)           // 9007199254740992  (precision loss, as in JS)`
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

export const terminal = `$ git clone https://github.com/memphisx/KlainMainLang
$ cd KlainMainLang && make build   # → ./klainmain
$ ./klainmain app.ts   # → native binary
$ ./app
hello, native world`

// Coverage figures — source: docs/status/README.md (2026-08-26).
//   pct    = Coverage      (works for its core case; real caveats disclosed)
//   strict = Strict Coverage (works with ZERO known caveats/bugs of any severity)
// The gap between the two is "works, but with a documented divergence from JS".
export const coverage = [
  { area: 'Async / Promise', pct: 100, strict: 100, group: 'Language' },
  { area: 'Classes / OOP', pct: 100, strict: 59, group: 'Language' },
  { area: 'Array methods', pct: 100, strict: 71, group: 'Language' },
  { area: 'Number / Math', pct: 100, strict: 74, group: 'Language' },
  { area: 'Type primitives', pct: 100, strict: 58, group: 'Language' },
  { area: 'Object & collections', pct: 97, strict: 59, group: 'Language' },
  { area: 'Modules', pct: 94, strict: 50, group: 'Language' },
  { area: 'String methods', pct: 93, strict: 69, group: 'Language' },
  { area: 'JSON', pct: 87, strict: 67, group: 'Language' },
  { area: 'Type system features', pct: 82, strict: 37, group: 'Language' },

  { area: 'Networking (fetch, WS, SSE)', pct: 100, strict: 17, group: 'Web platform' },
  { area: 'Streams', pct: 100, strict: 13, group: 'Web platform' },
  { area: 'Web Crypto', pct: 100, strict: 13, group: 'Web platform' },
  { area: 'Workers / Concurrency', pct: 100, strict: 0, group: 'Web platform' },
  { area: 'Binary data & Typed Arrays', pct: 100, strict: 11, group: 'Web platform' },
  { area: 'URL', pct: 100, strict: 0, group: 'Web platform' },
  { area: 'Timers', pct: 100, strict: 50, group: 'Web platform' },

  { area: 'HTTP Server', pct: 100, strict: 100, group: 'Node.js' },
  { area: 'events (EventEmitter)', pct: 100, strict: 50, group: 'Node.js' },
  { area: 'path', pct: 100, strict: 88, group: 'Node.js' },
  { area: 'os', pct: 100, strict: 86, group: 'Node.js' },
  { area: 'File System (fs)', pct: 93, strict: 50, group: 'Node.js' },
  { area: 'Process / CLI I/O', pct: 92, strict: 46, group: 'Node.js' },
  { area: 'Other core modules', pct: 92, strict: 8, group: 'Node.js' }
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
