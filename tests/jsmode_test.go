package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
)

// --- TDD-00022 JS-compat mode (`-compat=js`): constructor-inferred class
// fields, call-site-inferred constructor parameters, and suppressed
// definite-assignment. Strict mode must stay byte-identical. ---

// compileCompatJS runs parse+codegen under -compat=js (no clang), for
// negative tests asserting clean JS-mode rejections.
func compileCompatJS(src string) (string, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return "", err
	}
	em := llvm.NewEmitter()
	em.SetCompatMode("js")
	return em.EmitProgram(prog)
}

func TestE2EJSModeClassFieldsFromConstructor(t *testing.T) {
	assertOutputCompatJS(t, `
class Point {
  constructor(x, y) {
    this.x = x
    this.y = y
    this.label = "pt"
  }
  dist() { return Math.sqrt(this.x * this.x + this.y * this.y) }
  move(dx) { this.x = this.x + dx }
}
const p = new Point(3, 4)
console.log(p.x, p.y, p.label)
console.log(p.dist())
p.move(2)
console.log(p.x)
`, "3 4 pt\n5\n5")
}

func TestE2EJSModeCtorParamCallSiteInference(t *testing.T) {
	// Unannotated `name`/`breed` are typed string from the `new` sites —
	// the vanilla-JS inheritance shape works end to end.
	assertOutputCompatJS(t, `
class Animal {
  constructor(name) { this.name = name; this.alive = true }
  speak() { return this.name + " makes a sound" }
}
class Dog extends Animal {
  constructor(name, breed) { super(name); this.breed = breed }
  speak() { return this.name + " barks" }
}
const d = new Dog("Rex", "lab")
console.log(d.speak(), d.breed, d.alive)
const a = new Animal("Cat")
console.log(a.speak())
`, "Rex barks lab true\nCat makes a sound")
}

// A constructor storing values of different types in one field: the field
// holds any, as JavaScript allows (checkJs types it the same way).
func TestE2EJSModeCtorFieldTypeConflict(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
class C { constructor(f) { this.v = 1; if (f) this.v = "s" } }
console.log(new C(0).v, new C(1).v, typeof new C(1).v)
`)
}

func TestE2EJSModeConflictingCallSitesWidenToAny(t *testing.T) {
	// A constructor parameter whose call sites disagree on type is widened to
	// `any`, mirroring the plain-function behavior (see the polymorphic-param
	// test) — vanilla JS lets a constructor receive different types at
	// different sites, and -compat=js must be at least as permissive as strict
	// (which has no call-site-inference pass and no such rejection). Previously
	// this hard-errored, which made the js lane fail files the strict lane
	// accepts (the Test262 harness's own `new Test262Error('msg')` vs
	// `new Test262Error('msg' + n)`). A function-constructor's `this.field`
	// lives on a D1 dynamic object, which stores the widened `any`.
	assertOutputCompatJS(t, `
function Box(v) { this.v = v }
const a = new Box(1)
const b = new Box("s")
console.log(a.v)
console.log(b.v)
`, "1\ns")
}

func TestJSModeMethodIntroducedProperty(t *testing.T) {
	// A method adding a property gives the instance an own property, as in
	// JavaScript (the constructor's assignments stay the class's fields).
	assertSameAsNodeCompatJS(t, `
class C { constructor() { this.a = 1 } grow() { this.b = 2 } }
const c = new C()
console.log(c.b)
c.grow()
console.log(c.a, c.b, Object.keys(c))
`)
}

func TestStrictModeClassFieldInferenceOff(t *testing.T) {
	// Strict mode is untouched: the same vanilla class stays a clean error.
	_, err := parseAndCompile(`
class Point { constructor(x, y) { this.x = x } }
new Point(1, 2)
`)
	// tsc's TS2339: the class declares no `x`.
	if err == nil || !strings.Contains(err.Error(), "property 'x' does not exist on type 'Point'") {
		t.Fatalf("expected strict-mode no-field rejection, got: %v", err)
	}
}

func TestStrictCtorUnannotatedParamIsImplicitAny(t *testing.T) {
	// An unannotated constructor parameter is TS7006 in the strict lane
	// (ADR-01428).
	_, err := parseAndCompile(`
class C { v: number; constructor(v) { this.v = v } }
new C("oops")
`)
	if err == nil || !strings.Contains(err.Error(), "parameter 'v' implicitly has an 'any' type") {
		t.Fatalf("expected TS7006, got: %v", err)
	}
}

func TestE2EJSModeFunctionParamCallSiteInference(t *testing.T) {
	// TDD-00022 sub-problem 1, plain-function slice: unannotated parameters
	// take the types their call sites actually pass — including mixed
	// string/number params, and calls before the site that types them.
	assertOutputCompatJS(t, `
console.log(greet("early"))
function greet(name) { return "hello " + name }
function add(a, b) { return a + b }
function tag(label, n) { return label + ": " + n }
console.log(greet("world"))
console.log(add(2, 3))
console.log(tag("count", 7))
`, "hello early\nhello world\n5\ncount: 7")
}

func TestE2EJSModeFunctionClassInstanceArg(t *testing.T) {
	// An identifier argument bound at top level to a class instance types
	// the parameter via the top-level-binding map (post-registerClasses).
	assertOutputCompatJS(t, `
class City { constructor(name) { this.name = name } }
const home = new City("Thessaloniki")
function shout(city) { return city.name + "!" }
console.log(shout(home))
`, "Thessaloniki!")
}

func TestE2EJSModePolymorphicParamBecomesAny(t *testing.T) {
	// Call sites disagreeing on a parameter's type no longer error under
	// `-compat=js`: the parameter is genuinely polymorphic — implicit-`any`
	// (TDD-00076 A1) boxes it and the runtime dispatch handles both.
	assertOutputCompatJS(t, `
function f(v) { return v }
console.log(f(1))
console.log(f("s"))
console.log(f(2) + 1)
console.log(f("con") + "cat")
`, "1\ns\n3\nconcat")
}

func TestJSModeBuiltinMixedArgsUnaffected(t *testing.T) {
	// Builtins share the callee namespace with user functions — mixed-type
	// builtin calls must never trip the conflict validation.
	assertOutputCompatJS(t, `
console.log(1, "mixed", true)
console.log("still fine")
`, "1 mixed true\nstill fine")
}

func TestStrictFunctionUnannotatedParamIsImplicitAny(t *testing.T) {
	_, err := parseAndCompile(`
function greet(name) { return "hello " + name }
console.log(greet("world"))
`)
	if err == nil || !strings.Contains(err.Error(), "parameter 'name' implicitly has an 'any' type") {
		t.Fatalf("expected TS7006, got: %v", err)
	}
}

// --- D1 Stage 4 (TDD-00155): vanilla-JS prototype classes under -compat=js ---

func TestE2EJSModeProtoClassBasics(t *testing.T) {
	assertOutputCompatJS(t, `
function Animal(name) {
  this.name = name
  this.alive = true
}
Animal.prototype.speak = function() { return this.name + " makes a sound" }
const a = new Animal("Cat")
console.log(a.speak(), a.name, a.alive)
console.log(typeof a, typeof a.speak)
`, "Cat makes a sound Cat true\nobject function")
}

func TestE2EJSModeProtoClassInheritance(t *testing.T) {
	// The classic pre-ES6 chain: Base.call(this, ...), Object.create of the
	// base prototype, method override, and inherited dispatch.
	assertOutputCompatJS(t, `
function Animal(name) { this.name = name }
Animal.prototype.speak = function() { return this.name + " makes a sound" }
Animal.prototype.who = function() { return this.name }
function Dog(name, breed) {
  Animal.call(this, name)
  this.breed = breed
}
Dog.prototype = Object.create(Animal.prototype)
Dog.prototype.speak = function() { return this.name + " barks" }
const d = new Dog("Rex", "lab")
console.log(d.speak(), d.breed, d.who())
console.log(Object.keys(d))
console.log(d.__proto__ === Dog.prototype, Object.getPrototypeOf(Dog.prototype) === Animal.prototype)
`, "Rex barks lab Rex\n[ 'name', 'breed' ]\ntrue true")
}

func TestE2EJSModeProtoCtorArity(t *testing.T) {
	// A constructor function called with fewer arguments reads the missing
	// ones as undefined; an extra one is evaluated, then dropped — through
	// new and through Base.call(this, ...) alike.
	assertSameAsNodeCompatJS(t, `
function P(a, b) { this.a = a; this.b = b }
let calls = 0
function side() { calls++; return 9 }
const p = new P()
const q = new P(1)
const r = new P(1, 2, side())
console.log(p.a, p.b, q.a, q.b === undefined, r.a, r.b, calls, typeof p.a)
function Q(a, b, c) { P.call(this, a); this.c = c }
Q.prototype = Object.create(P.prototype)
const s = new Q(5)
console.log(s.a, s.b, s.c, Object.keys(s))
`)
}

func TestE2EJSModeProtoMethodArgs(t *testing.T) {
	// Method params are boxed; a missing argument reads undefined, and a
	// missing method is undefined to read but TypeError to call — all JS.
	assertOutputCompatJS(t, `
function Greeter(name) { this.name = name }
Greeter.prototype.greet = function(greeting, punct) {
  return greeting + ", " + this.name + punct
}
const g = new Greeter("Thessaloniki")
console.log(g.greet("hello", "!"))
console.log(g.greet("hi"))
console.log(g.nope === undefined)
try { g.nope() } catch (e) { console.log("caught:", e.message) }
`, "hello, Thessaloniki!\nhi, Thessalonikiundefined\ntrue\ncaught: g.nope is not a function")
}

func TestStrictModeProtoClassesOff(t *testing.T) {
	// Strict mode is untouched: `this` in a plain function stays an error —
	// tsc's TS2683.
	_, err := parseAndCompile(`
function Animal(name) { this.name = name }
new Animal("x")
`)
	if err == nil || !strings.Contains(err.Error(), "'this' implicitly has type 'any'") {
		t.Fatalf("expected strict-mode this rejection, got: %v", err)
	}
}

func TestE2EJSModeStringConcatWithDynamic(t *testing.T) {
	assertOutputCompatJS(t, `
let v: any = 42
console.log("n=" + v)
v = "str"
console.log(v + "!")
v = null
console.log("v is " + v)
`, "n=42\nstr!\nv is null")
}

func TestJSModeDefiniteAssignmentSuppressed(t *testing.T) {
	src := `
let x: number
const c = 1 > 0
if (c) { x = 1 }
if (!c) { x = 2 }
console.log(x)
`
	// Strict: the TS definite-assignment early error.
	if _, err := resolveMultiFile(t, map[string]string{"main.ts": src}, "main.ts"); err == nil || !strings.Contains(err.Error(), "used before being assigned") {
		t.Fatalf("expected strict definite-assignment error, got: %v", err)
	}
	// -compat=js: plain JS has no such concept — resolves cleanly.
	if _, err := resolveMultiFilePermissive(t, map[string]string{"main.ts": src}, "main.ts"); err != nil {
		t.Fatalf("expected -compat=js to suppress definite-assignment, got: %v", err)
	}
}

// --- TDD-00162: -compat=js cross-type binding widening ---

func TestE2EJSModeCrossTypeBindingWidening(t *testing.T) {
	// An untyped binding assigned two or more distinct scalar kinds over its
	// lifetime is backed by the any-box under -compat=js (crossTypeWidenedBindings)
	// — the JS-faithful "dynamic variable" — where strict rejects it (ADR-00651).
	// Covers an initialized binding (let x = 5; x = 'hi'), three kinds, a
	// module-global widened binding read by a named function, and that a
	// single-kind binding is NOT widened (stays a plain number, arithmetic works).
	assertOutputCompatJS(t, `
let x = 5
x = 'hi'
console.log(x)

let t = 1
t = 'two'
t = true
console.log(typeof t, t)

let g = 10
g = 'later'
function show() { console.log(g) }
show()

let n = 1
n = 2
console.log(n + 10)
`, "hi\nboolean true\nlater\n12")
}

func TestE2EJSModeCrossTypeWideningInFunction(t *testing.T) {
	// The same widening inside a function scope (its own pre-pass).
	assertOutputCompatJS(t, `
function f() {
  let v = 42
  v = 'answer'
  console.log(v)
}
f()
`, "answer")
}

func TestE2EStrictRejectsCrossTypeReassignment(t *testing.T) {
	// The strict-lane counterpart (ADR-00651): the same program is a clean
	// rejection, not widened and not invalid IR. Strict must stay opinionated.
	if _, err := parseAndCompile(`let x = 5; x = 'hi';`); err == nil {
		t.Fatal("strict must reject an incompatible reassignment")
	}
}

// TestE2EClosureCapturesInsideTryForInThrow exercises the free-variable
// scanner fix (ADR-00662): scanStmtsFV/capScanStmts previously had no case for
// try/for-in/throw/do-while/labeled statements, so a variable referenced free
// *only* inside one of those constructs was never detected as captured — the
// closure then failed to resolve it (undefined variable / wrong slot). This is
// a mode-independent capture-correctness fix.
func TestE2EClosureCapturesInsideTry(t *testing.T) {
	assertOutput(t, `
let n: number = 10
const f = (): number => {
  try {
    return n + 1
  } catch (e) {
    return -1
  }
}
console.log(f())
`, "11")
}

func TestE2EClosureCapturesInsideForInAndDoWhile(t *testing.T) {
	assertOutput(t, `
const base: number = 100
const obj = { a: 1, b: 2 }
const f = (): number => {
  let total: number = 0
  for (const k in obj) {
    total = total + base
  }
  let i: number = 0
  do {
    total = total + base
    i = i + 1
  } while (i < 1)
  return total
}
console.log(f())
`, "300")
}

// TestE2EJSModeForInOverCapturedDynamicObject: -compat=js for...in over a
// dynamic (D1) object captured into a closure. Before ADR-00662 the capture
// scan skipped the for-in object, so the dynamic key iteration never saw it.
func TestE2EJSModeForInOverCapturedDynamicObject(t *testing.T) {
	assertOutputCompatJS(t, `
var obj = { name: "Luna", age: 3 }
var f = function() {
  var out = ""
  for (var k in obj) { out = out + k + "," }
  return out
}
console.log(f())
`, "name,age,")
}

// TestE2EJSModeDestructureDynamicObjectInForOf: -compat=js object-pattern
// destructuring of a dynamic (D1) element (ADR-00663). The element has no
// static struct shape, so each property is read through the runtime dynamic-get.
func TestE2EJSModeDestructureDynamicObjectInForOf(t *testing.T) {
	assertOutputCompatJS(t, `
var sum = 0
for (const { x, } of [{ x: 23 }, { x: 19 }]) { sum += x }
console.log(sum)
`, "42")
}

// TestE2EJSModeDestructureDynamicObjectRenamedAndDefault covers the renamed
// (`{ k: local }`) and defaulted (`{ k = d }`) leaf forms over a dynamic source.
func TestE2EJSModeDestructureDynamicObjectRenamedAndDefault(t *testing.T) {
	assertOutputCompatJS(t, `
for (const { x: y, z = 7 } of [{ x: 5 }]) { console.log(y, z) }
`, "5 7")
}

// TestE2EJSModeDestructureDynamicObjectNestedArray covers a nested array
// sub-pattern (`{ k: [a] }`) over a dynamic element holding a dynamic array.
func TestE2EJSModeDestructureDynamicObjectNestedArray(t *testing.T) {
	assertOutputCompatJS(t, `
for (const { x: [a], } of [{ x: [45] }]) { console.log(a) }
`, "45")
}

// -compat=js: a property of a union whose one object member declares it
// reads at run time (JavaScript's read): the member's field, a primitive's
// property, or JavaScript's TypeError on null.
func TestCompatJSUnionSoleObjectMemberRead(t *testing.T) {
	assertOutputCompatJS(t, `
function addr(k: number): { port: number } | string | null {
  if (k === 0) return { port: 8080 }
  if (k === 1) return "pipe"
  return null
}
const a = addr(0), b = addr(1), c = addr(2)
console.log(a.port, b.port)
try { console.log(c.port) } catch (e) { console.log(e.message) }
`, "8080 undefined\nCannot read properties of null (reading 'port')")
}

// -compat=js: the builtin modules written in TypeScript (http over net)
// compile in the strict lane whatever the program's lane; a js-lane program
// using them runs as Node does.
func TestCompatJSProgramUsingHTTP(t *testing.T) {
	bin := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "main.js")
	if err := os.WriteFile(src, []byte(`
const http = require('http');
const server = http.createServer(function(req, res) { res.end('ok:' + req.url); });
server.listen(0, '127.0.0.1', function() {
  const port = server.address().port;
  http.get({ port: port, path: '/x' }, function(res) {
    let body = '';
    res.on('data', function(c) { body += c; });
    res.on('end', function() { console.log(res.statusCode, body); server.close(); });
  });
});
`), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if b, err := exec.Command(bin, "-compat=js", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, b)
	}
	got, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	compareLines(t, strings.TrimRight(string(got), "\n"), "200 ok:/x")
}

// -compat=js: Node's function-style constructors construct when called
// without `new` (`http.Server(…)`, `net.Server()`, `stream.Readable(…)`);
// the strict lane rejects the call, tsc's TS2348.
func TestCompatJSCallableNodeConstructors(t *testing.T) {
	bin := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "main.js")
	if err := os.WriteFile(src, []byte(`
const http = require('http');
const net = require('net');
const { Readable } = require('stream');
const server = http.Server(function(req, res) { res.end('ok'); });
console.log(server instanceof http.Server);
const s2 = net.Server();
console.log(s2 instanceof net.Server);
const r = Readable({ read() {} });
r.push('x'); r.push(null);
r.on('data', (c) => console.log('data', c.toString()));
`), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if b, err := exec.Command(bin, "-compat=js", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, b)
	}
	got, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	compareLines(t, strings.TrimRight(string(got), "\n"), "true\ntrue\ndata x")
	tsSrc := filepath.Join(dir, "strict.ts")
	if err := os.WriteFile(tsSrc, []byte("class A { x = 1 }\nconst a = A()\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command(bin, "-o", out, tsSrc).CombinedOutput()
	if err == nil || !strings.Contains(string(b), "value of type 'typeof A' is not callable") {
		t.Fatalf("want TS2348, got %v\n%s", err, b)
	}
}

// -compat=js: a top-level binding a function reads (`const server =
// http.createServer(…).listen(0, client)` read in `function client()`) is a
// module global.
func TestCompatJSTopLevelReadInFunction(t *testing.T) {
	bin := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "main.js")
	if err := os.WriteFile(src, []byte(`
const http = require('http');
const server = http.createServer((req, res) => { res.end('ok'); }).listen(0, '127.0.0.1', client);
function client() {
  const port = server.address().port;
  http.get({ port }, (res) => { res.resume(); res.on('end', () => { console.log('status', res.statusCode); server.close(); }); });
}
`), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if b, err := exec.Command(bin, "-compat=js", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, b)
	}
	got, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	compareLines(t, strings.TrimRight(string(got), "\n"), "status 200")
}

