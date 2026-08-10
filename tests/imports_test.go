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

func TestE2EImportExecutableStatementInAcyclicNonEntryFileRuns(t *testing.T) {
	// TDD-00052: an acyclic imported file's top-level executable code now
	// really runs, once, in dependency order — strictly before the entry
	// file's own top-level statements.
	assertMultiFileOutput(t, map[string]string{
		"sideeffect.ts": `
export function foo(): number { return 1 }
console.log("side effect")
`,
		"main.ts": `
import { foo } from './sideeffect'
console.log(foo())
`,
	}, "main.ts", "side effect\n1")
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

// --- export default / default imports / namespace imports (TDD-00042) ---

func TestE2EExportDefaultNamedFunction(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"greet.ts": `
export default function greet(name: string): string {
    return "hello " + name
}
`,
		"main.ts": `
import greet from './greet'
console.log(greet("world"))
`,
	}, "main.ts", "hello world")
}

func TestE2EExportDefaultAnonymousFunction(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"greet.ts": `
export default function(name: string): string {
    return "hi " + name
}
`,
		"main.ts": `
import greet from './greet'
console.log(greet("there"))
`,
	}, "main.ts", "hi there")
}

func TestE2EExportDefaultAnonymousClass(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"widget.ts": `
export default class {
    label(): string { return "anon widget" }
}
`,
		"main.ts": `
import Widget from './widget'
const w = new Widget()
console.log(w.label())
`,
	}, "main.ts", "anon widget")
}

func TestE2EExportDefaultNamedClassSelfReferenceStillWorks(t *testing.T) {
	// A named default export keeps its own name usable for the rest of the
	// file (recursion, self-reference) — only the *export* is exposed
	// solely under "default", not the name itself (TDD-00042).
	assertMultiFileOutput(t, map[string]string{
		"counter.ts": `
export default class Counter {
    static make(): Counter { return new Counter() }
    value(): number { return 42 }
}
`,
		"main.ts": `
import Counter from './counter'
const c = Counter.make()
console.log(c.value())
`,
	}, "main.ts", "42")
}

func TestE2EExportDefaultExpression(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"answer.ts": `
export default 42
`,
		"main.ts": `
import answer from './answer'
console.log(answer)
`,
	}, "main.ts", "42")
}

func TestE2EExportDefaultCombinedWithNamedImport(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"math.ts": `
export function add(a: number, b: number): number { return a + b }
export default function square(x: number): number { return x * x }
`,
		"main.ts": `
import square, { add } from './math'
console.log(square(5))
console.log(add(2, 3))
`,
	}, "main.ts", "25\n5")
}

func TestE2EExportDefaultNamedExportNotAlsoAvailableByName(t *testing.T) {
	// Real ES modules: `export default function foo() {}` does not also
	// make `foo` importable via `import { foo }`.
	_, err := resolveMultiFile(t, map[string]string{
		"lib.ts": `export default function foo(): number { return 1 }`,
		"main.ts": `
import { foo } from './lib'
console.log(foo())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error importing a default export by its own declared name, got none")
	}
}

func TestE2EExportDefaultTwiceInOneFileRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"lib.ts": `
export default function foo(): number { return 1 }
export default function bar(): number { return 2 }
`,
		"main.ts": `
import x from './lib'
console.log(x())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for two 'export default' in the same file, got none")
	}
}

func TestE2ENamespaceImportFunctionsAndDefault(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"lib.ts": `
export function add(a: number, b: number): number { return a + b }
export default function greet(name: string): string { return "hello " + name }
`,
		"main.ts": `
import * as lib from './lib'
console.log(lib.add(10, 20))
console.log(lib.default("via-ns"))
`,
	}, "main.ts", "30\nhello via-ns")
}

func TestE2ENamespaceImportClassStaticMethod(t *testing.T) {
	// `new ns.SomeClass(...)` isn't reachable through a namespace import —
	// not a namespace-specific gap: parser.parseNew already requires a
	// single IDENT after `new`, with no member-expression form at all,
	// regardless of namespaces (see TDD-00042's Design section). A class's
	// *static* member access (`ns.SomeClass.method()`) has no such
	// restriction: `ns.SomeClass` alone resolves to a plain identifier
	// (the class's mangled name) before `.method()` is ever considered.
	assertMultiFileOutput(t, map[string]string{
		"shapes.ts": `
export class Squares {
    static area(side: number): number { return side * side }
}
`,
		"main.ts": `
import * as shapes from './shapes'
console.log(shapes.Squares.area(4))
`,
	}, "main.ts", "16")
}

