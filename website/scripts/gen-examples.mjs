// Generates the website's examples data from the repo's examples/ directory.
//   node scripts/gen-examples.mjs   (also runs automatically before `npm run build`)
//
// Convention — literate comments:
//   The leading comment block at the top of each .ts file (contiguous `//`
//   lines) is harvested as the example's DESCRIPTION and rendered as prose,
//   outside the highlighted code. The rest of the file stays as code.
//
// Outputs (committed):
//   src/data/examples-tree.json     — nested category tree for the sidebar
//   src/data/examples-content.json  — per-example { title, description, code }

import { readdirSync, readFileSync, writeFileSync, statSync, mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const EXAMPLES_DIR = join(here, '..', '..', 'examples')
const DATA_DIR = join(here, '..', 'src', 'data')

// Directories that are package sources / fixtures, not standalone examples.
const EXCLUDE_DIRS = new Set(['klain_modules', 'node_modules'])

// Nice labels for category directories (acronyms etc.); fallback is Title Case.
const LABELS = {
  arrays: 'Arrays', async: 'Async / Promise', assert: 'assert', basics: 'Basics',
  bigint: 'BigInt', blob: 'Blob', buffer: 'Buffer', child_process: 'child_process',
  classes: 'Classes', closures: 'Closures', cluster: 'cluster', console: 'console',
  control_flow: 'Control flow', crypto: 'Web Crypto', date: 'Date', dgram: 'dgram',
  dns: 'dns', dynamic: 'Dynamic', encoding_text: 'Encoding / Text', enums: 'Enums',
  eventemitter: 'EventEmitter', events: 'Events', eventsource: 'EventSource',
  fetch: 'fetch', fs: 'File system', fs_async: 'fs (async)', fs_streams: 'fs (streams)',
  function_expressions: 'Function expressions', generators: 'Generators',
  generics: 'Generics', globals: 'Globals', http: 'HTTP server', http2: 'HTTP/2',
  imports: 'Modules', inspect: 'util.inspect', interfaces: 'Interfaces', json: 'JSON',
  jsdoc: 'JSDoc',
  language: 'Language', literal_expressions: 'Literal expressions', map: 'Map',
  math: 'Math', memory: 'Memory management', nested_functions: 'Nested functions',
  net: 'net', nullable_scalars: 'Nullable scalars', nullish: 'Nullish', objects: 'Objects',
  os: 'os', path: 'path', process: 'Process / CLI', process_stdin: 'Process stdin',
  querystring: 'querystring', readline: 'readline', regexp: 'RegExp', set: 'Set',
  streams: 'Streams', strings: 'Strings', tls: 'tls', tui: 'Terminal UI (klain:tui)',
  typed_arrays: 'Typed Arrays',
  url: 'URL', util: 'util', webview: 'Webview (Desktop)', websocket: 'WebSocket',
  workers: 'Workers', zlib: 'zlib'
}

function titleCase (s) {
  return s.replace(/[_-]+/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}
function catLabel (dir) {
  return LABELS[dir] || titleCase(dir)
}
function exampleTitle (file) {
  const base = file.replace(/\.ts$/, '')
  return base.replace(/[_-]+/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}

// Pull the leading `//` comment block; return { description, code }.
function splitLiterate (raw) {
  const lines = raw.replace(/\r\n/g, '\n').split('\n')
  const commentLines = []
  let i = 0
  for (; i < lines.length; i++) {
    const l = lines[i]
    if (/^\s*\/\//.test(l)) {
      commentLines.push(l.replace(/^\s*\/\/ ?/, ''))
    } else {
      break
    }
  }
  // Build description: blank comment lines become paragraph breaks. Strip the
  // internal ADR/TDD references (not meant for a public site) without mangling
  // method names like `.race` / `.all`.
  let description = commentLines.join('\n')
    .replace(/\s*\((?:see\s+)?docs\/[^)]*\)/gi, '')         // (docs/tdd/TDD-00006.md)
    .replace(/\s*\bdocs\/[a-z]+\/[A-Za-z0-9._-]+/g, '')     // bare docs/tdd/… paths
    .replace(/\s*\((?:ADR|TDD)-\d+[^)]*\)/g, '')            // (TDD-00006 Part 1)
    .replace(/\s*[—–-]\s*(?:(?:ADR|TDD)-\d+[^.\n]*)/g, '')  // — ADR-00073, TDD-00016
    .replace(/[,;]?\s*\b(?:ADR|TDD)-\d+\b/g, '')            // any leftover token
    .replace(/\(\s*\)/g, '')                                 // empty parens
    .replace(/[ \t]{2,}/g, ' ')
    .split('\n').map((s) => s.trimEnd()).join('\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim()

  // Code = everything after the leading comment block, with leading blank lines trimmed.
  let code = lines.slice(i).join('\n').replace(/^\n+/, '').replace(/\s+$/, '')
  return { description, code }
}

// Recursively collect example files into a tree.
function walk (absDir, relParts) {
  const entries = readdirSync(absDir).sort()
  const categories = []
  const examples = []
  for (const name of entries) {
    if (name.startsWith('.') || EXCLUDE_DIRS.has(name)) continue
    const abs = join(absDir, name)
    const st = statSync(abs)
    if (st.isDirectory()) {
      const child = walk(abs, [...relParts, name])
      if (child.children.length) categories.push(child)
    } else if (name.endsWith('.ts')) {
      examples.push({ name, abs })
    }
  }
  const children = []
  // examples first, then sub-categories (matches typical docs ordering)
  for (const ex of examples) {
    const key = [...relParts, ex.name.replace(/\.ts$/, '')].join('/')
    children.push({ type: 'example', key, label: exampleTitle(ex.name), file: ex.name })
  }
  for (const c of categories) children.push(c)

  const dir = relParts[relParts.length - 1] || ''
  return {
    type: 'category',
    key: relParts.join('/') || '',
    label: dir ? catLabel(dir) : 'Examples',
    children
  }
}

function main () {
  const rootRel = []
  const root = walk(EXAMPLES_DIR, rootRel)

  // Content map + collect leaves for tree (strip abs paths).
  const content = {}
  let count = 0
  const catPathLabels = {}

  function process (node, labelTrail) {
    if (node.type === 'example') {
      const abs = join(EXAMPLES_DIR, node.key + '.ts')
      const raw = readFileSync(abs, 'utf8')
      const { description, code } = splitLiterate(raw)
      content[node.key] = {
        title: node.label,
        file: node.key + '.ts',
        category: labelTrail,
        description,
        code
      }
      count++
      return { type: 'example', key: node.key, label: node.label }
    }
    // category
    const trail = node.label === 'Examples' ? [] : [...labelTrail, node.label]
    const children = node.children.map((c) => process(c, trail))
    return { type: 'category', key: node.key, label: node.label, children }
  }

  const tree = process(root, [])

  mkdirSync(DATA_DIR, { recursive: true })
  writeFileSync(join(DATA_DIR, 'examples-tree.json'), JSON.stringify(tree.children, null, 0))
  writeFileSync(join(DATA_DIR, 'examples-content.json'), JSON.stringify(content, null, 0))
  console.log(`Generated ${count} examples across ${tree.children.length} top-level categories.`)
}

main()
