// Copies the machine-readable conformance summary the conformance tool emits
// (docs/testing/conformance-summary.json — see tools/conformance/summary.go)
// into the website's data dir, so the landing/docs source their figures from
// data instead of hand-copied prose that drifts.
//   node scripts/gen-conformance.mjs   (also runs automatically before `npm run build`)
//
// Output (committed):
//   src/data/conformance-summary.json  — { schemaVersion, suites: { <suite>: { lanes: { strict, js } } } }
//
// If the canonical file is missing (e.g. conformance was never run on this
// checkout), the existing committed copy is left untouched so the build still
// works — the numbers are simply as fresh as the last committed run.

import { readFileSync, writeFileSync, existsSync, mkdirSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const TESTING_DIR = join(here, '..', '..', 'docs', 'testing')
// Reports/summaries are per platform (docs/testing/<platform>/ — TDD-00204).
// The website publishes one platform's figures: the primary dev platform
// first, else whichever platform has a summary committed.
const PLATFORM_PRIORITY = ['macos-arm64', 'linux-x64', 'linux-arm64', 'windows-x64']
function findSummary() {
  const candidates = existsSync(TESTING_DIR)
    ? readdirSync(TESTING_DIR, { withFileTypes: true }).filter((d) => d.isDirectory()).map((d) => d.name)
    : []
  const ordered = [...PLATFORM_PRIORITY.filter((p) => candidates.includes(p)), ...candidates.filter((p) => !PLATFORM_PRIORITY.includes(p))]
  for (const p of ordered) {
    const f = join(TESTING_DIR, p, 'conformance-summary.json')
    if (existsSync(f)) return f
  }
  return null
}
const SRC = findSummary()
const DATA_DIR = join(here, '..', 'src', 'data')
const OUT = join(DATA_DIR, 'conformance-summary.json')

if (!SRC) {
  console.warn(`[gen-conformance] no docs/testing/<platform>/conformance-summary.json found — keeping the existing committed copy. Run \`make conformance\` (and -node/-ts/-wpt) to refresh it.`)
  process.exit(0)
}

const summary = JSON.parse(readFileSync(SRC, 'utf8'))
mkdirSync(DATA_DIR, { recursive: true })
// Stable, minified — the numbers, not formatting, are what matter here.
writeFileSync(OUT, JSON.stringify(summary))
console.log(`[gen-conformance] wrote ${OUT} (schemaVersion ${summary.schemaVersion})`)