// A top-level variable declared with no initializer that a function reads
// or assigns is one module global holding any value (tsc's captured `any`).
func TestE2EUninitializedVarFunctionAssigned(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var value;
function dflt(a, b = 0) { value = a; }
dflt(1);
console.log(value, typeof value);
var u;
function r() { return u; }
console.log(r());
u = 5;
console.log(r() + 1);
u = "x";
console.log(r());
`)
}

// A function expression's or arrow's parameter takes its call sites'
// type; an argument held as any keeps it any (ADR-01507).
func TestJSModeFunctionExpressionSiteParams(t *testing.T) {
	assertSameAsNodeCompatJS(t, `
var km = val => ({ key: val });
console.log(km(1).key + 1);
var fe = function (v) { return v * 2; };
console.log(fe(3));
var g = function (s) { return s.length; };
console.log(g("abc"));
var anyv; anyv = "xy";
var h = (q) => q;
console.log(h(1), h(anyv));
var cb = (z) => z + 1;
console.log([1, 2].map(cb));
`)
}

// -compat=js: an overloaded lib function whose result is held boxed
// (`readFileSync`'s `string | Buffer`) is read as the box when a method is
// called on the call directly, whichever overload the checker picks.
func TestCompatJSReadFileSyncChainedToString(t *testing.T) {
	bin := buildCLI(t)
	dir := tempDir(t)
	data := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(data, []byte("xyz\n"), 0644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.js")
	if err := os.WriteFile(src, []byte(`const fs = require('fs');
console.log(JSON.stringify(fs.readFileSync(process.argv[2]).toString()));
console.log(JSON.stringify(fs.readFileSync(process.argv[2]).toString('utf8')));
`), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if b, err := exec.Command(bin, "-compat=js", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, b)
	}
	got, err := exec.Command(out, data).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	compareLines(t, strings.TrimRight(string(got), "\n"), "\"xyz\\n\"\n\"xyz\\n\"")
}

// An object literal whose property contradicts the parameter's declared
// type compiles; the value it cannot hold is a TypeError at run time.
func TestE2EJSModeLiteralContradictsParam(t *testing.T) {
	assertOutputCompatJS(t, `
function f(o) { return o.argv }
console.log(f({ argv: 'foo' }))
const { Worker } = require('worker_threads')
try { new Worker(process.argv[1], { argv: 'foo' }) } catch (e) { console.log(e.code, e.message) }
`, "foo\nERR_INVALID_ARG_TYPE The \"options.argv\" property must be an instance of Array. Received type string ('foo')")
}

func TestE2ECompatJSNullIntoNumberBinding(t *testing.T) {
	// -compat=js: a number binding later assigned null holds null, and so
	// does a binding copied from it (ADR-01752).
	assertOutputCompatJS(t, `
var a = 1;
var b = 0;
for (var i = 0; i < 2; i++) { b = a; a = null; }
console.log(a, b, a !== null);
var c = true;
c = undefined;
console.log(c);
var d = [1, 2];
var e = d;
d = null;
console.log(d, e.length);
`, "null null false\nundefined\nnull 2")
}

func TestE2ECompatJSBareSetForOfPush(t *testing.T) {
	// -compat=js: a bare new Set()'s element type reaches an array its
	// for-of loop pushes into (ADR-01752).
	assertOutputCompatJS(t, `
var set = new Set();
set.add(0);
set.add(1);
var seen = [];
for (var x of set) { seen.push(x); if (x === 0) { set.delete(1); set.add(1); } }
console.log(seen, seen[0] + seen[1]);
`, "[ 0, 1 ] 1")
}

func TestE2ECompatJSDefaultFromParam(t *testing.T) {
	// -compat=js: a parameter defaulted from another one is held as a box,
	// through a plain and an async function's result (ADR-01752).
	assertOutputCompatJS(t, `
var f = (x, y = x, z = y) => { return z; };
console.log(f(3) + 1, f(1, "a"));
var g = async (x, y = x) => { return y; };
g(3).then(v => console.log(v + 1));
`, "4 a\n4")
}

func TestE2ECompatJSDeleteResultBinding(t *testing.T) {
	// -compat=js: delete's result held in a binding is a boolean (ADR-01753).
	assertOutputCompatJS(t, `
var o = {};
Object.defineProperty(o, "foo", { value: 1, configurable: true });
var d = delete o.foo;
console.log(d, typeof d, o.hasOwnProperty("foo"));
`, "true boolean false")
}

func TestE2ECompatJSClassInstanceComputedKey(t *testing.T) {
	// -compat=js: a computed key into a class instance reads and writes its
	// property by name (ADR-01756).
	assertOutputCompatJS(t, `
class C { x = 1; y = "s"; }
var c = new C();
for (const k of ["x", "y", "z"]) console.log(k, c[k]);
var key = "x";
c[key] = 5;
console.log(c.x, c[key]);
`, "x 1\ny s\nz undefined\n5 5")
}

func TestE2ECompatJSModuleGlobalBuiltinFunctionValue(t *testing.T) {
	// -compat=js: a module var holding a builtin function is readable and
	// callable from a function (ADR-01757).
	assertOutputCompatJS(t, `
var gopd = Object.getOwnPropertyDescriptor;
var p = parseInt;
function f(o, n) { return gopd(o, n).value + p("1"); }
console.log(f({ a: 1 }, "a"), typeof JSON.stringify);
`, "2 function")
}

func TestE2ECompatJSArrayHOFThisArg(t *testing.T) {
	// -compat=js: an array method's thisArg is its callback's `this`, and a
	// module var the callback assigns is a global (ADR-01759).
	assertOutputCompatJS(t, `
var result;
function cb(v) { result = this.res; }
[1].forEach(cb, { res: true });
var o = { k: 10 };
console.log(result, [1, 2, 3].map(function (v, i) { return v * this.k + i; }, o).join(","),
  [1, 2, 3].find(function (v) { return v === this.k; }, { k: 2 }),
  [1, 2].flatMap(function (v) { return [v, this.k]; }, { k: 0 }).join(","));
`, "true 10,21,32 2 1,0,2,0")
}

func TestE2ECompatJSNonCallableArrayCallback(t *testing.T) {
	// -compat=js: a callback that is not a function throws Node's TypeError
	// before iterating (ADR-01761).
	assertOutputCompatJS(t, `
function t(fn) { try { fn(); console.log("no throw"); } catch (e) { console.log(e.constructor.name, e.message); } }
t(function () { new Array(3).reduce(null); });
t(function () { [1].filter(undefined); });
t(function () { [].some(5); });
`, "TypeError object null is not a function\nTypeError undefined is not a function\nTypeError number 5 is not a function")
}

func TestE2ECompatJSSymbolToNumberThrows(t *testing.T) {
	// -compat=js: a Symbol where a number is taken is Node's run-time
	// TypeError, not a compile error (ADR-01762).
	assertOutputCompatJS(t, `
function t(fn) { try { console.log(fn()); } catch (e) { console.log(e.constructor.name, e.message); } }
var s = Symbol("k");
t(function () { return [1].includes(1, s); });
t(function () { return "abc".charAt(s); });
`, "TypeError Cannot convert a Symbol value to a number\nTypeError Cannot convert a Symbol value to a number")
}

func TestE2ECompatJSInstanceofBuiltinCtors(t *testing.T) {
	// -compat=js: instanceof Object/Function/Symbol on any value, and a
	// builtin constructor held in a variable (ADR-01763).
	assertSameAsNodeCompatJS(t, `
var f = function () {}; class K {} var o = {}; var a = []; var n = Object.create(null); var s = Symbol("q");
var j = JSON.parse('{"x":[1]}'); var c = Object.create(Object.create(null));
console.log(f instanceof Function, K instanceof Function, o instanceof Function, j instanceof Function);
console.log(o instanceof Object, n instanceof Object, j instanceof Object, j.x instanceof Object, c instanceof Object, Object.prototype instanceof Object, s instanceof Symbol);
function chk(F) {
  return [f instanceof F, K instanceof F, o instanceof F, a instanceof F, n instanceof F, 5 instanceof F, new Map() instanceof F, new TypeError("x") instanceof F].join(",");
}
console.log(chk(Function), chk(Object), chk(Array), chk(Map), chk(Error));
`)
}

// A function declared twice is its last declaration (hoisting replaces the
// first), and a loop of bindings reassigned from calls reading each other
// types without exhausting the checker.
func TestE2EJSRedeclaredFunctionAndChainedCalls(t *testing.T) {
	body := strings.Repeat("    a = F(a, b, c, d); d = F(d, a, b, c); c = F(c, d, a, b); b = F(b, c, d, a);\n", 16)
	src := `function f() { return 1; }
function f() { return "A"; }
var r = f();
console.log(r, typeof r);
function F(a, b, c, d) { return a + b + c + d; }
function sum() {
  var k, a, b, c, d;
  a = 1; b = 2; c = 3; d = 4;
  for (k = 0; k < 1; k++) {
` + body + `  }
  return (a + b + c + d) % 1000;
}
console.log(sum());
`
	assertOutputCompatJS(t, src, "A string\n200")
}

// An object literal whose method returns `this` is one class: an async
// `yield*` over it reaches its `return` accessor (a link error before).
func TestE2EJSAsyncYieldStarOverLiteralWithReturnGetter(t *testing.T) {
	assertSameAsNodeCompatJS(t, `var n = 0;
var it = {
  [Symbol.asyncIterator]: function () { return this; },
  next: function () { n++; return { value: n, done: n > 2 }; },
  get return() { return null; },
};
async function* g() { yield* it; }
(async () => { for await (const v of g()) console.log(v); })();
`)
}

// A function expression reading a name declared only in an earlier, closed
// block reads the global: "is not defined", not the block's dead binding.
func TestE2EJSClosedBlockLetUnseenByLaterFunction(t *testing.T) {
	assertSameAsNodeCompatJS(t, `try { { let xx = 18; throw 25; } } catch (e) {
  (function () { try { console.log(xx); } catch (e2) { console.log(e2 instanceof ReferenceError, e2.message); } })();
}
{ let yy = 1; }
(function () { try { console.log(yy); } catch (e) { console.log(e.message); } })();
`)
}

// A Map or Set seeded with pairs whose values differ in kind (an array, a
// string) keeps each value as given.
func TestE2EJSMapSeedMixedValueKinds(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const m = new Map([[1, [1, 2]], [2, "x"]]);
console.log(m.get(1), m.get(2));
const s = new Set([[1, "a"], ["b", 2]]);
console.log(s.size, [...s]);
console.log("a,b,c".split({ toString: function () { return ","; } }).length);
`)
}

