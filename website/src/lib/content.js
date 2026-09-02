// Shared content: code samples (verbatim from the repo's examples/) and the
// coverage figures (mirrored from docs/status/README.md). Keeping them here
// means the marketing page and the docs never drift apart.

export const GITHUB_URL = 'https://github.com/memphisx/KlainMainLang'

export const samples = {
  concurrency: {
    filename: 'parallel_primes.ts',
    code: `import { go, Channel, select, defaultCase } from 'klain:sync'

const primes = new Channel<number>(1024)  // primes stream to the collector
const done = new Channel<number>(0)        // a worker signals when finished

// Fan out: 8 goroutines test interleaved slices, in parallel on every core.
for (let id = 0; id < 8; id++) {
  go(() => {
    for (let n = 2 + id; n <= 500000; n += 8) {
      if (isPrime(n)) primes.send(n)
    }
    done.send(id)
  })
}

// Fan in: select takes whichever channel is ready — count primes as they
// arrive, tally completions, then drain the last buffered results.
let count = 0, finished = 0
while (finished < 8) {
  select(
    primes.recvCase((p: number) => { count += 1 }),
    done.recvCase((id: number) => { finished += 1 }),
  )
}
let draining = true
while (draining) {
  select(
    primes.recvCase((p: number) => { count += 1 }),
    defaultCase(() => { draining = false }),
  )
}
console.log(\`primes below 500000: \${count}\`)  // 41538`
  },

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

  desktop: {
    filename: 'embedded.ts',
    code: `import { Webview } from 'klain:webview'

// A single-file desktop app. \`serve\` embeds your built SPA directory
// (quasar/vite/react/svelte — any static dist/) straight into the
// compiled binary and serves it from an in-binary server. The result
// is ONE executable with no dist/ folder beside it at runtime.

const w = new Webview({
  title: "My App",
  width: 900,
  height: 640,
  serve: "./dist",
})

w.run()
// klainmain app.ts && ./app   →   package it: klainmain -package app.ts`
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

// Coverage figures — source: docs/status/README.md (2026-08-30).
//   pct    = Coverage      (works for its core case; real caveats disclosed)
//   strict = Strict Coverage (works with ZERO known caveats/bugs of any severity)
// The gap between the two is "works, but with a documented divergence from JS".
export const coverage = [
  { area: 'Async / Promise', pct: 100, strict: 100, group: 'Language' },
  { area: 'Classes / OOP', pct: 100, strict: 56, group: 'Language' },
  { area: 'Array methods', pct: 100, strict: 63, group: 'Language' },
  { area: 'Number / Math', pct: 100, strict: 74, group: 'Language' },
  { area: 'Type primitives', pct: 100, strict: 58, group: 'Language' },
  { area: 'Object & collections', pct: 97, strict: 55, group: 'Language' },
  { area: 'Type system features', pct: 95, strict: 26, group: 'Language' },
  { area: 'Modules', pct: 94, strict: 50, group: 'Language' },
  { area: 'String methods', pct: 93, strict: 70, group: 'Language' },
  { area: 'JSON', pct: 87, strict: 67, group: 'Language' },

  { area: 'Networking (fetch, WS, SSE)', pct: 100, strict: 17, group: 'Web platform' },
  { area: 'Streams', pct: 100, strict: 11, group: 'Web platform' },
  { area: 'Web Crypto', pct: 100, strict: 11, group: 'Web platform' },
  { area: 'Workers / Concurrency', pct: 100, strict: 0, group: 'Web platform' },
  { area: 'Binary data & Typed Arrays', pct: 100, strict: 0, group: 'Web platform' },
  { area: 'URL', pct: 100, strict: 0, group: 'Web platform' },
  { area: 'Timers', pct: 100, strict: 50, group: 'Web platform' },

  { area: 'HTTP Server', pct: 100, strict: 88, group: 'Node.js' },
  { area: 'events (EventEmitter)', pct: 100, strict: 50, group: 'Node.js' },
  { area: 'path', pct: 100, strict: 88, group: 'Node.js' },
  { area: 'os', pct: 100, strict: 86, group: 'Node.js' },
  { area: 'Process / CLI I/O', pct: 100, strict: 45, group: 'Node.js' },
  { area: 'File System (fs)', pct: 94, strict: 41, group: 'Node.js' },
  { area: 'Other core modules', pct: 94, strict: 13, group: 'Node.js' },

  { area: 'Desktop (klain:webview)', pct: 100, strict: 0, group: 'Desktop' }
]

// Headline area figures (docs/status/README.md section totals). These are
// curated feature-area checklists — "does the core case work?" — NOT external
// conformance. See `conformance` below for the honest, unflattering numbers.
export const headline = [
  { label: 'TypeScript core language', value: '~96%', sub: '339 / 352 targeted features' },
  { label: 'Web Platform APIs', value: '100%', sub: '57 / 57 targeted features' },
  { label: 'Node.js APIs', value: '~98%', sub: '97 / 99 targeted features' }
]

// External conformance — full public test suites, run unfiltered.
// Source: docs/testing/CONFORMANCE-RESULTS*.md. Deliberately kept visible: the
// feature numbers above measure the paths this compiler targets; these measure
// it against everything, most of which is out of scope by design.
export const conformance = [
  { label: 'Test262 (in-scope subset)', value: '15.4%', sub: '5,279 / 34,334 · 11.3% over the full corpus' },
  { label: 'TypeScript accept/reject', value: '57.2%', sub: '5,293 / 9,256 cases agree with tsc' },
  { label: 'Node.js test/parallel', value: '1.8%', sub: '45 / 2,451 runnable files pass (both compat lanes)' }
]
