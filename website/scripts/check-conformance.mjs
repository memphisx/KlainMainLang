// Anti-drift guard for the conformance data feed. The website ships a committed
// copy of the per-platform conformance numbers (src/data/conformance-platforms.json)
// generated from docs/testing/<platform>/conformance-summary.json. This guard
// fails if that copy no longer matches what the generator would produce from the
// canonical summaries — i.e. someone re-ran `make conformance*` (updating
// docs/testing) but forgot to regenerate and commit the website copy. That exact
// lag is how the site quietly shipped stale numbers before this check existed.
//
// It does NOT check whether docs/testing itself is fresh versus the compiler —
// that is the `make conformance` discipline. This guard only keeps the website
// copy in lockstep with the committed canonical summaries.
//
// Usage:
//   node scripts/check-conformance.mjs   check, exit 1 on drift (build gate)

import { readFileSync } from 'node:fs'
import { collectPlatforms, serialize, OUT } from './lib-conformance.mjs'

const expected = collectPlatforms()

if (expected.platforms.length === 0) {
  // Nothing canonical to compare against (conformance never run here) — don't
  // block a build that legitimately has no summaries. gen-conformance keeps the
  // existing copy in that case too.
  console.warn('[check-conformance] no docs/testing/<platform>/conformance-summary.json found — skipping drift check.')
  process.exit(0)
}

let committed
try {
  committed = readFileSync(OUT, 'utf8')
} catch {
  console.error('[check-conformance] ✗ src/data/conformance-platforms.json is missing. Run `npm run gen:conformance` and commit it.')
  process.exit(1)
}

const want = serialize(expected)
if (committed.trim() !== want.trim()) {
  console.error('[check-conformance] ✗ website conformance data drifted from docs/testing/.')
  console.error('  The committed src/data/conformance-platforms.json no longer matches the canonical')
  console.error('  per-platform summaries. Regenerate and commit:  npm run gen:conformance')
  process.exit(1)
}

console.log(`[check-conformance] ✓ in sync (${expected.platforms.length} platform(s): ${expected.platforms.map((p) => p.platform).join(', ')})`)
