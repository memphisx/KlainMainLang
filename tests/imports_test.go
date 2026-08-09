package tests

import (
	"testing"
)

// --- imports / exports (multi-file) ---

func TestE2EImportFunctionsAndInterface(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"math.ts": `
export function add(a: number, b: number): number {
    return a + b
}
export function mul(a: number, b: number): number {
    return a * b
}
export interface Point { x: number; y: number }
`,
		"main.ts": `
import { add, mul } from './math'
import { Point } from './math'

console.log(add(2, 3))
console.log(mul(4, 5))

const p: Point = { x: 1, y: 2 }
console.log(p.x + p.y)
`,
	}, "main.ts", "5\n20\n3")
}

func TestE2EImportEnumAndTypeAliasThroughChain(t *testing.T) {
	// a imports from b (and also directly from c); b imports from c —
	// a 3-file, diamond-shaped import graph.
	assertMultiFileOutput(t, map[string]string{
		"c.ts": `
export enum Color { Red, Green, Blue }
export type Pair = { a: number; b: number }
`,
		"b.ts": `
import { Color, Pair } from './c'
export function describe(c: Color): string {
    if (c === Color.Red) return "red"
    return "other"
}
export function makePair(a: number, b: number): Pair {
    return { a, b }
}
`,
		"a.ts": `
import { describe, makePair } from './b'
import { Color } from './c'
console.log(describe(Color.Red))
console.log(describe(Color.Blue))
const p = makePair(10, 20)
console.log(p.a + p.b)
`,
	}, "a.ts", "red\nother\n30")
}

func TestE2EImportCircular(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"circA.ts": `
import { helperB } from './circB'
export function helperA(): number { return 1 }
export function useB(): number { return helperB() }
`,
		"circB.ts": `
import { helperA } from './circA'
export function helperB(): number { return 2 }
export function useA(): number { return helperA() }
`,
		"main.ts": `
import { useB } from './circA'
import { useA } from './circB'
console.log(useB())
console.log(useA())
`,
	}, "main.ts", "2\n1")
}

func TestE2EImportNonExportedNameRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"math.ts": `
function internalHelper(): number { return 42 }
export function add(a: number, b: number): number { return a + b }
`,
		"main.ts": `
import { internalHelper } from './math'
console.log(internalHelper())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for importing a non-exported name, got none")
	}
}

func TestE2EImportUnknownNameRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"math.ts": `export function add(a: number, b: number): number { return a + b }`,
		"main.ts": `
import { doesNotExist } from './math'
console.log(doesNotExist())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for importing a name that doesn't exist, got none")
	}
}

func TestE2EImportExecutableStatementInNonEntryFileRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"sideeffect.ts": `
export function foo(): number { return 1 }
console.log("side effect")
`,
		"main.ts": `
import { foo } from './sideeffect'
console.log(foo())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for an executable top-level statement in a non-entry file, got none")
	}
}

func TestE2EImportSameLocalNameFromTwoFilesWithoutAliasRejected(t *testing.T) {
	// TDD-00041: two different files may now freely declare the same
	// top-level name (see TestE2EImportSameNameDifferentFilesNoCollision
	// below) — but binding two different files' exports to the very same
	// local name in one importing file, with no `as` to disambiguate, is
	// still a real conflict (same as real ES modules) and still rejected.
	_, err := resolveMultiFile(t, map[string]string{
		"math.ts": `export function add(a: number, b: number): number { return a + b }`,
		"dup.ts":  `export function add(a: number, b: number): number { return a - b }`,
		"main.ts": `
import { add } from './math'
import { add } from './dup'
console.log(add(1, 2))
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for importing two different files' exports under the same local name, got none")
	}
}

func TestE2EImportSameNameDifferentFilesNoCollision(t *testing.T) {
	// TDD-00041: two unrelated files may each privately declare a
	// same-named helper without colliding — true per-file module scope.
	assertMultiFileOutput(t, map[string]string{
		"a.ts": `
function helper(): string { return "from a" }
export function useA(): string { return helper() }
`,
		"b.ts": `
function helper(): string { return "from b" }
export function useB(): string { return helper() }
`,
		"main.ts": `
import { useA } from './a'
import { useB } from './b'
console.log(useA())
console.log(useB())
`,
	}, "main.ts", "from a\nfrom b")
}

func TestE2EImportSameExportedNameFromDifferentFilesViaAliasing(t *testing.T) {
	// TDD-00041: two files may even both *export* the same name — as long
	// as an importing file that wants both disambiguates with `as`.
	assertMultiFileOutput(t, map[string]string{
		"a.ts": `export function run(): string { return "a" }`,
		"b.ts": `export function run(): string { return "b" }`,
		"main.ts": `
import { run as runA } from './a'
import { run as runB } from './b'
console.log(runA())
console.log(runB())
`,
	}, "main.ts", "a\nb")
}

func TestE2EImportNonexistentModuleRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
import { x } from './doesnotexist'
console.log(x)
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for importing a nonexistent module, got none")
	}
}

func TestE2EImportBarePackageNameRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
import { x } from 'somepackage'
console.log(x)
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a non-relative (bare) import path, got none")
	}
}

func TestE2EImportAliasing(t *testing.T) {
	// TDD-00041: import aliasing works now — a direct consequence of the
	// per-file rename mechanism (the local alias just *is* the target's
	// mangled name inside the importing file).
	assertMultiFileOutput(t, map[string]string{
		"math.ts": `export function add(a: number, b: number): number { return a + b }`,
		"main.ts": `
import { add as sum } from './math'
console.log(sum(1, 2))
`,
	}, "main.ts", "3")
}