func TestE2EModuleThis(t *testing.T) {
	// A module's own `this`, read at its top level or by an arrow there:
	// CommonJS's module.exports (one object) in a script, as Node runs it.
	assertSameAsNodeCompatJS(t, `const f = () => this;
console.log(typeof f(), f() === undefined, f() === f());
const g = () => () => this;
console.log(g()() === f(), typeof this);
`)
	// undefined in an ES module, in either lane.
	assertSameAsNode(t, `export {};
const f = () => this;
console.log(typeof f(), f() === undefined);
`)
}

func TestStrictArrowCapturesGlobalThis(t *testing.T) {
	// A script's top-level arrow capturing `this` is tsc's TS7041.
	_, err := parseAndCompile(`const f = () => this;
console.log(typeof f());
`)
	if err == nil || !strings.Contains(err.Error(), "captures the global value of 'this'") {
		t.Fatalf("expected TS7041, got: %v", err)
	}
}

// A var declared again with a value of another type, or an array binding
// assigned a scalar (and back), holds each in turn.
func TestE2EVarRedeclaredOtherType(t *testing.T) {
	assertSameAsNodeCLIJS(t, `var a = [2n, 1];
for (var x of a) console.log(x);
var a = [3, 1][0];
console.log(a);
var b = [2, 1];
for (var x of b) console.log(x);
var b = "s";
console.log(b);
let lx = [1, 2]; console.log(lx.length); lx = 5; console.log(lx);
var y = "s"; y = [3]; console.log(y[0]);
var q = [2n, Symbol("z")];
for (var e of q) console.log(typeof e);
var q = Symbol("w"); console.log(q.toString());
`)
}

// A builtin called with fewer arguments than it declares reads undefined
// for each missing one, and one called with more still evaluates the rest,
// in order: Math/Number helpers and a generator's .throw().
func TestE2EBuiltinArgumentCountJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
console.log(Math.abs(), Math.abs(-2.5, console.log("x")), Math.imul(3), Math.imul(3, 4, console.log("y")), isNaN(), Math.sign(-3, 1), Math.atan2(1), Number.isInteger(), parseFloat("2.5", 9), Math.clz32());
function* g() { try { yield 1; } catch (e) { console.log("caught", e); yield 2; } }
const it = g(); it.next(); console.log(it.throw());
const j = g(); j.next(); console.log(j.throw(5, console.log("side")));
`)
}

// A function declaration in an object literal's method, hoisted as in any
// function body, and a parameter's eval declaring a var the body shadows.
func TestE2EObjectMethodNestedFunctionJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
let o = {
  f() { return y(); function y() { return 3; } },
  g() { function arguments() { return 4; } return typeof arguments; },
  h(p = eval("var x = 1")) { function x() {} return typeof x; },
  k: function (p = eval("var z = 2")) { return z; },
  n: 1,
};
o.m = 2;
console.log(o.f(), o.g(), o.h(), o.k());
`)
}

// `delete` of what is not a deletable property: a declared binding (false),
// a non-reference (its operand runs; true), a builtin constant (false, or
// strict code's TypeError), and a null or undefined base (TypeError).
func TestE2EDeleteNonPropertiesJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
var x = 1; function f() {} class K {}
var n = 0;
console.log(delete x, delete f, delete K, delete undeclaredName, delete 0, delete delete x, delete (n++, 5), n, x);
console.log(delete Math.LN2, delete Number.MAX_VALUE);
(function () { "use strict"; try { delete Math.PI; } catch (e) { console.log(e.constructor.name, e.message); } })();
var base = null, u;
try { delete base.prop; } catch (e) { console.log(e.constructor.name, e.message); }
try { delete u[1]; } catch (e) { console.log(e.constructor.name, e.message); }
`)
}

// The strict lane reports tsc's delete-operand errors: TS2703, TS2704,
// TS2790.
func TestE2EDeleteOperandStrict(t *testing.T) {
	assertCLICompileError(t, false, "console.log(delete 0);\n", "must be a property reference")
	assertCLICompileError(t, false, "console.log(delete Math.PI);\n", "cannot be a read-only property")
	assertCLICompileError(t, false, "const o = { a: 1 }; delete o.a; console.log(o);\n", "must be optional")
}

// Deleting a class field with no initializer (a boxed slot), or one keyed
// at run time, removes its own key.
func TestE2EDeleteBoxedClassFieldJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
var k = "h";
class C { f = 1; g; [k]; }
let c = new C();
console.log(Object.keys(c));
delete c.g; delete c.h;
console.log(c.hasOwnProperty("g"), c.hasOwnProperty("h"), "g" in c, Object.keys(c), c.g);
`)
}

// A parameter its only call passes an array, wrapped in an array where a
// test narrowed the array away (`names = [names]`), compiles; the branch
// never runs.
func TestE2EWrapNarrowedAwayArrayJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
function get(names) {
  if (names && !Array.isArray(names)) {
    names = [names];
  }
  return ["A", "B"].filter(n => !names || names.includes(n));
}
function run(names) { console.log(get(names)); }
run(["A"]);
`)
}

// An accessor literal passed straight to a typed parameter (`new Event(type,
// { get bubbles() { … } })`) reads the enclosing function's locals.
func TestE2EInlineAccessorLiteralCapturesJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
function g() {
  var called = [];
  var ev = new Event("Xx", { get bubbles() { called.push("bubbles"); return true; }, get cancelable() { called.push("cancelable"); return false; } });
  console.log(ev.bubbles, ev.cancelable, called);
}
g();
`)
}

// A nested function reading its own `this`, named above its declaration
// (a request handler passed before it is declared), is hoisted.
func TestE2EHoistedNestedThisFunctionJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
function run(cb) { return cb(2); }
function check(k) {
  console.log(run(add), run(kind));
  if (k) {
    console.log(run(inner));
    function inner(a) { return typeof this + a; }
  }
  function add(a) { return a + k; }
  function kind(a) { return typeof this; }
}
check(10);
`)
}

// A constructor a top-level destructuring binds, constructed only inside a
// function (`new S()`), is the module's binding there.
func TestE2EDestructuredConstructorInFunctionJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const o0 = { S: class { constructor() { this.v = 1; } } };
const { S } = o0;
function f() { return new S().v; }
console.log(f());
`)
}

// Library methods that check their own arguments (a WebIDL conversion, a
// Node validator, a callback that is not a function ignored) see the
// value JavaScript passed, not a type error at compile time.
func TestE2ELibArgumentsCheckedAtRunTimeJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const http = require('http');
const net = require('net');
const dgram = require('dgram');
const h = new Headers();
try { h.forEach(1); } catch (e) { console.log(e.constructor.name); }
const c = new BroadcastChannel(123);
console.log(c.name, typeof c.name);
c.close();
const s = dgram.createSocket('udp4');
s.bind(0, () => {
  try { s.setMulticastInterface(1); } catch (e) { console.log(e.code); }
  s.close();
});
const srv = http.createServer((req, res) => {
  try { res.setHeader(0xf00, 'bar'); } catch (e) { console.log(e.code); }
  res.end('ok');
});
srv.listen(0, () => http.get({ port: srv.address().port }, (r) => { r.resume(); r.on('end', () => srv.close(2)); }));
const ns = net.createServer();
ns.listen(0, () => ns.close(5));
`)
}

// The stream module's value is the Stream class: extended, constructed and
// tested with instanceof, its members still the module's; a parameter of
// the same name is its own.
func TestE2EStreamModuleAsClass(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const stream = require('stream');
class M extends stream {}
const m = new M();
console.log(typeof m.pipe, m instanceof stream, new stream.Readable() instanceof stream);
function f(stream) { return stream instanceof Object; }
console.log(f({}));
`)
	assertSameAsNode(t, `
import stream from 'stream'
class M extends stream {}
console.log(typeof new M().pipe, new stream.PassThrough() instanceof stream);
`)
}

// A destructuring of a value no local holds — a class (its statics), the
// global console, a name nothing declares (its ReferenceError) — reads the
// value.
func TestE2EDestructureNonLocalValuesJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const { Readable } = require('stream');
const events = require('events');
const { from } = Readable;
const { EventEmitter, defaultMaxListeners } = events;
console.log(typeof from, typeof EventEmitter, defaultMaxListeners);
const { log: log0 } = console;
log0("logged");
try { const { isWindows } = common; } catch (e) { console.log(e.name, e.message); }
const { Console, log } = console;
const c = new Console(process.stdout);
c.log('via class');
log(typeof Console);
`)
}

// A call through a destructured union of functions, awaited, is a dynamic
// call whose result is awaited as a box.
func TestE2EAwaitDynamicUnionCallJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
function a(x) { return x + 1; }
async function b(x) { return x * 2; }
(async function() {
  for (const [ f, name ] of [[a, 'a'], [b, 'b']]) {
    const r = await f(3);
    console.log(name, r);
  }
})();
for (const [ f, name ] of [[a, 'a'], [Math.abs, 'b']]) console.log(name, f(-3));
`)
}

// subarray/slice indexes run ToNumber: a string, an object's valueOf, an
// array, null and undefined, as Node does.
func TestE2ESubarrayCoercedIndexesJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const b = Buffer.from('abcdef');
console.log(b.subarray('2', '4').toString(), b.subarray({ valueOf() { return 1; } }).toString(), b.slice([3]).toString(), b.subarray(undefined, 2).toString());
const u = new Uint8Array([1,2,3,4]);
console.log(u.subarray('1').length, u.subarray(null, '3').length);
`)
}

// Buffer numeric arguments: offsets and compare ranges take numbers only
// (ERR_INVALID_ARG_TYPE), Buffer.from(ab, off, len) runs ToNumber, btoa
// runs ToString, as Node does.
func TestE2EBufferNumericArgumentsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const a=Buffer.from('abcd'),b=Buffer.from('bcd');
for (const f of [()=>a.compare(b,'1'),()=>a.compare(b,{}),()=>a.readUInt8('1'),()=>a.readUInt8({}),()=>a.compare(b,1)]) try{console.log(f())}catch(e){console.log(e.code,e.message)}
console.log(Buffer.from(new ArrayBuffer(4),'1').length, Buffer.from(new ArrayBuffer(4),[2],'1').length, btoa(12), btoa(true));
let o; console.log(a.readUInt8(o), a.readUInt8(2));
`)
}

// A primitive zlib options argument is the defaults, as in Node.
func TestE2EZlibPrimitiveOptionsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const zlib = require('zlib');
const g = zlib.createGzip(9);
const d = zlib.createGunzip(15);
g.pipe(d);
let out = '';
d.on('data', c => out += c);
d.on('end', () => console.log(out, typeof zlib.createDeflate('x')));
g.end('hello zlib');
`)
}

// net.connect/new net.Socket take an AbortSignal: an abort destroys the
// socket with an AbortError, as Node does; a captured union-member read
// (`server.address().port`) is any, as tsc types it.
func TestE2ENetConnectAbortSignalJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const net = require('net');
const server = net.createServer();
server.listen(0, async () => {
  const port = server.address().port;
  const host = 'localhost';
  const socketOptions = (signal) => ({ port, host, signal });
  const ac = new AbortController();
  const socket = net.connect(socketOptions(ac.signal));
  socket.on('error', (e) => { console.log(e.name); server.close(); });
  ac.abort();
});
`)
	assertSameAsNodeCLIJS(t, `
const net = require('net');
const ac2 = new AbortController();
const s = new net.Socket({ signal: ac2.signal });
s.on('error', (e) => console.log('err', e.name, e.code));
s.on('close', () => console.log('close'));
ac2.abort();
const s2 = new net.Socket({ signal: AbortSignal.abort() });
s2.on('error', (e) => console.log('err2', e.name));
`)
}

// A class whose base names only a global type (no value, as EventEmitter
// outside the events module) throws ReferenceError, as in Node.
func TestE2EExtendsTypeOnlyGlobalJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
try {
  class F extends EventEmitter { resume() {} }
  console.log(new F());
} catch (e) { console.log(e.name, e.message) }
`)
}

// A match()/exec() result held as a dynamic value keeps index, input and
// groups (undefined without named groups), as Node does.
func TestE2EBoxedMatchResultJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
let s = 'xa+ya+'; if (Math.random() > 2) s = 1;
console.log(s.match(/(?<S>a)\+/));
console.log(s.match(/a/g), s.match(/q/));
console.log(Object.keys(s.match(/(a)/)));
const r = /(b)/.exec('ab'); const anyR = [r][0]; console.log(anyR.index, anyR.groups);
const m = 'xa'.match(/(a)/);
console.log(Object.keys(m));
const g = 'xa'.match(/a/g);
console.log(Object.keys(g));
function f2(b) { return b ? 'xa+' : Buffer.from('xa+'); }
const e2 = f2(true);
const m2 = e2.match(/(?<S>a)\+/);
console.log(m2.groups.S, typeof e2.foo);
try { f2(false).match(/a/) } catch (err) { console.log(err.name) }
`)
}

// A key typed with a non-key member (a mixed tuple's element) indexes
// by its run-time value, as in Node.
func TestE2EUnionIndexKeyJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const zlib = require('zlib');
for (const method of [['createGzip', 'createGunzip', false], ['createDeflate', 'createInflate', true]]) {
  const comp = zlib[method[0]]();
  console.log(typeof comp.write);
}
`)
}

