// Copies the machine-readable conformance summaries the conformance tool emits
// (docs/testing/<platform>/conformance-summary.json — see tools/conformance/summary.go)
// into the website's data dir, so the landing/docs/conformance pages source their
// figures from data instead of hand-copied prose that drifts.
//   node scripts/gen-conformance.mjs   (also runs automatically before `npm run build`)
//
// Output (committed):
//   src/data/conformance-platforms.json
//     { schemaVersion, platforms: [ { platform, suites: { <suite>: { lanes } } }, … ] }
//
// EVERY platform with a committed summary is published (primary dev platform
// first), so the conformance page can tab across them. Drift between this copy
// and docs/testing is caught by check-conformance.mjs, which the build runs
// before this generator — so a stale copy fails the build instead of shipping.
//
// If no summary exists at all (conformance never run on this checkout), the
// existing committed copy is left untouched so the build still works.

import { writeFileSync, mkdirSync } from 'node:fs'
import { dirname } from 'node:path'
import { collectPlatforms, serialize, OUT } from './lib-conformance.mjs'

const doc = collectPlatforms()

if (doc.platforms.length === 0) {
  console.warn('[gen-conformance] no docs/testing/<platform>/conformance-summary.json found — keeping the existing committed copy. Run `make conformance` (and -node/-ts/-wpt) to refresh it.')
  process.exit(0)
}

mkdirSync(dirname(OUT), { recursive: true })
writeFileSync(OUT, serialize(doc))
console.log(`[gen-conformance] wrote ${OUT} (${doc.platforms.length} platform(s): ${doc.platforms.map((p) => p.platform).join(', ')})`)
