package tests

import (
	"strings"
	"testing"
)

// TS namespace declarations + function merging (TDD-00095/ADR-00290): the
// bare-call form and the namespace-member form coexist on one name, and a
// plain namespace's exported functions/consts resolve via `X.member`.
func TestE2ENamespaceFunctionMerging(t *testing.T) {
	assertOutput(t, `
function greet(name: string): string { return "hi " + name; }
namespace greet {
  export function loud(name: string): string { return "HI " + name; }
  export const version = 3;
}
console.log(greet("ann"));
console.log(greet.loud("bob"));
console.log(greet.version);
`, "hi ann\nHI bob\n3")
}

func TestE2ENamespacePlain(t *testing.T) {
	assertOutput(t, `
namespace util {
  export function twice(n: number): number { return n * 2; }
  export function shout(s: string): string { return s + "!"; }
}
console.log(util.twice(21));
console.log(util.shout("hey"));
`, "42\nhey!")
}

func TestE2ENamespaceNonExportedMemberOutsideAccessIsError(t *testing.T) {
	// TDD-00148 Stage 2: a non-exported member now *parses* (it desugars
	// like any member) but outside access stays a clean rejection.
	_, err := parseAndCompile(`
namespace util {
  function hidden(): number { return 1; }
}
console.log(util.hidden());
`)
	if err == nil {
		t.Fatal("expected a compile error for outside access to a non-exported namespace member")
	}
	if !strings.Contains(err.Error(), "not exported") {
		t.Errorf("unexpected error message: %v", err)
	}
}


// --- TDD-00148: namespaces V2 ---

func TestE2ENamespaceV2ModuleSynonymAndMembers(t *testing.T) {
	assertOutput(t, `
module Geometry {
    const SCALE: number = 2;
    function area(w: number, h: number): number {
        return w * h * SCALE;
    }
    export function describe(w: number, h: number): string {
        return "area=" + String(area(w, h));
    }
    export class Point {
        constructor(public x: number, public y: number) {}
        sum(): number { return this.x + this.y; }
    }
    export enum Kind { Flat, Tall }
}
console.log(Geometry.describe(3, 4));
const p = new Geometry.Point(1, 2);
console.log(p.sum());
const k: Kind = Kind.Tall;
console.log(k);
`, "area=24\n3\n1")
}

func TestE2ENamespaceV2ExportNamespaceTolerated(t *testing.T) {
	assertOutput(t, `
export namespace Extra {
    export function hi(): string { return "hi"; }
}
console.log(Extra.hi());
`, "hi")
}

func TestE2ENamespaceV3NestedAndDotted(t *testing.T) {
	// TDD-00148 V3: nested namespaces, dotted declarations, relative
	// sibling-namespace references, and outside `A.B.member` chains.
	assertOutput(t, `
module A {
    export module B {
        export function f(): number { return 7; }
        function hidden(): number { return 1; }
        export function g(): number { return f() + hidden(); }
    }
    export function top(): number { return B.f(); }
}
module C.D {
    export const val: number = 5;
    export function get(): number { return val; }
}
console.log(A.B.f());
console.log(A.B.g());
console.log(A.top());
console.log(C.D.get());
`, "7\n8\n7\n5")
}

func TestE2ENamespaceV2ModuleStaysAnIdentifier(t *testing.T) {
	assertOutput(t, `
const module = 5;
console.log(module + 1);
`, "6")
}

func TestE2ENamespaceImportEqualsAliases(t *testing.T) {
	// ADR-00456: import-equals aliases — namespace-scoped `export import`,
	// top-level alias of a nested namespace, and alias of a whole namespace.
	assertOutput(t, `
module M {
    export module N {
        export function hello(): string { return "hi from N"; }
    }
    export import X = N;
    export function viaAlias(): string { return X.hello(); }
}
import r = M.X;
import m2 = M;
console.log(M.viaAlias());
console.log(r.hello());
console.log(m2.N.hello());
`, "hi from N\nhi from N\nhi from N")
}

func TestE2EImportEqualsRequireIsError(t *testing.T) {
	_, err := parseAndCompile(`
import fs = require("fs");
`)
	if err == nil {
		t.Fatal("expected a clean rejection for the require() import-equals form")
	}
	if !strings.Contains(err.Error(), "use an ES import declaration") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2ENamespaceDeclareMembersErased(t *testing.T) {
	// ADR-00462: `export declare …` namespace members are ambient — erased
	// like top-level `declare`; real members alongside them still work.
	assertOutput(t, `
module M {
    export declare var x;
    export declare function f();
    export function real(): number { return 5; }
}
console.log(M.real());
`, "5")
}

func TestE2EObjectTypeConstructSignature(t *testing.T) {
	assertOutput(t, `
function bar(x: { new(): string; }): string { return x(); }
console.log(bar((): string => "made"));
`, "made")
}

func TestE2ENamespaceExecutableStatements(t *testing.T) {
	// ADR-00468: namespace-body statements run at initialization —
	// flattened to top level in declaration order; `export declare
	// namespace` at top level is ambient-erased.
	assertOutput(t, `
export declare namespace Foo {
  export var x: number;
}
let inited = 0;
module M {
    export function get(): number { return inited; }
    inited = 41;
    inited++;
}
console.log(M.get());
`, "42")
}

func TestE2EQualifiedTypeReference(t *testing.T) {
	// ADR-00470: `ns.Type` in a type position resolves to the final
	// segment's bare desugared name (the qualified-new precedent).
	assertOutput(t, `
module m {
    export interface Point { x: number; y: number; }
    export type Pair = { a: number; b: number };
}
const p: m.Point = { x: 1, y: 2 };
const q: m.Pair = { a: 3, b: 4 };
console.log(p.x + p.y + q.a + q.b);
`, "10")
}

func TestE2EAmbientNamespaceMembers(t *testing.T) {
	// ADR-00474: identifier-named `declare namespace/module` routes through
	// the real namespace parser — var members zero-init and are assignable
	// (incl. nested chains), function members are throwing stubs; the
	// string-named external-module and `global` forms stay erased.
	assertOutput(t, `
declare module Foo.Bar { export var foo; }
Foo.Bar.foo = 5;
console.log(Foo.Bar.foo);
declare namespace Svc {
    export function ping(): void;
    export var count: number;
}
console.log(Svc.count);
try { Svc.ping(); } catch (e) { console.log(e.message); }
declare module "fs-extra" { }
declare global { }
console.log("erased forms ok");
`, "5\n0\nambient function 'ping' has no implementation\nerased forms ok")
}

func TestE2ENamespaceTypeMemberChains(t *testing.T) {
	// ADR-00480: `X.Enum.Member`, `X.Class.staticMethod()`, and
	// `X.Class.staticField` resolve through the bare-desugar qualifier strip.
	assertOutput(t, `
module Shapes {
    export enum Color { Red, Green, Blue }
    export class Point {
        static origin(): string { return "0,0"; }
        static count: number = 3;
        constructor(public x: number) {}
    }
}
console.log(Shapes.Color.Green);
console.log(Shapes.Point.origin());
console.log(Shapes.Point.count);
console.log(new Shapes.Point(4).x);
`, "1\n0,0\n3\n4")
}