// A key of several values, not each a property, reads undefined for the
// rest, as in Node.
func TestE2EUnionKeyMissingMemberJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const by = { linux: ['a'], darwin: ['b'] };
for (const platform of ['ibmi', process.platform, 'darwin']) console.log(by[platform] || ['']);
`)
}

// `{ 0: a, 1: b } = tuple`: the elements at those indexes, as in Node.
func TestE2ENumericKeyTupleDestructureJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const rs = new ReadableStream({ start(c) { c.enqueue('a'); c.close(); } });
const { 0: rs1, 1: rs2 } = rs.tee();
const reader1 = rs1.getReader();
reader1.read().then((r) => console.log(r.value, rs2 instanceof ReadableStream));
const { 0: x, 1: y, length } = [7, 'q'];
console.log(x, y, length);
`)
}

// A module export the declarations do not list reads as undefined at run
// time in JavaScript (not a compile error), as in Node.
func TestE2EUnlistedModuleExportJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const crypto = require('crypto');
const h = () => crypto.nope;
console.log(typeof h(), typeof crypto.nope);
`)
}

// A file that is its own worker: its top-level bindings (plain and
// destructured) read on the worker thread from its closures, and the
// worker's process.argv from options.argv, as in Node.
func TestE2EWorkerSelfModuleGlobalsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const { Worker, isMainThread, parentPort } = require('worker_threads');
const g = Math.max;
const n = 5;
const { PI, abs } = Math;
const f = () => [typeof g, n, PI, typeof abs, process.argv.slice(2)].join(' ');
if (isMainThread) {
  console.log(f());
  const w = new Worker(__filename, { argv: ['iamalive', 3] });
  w.on('message', (m) => console.log('worker', m));
} else {
  setTimeout(() => parentPort.postMessage(f()), 5);
}
`)
}

// performance.nodeTiming and eventLoopUtilization, and a plain object's
// prototype, as in Node.
func TestE2EEventLoopUtilizationJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const assert = require('assert');
const { performance } = require('perf_hooks');
const { nodeTiming, eventLoopUtilization } = performance;
const lit = { a: 1 };
console.log(Object.getPrototypeOf(lit) === Object.prototype);
assert.deepStrictEqual(eventLoopUtilization(), { idle: 0, active: 0, utilization: 0 });
console.log(nodeTiming.loopStart, nodeTiming.name, nodeTiming.entryType);
setTimeout(() => {
  const e1 = eventLoopUtilization();
  const t = Date.now(); while (Date.now() - t < 30);
  const e2 = eventLoopUtilization(e1);
  console.log(nodeTiming.loopStart > 0, e2.idle, e2.utilization, e2.active >= 25);
  const e3 = eventLoopUtilization(e2, e1);
  console.log(e3.active === e2.active - e1.active);
}, 10);
`)
}

// net and http servers' [Symbol.asyncDispose], as in Node.
func TestE2EServerAsyncDisposeJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const net = require('net');
const http = require('http');
const server = net.createServer();
server.listen(0, () => {
  server.on('close', () => console.log('closed'));
  server[Symbol.asyncDispose]().then(() => console.log('disposed'));
});
const s1 = net.createServer();
s1[Symbol.asyncDispose]().then(() => console.log('net idle disposed'));
const s2 = http.createServer();
s2[Symbol.asyncDispose]().catch((e) => console.log('http idle', e.code));
`)
}

// The punycode module (Node's vendored punycode.js) and its DEP0040
// warning, as in Node.
func TestE2EPunycodeModuleJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const punycode = require('punycode');
process.on("warning", (w) => console.log(w.name, w.code));
console.log(punycode.version);
console.log(punycode.encode('ü'), punycode.encode('Goethe'), punycode.decode('tda'));
console.log(punycode.toASCII('mañana.com'), punycode.toUnicode('xn--maana-pta.com'), punycode.toASCII('jo@mañana.com'));
console.log(punycode.ucs2.decode('a😀'), punycode.ucs2.encode([97, 0x1F600]));
try { punycode.decode('ls8h='); } catch (e) { console.log(e.name, e.message); }
try { punycode.decode('\x81-'); } catch (e) { console.log(e.name, e.message); }
`)
}

// Buffer.of, and Buffer.from of a literal whose elements need ToNumber, as
// in Node.
func TestE2EBufferOfJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const arr = [7, 8];
const b = Buffer.of(1, 2, 3);
console.log(b, b instanceof Buffer, Buffer.of().length, Buffer.of(256, -1, 1.7), Buffer.of(...arr));
console.log(Buffer.of('65', null, true));
const x = JSON.parse('"9"');
console.log(Buffer.from([x, '0x10', {valueOf() { return 5; }}, undefined, 'z']));
`)
}

// The CryptoKey global, and `const { subtle } = crypto` as a real binding
// (typeof, reads in callbacks), as in Node.
func TestE2ECryptoKeyGlobalJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
try { new CryptoKey(); } catch (e) { console.log(e.name, e.message, e.code); }
console.log(typeof CryptoKey, CryptoKey.name);
crypto.subtle.generateKey({ name: 'HMAC', hash: 'SHA-256' }, true, ['sign']).then((k) => console.log(k instanceof CryptoKey, k.type));
const { subtle } = globalThis.crypto;
console.log(typeof subtle);
Promise.resolve(1).then(() => { console.log(typeof subtle.importKey); });
`)
}

// new CompressionStream()/DecompressionStream() with the format missing,
// directly and through a value: Node's TypeError at run time.
func TestE2ECompressionStreamMissingFormatJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
for (const C of [CompressionStream, DecompressionStream]) { try { new C(); } catch (e) { console.log(e.name, e.code, e.message); } }
try { new CompressionStream(); } catch (e) { console.log(e.message); }
try { new DecompressionStream(); } catch (e) { console.log(e.message); }
`)
}

// http2 servers' [Symbol.asyncDispose], as in Node.
func TestE2EHttp2ServerAsyncDisposeJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const http2 = require('http2');
const s = http2.createServer();
s.listen(0, () => {
  s.on('close', () => console.log('h2 closed'));
  s[Symbol.asyncDispose]().then(() => {
    console.log('h2 disposed');
    http2.createServer()[Symbol.asyncDispose]().catch((e) => console.log('h2 idle', e.code));
  });
});
`)
}

// querystring.escape (Node's encodeStr), path.toNamespacedPath and
// url.pathToFileURL of non-string values, and encodeURIComponent of a lone
// surrogate, as in Node.
func TestE2ENonStringPathQueryArgsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const url = require('url'), path = require('path'), qs = require('querystring');
for (const v of [['a'], 5, null]) { try { url.pathToFileURL(v); } catch (e) { console.log(e.code, e.message); } }
console.log(path.toNamespacedPath(null), path.toNamespacedPath(100), path.win32.toNamespacedPath(false), path.toNamespacedPath(path) === path, path.posix.toNamespacedPath(3));
try { qs.escape('\uD800'); } catch (e) { console.log(e.name, e.code, e.message); }
console.log(qs.escape(5), qs.escape({}), qs.escape([5, 10]), qs.escape(null), qs.escape(undefined));
console.log(url.pathToFileURL('/tmp/a b').href);
try { console.log(encodeURIComponent('\uD800')); } catch (e) { console.log(e.name, e.message); }
const s = String.fromCharCode(0xD801) + 'test';
try { console.log(encodeURIComponent(s)); } catch (e) { console.log(e.name, e.message); }
try { console.log(qs.escape(s)); } catch (e) { console.log(e.name, e.code); }
`)
}

// String.prototype.normalize: NFC/NFD/NFKC/NFKD, Hangul, blocked marks,
// composition exclusions and the RangeError for an unknown form, as Node.
func TestE2EStringNormalizeJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const show = (s) => [...s].map((c) => c.codePointAt(0).toString(16)).join(' ');
const s = 'café';
console.log(s.normalize().length, s.normalize('NFD').length, s.normalize('NFC') === 'café', 'abc'.normalize(undefined));
try { 'a'.normalize('x'); } catch (e) { console.log(e.name, e.message); }
console.log(show('ẛ̣'.normalize('NFKC')), show('ẛ̣'.normalize('NFC')), show('ẛ̣'.normalize('NFD')), show('ẛ̣'.normalize('NFKD')));
console.log(show('각'.normalize('NFD')), show('각'.normalize()), show('각'.normalize('NFKD')));
console.log(show('Ạ̊'.normalize('NFC')), 'ﬁ'.normalize('NFKD'), show('Ω'.normalize()), show('́a'.normalize()), show('á́'.normalize()));
console.log(show('ཱི'.normalize()), show('̈́'.normalize()), show('Å̧'.normalize()), show('Å'.normalize('NFD')), show('⁵½'.normalize('NFKC')));
console.log(show('क़'.normalize()), show('\uD800'.normalize()), show('xֱָֹ֑׃ְ֬֟'.normalize()));
`)
}

// A call through a member of an undeclared name (`zork.f(x.d)`) asserts
// nothing: what flows past it keeps its type, as tsc's error type gives.
func TestE2EUndeclaredDottedCallNoAssertJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const assert = require('assert');
const t = {
  a: (x) => assert.strictEqual(zork.f(x.d), x.e),
  b: (x) => assert.strictEqual(1, x.e),
};
for (const k of Object.keys(t)) {
  try { t[k]({ d: 1, e: 2 }); } catch (e) { console.log(k, e.name); }
}
`)
}

// A method called by a key known at run time on a dynamic value or on
// console, a Buffer write of a non-number value (+value), a search's
// non-number fromIndex, and a Buffer or Uint8Array needle, as in Node.
func TestE2EDynamicKeyCallArgsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const o = JSON.parse('{}'); o.log = (a, b) => console.log('got', a, b);
const k = String('log');
o[k]('foo');
o[k]('a', 'b');
const arr = [1, 2, 3, 2];
console.log(arr.includes(2, '2'), arr.indexOf(2, 'x'), arr.lastIndexOf(2, '2'), arr.indexOf(2, null), arr.includes(1, -Infinity), arr.indexOf(2, Infinity));
const ms = ['log', 'info', 'warn', 'error', 'debug'];
for (const m of ms) { console[m]('foo'); }
const b = Buffer.alloc(8);
b.writeDoubleLE('1.5'); console.log(b.readDoubleLE(0)); b.writeInt32LE('7', 0); console.log(b.readInt32LE(0)); b.writeUInt8(true, 0); console.log(b[0]);
const h = Buffer.from('hello');
console.log(h.includes(Buffer.from('ll')), h.indexOf(Buffer.from('l')), h.lastIndexOf(Buffer.from('l')), h.includes(new Uint8Array([104, 101])));
console.log(h.indexOf(Buffer.from('l'), 3), h.lastIndexOf(Buffer.from('l'), 2), h.indexOf(Buffer.alloc(0)), h.lastIndexOf(Buffer.alloc(0)), h.includes(Buffer.from('lo'), -2));
`)
}

// An array index argument that is a function or an object (ToPrimitive,
// then ToIntegerOrInfinity), and a listener registered through a run-time
// method key, as in Node.
func TestE2EArrayIndexObjectArgJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const a = [1, 2, 3, 4];
console.log(a.fill(0, () => 1), [1, 2, 3].fill(9, {}), [1, 2, 3].fill(7, { valueOf() { return 2; } }), [1, 2, 3].slice({ valueOf() { return 1; } }));
const o = JSON.parse('{}'); o.on = (ev, fn) => console.log('on', ev, typeof fn);
for (const k of ['on']) o[k]('data', function (d) {});
`)
}

// dgram's socket.close(cb) ignores a non-function, as in Node.
func TestE2EDgramCloseNonFunctionJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const dgram = require('dgram');
const s = dgram.createSocket('udp4');
s.bind(0, () => {
  s.on('close', () => console.log('closed'));
  s.close(5);
});
`)
}

// Array.prototype.concat with spread arguments (`[].concat(...parts)` form):
// each spread element an argument, flattened one level, as in Node.
func TestE2EConcatSpreadArgsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const p = [[1, 2], [3]];
const m = [4, 5];
const a = [].concat(...p, ...m);
console.log(a, [0].concat(...p), [].concat(...[[6]], 7));
const fs = [[() => 1, () => 2]];
const cs = [].concat(...fs, ...[() => 3]);
console.log(cs.length, cs.map((f) => f()));
`)
}

// The same in the strict lane, typed.
func TestE2EConcatSpreadArgsStrict(t *testing.T) {
	assertSameAsNode(t, `
const p: number[][] = [[1, 2], [3]];
const a: number[] = ([] as number[]).concat(...p);
console.log(a, a.length, [0].concat(...p).map((x) => x * 2));
const s: string[] = ['x'].concat(...[['y'], ['z']]);
console.log(s.join('-'));
`)
}

// A function destructured at the top level from a module object
// (`const { open } = fs.promises`) and called inside a function, as in
// Node: held as a box when its signature has no representation.
func TestE2EDestructuredModuleFunctionInFunction(t *testing.T) {
	assertSameAsNode(t, `
import fs from 'fs';
import path from 'path';
const { open, readFile } = fs.promises;
const { join } = path;
async function main(): Promise<void> {
  const fh = await open('/dev/null', 'r');
  const n: number = fh.fd + 1;
  console.log(n > 1, join('a', 'b'));
  const st = await fh.stat();
  console.log(st.isFile(), typeof st.size);
  await fh.close();
  const txt = await readFile('/dev/null', 'utf8');
  console.log(JSON.stringify(txt), txt.length);
  try {
    await open('/nonexistent/x', 'r');
  } catch (e) {
    console.log((e as NodeJS.ErrnoException).code);
  }
}
main();
`)
}