func TestE2ENamespaceImportNonExportedMemberRejected(t *testing.T) {
	// Only actually-exported members are reachable through the namespace
	// object — same visibility rule a named `import { x }` is held to.
	// Resolution itself doesn't catch this (an unresolved "ns.member"
	// simply reaches codegen as a member access with no matching type),
	// so this asserts through codegen too, like the bare-use case above.
	err := resolveAndEmitMultiFile(t, map[string]string{
		"lib.ts": `
function internalHelper(): number { return 1 }
export function add(a: number, b: number): number { return a + b }
`,
		"main.ts": `
import * as lib from './lib'
console.log(lib.internalHelper())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error accessing a non-exported member through a namespace import, got none")
	}
}

func TestE2ENamespaceImportBareUseRejected(t *testing.T) {
	// A namespace import is a compile-time-only construct (TDD-00042) —
	// it has no runtime representation, so using it as anything other than
	// the object of a dotted member access is rejected. Resolution itself
	// doesn't catch this (there's no special-case detection — see the
	// TDD's Open Questions); the bare "lib" identifier simply reaches
	// codegen unresolved and fails there.
	err := resolveAndEmitMultiFile(t, map[string]string{
		"lib.ts": `export function add(a: number, b: number): number { return a + b }`,
		"main.ts": `
import * as lib from './lib'
console.log(lib)
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error using a namespace import as a bare value, got none")
	}
}

// --- re-exports (TDD-00051) ---

func TestE2EReExportNamed(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"core.ts": `export function add(a: number, b: number): number { return a + b }`,
		"lib.ts":  `export { add } from './core'`,
		"main.ts": `
import { add } from './lib'
console.log(add(2, 3))
`,
	}, "main.ts", "5")
}

func TestE2EReExportAliased(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"core.ts": `export function add(a: number, b: number): number { return a + b }`,
		"lib.ts":  `export { add as sum } from './core'`,
		"main.ts": `
import { sum } from './lib'
console.log(sum(4, 5))
`,
	}, "main.ts", "9")
}

func TestE2EReExportDefaultAsDefault(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"core.ts": `export default function greet(name: string): string { return "hi " + name }`,
		"lib.ts":  `export { default } from './core'`,
		"main.ts": `
import greet from './lib'
console.log(greet("world"))
`,
	}, "main.ts", "hi world")
}

func TestE2EReExportDefaultAsNamed(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"core.ts": `export default function greet(name: string): string { return "hi " + name }`,
		"lib.ts":  `export { default as greet } from './core'`,
		"main.ts": `
import { greet } from './lib'
console.log(greet("there"))
`,
	}, "main.ts", "hi there")
}

func TestE2EReExportNamedAsDefault(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"core.ts": `export function greet(name: string): string { return "hey " + name }`,
		"lib.ts":  `export { greet as default } from './core'`,
		"main.ts": `
import greet from './lib'
console.log(greet("you"))
`,
	}, "main.ts", "hey you")
}

func TestE2EReExportStar(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"core.ts": `
export function add(a: number, b: number): number { return a + b }
export function mul(a: number, b: number): number { return a * b }
`,
		"lib.ts": `export * from './core'`,
		"main.ts": `
import { add, mul } from './lib'
console.log(add(2, 3))
console.log(mul(2, 3))
`,
	}, "main.ts", "5\n6")
}

func TestE2EReExportStarExcludesDefault(t *testing.T) {
	// A star re-export never forwards a default export — real ES module
	// semantics.
	_, err := resolveMultiFile(t, map[string]string{
		"core.ts": `
export default function greet(): string { return "hi" }
export function add(a: number, b: number): number { return a + b }
`,
		"lib.ts": `export * from './core'`,
		"main.ts": `
import greet from './lib'
console.log(greet())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error importing a default through a star re-export, got none")
	}
}

func TestE2EReExportChain(t *testing.T) {
	// b re-exports from a, c re-exports from b — an arbitrarily deep chain
	// should resolve transitively.
	assertMultiFileOutput(t, map[string]string{
		"a.ts": `export function add(x: number, y: number): number { return x + y }`,
		"b.ts": `export { add } from './a'`,
		"c.ts": `export { add as sum } from './b'`,
		"main.ts": `
import { sum } from './c'
console.log(sum(7, 8))
`,
	}, "main.ts", "15")
}

func TestE2EReExportViaNamespaceImport(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"core.ts": `export function add(a: number, b: number): number { return a + b }`,
		"lib.ts":  `export { add } from './core'`,
		"main.ts": `
import * as lib from './lib'
console.log(lib.add(1, 2))
`,
	}, "main.ts", "3")
}

func TestE2EReExportDoesNotBindLocalName(t *testing.T) {
	// `export { x } from './other'` forwards x to importers of this file —
	// it does not introduce x as a usable bare identifier inside the
	// re-exporting file itself, matching real ES modules. Not caught at
	// resolve time (there's no dedicated check — an identifier the lookup
	// table doesn't know about is simply left unrenamed), so this asserts
	// through codegen, same pattern as the namespace bare-use case above.
	err := resolveAndEmitMultiFile(t, map[string]string{
		"core.ts": `export function add(a: number, b: number): number { return a + b }`,
		"lib.ts": `
export { add } from './core'
export function useAdd(): number { return add(1, 2) }
`,
		"main.ts": `
import { useAdd } from './lib'
console.log(useAdd())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error referencing a re-exported name as a bare local identifier, got none")
	}
}

func TestE2EReExportUnknownMemberRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"core.ts": `export function add(a: number, b: number): number { return a + b }`,
		"lib.ts":  `export { doesNotExist } from './core'`,
		"main.ts": `
import { doesNotExist } from './lib'
console.log(doesNotExist())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error re-exporting a name that doesn't exist, got none")
	}
}

func TestE2EReExportCollisionWithOwnExportRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"core.ts": `export function add(a: number, b: number): number { return a + b }`,
		"lib.ts": `
export function add(a: number, b: number): number { return a - b }
export { add } from './core'
`,
		"main.ts": `
import { add } from './lib'
console.log(add(1, 2))
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a re-export colliding with this file's own export, got none")
	}
}

func TestE2EReExportFromBuiltinModuleRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
export { readFileSync } from 'fs'
console.log("unused")
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error re-exporting from a built-in module, got none")
	}
}

func TestE2EReExportNamespaceStarNotSupported(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"core.ts": `export function add(a: number, b: number): number { return a + b }`,
		"main.ts": `
export * as core from './core'
console.log("unused")
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for 'export * as ns from', got none")
	}
}

// --- top-level side-effecting code in imported files (TDD-00052) ---

func TestE2ETopLevelSideEffectDependencyOrderChain(t *testing.T) {
	// A 3-level acyclic chain — each file's top-level code must run once,
	// strictly before whatever imports it, entry file last.
	assertMultiFileOutput(t, map[string]string{
		"a.ts": `
console.log("a")
export function markerA(): string { return "A" }
`,
		"b.ts": `
import { markerA } from './a'
console.log("b:" + markerA())
export function markerB(): string { return "B" }
`,
		"main.ts": `
import { markerB } from './b'
console.log("entry:" + markerB())
`,
	}, "main.ts", "a\nb:A\nentry:B")
}

func TestE2ETopLevelSideEffectDiamondRunsOnce(t *testing.T) {
	// d.ts is reached from both b.ts and c.ts — its top-level code must run
	// exactly once, before either of them, not twice.
	assertMultiFileOutput(t, map[string]string{
		"d.ts": `
console.log("d")
export function tag(): string { return "D" }
`,
		"b.ts": `
import { tag } from './d'
console.log("b")
export function useB(): string { return "B" + tag() }
`,
		"c.ts": `
import { tag } from './d'
console.log("c")
export function useC(): string { return "C" + tag() }
`,
		"main.ts": `
import { useB } from './b'
import { useC } from './c'
console.log(useB())
console.log(useC())
`,
	}, "main.ts", "d\nb\nc\nBD\nCD")
}

func TestE2ECyclicFileBareStatementStillRejected(t *testing.T) {
	// A file that genuinely participates in an import cycle keeps the
	// original declarations-only restriction — only the acyclic case gets
	// full top-level statement freedom.
	_, err := resolveMultiFile(t, map[string]string{
		"circA.ts": `
import { helperB } from './circB'
export function helperA(): number { return 1 }
console.log("side effect in cyclic file")
`,
		"circB.ts": `
import { helperA } from './circA'
export function helperB(): number { return 2 }
`,
		"main.ts": `
import { helperA } from './circA'
console.log(helperA())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a bare executable statement in a cyclic file, got none")
	}
}

func TestE2ECyclicFileNonLiteralVarInitRejected(t *testing.T) {
	// The bug this TDD closes: circA's top-level `valueA` initializer reads
	// circB's top-level `valueB` — before TDD-00052, this was already legal
	// syntax (VarDeclaration was unconditionally "a declaration" regardless
	// of its Init), and could read an uninitialized binding across the
	// cycle. Now rejected at compile time instead.
	_, err := resolveMultiFile(t, map[string]string{
		"circA.ts": `
import { valueB } from './circB'
export const valueA: number = valueB + 1
`,
		"circB.ts": `
import { valueA } from './circA'
export const valueB: number = 10
`,
		"main.ts": `
import { valueA } from './circA'
console.log(valueA)
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a non-literal top-level var initializer in a cyclic file, got none")
	}
}

func TestE2ECyclicFileLiteralVarInitStillAllowed(t *testing.T) {
	// A cyclic file's top-level var/let/const stays usable as long as its
	// initializer is a compile-time literal — it can't observe any
	// not-yet-run initialization from anywhere, so it's always safe.
	assertMultiFileOutput(t, map[string]string{
		"circA.ts": `
import { helperB } from './circB'
export function helperA(): number { return 1 }
export const MAX = 100
`,
		"circB.ts": `
import { helperA } from './circA'
export function helperB(): number { return 2 }
`,
		"main.ts": `
import { helperA, MAX } from './circA'
import { helperB } from './circB'
console.log(helperA())
console.log(helperB())
console.log(MAX)
`,
	}, "main.ts", "1\n2\n100")
}
