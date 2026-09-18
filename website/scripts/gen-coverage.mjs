// Generates the website's feature-area coverage figures from the canonical
// status rollup (docs/status/coverage-rollup.json — emitted by `make status`).
//   node scripts/gen-coverage.mjs   (also runs automatically before `npm run build`)
//
// Output (committed):
//   src/data/coverage.json  — { coverage: [{area,pct,strict,group}], headline: [{label,value,sub}] }
//
// The numbers are never hand-typed here; only the editorial curation (which
// areas to surface, their short labels and grouping) lives in lib-coverage.mjs.
// check-coverage.mjs guards the committed copy against drift from the rollup.

import { writeFileSync, mkdirSync } from 'node:fs'
import { dirname } from 'node:path'
import { buildCoverageDoc, serialize, OUT } from './lib-coverage.mjs'

const doc = buildCoverageDoc()
mkdirSync(dirname(OUT), { recursive: true })
writeFileSync(OUT, serialize(doc))
console.log(`[gen-coverage] wrote ${OUT} (${doc.coverage.length} areas, ${doc.headline.length} headline sections)`)
