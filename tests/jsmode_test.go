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
