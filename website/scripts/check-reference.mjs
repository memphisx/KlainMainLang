// Anti-drift guard for the API reference (src/data/reference/*.json) against the
// status source of truth (docs/status/*.md). The reference prose is original and
// hand-authored — this script does NOT generate it. What it guarantees is that
// the *structure* never silently diverges from the status tables:
//
//   • every entry's `statusRow` still names a real row on its status page
//     (catches a renamed/removed/retyped status row leaving a dangling entry),
//   • for a surface that claims to cover a whole page ("coversRows":"all"),
//     every ✅ row has at least one entry (catches a newly-added method that
//     never got a reference entry),
//   • the coverage badge matches the page header (unless the surface is a
//     declared subset of the page),
//   • the coverage badge (exact/caveats/missing) doesn't contradict the row's
//     status/caveats.
//
// It also scaffolds the *structure* of a new surface from a status page:
//   node scripts/check-reference.mjs --scaffold docs/status/FOO.md > out.json
// emitting one stub entry per row (badge derived, name = feature, TODO prose) —
// the author then writes the original descriptions/snippets/spec links.
//
// Usage:
//   node scripts/check-reference.mjs              check every surface, exit 1 on error
//   node scripts/check-reference.mjs --scaffold <status.md>   print a skeleton JSON

import { readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const REF_DIR = join(here, '..', 'src', 'data', 'reference')
const STATUS_DIR = resolve(here, '..', '..', 'docs', 'status')

// ── Normalization ─────────────────────────────────────────────────────────────
// Identify a status row by its Feature-cell text, tolerant of the differences
// between a terse table cell (`.replaceAll()`) and a fuller entry name/statusRow
// (`.replaceAll(from, to)`): drop parenthesized groups, then keep [a-z0-9]. If
// that empties the string (e.g. the bare `+` concatenation row), fall back to
// normalizing the original so it stays a distinct, non-empty key.
function stripParens (s) {
  let prev
  do { prev = s; s = s.replace(/\([^()]*\)/g, '') } while (s !== prev)
  return s
}
const alnum = (s) => s.toLowerCase().replace(/[^a-z0-9]/g, '')
// Full-identity key: like alnum but keeps [] so `T` and `T[]` rows stay distinct
// (e.g. `JSON.stringify(number)` vs `JSON.stringify(number[])`).
const alnumB = (s) => s.toLowerCase().replace(/[^a-z0-9[\]]/g, '')
function normRow (s) {
  const t = alnum(stripParens(s))
  return t || alnum(s)
}
// "20/21" → 95 (rounded integer percent), matching the hand-authored convention.
function fractionPct (frac) {
  const [n, d] = frac.split('/').map(Number)
  return d ? Math.round((n / d) * 100) : 0
}

// ── Status-page parsing ───────────────────────────────────────────────────────
function parseStatusPage (absPath) {
  const raw = readFileSync(absPath, 'utf8')
  const lines = raw.split('\n')

  let loose = null
  let strict = null
  for (const l of lines) {
    const m = l.match(/\*\*Coverage\*\*:\s*([0-9]+\/[0-9]+)/)
    if (m) loose = m[1]
    const s = l.match(/Strict Coverage\*\*:\s*([0-9]+\/[0-9]+)/)
    if (s) strict = s[1]
  }

  const rows = []
  const splitRow = (l) => l.split(/(?<!\\)\|/).slice(1, -1).map((c) => c.replace(/\\\|/g, '|').trim())
  // The Caveats column is not always column index 2 — a 3-column page
  // (`Feature | Status | Notes`) has none, and reading Notes as caveats would
  // mislabel every row. Find it by header name; -1 means "no caveats column".
  let caveatsIdx = -1
  for (const l of lines) {
    if (!l.trim().startsWith('|')) continue
    if (/^\s*\|\s*:?-{2,}/.test(l)) continue // separator
    const cells = splitRow(l)
    if (cells.length < 2) continue
    // A header row (every table here has a Status column) — re-detect the
    // Caveats column, since multi-table pages repeat the header per table.
    if (cells[1].toLowerCase() === 'status') {
      caveatsIdx = cells.findIndex((c) => c.toLowerCase().replace(/`/g, '') === 'caveats')
      continue
    }
    const [feature, status] = cells
    const caveats = caveatsIdx >= 0 ? (cells[caveatsIdx] || '').replace(/•/g, '').trim() : ''
    rows.push({
      feature: feature.replace(/`/g, ''),
      // Two identities: keyFull keeps parenthesized args (so rows that differ
      // ONLY by their signature — e.g. `JSON.stringify(v)` vs `JSON.stringify(v, null, 2)`
      // — stay distinct), keyStrip drops them (so a terse cell `.replaceAll()`
      // still matches a fuller entry name `.replaceAll(from, to)`).
      keyFull: alnumB(feature),
      keyStrip: normRow(feature),
      checked: status.includes('✅'),
      hasCaveats: caveats.length > 0
    })
  }
  return { loose, strict, rows }
}

// ── Checking ──────────────────────────────────────────────────────────────────
function checkSurface (jsonPath, errors, warnings, info) {
  const surface = JSON.parse(readFileSync(jsonPath, 'utf8'))
  const tag = surface.surface || jsonPath
  const statusAbs = join(STATUS_DIR, surface.statusPage)
  let page
  try {
    page = parseStatusPage(statusAbs)
  } catch {
    errors.push(`[${tag}] status page not found: docs/status/${surface.statusPage}`)
    return
  }

  // Match on full-signature identity first (distinct), then the paren-stripped
  // fallback (tolerant of terse-vs-full). Track claimed rows by feature text.
  const byFull = new Map(page.rows.map((r) => [r.keyFull, r]))
  const byStrip = new Map(page.rows.map((r) => [r.keyStrip, r]))
  const claimed = new Set() // row.feature values some entry maps to

  for (const e of surface.entries) {
    // A `statusRow` of null (or an explicit non-row gap) is an intentional entry
    // with no backing table row (a documented gap, or a method the status page
    // lists only in prose). Everything else must resolve to a real row.
    if (e.statusRow === null) continue
    const src = e.statusRow || e.name
    const row = byFull.get(alnumB(src)) || byStrip.get(normRow(src))
    if (!row) {
      errors.push(`[${tag}] entry "${e.name}" (id ${e.id}) maps to no status row ` +
        `(looked for "${src}") — renamed/removed row, or add statusRow:null for a gap`)
      continue
    }
    claimed.add(row.feature)
    // Badge vs. status/caveats consistency.
    if (!row.checked && e.badge !== 'missing') {
      warnings.push(`[${tag}] "${e.name}" is badge ${e.badge} but its status row is ❌ (expected missing)`)
    }
    if (row.checked && e.badge === 'missing') {
      warnings.push(`[${tag}] "${e.name}" is badge missing but its status row is ✅`)
    }
    if (row.checked && !row.hasCaveats && e.badge === 'caveats') {
      warnings.push(`[${tag}] "${e.name}" is badge caveats but its status row has no caveats`)
    }
  }

  // Coverage badge vs. the page header (skipped for a declared subset). In
  // --sync mode, rewrite the surface's stored counts (and the derived
  // percentages) from the page instead of erroring on a mismatch — the status
  // pages are the source of truth, so a drift is a stale hand-edit to fix, not
  // a failure to report (TDD-00149).
  const subset = surface.coverageScope === 'subset'
  if (!subset) {
    if (SYNC && page.loose && page.strict && surface.coverage) {
      // Surgical text edit of just the coverage numbers, so the file's
      // hand-authored formatting (compact one-line objects/arrays) is preserved
      // rather than reflowed by a JSON re-serialize.
      const raw = readFileSync(jsonPath, 'utf8')
      const updated = raw.replace(/"coverage"\s*:\s*\{[^}]*\}/, (block) => block
        .replace(/("loose"\s*:\s*")[^"]*(")/, `$1${page.loose}$2`)
        .replace(/("strict"\s*:\s*")[^"]*(")/, `$1${page.strict}$2`)
        .replace(/("loosePct"\s*:\s*)\d+/, `$1${fractionPct(page.loose)}`)
        .replace(/("strictPct"\s*:\s*)\d+/, `$1${fractionPct(page.strict)}`))
      if (updated !== raw) {
        writeFileSync(jsonPath, updated)
        info.push(`[${tag}] synced coverage → ${page.loose} loose / ${page.strict} strict`)
      }
    } else {
      if (page.loose && surface.coverage?.loose !== page.loose) {
        errors.push(`[${tag}] coverage.loose ${surface.coverage?.loose} != page ${page.loose}`)
      }
      if (page.strict && surface.coverage?.strict !== page.strict) {
        errors.push(`[${tag}] coverage.strict ${surface.coverage?.strict} != page ${page.strict}`)
      }
    }
  }

  // Every ✅ row covered — enforced only when the surface claims the whole page.
  const checkedRows = page.rows.filter((r) => r.checked)
  const uncovered = checkedRows.filter((r) => !claimed.has(r.feature))
  if (surface.coversRows === 'all') {
    for (const r of uncovered) {
      errors.push(`[${tag}] ✅ row "${r.feature}" has no reference entry`)
    }
  } else if (uncovered.length) {
    info.push(`[${tag}] subset surface — ${uncovered.length} ✅ row(s) on the page not yet covered here: ` +
      uncovered.map((r) => r.feature).join(', '))
  }

  const claimedChecked = checkedRows.filter((r) => claimed.has(r.feature)).length
  info.push(`[${tag}] ${surface.entries.length} entries · ${claimedChecked}/${checkedRows.length} ✅ rows claimed`)
}

// ── Scaffold ──────────────────────────────────────────────────────────────────
function scaffold (statusRel) {
  const abs = resolve(process.cwd(), statusRel)
  const page = parseStatusPage(abs)
  const entries = page.rows.map((r) => ({
    id: 'TODO.' + alnum(r.feature).slice(0, 24),
    name: r.feature,
    statusRow: r.feature,
    signature: 'TODO',
    badge: !r.checked ? 'missing' : (r.hasCaveats ? 'caveats' : 'exact'),
    description: 'TODO — original prose in this project\'s own voice.',
    differences: r.hasCaveats ? ['TODO — pull the caveat from the status row, strip ADR/TDD refs.'] : [],
    seeAlso: [],
    spec: { mdn: 'TODO' }
  }))
  const out = {
    surface: 'TODO',
    title: 'TODO',
    kind: 'language',
    statusPage: statusRel.replace(/^.*docs\/status\//, ''),
    coversRows: 'all',
    coverage: { loose: page.loose, strict: page.strict },
    lede: 'TODO',
    entries
  }
  process.stdout.write(JSON.stringify(out, null, 2) + '\n')
}

// ── Main ──────────────────────────────────────────────────────────────────────
// --sync rewrites each surface's stored coverage counts/percentages from its
// status page (TDD-00149) instead of erroring on drift.
const SYNC = process.argv.includes('--sync')
const scaffoldIdx = process.argv.indexOf('--scaffold')
if (scaffoldIdx !== -1) {
  const target = process.argv[scaffoldIdx + 1]
  if (!target) { console.error('--scaffold needs a path to a docs/status/*.md file'); process.exit(2) }
  scaffold(target)
} else {
  const errors = []
  const warnings = []
  const info = []
  const files = readdirSync(REF_DIR).filter((f) => f.endsWith('.json')).sort()
  for (const f of files) checkSurface(join(REF_DIR, f), errors, warnings, info)

  for (const i of info) console.log('  ' + i)
  for (const w of warnings) console.warn('  ⚠ ' + w)
  for (const e of errors) console.error('  ✗ ' + e)

  if (errors.length) {
    console.error(`\nReference anti-drift: ${errors.length} error(s), ${warnings.length} warning(s).`)
    process.exit(1)
  }
  writeIndex(files)
  console.log(`\nReference anti-drift OK — ${files.length} surface(s), ${warnings.length} warning(s).`)
}

// Generate the index module the site imports (static imports, no glob — Vite's
// import.meta.glob isn't available in the SSG router context). Maps each surface
// to its data and a nav-ordered metadata list (by kind, then title).
function writeIndex (files) {
  const KIND_ORDER = { language: 0, web: 1, node: 2 }
  const metas = files.map((f) => {
    const surface = f.replace(/\.json$/, '')
    const d = JSON.parse(readFileSync(join(REF_DIR, f), 'utf8'))
    return { surface, title: d.title, kind: d.kind || 'language' }
  }).sort((a, b) => (KIND_ORDER[a.kind] - KIND_ORDER[b.kind]) || a.title.localeCompare(b.title))

  const imports = metas.map((m, i) => `import s${i} from './reference/${m.surface}.json'`).join('\n')
  const surfaces = metas.map((m, i) => `  ${JSON.stringify(m.surface)}: s${i}`).join(',\n')
  const index = metas.map((m) => `  { surface: ${JSON.stringify(m.surface)}, title: ${JSON.stringify(m.title)}, kind: ${JSON.stringify(m.kind)} }`).join(',\n')
  const body = `// AUTO-GENERATED by scripts/check-reference.mjs — do not edit by hand.\n` +
    `${imports}\n\nexport const surfaces = {\n${surfaces}\n}\n\nexport const index = [\n${index}\n]\n`
  writeFileSync(join(REF_DIR, '..', 'reference-index.js'), body)
}
