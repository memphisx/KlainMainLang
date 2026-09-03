// Unified documentation search index.
//
// The command palette (components/docs/ReferenceSearch.vue) searches EVERYTHING
// the docs site holds, not just the API reference: hand-authored guide/overview
// pages, the per-surface reference landing pages, every individual API entry
// (method/property level), and every runnable example. Each source is projected
// into one flat record shape so the palette can rank and render them uniformly:
//
//   { kind, name, title, desc, badge, route, hash, hay }
//
//   kind   — 'page' | 'reference' | 'api' | 'example' (drives the result chip
//            and how choose() navigates)
//   name   — primary label (page title, surface title, method name, example title)
//   title  — context crumb (section for a page, surface title for an api entry,
//            category for an example)
//   desc   — one-line description (may be '')
//   badge  — coverage badge for an api entry ('exact'|'caveats'|'missing'); null otherwise
//   route  — router path to navigate to
//   hash   — optional in-page anchor (api entries scroll to their id)
//   hay    — precomputed lowercased haystack (name + title + desc + keywords)
//
// Pages are hand-authored here (with search keywords) because they have no
// generated manifest; reference surfaces, api entries, and examples are derived
// from their existing data so a new surface/entry/example registers itself.

import { surfaces } from './reference-index.js'
// The lightweight example TREE (key + label + category), NOT examples-content.json
// — the tree is already a DocsLayout dependency (the nav), so indexing examples
// adds no bundle weight, whereas the full content file is 600KB+ of source code.
import examplesTree from './examples-tree.json'

// --- 1. Hand-authored doc pages (guides, overviews, references-as-a-page) -----
// `keywords` widen recall without cluttering the visible description.
const PAGES = [
  { name: 'Documentation overview', title: 'Start', route: '/docs', desc: 'Entry point to the KlainMainLang docs.', keywords: 'home index start docs' },
  { name: 'Installation', title: 'Start', route: '/docs/install', desc: 'Install the compiler and its toolchain prerequisites.', keywords: 'install setup clang llvm build requirements brew apt' },
  { name: 'Getting started', title: 'Start', route: '/docs/getting-started', desc: 'Compile and run your first program.', keywords: 'quickstart first program hello world compile run tutorial' },
  { name: 'Language guide', title: 'Guide', route: '/docs/language', desc: 'How the typed subset maps to native code — types, JSDoc widths, semantics.', keywords: 'language types jsdoc int8 uint64 semantics guide typescript subset' },
  { name: 'Standard library', title: 'Guide', route: '/docs/stdlib', desc: 'Tour of the built-in modules and globals.', keywords: 'stdlib standard library modules builtin globals' },
  { name: 'CLI & flags', title: 'Guide', route: '/docs/cli', desc: 'Command-line flags: -compat, -mm, targets, sanitizers, output.', keywords: 'cli flags options compat strict js mm gc manual sanitizer asan ubsan static target klainmain command line' },
  { name: 'Coverage matrix', title: 'Reference', route: '/docs/coverage', desc: 'Per-area coverage plus external conformance (Test262, Node, TS).', keywords: 'coverage conformance matrix test262 node oracle percentage strict caveats compat flag' },
  { name: 'Guides overview', title: 'Guides', route: '/docs/guides', desc: 'End-to-end walkthroughs building real programs.', keywords: 'guides walkthrough tutorials overview' },
  { name: 'TUI · Layout & components', title: 'Guides', route: '/docs/guides/tui/layout', desc: 'Build a terminal UI with klain:tui — flexbox layout and the component kit.', keywords: 'tui terminal ui klain:tui yoga flexbox box text list spinner progress layout components' },
  { name: 'TUI · Input & state', title: 'Guides', route: '/docs/guides/tui/input-state', desc: 'Handle keyboard input and drive a state → view loop.', keywords: 'tui input keyboard state update textinput interactive terminal' },
  { name: 'TUI · Live dashboards', title: 'Guides', route: '/docs/guides/tui/live-dashboard', desc: 'A repainting live dashboard with timers and resize handling.', keywords: 'tui dashboard live repaint sigwinch resize timers terminal' },
  { name: 'Desktop · File explorer', title: 'Guides', route: '/docs/guides/webview', desc: 'Build a desktop app with klain:webview.', keywords: 'webview desktop gui file explorer klain:webview gtk cocoa app' },
  { name: 'Concurrency · Load tester', title: 'Guides', route: '/docs/guides/concurrent-load-tester', desc: 'A concurrent HTTP load tester with klain:sync goroutines and channels.', keywords: 'concurrency load tester klain:sync goroutines channels select http klainload' },
  { name: 'klain: overview', title: 'klain:', route: '/docs/klain', desc: 'The klain: namespace — bespoke, Go-powered reinterpretations of Node modules.', keywords: 'klain namespace bespoke go power opt-in reimagined' },
  { name: 'klain:webview', title: 'klain:', route: '/docs/klain/webview', desc: 'Native desktop webview windows.', keywords: 'klain:webview desktop gui window native app' },
  { name: 'klain:http', title: 'klain:', route: '/docs/klain/http', desc: 'The bespoke HTTP server surface.', keywords: 'klain:http server listen createServer routing' },
  { name: 'klain:sync', title: 'klain:', route: '/docs/klain/sync', desc: 'Go goroutines, channels, select, and preemption.', keywords: 'klain:sync goroutines channels select preemption concurrency go' },
  { name: 'Examples overview', title: 'Examples', route: '/docs/examples', desc: 'Browse every runnable example by category.', keywords: 'examples samples snippets overview browse' }
]