// ToBoolean of a boxed bigint: 0n (and -0n) is falsy, as in Node.
func TestE2EBoxedBigIntTruthyJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const x = JSON.parse('0') ? 1n : 0n;
const o = { v: x };
const a = [0n, 1n, -0n, 5n];
for (const y of a) console.log(Boolean(y), !y, y ? 't' : 'f');
let z; z = 0n; if (z) console.log('bad'); else console.log('good');
`)
}

// An http2 server's timeout holds any value (`server.timeout = 0n`):
// falsy, so no session timer starts, as in Node.
func TestE2EHttp2ServerTimeoutAnyJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const http2 = require('http2');
const server = http2.createServer();
server.timeout = 0n;
console.log(typeof server.timeout, server.timeout);
server.on('request', (req, res) => res.end('ok'));
server.listen(0, () => {
  const client = http2.connect('http://localhost:' + server.address().port);
  const req = client.request({ ':path': '/' });
  let body = '';
  req.setEncoding('utf8');
  req.on('data', (d) => { body += d; });
  req.on('end', () => { console.log(body); client.close(); server.close(); });
  req.end();
});
`)
}

// An async method overriding one that returns nothing (a stream's
// _final/_write), as in Node.
func TestE2EAsyncOverrideOfVoidMethodJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const { Duplex, Writable } = require('stream');
const { setTimeout: sleep } = require('timers/promises');
class Foo extends Duplex {
  async _final(callback) {
    await sleep(1);
    console.log('final ran');
    callback();
  }
  _read() {}
}
const foo = new Foo();
foo._write = (chunk, encoding, cb) => { console.log('write', String(chunk)); cb(); };
foo.end('test', () => console.log('end cb'));
foo.on('finish', () => console.log('finish'));
class W extends Writable {
  async _write(chunk, enc, cb) { await sleep(1); console.log('w', String(chunk)); cb(); }
}
const w = new W();
w.write('a'); w.end('b', () => console.log('w done'));
`)
}

// The same in the strict lane, with parameters to adapt (number to any,
// a trailing optional parameter).
func TestE2EAsyncOverrideOfVoidMethod(t *testing.T) {
	assertSameAsNode(t, `
class Base {
  run(x: number, cb: (r: string) => void): void {
    cb('base ' + x);
  }
  go(): void {
    this.run(7, (r) => console.log('cb', r));
  }
}
class Child extends Base {
  async run(x: any, cb: (r: string) => void): Promise<void> {
    await new Promise<void>((resolve) => setTimeout(resolve, 1));
    cb('child ' + typeof x + ' ' + x);
  }
}
class Opt extends Base {
  async run(x: number, cb: (r: string) => void, extra?: string): Promise<void> {
    await Promise.resolve();
    cb('opt ' + x + ' ' + extra);
  }
}
new Base().go();
new Child().go();
new Opt().go();
const b: Base = new Child();
b.run(3, (r) => console.log('direct', r));
`)
}

// `return f()` of a call that returns nothing, in a function returning a
// union: undefined, as in Node.
func TestE2EReturnVoidCallInUnionJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
function f(n) {
  switch (n) {
    case 0: return setImmediate(() => {});
    case 1: return process.nextTick(() => {});
    case 2: return true;
    default: return console.log('x');
  }
}
for (let i = 0; i < 4; i++) { const r = f(i); console.log(i, typeof r, r === undefined); }
`)
}

// A JavaScript class in a block that names one of the block's locals: its
// constructor-created fields (this.buffer = …) exist beside the hidden
// capture, and an in-place array method on one works, as in Node.
func TestE2EBlockClassCaptureCtorFieldsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
{
  const hwm = 4;
  class A { constructor() { this.buffer = ['x']; } get() { const c = this.buffer.shift(); return hwm + this.buffer.length; } }
  console.log(new A().get());
}
const { Readable } = require('stream');
{
  const highWaterMark = 50;
  class ArrayReader extends Readable {
    constructor(opt) {
      super({ highWaterMark });
      this.buffer = new Array(highWaterMark * 2).fill(0).map(String);
    }
    _read(size) {
      while (this.buffer.length) {
        const chunk = this.buffer.shift();
        if (!this.buffer.length) {
          this.push(chunk);
          this.push(null);
          return true;
        }
        if (!this.push(chunk))
          return;
      }
    }
  }
  function onRead() {
    while (null !== (stream.read())) {
      // Remove the 'readable' listener before unshifting
      stream.removeListener('readable', onRead);
      stream.unshift('a');
      stream.on('data', (chunk) => {
        console.log(chunk.length);
      });
      break;
    }
  }
  const stream = new ArrayReader();
  stream.once('readable', onRead);
  stream.on('end', () => console.log('end'));
}
`)
}

// The legacy Stream carries the module's classes and functions as statics
// (Stream.Readable, Stream.pipeline), as Node's stream.js assigns them.
func TestE2EStreamStaticsStrict(t *testing.T) {
	assertSameAsNode(t, `
import { Stream, PassThrough } from 'stream';
import stream from 'stream';
const R = Stream.Readable;
const W = Stream.Writable;
const r = new R({ read() {} });
const w = new W({ write(chunk: any, enc: BufferEncoding, cb: () => void) { console.log('got', String(chunk)); cb(); } });
r.pipe(w);
r.push('hi');
r.push(null);
console.log(Stream.Stream === Stream, Stream.PassThrough === PassThrough, typeof Stream.pipeline, stream.Readable === R);
`)
}

// OutgoingMessage._writeRaw(data, encoding?, callback?) with a string and a
// callback in the encoding's place (an informational response written by
// hand), as in Node.
func TestE2EHttpWriteRawInformationJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const http = require('http');
const server = http.createServer((req, res) => {
  res._writeRaw('HTTP/1.1 102 Processing\r\n');
  res._writeRaw('Foo: Bar\r\n');
  res._writeRaw('\r\n', () => console.log('raw cb'));
  res.writeHead(200, { 'Content-Type': 'text/plain' });
  res.end('body\n');
});
server.listen(0, function () {
  const req = http.request({ port: this.address().port, path: '/' });
  req.on('information', (r) => console.log('info', r.statusCode, r.headers.foo));
  req.on('response', (r) => {
    let b = '';
    r.setEncoding('utf8');
    r.on('data', (d) => { b += d; });
    r.on('end', () => { console.log('response', r.statusCode, JSON.stringify(b)); server.close(); });
  });
  req.end();
});
`)
}

// A class whose base is a loop's variable (a class expression or a
// declaration in the loop's block), over web streams too: their
// prototypes are real objects, as in Node.
func TestE2ELoopVariableClassBaseJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
for (const StreamClass of [ReadableStream, WritableStream, TransformStream]) {
  const extended = class extends StreamClass {};
  const o = new extended();
  console.log(o instanceof StreamClass, o instanceof extended);
}
for (const K of [ReadableStream, WritableStream, TransformStream]) {
  const o = new K();
  console.log(typeof K.prototype, Object.getPrototypeOf(o) === K.prototype, Object.prototype.toString.call(o), K.prototype.constructor === K, Object.getOwnPropertyNames(K.prototype).sort().join());
}
console.log(typeof ReadableStream.prototype.getReader);
class A {} class B {}
for (const K of [A, B]) { const E = class extends K {}; const o = new E(); console.log(o instanceof K, o instanceof E, E.name); }
for (const K of [A, B]) { class F extends K {}; console.log(new F() instanceof K); }
`)
}

// structuredClone counts its arguments and converts its options as Node's
// WebIDL does, called directly and by value.
func TestE2EStructuredCloneArgsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
for (const f of [() => structuredClone(), () => structuredClone(undefined, ''), () => structuredClone(undefined, 1), () => structuredClone(undefined, { transfer: 1 }), () => structuredClone(undefined, { transfer: '' }), () => structuredClone(undefined, { transfer: null }), () => structuredClone(5, 'x'), () => structuredClone({ a: 1 }, 7)]) {
  try { console.log('ok', f()); } catch (e) { console.log(e.name, e.code, e.message); }
}
console.log(structuredClone(undefined), structuredClone(undefined, null), structuredClone(undefined, {}), structuredClone({ a: [1] }, { transfer: [] }));
console.log(structuredClone({}, { transfer: { *[Symbol.iterator]() {} } }));
const sc = structuredClone;
try { sc(); } catch (e) { console.log(e.code); }
console.log(sc({ b: 2 }, undefined));
`)
}

func TestE2EStructuredCloneArgs(t *testing.T) {
	assertSameAsNode(t, `
const a = structuredClone({ x: [1, 2] }, { transfer: [] });
console.log(a);
const opts: any = { transfer: 1 };
try { structuredClone({ y: 1 }, opts); } catch (e) { console.log((e as any).code); }
const sc = structuredClone;
console.log(sc(new Map([[1, 2]])), sc.length);
for (const K of [ReadableStream, WritableStream, TransformStream]) {
  const o = new K();
  console.log(Object.getPrototypeOf(o) === K.prototype);
}
`)
}

// stream/web exports the WHATWG stream globals: named, default and
// namespace imports all read the globals, as in Node.
func TestE2EStreamWebExports(t *testing.T) {
	assertSameAsNode(t, `
import webstreams from 'stream/web';
import { CountQueuingStrategy, TextEncoderStream, WritableStreamDefaultWriter } from 'stream/web';
console.log(webstreams.ReadableStream === ReadableStream, webstreams.CountQueuingStrategy === CountQueuingStrategy);
const q = new CountQueuingStrategy({ highWaterMark: 3 });
console.log(q.highWaterMark, typeof TextEncoderStream, typeof WritableStreamDefaultWriter);
const w = new webstreams.WritableStream({ write(c: string) { console.log('w', c); } });
const wr = w.getWriter();
console.log(wr instanceof WritableStreamDefaultWriter);
wr.write('x').then(() => console.log('done'));
`)
}

// A builtin module's default import is its exports object (buffer,
// stream/web), as is a namespace import.
func TestE2EBuiltinModuleDefaultImport(t *testing.T) {
	assertSameAsNode(t, `
import buffer from 'buffer';
import * as web from 'stream/web';
import * as bns from 'buffer';
console.log(buffer.Buffer === Buffer, buffer.Blob === Blob, bns.atob('aGk='), web.TransformStream === TransformStream);
console.log(buffer.Buffer.from('hi').toString('hex'));
`)
}

func TestE2EStreamWebGlobalsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
import assert from 'assert';
import webstreams from 'stream/web'




assert.strictEqual(ReadableStream, webstreams.ReadableStream);
assert.strictEqual(ReadableStreamDefaultReader, webstreams.ReadableStreamDefaultReader);
assert.strictEqual(ReadableStreamDefaultController, webstreams.ReadableStreamDefaultController);
assert.strictEqual(TransformStream, webstreams.TransformStream);
assert.strictEqual(TransformStreamDefaultController, webstreams.TransformStreamDefaultController);
assert.strictEqual(WritableStream, webstreams.WritableStream);
assert.strictEqual(WritableStreamDefaultWriter, webstreams.WritableStreamDefaultWriter);
assert.strictEqual(WritableStreamDefaultController, webstreams.WritableStreamDefaultController);
assert.strictEqual(ByteLengthQueuingStrategy, webstreams.ByteLengthQueuingStrategy);
assert.strictEqual(CountQueuingStrategy, webstreams.CountQueuingStrategy);
assert.strictEqual(TextEncoderStream, webstreams.TextEncoderStream);
assert.strictEqual(TextDecoderStream, webstreams.TextDecoderStream);
assert.strictEqual(CompressionStream, webstreams.CompressionStream);
assert.strictEqual(DecompressionStream, webstreams.DecompressionStream);

console.log('ok');
`)
}

// ES2025 Set algebra over Sets, Maps and set-like objects, with
// GetSetRecord's validation, as in Node.
func TestE2ESetAlgebraJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const a = new Set(['x', 'y', 1, -0]);
const b = new Set(['y', 'z', 0]);
console.log(a.union(b), a.intersection(b), a.difference(b), a.symmetricDifference(b));
console.log(a.isSubsetOf(b), new Set(['y']).isSubsetOf(b), a.isSupersetOf(new Set(['x'])), a.isDisjointFrom(new Set(['q'])), a.isDisjointFrom(b));
const m = new Map([['x', 1], ['w', 2]]);
console.log(a.union(m), a.intersection(m), a.difference(m));
const like = { size: 2, has: (v) => v === 'x' || v === 'q', keys: () => ['x', 'q'][Symbol.iterator]() };
console.log(a.union(like), a.intersection(like), a.isSupersetOf(like));
const o = { a: new Set([1, 2, 3]) };
console.log([...o.a.difference(new Set([2]))].sort(), o.a.union(new Set([4])).size);
for (const bad of [null, 5, {}, { size: NaN, has() {}, keys() {} }, { size: -1, has() {}, keys() {} }, { size: 1, has: 1, keys() {} }, { size: 1, has() {}, keys: 2 }]) {
  try { a.union(bad); console.log('no throw'); } catch (e) { console.log(e.constructor.name); }
}
try { Set.prototype.union.call([], b); } catch (e) { console.log(e.constructor.name); }
const big = new Set([1, 2, 3, 4, 5]);
console.log(big.intersection(new Set([5, 1])), big.difference(new Set([5, 1])));
console.log(typeof Set.prototype.union, Set.prototype.union.length, Set.prototype.isDisjointFrom.name);
`)
}

// The same over typed Sets (a Set<number> holds raw doubles), the results
// read back through their declared types.
func TestE2ESetAlgebra(t *testing.T) {
	assertSameAsNode(t, `
const a = new Set([1, 2, 3, 4]);
const b = new Set([3, 4, 5]);
const u: Set<number> = a.union(b);
let sum = 0;
for (const x of u) sum += x;
console.log(u, u.size, u.has(5), sum, [...a.intersection(b)].map((x) => x * 10));
const d = a.difference(b);
d.add(99);
console.log(d, d.has(99), a.symmetricDifference(b), a.isSubsetOf(u), u.isSupersetOf(b), a.isDisjointFrom(new Set([7])));
const s1 = new Set(['a', 'b']);
const s2: Set<string> = s1.union(new Set(['c']));
console.log(s2, [...s2].join('+'), s1.intersection(new Set(['b', 'z'])));
const mixed: Set<string | number> = s1.union(a);
console.log(mixed, mixed.size);
const m = new Map<string, number>([['a', 1], ['q', 2]]);
console.log(s1.difference(m), s1.intersection(m));
`)
}

