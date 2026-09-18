// Shared builder for the feature-area coverage figures (the landing "what
// actually works" headline + the /docs/coverage bars). Both gen-coverage.mjs
// (writes the committed website copy) and check-coverage.mjs (fails the build on
// drift) import this so the two can't disagree.
//
// Source of truth: docs/status/coverage-rollup.json — emitted by statusgen
// (`make status`) from docs/status/data/*.json with the SAME counting the README
// uses, so every number here already matches the status pages. This module adds
// only EDITORIAL curation: which areas to surface as bars, their short display
// names, and their grouping — never the numbers, which are looked up by the
// status category name below.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
export const ROLLUP = join(here, '..', '..', 'docs', 'status', 'coverage-rollup.json')
export const OUT = join(here, '..', 'src', 'data', 'coverage.json')

// Editorial curation: the bars the site highlights, in order, each keyed to a
// status category (the exact `category` cell in the rollup — the numbers come
// from there). `area` is the short label the site shows.
const BARS = [
  // Language
  { area: 'Async / Promise', group: 'Language', category: 'Async / Promise' },
  { area: 'Classes / OOP', group: 'Language', category: 'Classes / OOP' },
  { area: 'Array methods', group: 'Language', category: 'Array methods' },
  { area: 'Number / Math', group: 'Language', category: 'Number / Math' },
  { area: 'Type primitives', group: 'Language', category: 'Type primitives' },
  { area: 'Object & collections', group: 'Language', category: 'Object & collections' },
  { area: 'Type system features', group: 'Language', category: 'Type system features' },
  { area: 'Modules', group: 'Language', category: 'Modules' },
  { area: 'String methods', group: 'Language', category: 'String methods' },
  { area: 'JSON', group: 'Language', category: 'JSON' },
  // Web platform
  { area: 'Networking (fetch, WS, SSE)', group: 'Web platform', category: 'Networking (fetch, WebSocket, SSE)' },
  { area: 'Streams', group: 'Web platform', category: 'Streams' },
  { area: 'Web Crypto', group: 'Web platform', category: 'Web Crypto' },
  { area: 'Workers / Concurrency', group: 'Web platform', category: 'Workers / Concurrency' },
  { area: 'Binary data & Typed Arrays', group: 'Web platform', category: 'Binary data & Typed Arrays' },
  { area: 'URL', group: 'Web platform', category: 'URL' },
  { area: 'Timers', group: 'Web platform', category: 'Timers' },
  // Node.js
  { area: 'HTTP Server', group: 'Node.js', category: 'HTTP Server' },
  { area: 'events (EventEmitter)', group: 'Node.js', category: '`events` (`EventEmitter`)' },
  { area: 'path', group: 'Node.js', category: '`path`' },
  { area: 'os', group: 'Node.js', category: '`os`' },
  { area: 'Process / CLI I/O', group: 'Node.js', category: 'Process / CLI I/O' },
  { area: 'File System (fs)', group: 'Node.js', category: 'File System (fs)' },
  { area: 'Other core modules', group: 'Node.js', category: 'Other core modules (`querystring`, `assert`, `test`, `zlib`, `net`, `util`, `dns`, `dgram`, `cluster`, `tls`, `http` client, `https`, `stream/web`, `vm`, `http2`)' },
  // Desktop
  { area: 'Desktop (klain:webview)', group: 'Desktop', category: 'Webview (desktop windows)' }
]

// The headline sections (top-line "what actually works"), keyed to rollup
// section names; `label` is the site's wording.
const HEADLINE = [
  { label: 'TypeScript core language', section: 'TypeScript Core Language' },
  { label: 'Web Platform APIs', section: 'Web Platform APIs' },
  { label: 'Node.js APIs', section: 'Node.js APIs' }
]

const fmtInt = (n) => n.toLocaleString('en-US')
// Match the status pages' tilde rule: a bare percent only when 100·n/d is exact.
const pctStr = (n, d) => (d && (100 * n) % d === 0 ? `${Math.round((100 * n) / d)}%` : `~${d ? Math.round((100 * n) / d) : 0}%`)

export function buildCoverageDoc () {
  const rollup = JSON.parse(readFileSync(ROLLUP, 'utf8'))
  const rowByCategory = new Map()
  const sectionByName = new Map()
  for (const sec of rollup.sections) {
    sectionByName.set(sec.section, sec)
    for (const r of sec.rows) rowByCategory.set(r.category, r)
  }

  const coverage = BARS.map(({ area, group, category }) => {
    const r = rowByCategory.get(category)
    if (!r) throw new Error(`coverage: no rollup row for category ${JSON.stringify(category)} (area ${JSON.stringify(area)})`)
    if (!r.coverage) throw new Error(`coverage: rollup row ${JSON.stringify(category)} has no numeric coverage`)
    return {
      area,
      pct: r.coverage.pct,
      strict: r.strict ? r.strict.pct : 0,
      group
    }
  })

  const headline = HEADLINE.map(({ label, section }) => {
    const sec = sectionByName.get(section)
    if (!sec || !sec.total) throw new Error(`headline: no rollup total for section ${JSON.stringify(section)}`)
    const { impl, total } = sec.total
    return { label, value: pctStr(impl, total), sub: `${fmtInt(impl)} / ${fmtInt(total)} targeted features` }
  })

  return { coverage, headline }
}

export function serialize (doc) {
  return JSON.stringify(doc)
}
