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
const k: Geometry.Kind = Geometry.Kind.Tall;
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

// Two dotted namespaces each open intermediate namespaces (`A`, `A.B`) that
// have no declaration of their own; binding them used to crash.
func TestE2ENamespaceTwoDottedChains(t *testing.T) {
	assertOutput(t, `
namespace A.B.C { export function f(): number { return 1 } }
namespace X.Y.Z { export function g(): number { return 2 } }
console.log(A.B.C.f(), X.Y.Z.g())
`, "1 2")
}

// A class name declared in two namespaces is a clean rejection: namespace
// type members share one top-level name.
func TestE2ENamespaceTypeMemberClashIsError(t *testing.T) {
	_, err := resolveAndCompile(t, `
namespace a { export class c {} }
namespace d { export class c {} }
console.log(typeof a.c)
`)
	if err == nil {
		t.Fatal("expected a compile error for a class name declared in two namespaces")
	}
	if !strings.Contains(err.Error(), "more than one namespace") || strings.Contains(err.Error(), "internal error") {
		t.Errorf("unexpected error message: %v", err)
	}
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
try { Svc.ping(); } catch (e) { console.log((e as Error).message); }
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

// `export declare var` is a namespace member: declared without a value, set
// and read through the namespace.
func TestE2ENamespaceDeclareVarMember(t *testing.T) {
	assertOutput(t, `
namespace M {
    export declare var n: number
    export var k = 3
}
M.n = 5
console.log(M.n + M.k)
`, "8")
}

// A namespace merged into a function declares its members: a call of
// one is that function's, whatever it returns (ADR-01527).
func TestFunctionMergedNamespaceMemberCall(t *testing.T) {
	assertOutput(t, `
function N(x: any): void { console.log("N", x); }
namespace N { export function h(a: any): number { return a + 1; } export const v = 5; }
const k = (e: any) => N.h(e);
console.log(k(2), N.h(3) * 2, N.v);
N(1);
console.log(typeof N.h);
function C(x: any): void { if (!x) throw new Error("x"); }
namespace C { export function same(a: any, b: any): void { if (a !== b) throw new Error("ne"); } }
const p = Promise.resolve(1);
p.then((v) => C.same(v, 1)).then(() => console.log("ok"));
const g = (e: any) => C.same(e, 2);
g(2);
`, "3 8 5\nN 1\nfunction\nok")
}

func TestE2ENamespaceTypeMembersAsValues(t *testing.T) {
	// A namespace's enum or class read as a value, or indexed, is the
	// member itself: its object, its reverse mapping, its constructor.
	assertOutput(t, `
namespace Geo {
    export enum Kind { Flat = 0.5, Tall = 2 ** 40 + 0.25 }
    export class Point { constructor(public x: number) {} }
}
const k: Geo.Kind = Geo.Kind.Tall;
console.log(Geo.Kind.Flat, k / 2, Geo.Kind[0.5], Object.keys(Geo.Kind));
console.log(Geo.Kind);
const P = Geo.Point;
console.log(new P(3).x, typeof Geo.Point, Geo.Point.name);
`, "0.5 549755813888.125 Flat [ 'Flat', '0.5', 'Tall', '1099511627776.25' ]\n{\n  Flat: 0.5,\n  '0.5': 'Flat',\n  Tall: 1099511627776.25,\n  '1099511627776.25': 'Tall'\n}\n3 function Point")
}

func TestE2ENamespaceAsValue(t *testing.T) {
	// A namespace read whole is TypeScript's emitted object: its exports in
	// declaration order, nested namespaces as objects, a `let` export live,
	// one object however it is reached.
	assertOutput(t, `
namespace App {
    export namespace Config {
        export const port = 8080;
        export function f(): number { return 1; }
    }
    export let count = 0;
    export function bump(): void { count++; }
    export class Point { constructor(public x: number) {} }
    export enum Kind { A, B }
    const hidden = 1;
    export const name = "app" + hidden;
}
namespace Outer.Inner { export const v = 3; }
import Cfg = App.Config;
console.log(Object.keys(App));
console.log(App.Config);
console.log(Object.keys(Cfg), Cfg.port);
const c = App.Config;
console.log(c.port, c === App.Config);
App.bump(); App.bump();
const a: any = App;
console.log(a.count, App.count, App === a);
console.log(Object.keys(Outer), Outer.Inner.v);
const P = App.Point;
console.log(new P(2).x, App.Kind.B, App.Config.f.name);
`, "[ 'Config', 'count', 'bump', 'Point', 'Kind', 'name' ]\n{ port: 8080, f: [Function: f] }\n[ 'port', 'f' ] 8080\n8080 true\n2 2 true\n[ 'Inner' ] 3\n2 1 f")
}

func TestE2ENamespaceSiblingsAtTopLevel(t *testing.T) {
	// A namespace's own top-level statements, and member functions storing
	// to a sibling, see its members by their bare names; a `let` export
	// read through the object is live, and writes through it reach the
	// binding.
	assertOutput(t, `
namespace A {
  const s = 2;
  export const t = s * 3;
  export function g(): number { return t + 1; }
  export const u = g() + s;
  export let w = t;
  w = w + u;
  export function reset(): void { w = 0; }
}
console.log(A.t, A.u, A.w, A.g());
const o: any = A;
A.reset();
console.log(o.w, Object.getOwnPropertyDescriptor(o, "w"));
o.w = 9;
console.log(A.w, JSON.stringify(o));
`, "6 9 15 7\n0 { value: 0, writable: true, enumerable: true, configurable: true }\n9 {\"t\":6,\"u\":9,\"w\":9}")
}
