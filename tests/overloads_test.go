package tests

import (
	"strings"
	"testing"
)

// --- TS overload signatures (erased at parse time; ADR pending) ---

func TestE2EFunctionOverloadSignaturesErased(t *testing.T) {
	assertOutput(t, `
function pick(x: string): string;
function pick(x: number): string;
function pick(x: any): string {
    return "got " + String(x);
}
console.log(pick("a"));
console.log(pick(5));
`, "got a\ngot 5")
}

func TestE2EMethodAndConstructorOverloadSignaturesErased(t *testing.T) {
	assertOutput(t, `
class Fmt {
    tag: string;
    constructor(tag: string);
    constructor(tag: any) { this.tag = String(tag); }
    render(x: string): string;
    render(x: number): string;
    render(x: any): string {
        return this.tag + ":" + String(x);
    }
}
const f = new Fmt("fmt");
console.log(f.render("z"));
console.log(f.render(7));
`, "fmt:z\nfmt:7")
}

func TestE2EFunctionOverloadSignatureWithoutImplementationIsError(t *testing.T) {
	_, err := parseAndCompile(`
function f(x: string): void;
console.log("hi");
`)
	if err == nil {
		t.Fatal("expected a parse error for an overload signature with no implementation")
	}
	if !strings.Contains(err.Error(), "overload signature for 'f'") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EMethodOverloadSignatureInterruptedIsError(t *testing.T) {
	_, err := parseAndCompile(`
class C {
    m(x: string): void;
    other(): void {}
}
`)
	if err == nil {
		t.Fatal("expected a parse error for an interrupted overload group")
	}
	if !strings.Contains(err.Error(), "overload signature for 'm'") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EMethodOverloadSignatureAtClassEndIsError(t *testing.T) {
	_, err := parseAndCompile(`
class C {
    m(x: string): void;
}
`)
	if err == nil {
		t.Fatal("expected a parse error for a trailing overload signature")
	}
	if !strings.Contains(err.Error(), "no implementation") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- TS constructor parameter properties (ADR-00447) ---

func TestE2EConstructorParameterProperties(t *testing.T) {
	assertOutput(t, `
class Point {
    constructor(public x: number, public y: number, private label: string, readonly id: number) {}
    describe(): string {
        return this.label + "(" + String(this.x) + "," + String(this.y) + ")#" + String(this.id);
    }
}
const p = new Point(3, 4, "pt", 7);
console.log(p.describe());
console.log(p.x + p.y);
`, "pt(3,4)#7\n7")
}

func TestE2EParameterPropertyOutsideConstructorIsError(t *testing.T) {
	_, err := parseAndCompile(`
function g(public x: number): void {}
`)
	if err == nil {
		t.Fatal("expected a parse error for a parameter property outside a constructor")
	}
	if !strings.Contains(err.Error(), "only allowed in a class constructor") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EPrivateParameterPropertyIsEnforced(t *testing.T) {
	_, err := parseAndCompile(`
class C {
    constructor(private secret: string) {}
}
const c = new C("s");
console.log(c.secret);
`)
	if err == nil {
		t.Fatal("expected a compile error for accessing a private parameter property")
	}
}

// --- Object-type method/call signatures (ADR-00448) ---

func TestE2EObjectTypeMethodSignature(t *testing.T) {
	assertOutput(t, `
type Ops = { double(n: number): number; describe(s: string): string };
function useOps(ops: Ops): string {
    return ops.describe("x") + String(ops.double(4));
}
console.log(useOps({ double: (n: number): number => n * 2, describe: (s: string): string => s + "! " }));
`, "x! 8")
}

func TestE2EObjectTypeCallSignature(t *testing.T) {
	assertOutput(t, `
function apply(fn: { (n: number): string }, n: number): string {
    return fn(n);
}
console.log(apply((n: number): string => "n=" + String(n), 5));
`, "n=5")
}

func TestE2EObjectTypeCallSignatureMixedIsError(t *testing.T) {
	_, err := parseAndCompile(`
const x: { (n: number): string; extra: number } = null;
`)
	if err == nil {
		t.Fatal("expected a parse error for a call signature mixed with other members")
	}
	if !strings.Contains(err.Error(), "call signature combined with other object-type members") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- numeric-literal types + string enum member names (ADR-00459) ---

func TestE2ENumericLiteralTypes(t *testing.T) {
	assertOutput(t, `
type Dir = -1 | 0 | 1;
const d: Dir = -1;
interface Cfg { retries: 3; mode: "fast" | "slow"; }
const c: Cfg = { retries: 3, mode: "fast" };
console.log(d + c.retries);
console.log(c.mode);
`, "2\nfast")
}

func TestE2EStringNamedEnumMember(t *testing.T) {
	assertOutput(t, `
enum E { A, B, "non identifier", C }
console.log(E.A);
console.log(E.C);
`, "0\n3")
}

// --- class index-sig erasure, comma statements, ambient enums (ADR-00476) ---

func TestE2EClassIndexSignatureErased(t *testing.T) {
	assertOutput(t, `
class C {
    [n: number]: string;
    real: number = 3;
}
console.log(new C().real);
`, "3")
}

func TestE2ECommaStatementsAndAmbientEnum(t *testing.T) {
	assertOutput(t, `
declare enum E { a, b }
console.log(E.b);
let x = 0;
x = 1, x = x + 1, console.log(x);
`, "1\n2")
}
