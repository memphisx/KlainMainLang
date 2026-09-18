// Shared collector for the conformance data feed. Both gen-conformance.mjs (writes
// the committed website copy) and check-conformance.mjs (fails the build if that
// copy drifted) import this, so the two can never disagree about shape or order.
//
// Source of truth: docs/testing/<platform>/conformance-summary.json — one per
// platform, each emitted by tools/conformance/summary.go (see there for the
// per-suite lane shapes). The website publishes EVERY platform that has a
// committed summary, primary dev platform first, so the marketing page can tab
// across them and consumers can treat platforms[0] as the headline.

import { readFileSync, existsSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
export const TESTING_DIR = join(here, '..', '..', 'docs', 'testing')
export const OUT = join(here, '..', 'src', 'data', 'conformance-platforms.json')

// Primary dev platform first; any other platform with a summary follows in a
// stable (alphabetical) order so the output is deterministic across machines.
const PLATFORM_PRIORITY = ['macos-arm64', 'linux-x64', 'linux-arm64', 'windows-x64']

// Read every docs/testing/<platform>/conformance-summary.json into the shape the
// website consumes: { schemaVersion, platforms: [{ platform, suites }, …] }.
export function collectPlatforms () {
  const dirs = existsSync(TESTING_DIR)
    ? readdirSync(TESTING_DIR, { withFileTypes: true }).filter((d) => d.isDirectory()).map((d) => d.name)
    : []
  const known = PLATFORM_PRIORITY.filter((p) => dirs.includes(p))
  const extra = dirs.filter((p) => !PLATFORM_PRIORITY.includes(p)).sort()
  const ordered = [...known, ...extra]

  const platforms = []
  let schemaVersion = 1
  for (const platform of ordered) {
    const f = join(TESTING_DIR, platform, 'conformance-summary.json')
    if (!existsSync(f)) continue
    const summary = JSON.parse(readFileSync(f, 'utf8'))
    if (typeof summary.schemaVersion === 'number') schemaVersion = summary.schemaVersion
    platforms.push({ platform, suites: summary.suites ?? {} })
  }
  return { schemaVersion, platforms }
}

// Canonical serialization — stable and minified, the numbers not formatting are
// what matter. Both the writer and the drift-check compare this exact string.
export function serialize (doc) {
  return JSON.stringify(doc)
}
