// Anti-drift guard for the website's feature-area coverage figures. The site
// ships a committed copy (src/data/coverage.json) built from the canonical status
// rollup (docs/status/coverage-rollup.json). This fails if that copy no longer
// matches what the builder produces — i.e. the status data changed (a caveat
// added, a row flipped) but the website copy wasn't regenerated. That is exactly
// how the landing/coverage numbers silently went stale before this guard existed.
//
// Usage:
//   node scripts/check-coverage.mjs   check, exit 1 on drift (build gate)

import { readFileSync } from 'node:fs'
import { buildCoverageDoc, serialize, OUT } from './lib-coverage.mjs'

let expected
try {
  expected = buildCoverageDoc()
} catch (e) {
  console.error(`[check-coverage] ✗ ${e.message}`)
  console.error('  The status rollup (docs/status/coverage-rollup.json) is missing or an editorial')
  console.error('  category no longer resolves. Run `make status`, then `npm run gen:coverage`.')
  process.exit(1)
}

let committed
try {
  committed = readFileSync(OUT, 'utf8')
} catch {
  console.error('[check-coverage] ✗ src/data/coverage.json is missing. Run `npm run gen:coverage` and commit it.')
  process.exit(1)
}

if (committed.trim() !== serialize(expected).trim()) {
  console.error('[check-coverage] ✗ website coverage figures drifted from docs/status/.')
  console.error('  The committed src/data/coverage.json no longer matches the canonical status rollup.')
  console.error('  Regenerate and commit:  npm run gen:coverage')
  process.exit(1)
}

console.log(`[check-coverage] ✓ in sync (${expected.coverage.length} areas, ${expected.headline.length} headline sections)`)
