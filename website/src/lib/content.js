// Shared content: code samples (verbatim from the repo's examples/) and the
// coverage figures (mirrored from docs/status/README.md). Keeping them here
// means the marketing page and the docs never drift apart.
//
// External-conformance NUMBERS are not written here — they are sourced from
// src/data/conformance-platforms.json (every platform's figures), which
// `npm run gen:conformance` collects from docs/testing/<platform>/conformance-summary.json
// (emitted by tools/conformance/summary.go), and check:conformance guards against
// drift. Only the prose/explanations below are hand-written; the figures are
// derived, so they never drift.

import conformancePlatforms from 'src/data/conformance-platforms.json'
import coverageData from 'src/data/coverage.json'

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
    filename: 'node_createserver.ts',
    code: `// The real Node.js shape — this file runs unchanged under Node too.
import http from 'http'

const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.setHeader('Content-Type', 'text/plain')

  if (req.url === '/hello') {
    res.writeHead(200)
    res.end('hello from a native binary')
    return
  }

  res.writeHead(404)
  res.end('not found: ' + req.url)
})

server.listen(8080)`
  },

  // The bespoke, more compact server model — a handler that returns a
  // { status, body } object instead of writing to res. Not a Node API, so it
  // lives under the explicit klain:http specifier.
  serverBespoke: {
    filename: 'http_server.ts',
    code: `import http from 'klain:http'

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

// Coverage figures — DERIVED, never hand-typed. `npm run gen:coverage` builds
// src/data/coverage.json from docs/status/coverage-rollup.json (emitted by
// `make status` from docs/status/data/*.json with the same counting the README
// uses); check:coverage guards it against drift. Only the editorial curation —
// which areas to surface, their short labels and grouping — lives in
// website/scripts/lib-coverage.mjs.
//   pct    = Coverage      (works for its core case; real caveats disclosed)
//   strict = Strict Coverage (works with ZERO known caveats/bugs of any severity)
// The gap between the two is "works, but with a documented divergence from JS".
export const coverage = coverageData.coverage

// Headline section totals (docs/status README rollups), same derived source.
// Curated feature-area checklists — "does the core case work?" — NOT external
// conformance. See `conformance` below for the honest, unflattering numbers.
export const headline = coverageData.headline

// External conformance — full public test suites, run unfiltered. The numbers
// come from conformanceSummary (generated); only the prose here is hand-written.
// The feature numbers above measure the paths this compiler targets; these
// measure it against everything, most of which is out of scope by design.

const fmtInt = (n) => (n ?? 0).toLocaleString('en-US')
const pctStr = (n, d) => (d ? `${((100 * n) / d).toFixed(1)}%` : '—')

// The two compat lanes, explained. Prose evolves; numbers never live here.
export const compatFlags = {
  strict: {
    id: 'strict',
    flag: '-compat=strict',
    name: 'Strict (default)',
    tagline: "The compiler's opinionated, safer-than-JS typed semantics — the default. Untyped-JS patterns it can't prove safe are rejected at compile time."
  },
  js: {
    id: 'js',
    flag: '-compat=js',
    name: 'JS-compat',
    tagline: 'Best-effort vanilla-JS compatibility — a permissive superset of strict, for running untyped JavaScript as-is. Trades some of strict’s guarantees for reach.'
  }
}

// Per-suite explanation (numbers come from the summary, not from here).
const suiteInfo = {
  test262: {
    label: 'Test262',
    blurb: 'The official ECMAScript conformance corpus, run unfiltered. Most of it is out of scope by design — eval-based assertions, Intl/Temporal, dynamic import — so the honest figure is the in-scope subset.'
  },
  wpt: {
    label: 'Web Platform Tests',
    blurb: 'The browser platform’s own test suite, run headless through a testharness shim. It is dominated by DOM/rendering tests this compiler doesn’t target, so the runnable-document figure is low by design — shown for the JS/encoding/URL corners that do apply.'
  },
  ts: {
    label: 'TypeScript accept/reject',
    blurb: "Agreement with tsc’s own accept/reject verdict over Microsoft’s compiler and conformance test cases — a measure of front-end fidelity, not runtime behavior."
  },
  node: {
    label: 'Node.js test/parallel',
    blurb: "How much of Node’s own behavioral test suite runs verbatim after a mechanical CommonJS→typed-ESM transform — a floor on Node fidelity, not a coverage claim."
  }
}

// Display order across the site (headline suite first).
const SUITE_ORDER = ['test262', 'wpt', 'ts', 'node']

// Human labels for the raw platform dir names (docs/testing/<platform>/).
const PLATFORM_LABELS = {
  'macos-arm64': 'macOS (Apple Silicon)',
  'linux-arm64': 'Linux (arm64)',
  'linux-x64': 'Linux (x86-64)',
  'windows-x64': 'Windows (x86-64)'
}
const platformLabel = (p) => PLATFORM_LABELS[p] || p

const platforms = conformancePlatforms?.platforms ?? []

// Headline value + subtitle for one suite/lane, derived from a platform's suites.
function laneFigure(suites, suite, lane) {
  const d = suites?.[suite]?.lanes?.[lane]
  if (!d) return { value: '—', sub: 'not yet run' }
  if (suite === 'test262') {
    return {
      value: pctStr(d.inScope.pass, d.inScope.total),
      sub: `${fmtInt(d.inScope.pass)} / ${fmtInt(d.inScope.total)} in-scope · ${pctStr(d.overall.pass, d.overall.total)} of the full corpus`
    }
  }
  if (suite === 'wpt') {
    return {
      value: pctStr(d.pass, d.runnable),
      sub: `${fmtInt(d.pass)} / ${fmtInt(d.runnable)} runnable documents · ${fmtInt(d.subtestPass)} / ${fmtInt(d.subtestTotal)} subtests where a document executed`
    }
  }
  if (suite === 'ts') {
    return { value: pctStr(d.agree, d.classified), sub: `${fmtInt(d.agree)} / ${fmtInt(d.classified)} cases agree with tsc` }
  }
  if (suite === 'node') {
    return { value: pctStr(d.pass, d.runnable), sub: `${fmtInt(d.pass)} / ${fmtInt(d.runnable)} runnable files pass` }
  }
  return { value: '—', sub: '' }
}

// Build the per-suite, per-lane view for a single platform's suites.
function suitesForPlatform(suites) {
  return SUITE_ORDER.filter((suite) => suites?.[suite]).map((suite) => ({
    suite,
    label: suiteInfo[suite].label,
    blurb: suiteInfo[suite].blurb,
    corpusCommit: suites[suite].corpusCommit || '',
    lanes: ['strict', 'js'].map((lane) => ({
      flag: lane,
      name: compatFlags[lane].name,
      ...laneFigure(suites, suite, lane)
    }))
  }))
}

// Full per-platform projection — every platform with a committed summary, each
// with every suite × both lanes. Drives the conformance page's OS tabs.
export const conformancePlatformsView = platforms.map((p) => ({
  platform: p.platform,
  label: platformLabel(p.platform),
  suites: suitesForPlatform(p.suites)
}))

// Primary (headline) platform — platforms[0], the primary dev platform.
const primary = platforms[0]?.suites ?? {}

// Per-flag view for the PRIMARY platform: each suite with both lanes side by
// side, for the split display on the Coverage docs page.
export const conformanceByFlag = suitesForPlatform(primary)

// Backward-compatible flat array (strict lane, primary platform) for the
// existing stat-card renderers on the landing page and Coverage docs page.
export const conformance = conformanceByFlag.map((s) => {
  const strict = s.lanes.find((l) => l.flag === 'strict') || s.lanes[0]
  return { label: s.label, value: strict.value, sub: strict.sub }
})