// --- assemble the flat index -------------------------------------------------
const records = []

for (const p of PAGES) {
  records.push({
    kind: 'page',
    name: p.name,
    title: p.title,
    desc: p.desc || '',
    badge: null,
    route: p.route,
    hash: '',
    hay: (p.name + ' ' + p.title + ' ' + (p.desc || '') + ' ' + (p.keywords || '')).toLowerCase()
  })
}

// 2. Each reference surface as a landing-page result (so "streams" finds the
//    Streams reference page, not only its individual methods).
// 3. Every individual API entry, method/property level.
for (const [surface, data] of Object.entries(surfaces)) {
  records.push({
    kind: 'reference',
    name: data.title,
    title: 'API reference',
    desc: (data.lede || '').slice(0, 120),
    badge: null,
    route: '/docs/reference/' + surface,
    hash: '',
    hay: (data.title + ' api reference ' + surface + ' ' + (data.lede || '')).toLowerCase()
  })
  for (const e of data.entries) {
    records.push({
      kind: 'api',
      name: e.name,
      title: data.title,
      desc: (e.description || '').slice(0, 120),
      badge: e.badge,
      route: '/docs/reference/' + surface,
      hash: e.id,
      hay: (e.name + ' ' + data.title + ' ' + (e.description || '')).toLowerCase()
    })
  }
}

// 4. Every runnable example, walked from the nav tree (label + its category
//    label). Descriptions live only in the heavy content file, so they're
//    omitted here — title + category is enough to find an example by name.
function walkExamples (nodes, category) {
  for (const node of nodes) {
    if (node.type === 'example') {
      records.push({
        kind: 'example',
        name: node.label,
        title: category || 'Examples',
        desc: '',
        badge: null,
        route: '/docs/examples/' + node.key,
        hash: '',
        hay: (node.label + ' ' + (category || '') + ' ' + node.key.replace(/[/_]/g, ' ') + ' example').toLowerCase()
      })
    } else if (node.children) {
      walkExamples(node.children, node.label || category)
    }
  }
}
walkExamples(examplesTree, '')

export const searchRecords = records

// Distinct navigable destinations represented (for the empty-state hint).
export const searchStats = {
  total: records.length,
  pages: records.filter((r) => r.kind === 'page').length,
  references: records.filter((r) => r.kind === 'reference').length,
  api: records.filter((r) => r.kind === 'api').length,
  examples: records.filter((r) => r.kind === 'example').length
}