// A binding narrowed present by a compound guard (`m !== undefined &&
// m !== ""`) or by the else of `p === undefined` reads its payload, the
// binding initialized from a possibly-absent read included.
func TestE2ENarrowedCompoundGuard(t *testing.T) {
	assertSameAsNode(t, `
function up(opt?: string): string {
  let m = opt;
  if (m !== undefined && m !== '') { m = m.toUpperCase(); } else { m = 'GET'; }
  return m;
}
class Q {
  private waiters: { resolve: (v: number) => void }[] = [];
  push(v: number) {
    const p = this.waiters.shift();
    if (p === undefined) { console.log('none', v); } else { p.resolve(v); }
  }
  wait() { this.waiters.push({ resolve: (v) => console.log('got', v) }); }
}
console.log(up('post'), up(''), up());
const q = new Q(); q.push(1); q.wait(); q.push(2);
`)
}

// Calling a primitive throws at run time; its (never produced) result
// called again must still compile.
func TestE2ECallOfPrimitiveResultJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
try { const f = "x"
(function(){ console.log(1); })() } catch (e) { console.log(e.constructor.name, e.message); }
try { const n = null; n(1)(); } catch (e) { console.log(e.message); }
try { (5)(() => 1)(2); } catch (e) { console.log(e.message); }
`)
}

// Response/Request clone(): a teed body read on each side, own Headers,
// an unusable body refused (Fetch's messages).
func TestE2EBodyCloneJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
(async () => {
  const r = new Response('hi', { status: 201, statusText: 'Made', headers: { 'x-a': '1' } });
  const c = r.clone();
  c.headers.set('x-a', '2');
  console.log(c.status, c.statusText, c.ok, r.headers.get('x-a'), c.headers.get('x-a'), r.bodyUsed, c.bodyUsed);
  console.log(await c.text(), c.bodyUsed, r.bodyUsed);
  console.log(await r.text(), r.bodyUsed);
  const s = new Response(new ReadableStream({ start(ctl) { ctl.enqueue(new TextEncoder().encode('ab')); ctl.enqueue(new TextEncoder().encode('cd')); ctl.close(); } }));
  const s2 = s.clone();
  console.log(await s.text(), await s2.text());
  const n = new Response(null, { status: 204 });
  const n2 = n.clone();
  console.log(n2.body, n2.status, await n2.text());
  const j = Response.json({ a: 1 }).clone();
  console.log(await j.json(), j.headers.get('content-type'));
  const b = new Response('bytes'); const b2 = b.clone();
  console.log(new Uint8Array(await b.arrayBuffer()).length, await b2.text());
  try { r.clone(); } catch (e) { console.log(e.constructor.name, e.message); }
  const q = new Request('http://x/', { method: 'POST', body: 'b', headers: { 'x-q': 'v' } });
  const q2 = q.clone();
  console.log(q2.method, q2.url, q2.headers.get('x-q'), await q2.text(), await q.text());
  try { q.clone(); } catch (e) { console.log(e.constructor.name, e.message); }
})();
`)
}

func TestE2EBodyClone(t *testing.T) {
	assertSameAsNode(t, `
(async () => {
  const r = new Response('hi', { status: 201, statusText: 'Made', headers: { 'x-a': '1' } });
  const c = r.clone();
  c.headers.set('x-a', '2');
  console.log(c.status, c.statusText, c.ok, r.headers.get('x-a'), c.headers.get('x-a'), r.bodyUsed, c.bodyUsed);
  console.log(await c.text(), c.bodyUsed, r.bodyUsed);
  console.log(await r.text(), r.bodyUsed);
  const s = new Response(new ReadableStream({ start(ctl) { (ctl as any).enqueue(new TextEncoder().encode('ab')); ctl.enqueue(new TextEncoder().encode('cd')); ctl.close(); } }));
  const s2 = s.clone();
  console.log(await s.text(), await s2.text());
  const n = new Response(null, { status: 204 });
  const n2 = n.clone();
  console.log(n2.body, n2.status, await n2.text());
  const j = Response.json({ a: 1 }).clone();
  console.log(await j.json(), j.headers.get('content-type'));
  const b = new Response('bytes'); const b2 = b.clone();
  console.log(new Uint8Array(await b.arrayBuffer()).length, await b2.text());
  try { r.clone(); } catch (e: any) { console.log(e.constructor.name, e.message); }
  const q = new Request('http://x/', { method: 'POST', body: 'b', headers: { 'x-q': 'v' } });
  const q2 = q.clone();
  console.log(q2.method, q2.url, q2.headers.get('x-q'), await q2.text(), await q.text());
  try { q.clone(); } catch (e: any) { console.log(e.constructor.name, e.message); }
})();
`)
}

// A fetched Response's clone: both sides read the transfer's bytes.
func TestE2EFetchCloneJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const http = require('http');
const srv = http.createServer((req, res) => { res.setHeader('x-s', 'v'); res.end('payload-' + req.url); });
srv.listen(0, async () => {
  const port = srv.address().port;
  const r = await fetch('http://127.0.0.1:' + port + '/a');
  const c = r.clone();
  console.log(c.status, c.ok, c.headers.get('x-s'), c.url === r.url, c.type);
  console.log(await c.text());
  console.log(await r.text());
  try { r.clone(); } catch (e) { console.log(e.message); }
  const r2 = await fetch('http://127.0.0.1:' + port + '/b');
  const c2 = r2.clone();
  console.log(await r2.text(), await c2.text());
  srv.close();
});
`)
}

// A class extending a binding that only ever holds one of the program's
// classes (an alias, a loop over class literals, an element read) is a
// real subclass of the class it holds: its methods, constructor and name.
func TestE2EClassValueBaseSpecializedJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
class A { constructor(o) { this.v = o.v; } }
class B { constructor(o) { this.v = o.v * 2; } w() { return 9; } }
const Al = A; class S extends Al { m() { return 1; } }
const s = new S({ v: 3 }); console.log(typeof s.m, s.constructor.name, s.v, s instanceof Al, s instanceof A);
let Bl = B; class S2 extends Bl { constructor(o) { super(o); this.x = 1; } }
const s2 = new S2({ v: 2 }); console.log(s2.v, s2.x, s2.w(), S2.name);
for (const K of [A, B]) {
  class T extends K { m() { return 2; } }
  const t = new T({ v: 7 }); console.log(typeof t.m, t.v, t.constructor.name, t instanceof K, Object.getPrototypeOf(T) === K);
  const E = class extends K { m() { return 3; } };
  const o = new E({ v: 1 }); console.log(o.m(), o instanceof E, E.name, o.v);
}
function f() { const K = [A][0]; class T extends K { m() { return 2; } } return new T({ v: 4 }); }
console.log(f().m(), f().v);
`)
}

// String.prototype.replace/replaceAll: a string search expands the
// replacement's $ patterns; a replacement known only at run time is called
// when it is a function (a bound one included), else converted.
func TestE2EStringReplaceSubstitutionJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
console.log("abcb".replace(/(b)/g, (...a) => a.join("|")), "abcb".replace("b", (...a) => a.join("|")), "abcb".replaceAll("b", (...a) => a.join("|")));
console.log("abcb".replace("b", "[$&|$\x60|$'|$$|$1]"), "abcb".replaceAll("b", "<$\x60>"), "ab".replaceAll("", "$&-"));
 function f(m) { return "[" + m + "]"; }
 const g = f.bind(null);
 const h = (m, off) => "<" + off + ">";
 const o = { k: 1, m(x) { return x + this.k; } };
 console.log("abcb".replace(/b/g, o.m.bind(o)), "abcb".replace("b", o.m.bind(o)), "abcb".replaceAll("b", o.m.bind(o)));
`)
}

func TestE2EStringReplaceSubstitution(t *testing.T) {
	assertSameAsNode(t, `
const r = "$&$&";
console.log("abcb".replace("b", r), "abcb".replace(/b/g, r), "abcb".replaceAll("b", "[$&]"));
const x: any = "$&$&"; const s: string = x;
console.log("abcb".replace("b", s), "abcb".replace(/b/, x));
const n: any = 5; console.log("abcb".replace("b", n), "abcb".replace(/b/g, n));
function f(m: string): string { return "[" + m + "]"; }
const g = f.bind(null);
console.log("abcb".replace(/b/g, g), "abcb".replace("b", f.bind(null)));
const a: any = f;
console.log("abcb".replace(/b/g, a), "abcb".replace("b", a), "abcb".replace("b", "$&$&" as any));
`)
}

// A field the constructor sets to undefined holds whatever is stored later
// (a bound method as a replace callback storing a number).
func TestE2EUndefinedCtorFieldEvolvesJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
// META: global=window,worker
'use strict';

class LipFuzzTransformer {
  constructor(substitutions) {
    this.substitutions = substitutions;
    this.partialChunk = '';
    this.lastIndex = undefined;
  }

  transform(chunk, controller) {
    chunk = this.partialChunk + chunk;
    this.partialChunk = '';
    // lastIndex is the index of the first character after the last substitution.
    this.lastIndex = 0;
    chunk = chunk.replace(/\{\{([a-zA-Z0-9_-]+)\}\}/g, this.replaceTag.bind(this));
    // Regular expression for an incomplete template at the end of a string.
    const partialAtEndRegexp = /\{(\{([a-zA-Z0-9_-]+(\})?)?)?$/g;
    // Avoid looking at any characters that have already been substituted.
    partialAtEndRegexp.lastIndex = this.lastIndex;
    this.lastIndex = undefined;
    const match = partialAtEndRegexp.exec(chunk);
    if (match) {
      this.partialChunk = chunk.substring(match.index);
      chunk = chunk.substring(0, match.index);
    }
    controller.enqueue(chunk);
  }

  flush(controller) {
    if (this.partialChunk.length > 0) {
      controller.enqueue(this.partialChunk);
    }
  }

  replaceTag(match, p1, offset) {
    let replacement = this.substitutions[p1];
    if (replacement === undefined) {
      replacement = '';
    }
    this.lastIndex = offset + replacement.length;
    return replacement;
  }
}
const t = new LipFuzzTransformer({ in: 'out', x: 'yy' });
const out = [];
const ctl = { enqueue: (c) => out.push(c) };
t.transform('a {{in}} b {{x', ctl); t.transform('}} c {{', ctl); t.flush(ctl);
console.log(JSON.stringify(out));
`)
}

// A JSDoc type naming nothing the program or the library declares is any
// in JavaScript, unchecked.
func TestE2EJSDocUnknownTypeJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
/** @param {bool} b */ function g(b) { return !!b; }
/** @param {Array<Preload>} p @returns {Window} */ function h(p) { return { n: p.length }; }
console.log(g(1), g(0), h([1, 2]).n);
`)
}

// Destructuring a host object (Response, URL) reads its properties, not its
// runtime layout; a property it lacks is undefined.
func TestE2EHostObjectDestructuringJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
(async () => {
  const response = new Response('abc');
  const { body } = response;
  await body.cancel();
  console.log(body === response.body);
  const r2 = new Response(new Blob(['T'], { type: 'text/plain' }));
  const reader = r2.body.getReader();
  const closed = reader.closed.then(() => reader.cancel());
  reader.read().then(function readMore({ done, value }) { if (!done) return reader.read().then(readMore); });
  await closed; console.log('closed');
})();
(async () => {
  const { status, ok, headers, statusText = 'dflt', missing = 7 } = new Response('x', { status: 201 });
  console.log(status, ok, headers.get('content-type'), statusText, missing);
  const u = new URL('http://h.example:81/p?q=1');
  const { hostname, port, searchParams } = u;
  console.log(hostname, port, searchParams.get('q'));
})();
`)
}

// ArrayBuffer.prototype.transfer/transferToFixedLength move the bytes and
// detach the buffer: its views read length 0 and their methods throw.
func TestE2EArrayBufferTransfer(t *testing.T) {
	assertSameAsNode(t, `
const ab = new ArrayBuffer(8);
const v = new Uint8Array(ab); v[0] = 7; v[7] = 9;
const t = ab.transfer(4);
console.log(ab.byteLength, ab.detached, t.byteLength, t.detached, v.length, v[0], new Uint8Array(t)[0]);
try { ab.transfer(); } catch (e) { console.log((e as Error).name, (e as Error).message); }
try { v.join(','); } catch (e) { console.log((e as Error).name, (e as Error).message); }
const u = new Uint8Array(4); u[1] = 5; const b = u.buffer; const t2 = b.transfer(6);
console.log(u.length, b.detached, t2.byteLength, new Uint8Array(t2).join(','));
const r = new ArrayBuffer(4, { maxByteLength: 16 });
const rt = r.transfer(); console.log(rt.resizable, rt.maxByteLength, r.resizable, r.detached, r.maxByteLength);
const f = rt.transferToFixedLength(2); console.log(f.resizable, f.byteLength);
try { new ArrayBuffer(2, { maxByteLength: 4 }).transfer(9); } catch (e) { console.log((e as Error).name); }
const any1: any = new Uint8Array(3).buffer; const t3 = any1.transfer(); console.log(t3.byteLength, any1.detached);
`)
}

// A class in a closure extends a variable another closure also captures.
func TestE2EClassHeritageCapturedJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
function test(f) { f(); }
for (const array of ['Int8Array', 'Uint8Array']) {
  const ctor = globalThis[array];
  test(function () { console.log(ctor.BYTES_PER_ELEMENT); });
  test(function () {
    class Buffer extends ctor {}
    console.log(new Buffer(2).length);
  });
}
`)
}

// A typed array subclass instance's constructor is its class.
func TestE2ETypedArraySubclassConstructor(t *testing.T) {
	assertSameAsNode(t, `
