package tests

// --- klmpm Stage 1: compiler-side klain_modules resolution (TDD-00054) ---
//
// The compiler never reads a project's own klain.json (that's klmpm's own
// bookkeeping, not built yet) — only a package directory found under
// klain_modules and its own klain.json's "main" field. These tests
// hand-construct that directory shape directly, exercising exactly the
// resolution path a real klmpm-fetched package would hit.

import (
	"testing"
)

func TestE2EKlmpmBarePackageImportResolves(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"klain_modules/greeter/klain.json": `{"main": "index.ts"}`,
		"klain_modules/greeter/index.ts": `
export function greet(name: string): string { return "hello " + name }
`,
		"main.ts": `
import { greet } from 'greeter'
console.log(greet("world"))
`,
	}, "main.ts", "hello world")
}

func TestE2EKlmpmMainWithoutTsExtensionAutoAppends(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"klain_modules/greeter/klain.json": `{"main": "index"}`,
		"klain_modules/greeter/index.ts": `
export function greet(name: string): string { return "hi " + name }
`,
		"main.ts": `
import { greet } from 'greeter'
console.log(greet("there"))
`,
	}, "main.ts", "hi there")
}

func TestE2EKlmpmPackageOwnRelativeImportsStillWork(t *testing.T) {
	// A package's own internal files keep resolving relative imports
	// exactly like any other file — no package-awareness needed past the
	// initial bare-specifier lookup.
	assertMultiFileOutput(t, map[string]string{
		"klain_modules/greeter/klain.json": `{"main": "index.ts"}`,
		"klain_modules/greeter/util.ts": `
export function shout(s: string): string { return s.toUpperCase() }
`,
		"klain_modules/greeter/index.ts": `
import { shout } from './util'
export function greet(name: string): string { return shout("hi " + name) }
`,
		"main.ts": `
import { greet } from 'greeter'
console.log(greet("world"))
`,
	}, "main.ts", "HI WORLD")
}

func TestE2EKlmpmTransitivePackageImport(t *testing.T) {
	// One package bare-importing another package — proves the lookup isn't
	// scoped to just the entry file's own imports.
	assertMultiFileOutput(t, map[string]string{
		"klain_modules/greeter/klain.json": `{"main": "index.ts"}`,
		"klain_modules/greeter/index.ts": `
import { shout } from 'shouter'
export function greet(name: string): string { return shout("hi " + name) }
`,
		"klain_modules/shouter/klain.json": `{"main": "index.ts"}`,
		"klain_modules/shouter/index.ts": `
export function shout(s: string): string { return s.toUpperCase() }
`,
		"main.ts": `
import { greet } from 'greeter'
console.log(greet("world"))
`,
	}, "main.ts", "HI WORLD")
}

func TestE2EKlmpmResolutionAnchoredAtEntryNotImporter(t *testing.T) {
	// TDD-00054's deliberate design choice: bare-specifier resolution is
	// anchored once at the entry file's own directory, not re-walked per
	// importing file the way Node does — every file in the whole program,
	// including one inside a fetched package, resolves against the same
	// single klain_modules. A decoy nested klain_modules inside greeter/
	// must be ignored entirely; only the top-level one is ever consulted.
	assertMultiFileOutput(t, map[string]string{
		"klain_modules/greeter/klain.json": `{"main": "index.ts"}`,
		"klain_modules/greeter/index.ts": `
import { tag } from 'tagger'
export function greet(): string { return tag() }
`,
		"klain_modules/tagger/klain.json": `{"main": "index.ts"}`,
		"klain_modules/tagger/index.ts": `
export function tag(): string { return "top-level tagger" }
`,
		"klain_modules/greeter/klain_modules/tagger/klain.json": `{"main": "index.ts"}`,
		"klain_modules/greeter/klain_modules/tagger/index.ts": `
export function tag(): string { return "nested decoy tagger" }
`,
		"main.ts": `
import { greet } from 'greeter'
console.log(greet())
`,
	}, "main.ts", "top-level tagger")
}

func TestE2EKlmpmBuiltinTakesPrecedenceOverPackage(t *testing.T) {
	// A built-in module name is checked, and always wins, before any
	// klain_modules lookup is even attempted — a decoy klain_modules/fs
	// package (whose only export is `decoy`, no default export at all)
	// must never shadow the real 'fs' built-in. If the decoy somehow won,
	// `import fs from 'fs'` would fail to resolve a default export at all,
	// rather than compiling and printing "0" below.
	assertMultiFileOutput(t, map[string]string{
		"klain_modules/fs/klain.json": `{"main": "index.ts"}`,
		"klain_modules/fs/index.ts": `
export function decoy(): string { return "not the real fs" }
`,
		"main.ts": `
import fs from 'fs'
console.log(fs.existsSync('/definitely/does/not/exist/kml_test'))
`,
	}, "main.ts", "false")
}

func TestE2EKlmpmMissingPackageRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"klain_modules/greeter/klain.json": `{"main": "index.ts"}`,
		"klain_modules/greeter/index.ts":   `export function greet(): string { return "hi" }`,
		"main.ts": `
import { x } from 'doesnotexist'
console.log(x)
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error importing a package not present in klain_modules, got none")
	}
}

func TestE2EKlmpmMissingManifestRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"klain_modules/greeter/index.ts": `export function greet(): string { return "hi" }`,
		"main.ts": `
import { greet } from 'greeter'
console.log(greet())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a package directory with no klain.json, got none")
	}
}

func TestE2EKlmpmManifestMissingMainFieldRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"klain_modules/greeter/klain.json": `{"name": "greeter"}`,
		"klain_modules/greeter/index.ts":   `export function greet(): string { return "hi" }`,
		"main.ts": `
import { greet } from 'greeter'
console.log(greet())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a klain.json with no \"main\" field, got none")
	}
}

func TestE2EKlmpmMainFileMissingRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"klain_modules/greeter/klain.json": `{"main": "does_not_exist.ts"}`,
		"main.ts": `
import { greet } from 'greeter'
console.log(greet())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a \"main\" field pointing at a nonexistent file, got none")
	}
}

func TestE2EKlmpmInvalidManifestJSONRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"klain_modules/greeter/klain.json": `{ not valid json `,
		"klain_modules/greeter/index.ts":   `export function greet(): string { return "hi" }`,
		"main.ts": `
import { greet } from 'greeter'
console.log(greet())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for invalid JSON in klain.json, got none")
	}
}

func TestE2EKlmpmNoKlainModulesDirBarePackageStillRejected(t *testing.T) {
	// No klain_modules directory anywhere above the entry file at all —
	// behavior is unchanged from before this TDD.
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
import { x } from 'somepackage'
console.log(x)
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a bare import with no klain_modules directory present, got none")
	}
}
