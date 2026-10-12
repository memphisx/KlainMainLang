package tests

import (
	"testing"

	"KlainMainLang/codegen/llvm"
)

// A builtin library module's unit compiles to the same IR whatever the
// program around it does, so its object is compiled once and shared
// (BACKLOG 156): a program that makes bigints and declares a class
// extending Array does not change it.
func TestLibUnitsSameAcrossPrograms(t *testing.T) {
	const imports = `import { EventEmitter } from "node:events";
import { spawnSync } from "node:child_process";
import * as util from "node:util";
`
	plain := imports + `
const e = new EventEmitter();
e.on("x", (v: any) => console.log(util.format("%s", v)));
e.emit("x", spawnSync("echo", ["hi"]).status);
`
	rich := imports + `
class Args extends Array<string> {
  slice(start?: number, end?: number): string[] { return super.slice(start, end); }
}
const a = new Args(); a.push("hi");
const big: any = 10n;
const e = new EventEmitter();
e.on("x", (v: any) => console.log(util.format("%s", v), big * big));
e.emit("x", spawnSync("echo", a).status);
`
	units := func(src string) map[string]string {
		ir, err := resolveAndCompile(t, src)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		u, err := llvm.SplitLibUnits(ir)
		if err != nil {
			t.Fatalf("split: %v", err)
		}
		return u.Modules
	}
	a, b := units(plain), units(rich)
	shared := 0
	for k, ua := range a {
		ub, ok := b[k]
		if !ok {
			continue
		}
		shared++
		if ua != ub {
			t.Errorf("library unit %s differs between the two programs", k)
		}
	}
	if shared == 0 {
		t.Fatal("the programs share no library unit")
	}
}

// The library calls Array's own methods on an array the program passes in,
// as Node's internals do through primordials: a subclass's override is not
// run, and the instance is accepted as the array it is.
func TestLibArraySubclassArgument(t *testing.T) {
	assertSameAsNodeImports(t, `import { spawnSync } from "node:child_process";
class Args extends Array<string> {
  slice(start?: number, end?: number): string[] { console.log("override slice"); return super.slice(start, end); }
}
const a = new Args(); a.push("hi");
const r = spawnSync("echo", a);
console.log(String(r.stdout).trim(), Array.isArray(a), a);
`)
}

// An Array method a subclass overrides with an array result, reached
// through a dynamic value; the override's super call makes the result with
// the subclass (species), and an instance initializes an array variable.
func TestArraySubclassOverrideArrayResultDynamic(t *testing.T) {
	assertSameAsNodeImports(t, `import * as util from "node:util";
class P extends Array<string> {
  filter(f: (v: string) => boolean): string[] { const r = super.filter(f); console.log("in", r); return r; }
}
class R extends Array<string> {
  map(f: (v: string) => string): string[] { return []; }
  up(): string[] { return super.filter((s: string) => s == "a"); }
}
const p = new P(); p.push("a", "b");
const d: any = p;
console.log(d.filter((s: string) => s == "b"));
const x: string[] = p;
console.log(x, util.format("%s", p));
const r = new R(); r.push("a", "c");
console.log(r.up());
`)
}

// An array the C runtime makes (String.prototype.split's) carries Array's
// header word: in a program that declares a class extending Array it is
// still a plain array, through `any` as well.
func TestSplitResultPlainArrayWithArrayClass(t *testing.T) {
	assertSameAsNode(t, `class Zq extends Array<number> {}
const parts = "a\nb".split("\n");
const d: any = parts;
console.log(d);
const x: string[] = d;
console.log(x, new Zq().length);
`)
}