class C extends Int8Array {}
const c = new C(2);
console.log(c.constructor === C, c.constructor.name, c instanceof C, c.length);
class D extends Int8Array { hi() { return 1; } }
const d = new D(2); console.log(d instanceof D, d.hi());
`)
}

// A subclass method over an inherited getter shadows it: read through any
// type, the property is the method. A queuing strategy's size is a
// non-constructor named size.
func TestE2EMethodShadowsInheritedGetterJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
class Base {
  get size() { return (n) => n * 10; }
  get hwm() { return 4; }
}
class Sub extends Base {
  size() { return 2; }
  other() { return true; }
}
const s = new Sub();
console.log(s.size(), s.hwm, s.other(), typeof s.size);
const b = [s, new Base()][0];
console.log(typeof b.size, b.size.call(s));
console.log(new Base().size(3));
const q = new CountQueuingStrategy({ highWaterMark: 1 });
class Q extends CountQueuingStrategy { size() { return 2; } }
const qq = new Q({ highWaterMark: 77 });
console.log(qq.size(), qq.highWaterMark, q.size(), qq.constructor.name, 'prototype' in q.size);
const cs = new CountQueuingStrategy({ highWaterMark: 1 }).size;
const bs = new ByteLengthQueuingStrategy({ highWaterMark: 1 }).size;
console.log(cs.name, cs.length, bs.name, bs.length, 'prototype' in cs);
try { new cs(); } catch (e) { console.log(e.name); }
`)
}

// An object literal accessor reads and writes its enclosing function's
// locals (by reference, a later write seen).
func TestE2EObjectAccessorCapturesLocals(t *testing.T) {
	assertSameAsNode(t, `
function f() {
  let n = 5;
  const arr = new Uint8Array(3);
  const o = {
    k: 1,
    get a() { return n + arr.length + this.k; },
    get b() { return arr.buffer.byteLength; },
    set c(v: number) { n = v; },
    get plain() { return this.k * 2; },
  };
  console.log(o.a, o.b, o.plain);
  o.c = 10; n += 1;
  console.log(o.a, Object.keys(o).join(','), JSON.stringify(o));
}
f();
function test(f: () => void) { f(); }
test(() => {
  const decoder = new TextDecoder();
  const arr = new Uint8Array(10).fill(42);
  const options = {
    get stream() {
      arr.buffer.transfer(0);
      return false;
    }
  };
  console.log(JSON.stringify(decoder.decode(arr, options)), arr.length);
});
`)
}

func TestE2EObjectAccessorCapturesLocalsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
function f() {
  let n = 5;
  const arr = new Uint8Array(3);
  const o = {
    k: 1,
    get a() { return n + arr.length + this.k; },
    get b() { return arr.buffer.byteLength; },
    set c(v) { n = v; },
    get plain() { return this.k * 2; },
  };
  console.log(o.a, o.b, o.plain);
  o.c = 10; n += 1;
  console.log(o.a, Object.keys(o).join(','), JSON.stringify(o));
}
f();
`)
}

// A function declared in a block reads a top-level destructured binding;
// Promise.all over a map whose callback returns any (boxed members); a
// key usage the algorithm refuses names it with an article.
func TestE2EBlockFunctionReadsPatternGlobalJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const assert = require('assert');
const { subtle } = globalThis.crypto;
const { a: five } = { a: 5 };
const [seven] = [7];
{
  async function test() { return typeof subtle + five + seven; }
  test().then((t) => console.log(t));
}
{
  function t2() { return typeof subtle + five + seven; }
  console.log(t2());
}
(async () => {
  const keyData = new Uint8Array(32);
  const r = await Promise.all([1, null, undefined, {}, []].map((format) =>
    assert.rejects(subtle.importKey(format, keyData, {}, false, ['wrapKey']), { code: 'ERR_INVALID_ARG_VALUE' })));
  console.log(r.length);
  try {
    await subtle.importKey('raw', keyData, { name: 'HMAC', hash: 'SHA-256' }, false, ['deriveBits']);
  } catch (e) { console.log(e.name, e.message); }
})();
`)
}

// Class fields keyed by null, undefined or an expression (JS; TypeScript
// rejects them with TS1166).
func TestE2EClassFieldRuntimeKeysJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
let C = class {
  [null] = null;
  static [null] = null;
};
let c = new C();
console.log(c[null], C[null], c[String(null)], C[String(null)]);
class D { [undefined] = 1; static [undefined] = 2; [1 + 1] = 3; }
console.log(new D()[undefined], D[undefined], new D()[2]);
`)
}

// Spreading a class instance copies the properties added at run time
// (assignment, Object.defineProperty) and performance.nodeTiming's own
// milestones; a missing name a destructuring require reads is undefined.
func TestE2EClassInstanceSpreadOwnPropsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const { performance } = require('perf_hooks');
const { inspect, notAnExport } = require('util');
console.log(typeof inspect, typeof notAnExport);
class V { constructor() { this.f = 0; } }
const v = new V(); v.z = 1;
Object.defineProperty(v, 'g', { enumerable: true, get() { return 2; } });
const c = { ...v };
console.log(Object.keys(c).join(), c.z, c.g);
class E { }
const e = new E(); e.z = 1;
console.log(Object.keys({ ...e }).join());
class U { constructor() { Object.defineProperty(this, 'k', { enumerable: true, value: 3 }); } }
console.log(Object.keys({ ...new U() }).join());
const t = { ...performance.nodeTiming };
console.log(Object.keys(t).filter((k) => k !== 'uvMetricsInfo').join(), t.name, typeof t.duration);
`)
}

// Object.defineProperties' value descriptor on a declared field makes it
// an own property.
func TestE2EDefinePropertiesDeclaredField(t *testing.T) {
	assertSameAsNode(t, `
class T {
  declare readonly a: string;
  declare readonly b: number;
  constructor() {
    Object.defineProperties(this, {
      a: { enumerable: true, configurable: true, value: 'x' },
      b: { enumerable: true, configurable: true, get(): number { return 7; } },
    });
  }
}
const t = new T();
console.log(Object.keys(t).join(), t.a, t.b, JSON.stringify(t));
`)
}

// Reflect read by a computed key, and a Reflect method called with a
// spread: the namespace's object, as tsc types `typeof Reflect`.
func TestE2EReflectComputedAndSpreadCalls(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
const log = [];
const h = {};
for (let prop of Object.getOwnPropertyNames(Reflect)) {
  h[prop] = function (...args) { log.push(prop); return Reflect[prop](...args); };
}
const p = new Proxy({ a: 1 }, h);
console.log(p.a, 'a' in p, Object.keys(p).join(), log.join());
console.log(Reflect['ownKeys']({ c: 1 }).join(), Reflect.has(...[{ b: 1 }, 'b']), Reflect.get(...[{ q: 4 }, 'q']));
`)
	assertSameAsNode(t, `
console.log(Reflect['ownKeys']({ c: 1 }).join());
const args: [object, string] = [{ b: 1 }, 'b'];
console.log(Reflect.has(...args));
`)
}

// A typed array built from an iterable (an iterator, a generator, a Map's
// keys, a Set); a parameter its call sites type one way and its body
// reassigns another (JS: any).
func TestE2ETypedArrayFromIterableJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
let iv = new Uint8Array(Array(4).keys());
console.log(iv.join(), new Uint8Array([1, 2].values()).join());
const m = new Map([[1, 2]]);
console.log(new Float64Array(m.keys()).join(), new Uint8Array(new Set([3, 4])).join());
function* g() { yield 5; yield 6; }
console.log(new Int16Array(g()).join());
function f(bytes) {
  if (!bytes) return null;
  bytes = new Uint8Array(bytes);
  return bytes.length;
}
console.log(f(new ArrayBuffer(2)));
`)
	assertSameAsNode(t, `
const iv = new Uint8Array(Array(4).keys());
console.log(iv.join(), new Uint8Array(new Set([3, 4])).join());
`)
}

func TestE2EBoxedConcatAndDynamicCollectionsJS(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
function run(options) {
  options.vectors.forEach(function (vector) {
    const s = vector.name + options.wrongLabel;
    console.log(s, vector.name + (options.missing || ' wrong'));
  });
}
run({ vectors: [{ name: 'a' }], dataLabel: 'p' });
const g = globalThis;
console.log(typeof g.crypto, typeof g.crypto.subtle);
const cache = new WeakMap();
function cachedKey(c, v, usages, importer) {
  let keys = c.get(v);
  if (keys === undefined) { keys = new Map(); c.set(v, keys); }
  const k = usages.join(',');
  if (!keys.has(k)) keys.set(k, importer());
  return keys.get(k);
}
const vec = {};
console.log(cachedKey(cache, vec, ['v'], () => ({ t: 1 })), cachedKey(cache, vec, ['v'], () => 2));
const w = new WeakMap();
const o = {};
console.log(w.get(o) === undefined);
w.set(o, 0);
console.log(w.get(o) === 0);
(async () => {
  const p = await cachedKey(cache, vec, ['s'], () => Promise.resolve({ type: 'public' }));
  console.log(p.type);
})();
`)
	assertSameAsNode(t, `
const keys: any = new Map();
keys.set(1, { t: 1 });
keys.set('a', 2);
console.log(keys.get(1), keys.get('a'), keys.size);
let k2: any = 0;
k2 = new Map();
k2.set(1, [1]);
console.log(k2.get(1));
`)
}

func TestE2ESubtleGetPublicKey(t *testing.T) {
	assertSameAsNode(t, `
(async () => {
  const kp = await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']);
  const pub = await crypto.subtle.getPublicKey(kp.privateKey, ['verify']);
  console.log(pub.type, pub.algorithm.name, pub.usages.join(), pub.extractable);
  try {
    await crypto.subtle.getPublicKey(kp.publicKey, ['verify']);
  } catch (e: any) {
    console.log(e.name);
  }
})();
`)
}

func TestE2EXHRUnimplementedMemberThrows(t *testing.T) {
	for _, src := range []string{`
const x = new XMLHttpRequest();
try {
  x.addEventListener('loadend', function () {});
} catch (e: any) {
  console.log(e instanceof TypeError, e.message);
}
console.log(typeof (x as any).upload);
`} {
		assertOutput(t, src, "true x.addEventListener is not a function\nundefined\n")
		assertOutputCompatJS(t, strings.ReplaceAll(strings.ReplaceAll(src, "e: any", "e"), " as any", ""), "true x.addEventListener is not a function\nundefined\n")
	}
}

func TestE2EGlobalModuleCtorArityStrict(t *testing.T) {
	assertCLICompileError(t, false, `const b = new BroadcastChannel();
`, "expected 1 arguments, but got 0")
	assertCLICompileError(t, false, `const m = new MessageChannel(1);
`, "expected 0 arguments, but got 1")
	assertOutputCompatJS(t, `try { new BroadcastChannel(); } catch (e) { console.log(e.code); }
`, "ERR_MISSING_ARGS\n")
}

func TestE2EHostUnimplementedMembersStrict(t *testing.T) {
	assertOutput(t, `const e = new MessageEvent('message');
try { (e as any).initMessageEvent(); } catch (x: any) { console.log(x instanceof TypeError); }
console.log(e.type, typeof (e as any).nope);
`, "true\nmessage undefined\n")
}

func TestE2EUnboundAndValueBaseSubclasses(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
function t(f) { try { f(); } catch (e) { console.log(e.name, e.message); } }
t(() => {
  class CustomImage extends Image {}
  var instance = new CustomImage();
  console.log(instance instanceof CustomImage);
});
class _Module extends WebAssembly.Module {}
const m = new _Module(new Uint8Array([0, 97, 115, 109, 1, 0, 0, 0]));
console.log(Object.getPrototypeOf(_Module) === WebAssembly.Module);
console.log(m instanceof _Module, m instanceof WebAssembly.Module, Object.getPrototypeOf(m) === _Module.prototype);
`)
	assertSameAsNode(t, `
class A { x = 1; }
class B extends A {}
const a = new A();
const b = new B();
console.log(a instanceof A, b instanceof A);
Object.setPrototypeOf(a, {});
Object.setPrototypeOf(b, A.prototype);
console.log(a instanceof A, b instanceof A, b instanceof B);
const c: any = new A();
Object.setPrototypeOf(c, B.prototype);
console.log(c instanceof B, c instanceof A);
`)
}

func TestE2EShadowedGlobalsAndLiteralKeys(t *testing.T) {
	// A parameter read while its own call sites are typed; a local
	// captured beside a same-named top-level destructured binding; a class
	// member keyed by null or a boolean, read by that key.
	assertSameAsNodeCLIJS(t, `
var vs = [{name: "A", r: "x"}, {name: "HMAC", r: Uint8Array}];
function spec(n) { var res = []; if (n.toUpperCase() === "HMAC") res.push({name: n}); return res; }
function g(names) {
  if (names && !Array.isArray(names)) {
    names = [names];
  }
  return vs.filter(v => !names || names.includes(v.name));
}
function run(a) {
  g(a).forEach(function (v) { var name = v.name; spec(name).forEach(function (x) { console.log(x); }); });
}
run(["HMAC"]);
let C = class {
  [null]() { return null; }
  static [null]() { return "s"; }
  static [true]() { return 1; }
};
let c = new C();
console.log(c[null](), C[null](), C[true](), c[String(null)](), C[String(null)]());
`)
	assertSameAsNode(t, `
const { signal } = { signal: null as string | null };
function f(o?: { signal?: any }) {
  const signal: any = o?.signal;
  const g = () => signal?.reason;
  const h = () => signal;
  return [g(), h()];
}
console.log(signal, f({ signal: { reason: 7 } }), f());
`)
}

func TestE2EIntrinsicHeldBoxes(t *testing.T) {
	// A `find` over an array held as boxes keeps its box; a Set seeded from
	// boxed rows, rows of mixed kinds read by index, a spread of a host
	// object read by a function, and a member a module object lacks.
	assertSameAsNodeCLIJS(t, `
const formData = new FormData();
formData.set("a", "x");
function ident(fd, name) {
  const entry = Array.from(fd).find(([n]) => n === name);
  console.log(entry[1], entry.length);
}
ident(formData, "a");
function check(expected) {
  const s = new Set(expected.map(ee => ee.name));
  console.log(s.size, [...s]);
}
check([{ t: "m", name: "a", detail: null }, { t: "m", name: "b", detail: {} }, { t: "m", name: "a", detail: null, startTime: 2 }]);
const kTests = [['SHA-1', 'sha1', 160], ['SHA-256', 'sha256', 256]];
kTests.map((test) => { console.log(test[1].toUpperCase(), test[2] + 1); });
function sum(p, c) { return p + c; }
const tests = [[[], sum, 0], [[1, 2, 3], sum, 0], [['a', 'b'], sum, ''], [[1, 2], sum]];
for (const [values, fn, initial] of tests) console.log(values.reduce(fn, initial));
const { performance } = require('perf_hooks');
function nt(t) { console.log(typeof t.name, t.startTime === initialTiming.startTime); }
const initialTiming = { ...performance.nodeTiming };
nt(initialTiming);
const fixtures = { dir: "/x" };
const U = fixtures.utf8TestText;
function u() { return U; }
console.log(u(), typeof U);
`)
}

