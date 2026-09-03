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

import { readFileSync, writeFileSync, existsSync, mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const SRC = join(here, '..', '..', 'docs', 'testing', 'conformance-summary.json')
const DATA_DIR = join(here, '..', 'src', 'data')
const OUT = join(DATA_DIR, 'conformance-summary.json')

if (!existsSync(SRC)) {
  console.warn(`[gen-conformance] ${SRC} not found — keeping the existing committed copy. Run \`make conformance\` (and -node/-ts) to refresh it.`)
  process.exit(0)
}

const summary = JSON.parse(readFileSync(SRC, 'utf8'))
mkdirSync(DATA_DIR, { recursive: true })
// Stable, minified — the numbers, not formatting, are what matter here.
writeFileSync(OUT, JSON.stringify(summary))
console.log(`[gen-conformance] wrote ${OUT} (schemaVersion ${summary.schemaVersion})`)