// A class extending a binding that holds a builtin (Map, a stream class)
// is a subclass of it: instanceof, its prototype, and the builtin's own
// members.
func TestE2EValueBaseBuiltinSubclass(t *testing.T) {
	assertSameAsNodeCLIJS(t, `
let T = Map;
class E4 extends T { twice(k) { return this.get(k) * 2; } }
const m = new E4();
m.set(1, 2);
console.log(m instanceof E4, m instanceof Map, m.get(1), m.size, m.twice(1));
let S = ReadableStream;
class E2 extends S { m() { return 1; } }
const o = new E2();
console.log(Object.getPrototypeOf(o) === E2.prototype, o instanceof ReadableStream, typeof o.m, typeof o.getReader);
`)
}

func TestE2EHostAndErrorSubclasses(t *testing.T) {
	// Subclasses of a host class, an error class and a typed array, beside
	// each other: the host bases are specialized, the error base is not.
	src := `class M extends Map<string, number> { total() { let t = 0; for (const v of this.values()) t += v; return t; } }
const m = new M(); m.set("a", 2); m.set("b", 3); console.log(m.total(), m instanceof Map);
class E extends Error { constructor(m: string) { super(m); this.name = "E"; } }
try { throw new E("x"); } catch (e) { console.log((e as E).name, (e as Error).message, e instanceof E); }
class U extends Uint8Array { sum() { return this.reduce((a, b) => a + b, 0); } }
console.log(new U([1, 2, 3]).sum());
`
	assertSameAsNode(t, src)
}

// A constructor-only function's `this` is never the global object: a base
// constructor called with `.call(this)` from a derived one keeps the
// program from carrying every global module (the WebAssembly engine among
// them). A plain call of the same function still sees the global object.
func TestE2EConstructorOnlyThisNotGlobal(t *testing.T) {
	src := `
function Shape(kind) { this.kind = kind; }
Shape.prototype.describe = function () { return "a " + this.kind; };
function Square(size) { Shape.call(this, "square"); this.size = size; }
Square.prototype = Object.create(Shape.prototype);
const s = new Square(3);
console.log(s.describe(), s.size, s instanceof Shape);
`
	assertSameAsNodeCompatJS(t, src)
	dir := tempDir(t)
	file := filepath.Join(dir, "main.js")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	ir, err := exec.Command(buildCLI(t), "-compat=js", "-emit-llvm", file).Output()
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if strings.Contains(string(ir), "kml_wasm") {
		t.Fatal("a constructor-only function's this pulled in every global module")
	}
	assertSameAsNodeCompatJS(t, `
function Tag(v) { this.v = v; return typeof this; }
console.log(new Tag(1).v, typeof Tag.call({}, 2));
`)
}

// Top-level `var`s declared inside a block or loop and read after it are
// hoisted in a fixed order: the emitted IR is the same on every compile.
func TestJSHoistedNestedVarsDeterministic(t *testing.T) {
	src := `
do { var a = 1; var b = 2; var c = 3; var d = 4; break; var e = "never"; var f = 6; } while (0);
if (a) { var g = 7; var h = 8; }
console.log(a, b, c, d, e, f, g, h);
`
	dir := tempDir(t)
	file := filepath.Join(dir, "main.js")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	cli := buildCLI(t)
	var first []byte
	for i := 0; i < 6; i++ {
		ir, err := exec.Command(cli, "-compat=js", "-emit-llvm", file).Output()
		if err != nil {
			t.Fatalf("emit: %v", err)
		}
		if i == 0 {
			first = ir
		} else if string(ir) != string(first) {
			t.Fatalf("compile %d emitted different IR from the first", i+1)
		}
	}
	assertSameAsNodeCompatJS(t, src)
}

// A hoisted var read where it may still be undefined has a nullable number
// type; new Array / Array of it took the presence-flagged pair for a
// double (invalid IR), where Node takes the number, and undefined's own
// one-element array.
func TestE2EJSNewArrayNullableLength(t *testing.T) {
	assertSameAsNodeCLIJS(t, `for (var n = 0; n <= 2; ++n) {
  var iterables = Array(n).fill([]);
  for (var k = 0; k <= n + 1; ++k) {
    var elements = Array(k).fill(0);
    var elementsIter = elements.values();
    var log = [];
    var padding = {
      next() { log.push("next"); return elementsIter.next(); },
    };
    padding.next();
    for (var i = 0; i < Math.min(n, k); ++i) log.push("x");
    if (n > k) log.push("y");
    console.log(n, k, iterables.length, log.join(","));
  }
}
var m;
console.log(new Array(m).length, Array(m)[0]);
`)
}

// A non-string search with a function replacer ToStrings, as replace
// does for a string replacement: it was handed on as a boolean (invalid IR).
func TestE2EJSReplaceNonStringSearchCallback(t *testing.T) {
	assertSameAsNodeCLIJS(t, `console.log("false".replace(false, x));
console.log("a1b1".replace(1, function (m, i) { return "<" + m + i + ">"; }));
console.log("a1b1".replaceAll(1, (m) => "[" + m + "]"));
var o = new Boolean;
o.replace = String.prototype.replace;
console.log(o.replace(function () { return false; }(), x));
var x;
`)
}

// A method a wrapper object holds as its own property is called with the
// wrapper as `this`, not its property bag, and shadows the native shortcut
// of its name (`o.replace = String.prototype.replace` on a Boolean).
func TestE2EJSWrapperOwnMethodThis(t *testing.T) {
	assertSameAsNodeCLIJS(t, `var o = new Boolean(true);
o.f = String.prototype.toUpperCase;
console.log(String(o), o.f.call(o), o.f());
var p = new Boolean(true);
p.g = function () { return String(this); };
console.log(p.g());
var o = new Boolean;
o.replace = String.prototype.replace;
console.log(o.replace("false", "y"));
var p = new Boolean;
p.toUpperCase = String.prototype.toUpperCase;
console.log(p.toUpperCase());
`)
}

// A class declared in a function reads a local holding a Map, Set or
// ArrayBuffer through a hidden field of the local's own type; it was any,
// and the method call on it had no Map receiver (strict: a compile error;
// js: invalid IR).
func TestE2EClassCapturedBuiltinLocal(t *testing.T) {
	assertSameAsNodeCLIJS(t, `function g() {
  const m = new Map(); m.set("a", 1);
  const s = new Set([1, 2]);
  const ab = new ArrayBuffer(8);
  class K { constructor() { console.log(m.size, m.get("a"), s.size, s.has(2), ab.byteLength); } }
  new K();
}
g();
`)
}

// A subclass of a run-time base (`extends C`) reads its captured locals in
// its constructor: the object the base built takes the hidden fields, and
// a capture of a resizable ArrayBuffer is used as one.
func TestE2EJSValueBaseCtorReadsCaptures(t *testing.T) {
	assertSameAsNodeCLIJS(t, `const ctors = [Uint8Array, Int16Array];
for (let ctor of ctors) {
  const rab = new ArrayBuffer(4 * ctor.BYTES_PER_ELEMENT, { maxByteLength: 8 * ctor.BYTES_PER_ELEMENT });
  let grow = false;
  class MyArray extends ctor {
    constructor(...params) {
      super(...params);
      if (grow) rab.resize(6 * ctor.BYTES_PER_ELEMENT);
    }
  }
  const a = new MyArray(rab, 0, 4);
  grow = true;
  const b = new MyArray(rab, 0, 4);
  console.log(ctor.name, a.length, b.length, rab.byteLength);
}
function g(C) { let n = 3; class M extends C { constructor(a) { super(a); console.log(n, this.length); } } new M(2); }
g(Uint8Array);
`)
}

// A captured local holding a builtin a helper returned (not a `new` the
// class sees) keeps its checked type in the hidden field.
func TestE2EJSClassCapturedHelperBuffer(t *testing.T) {
	assertSameAsNodeCLIJS(t, `function mk(n) { return new ArrayBuffer(n, { maxByteLength: 2 * n }); }
function g() {
  const rab = mk(4);
  const d = new Date(0);
  class K { constructor() { rab.resize(6); console.log(rab.byteLength, d.getTime()); } }
  new K();
}
g();
`)
}

// A subclass of a class value (`extends C`) whose `super()` builds C's own
// instance: the private brand check is the class tag, not the prototype
// chain the instance was given, so its fields (captures included) are read
// from the private table, never at the subclass's layout.
func TestE2EJSClassValueBaseLayout(t *testing.T) {
	assertSameAsNodeCLIJS(t, `function h(C) { let n = 4; class M extends C { constructor(a) { super(a); console.log(n); } m() { return n; } } const x = new M(2); console.log(x.m()); }
h(class { constructor(a) {} });
function h2(C) { let n = 4; class M extends C { constructor(a) { super(a); n++; } m() { return n; } } const x = new M(2); console.log(x.m(), n); }
h2(class { constructor(a) {} });
h2(function (a) { this.a = a; });
function h3(C) { class M extends C { #p = 1; get p() { return this.#p; } } console.log(new M().p); }
h3(class {});
`)
}

// An onFinally known only as any (a global read through globalThis) is
// called with no arguments when callable, and is a pass-through without
// the extra ticks otherwise; it was stored as a function pointer (invalid
// IR).
func TestE2EJSPromiseFinallyDynamicCallback(t *testing.T) {
	assertSameAsNodeCLIJS(t, `globalThis.fin = function () { console.log("fin", arguments.length); };
Promise.reject(3).finally(globalThis.fin).catch((e) => console.log("caught", e));
Promise.resolve(4).finally(globalThis.nope).then((v) => console.log("through", v));
Promise.resolve(5).finally(globalThis.fin).then((v) => console.log("value", v));
`)
}

// A Response held in an object's field (a boxed value in this lane) is
// cloned and read through its own methods, looked up at run time; the
// intrinsic took the boxed value for a Response (an internal error).
func TestE2EJSResponseInObjectFieldCloneRead(t *testing.T) {
	assertSameAsNodeCLIJS(t, `const r = new Response("x");
const o = { r };
o.r.clone().text().then((t) => console.log("a", t));
const s = new Response(new ReadableStream({ start(c) { c.enqueue(new Uint8Array([65])); c.close(); } }));
const q = { s };
const c2 = q.s.clone();
c2.text().then((t) => console.log("b", t));
q.s.text().then((t) => console.log("c", t));
`)
}

// typeof of a Response's or Request's body is its stream's, "object"; it
// answered from the raw body slot, "string".
func TestE2ETypeofResponseBody(t *testing.T) {
	assertSameAsNode(t, `const r = new Response("x");
console.log(typeof r.headers, typeof r.bodyUsed, typeof r.body, typeof (r.body));
const q = new Request("http://a/", { method: "POST", body: "x" });
console.log(typeof q.body, typeof new Response(null).body);
`)
}

// A program writing a property named body reads every host's `body`
// through the own-property shadow; a Response's clone reads its own body,
// not that box (invalid IR).
func TestE2EJSResponseCloneWithBodyPropertyWrite(t *testing.T) {
	assertSameAsNodeCLIJS(t, `const o = {};
o["body"] = 1;
const r = new Response(new ReadableStream({ start(c) { c.enqueue(new Uint8Array([65])); c.close(); } }));
const q = { r };
const c2 = q.r.clone();
c2.text().then((t) => console.log("b", t));
q.r.text().then((t) => console.log("c", t));
`)
}

// A nested generator in a callback emitted twice (as its typed parameter
// and as a value) is registered twice; each registration emits its own
// body (the second's was skipped: an undefined symbol at link).
func TestE2EJSNestedGeneratorInTwiceEmittedCallback(t *testing.T) {
	assertSameAsNodeCompatJS(t, `const self: any = globalThis;
class T { name: string; constructor(n: string) { this.name = n; } fail(e: unknown): void { console.log("fail", (e as Error).message); } }
function run(fn: (t: T) => any): void {
  const t: T = new T("x");
  Promise.resolve(fn(t)).then((): void => { console.log("ok"); }, (e: any): void => { t.fail(e); });
}
run(async t => {
  async function* gen() {
    try { for (let n = 0; n < 3; n++) { if (n === 1) { yield n; } } } finally { console.log("fin"); }
  }
  const from = self.nope ? self.nope.from : (g: any) => g;
  const it = from(gen());
  for await (const v of it) console.log("v", v);
});
`)
}

// A nested function using `this` moves to the top of its block; a `var`
// the block declares further down is hoisted with it, so the function
// captures that binding (undefined until its declaration runs).
func TestE2EJSThisFunctionCapturesLaterVar(t *testing.T) {
	assertSameAsNodeCompatJS(t, `function outer() {
  function early(this: any) { return yy; }
  console.log(early());
  var yy = 10;
  function inner(this: any) {
    console.log(yy);
    return () => this;
  }
  yy = 11;
  return inner;
}
console.log(typeof outer()());
`)
}

// A method only the program's own interface declares on a primitive is
// read from the prototype when the call runs: nothing put there, so the
// call throws a TypeError, dotted or bracketed.
func TestE2EPrimitiveAugmentedMethodCall(t *testing.T) {
	assertSameAsNodeCompatJS(t, `interface Boolean { doStuff(): string; }
interface Number { twice(): number; }
var x = true;
var n = 21;
try { console.log(x.doStuff()); } catch (e) { console.log("T1", (e as Error).constructor.name); }
try { console.log(x["doStuff"]()); } catch (e) { console.log("T2", (e as Error).constructor.name); }
try { console.log(n["twice"]()); } catch (e) { console.log("T3", (e as Error).constructor.name); }
`)
}

// A file's `interface Number {}` declares no value: `Number.prototype`
// still reads the global constructor.
func TestE2EInterfaceNamedLikeGlobalKeepsValue(t *testing.T) {
	assertSameAsNodeCompatJS(t, `interface Number { twice(): number; }
(Number.prototype as any).twice = function (this: number) { return this * 2; };
const n: any = 21;
console.log(n.twice(), Number("3") + 1);
`)
}
