package tests

import (
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

func TestJSModeCtorFieldTypeConflictRejected(t *testing.T) {
	_, err := compileCompatJS(`
class C { constructor(f) { this.v = 1; this.v = "s" } }
new C(0)
`)
	if err == nil || !strings.Contains(err.Error(), "assigned conflicting types") {
		t.Fatalf("expected conflicting-types rejection, got: %v", err)
	}
}

func TestJSModeConflictingCallSitesRejected(t *testing.T) {
	_, err := compileCompatJS(`
class C { constructor(v) { this.v = v } }
new C(1)
new C("s")
`)
	if err == nil || !strings.Contains(err.Error(), "disagrees with another call site") {
		t.Fatalf("expected call-site disagreement rejection, got: %v", err)
	}
}

func TestJSModeMethodIntroducedFieldStillRejected(t *testing.T) {
	// Constructor-only by design: a method inventing a field stays a clean
	// rejection, not a silent dynamic add.
	_, err := compileCompatJS(`
class C { constructor() { this.a = 1 } grow() { this.b = 2 } }
new C().grow()
`)
	if err == nil || !strings.Contains(err.Error(), "no field 'b'") {
		t.Fatalf("expected no-field rejection, got: %v", err)
	}
}

func TestStrictModeClassFieldInferenceOff(t *testing.T) {
	// Strict mode is untouched: the same vanilla class stays a clean error.
	_, err := parseAndCompile(`
class Point { constructor(x, y) { this.x = x } }
new Point(1, 2)
`)
	if err == nil || !strings.Contains(err.Error(), "no field 'x'") {
		t.Fatalf("expected strict-mode no-field rejection, got: %v", err)
	}
}

func TestStrictCtorUnannotatedNonNumericArgRejected(t *testing.T) {
	// The ADR-00042 guard now covers constructor calls too (previously a
	// string arg to a number-defaulted ctor param stored a pointer as a
	// number, silently).
	_, err := parseAndCompile(`
class C { v: number; constructor(v) { this.v = v } }
new C("oops")
`)
	if err == nil || !strings.Contains(err.Error(), "non-numeric argument") {
		t.Fatalf("expected non-numeric-argument rejection, got: %v", err)
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

func TestStrictModeFunctionParamInferenceOff(t *testing.T) {
	_, err := parseAndCompile(`
function greet(name) { return "hello " + name }
console.log(greet("world"))
`)
	if err == nil || !strings.Contains(err.Error(), "non-numeric argument") {
		t.Fatalf("expected strict-mode non-numeric rejection, got: %v", err)
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
`, "hello, Thessaloniki!\nhi, Thessalonikiundefined\ntrue\ncaught: nope is not a function")
}

func TestJSModeProtoCtorDirectCallRejected(t *testing.T) {
	_, err := compileCompatJS(`
function Animal(name) { this.name = name }
new Animal("x")
Animal("y")
`)
	if err == nil || !strings.Contains(err.Error(), "prototype constructor") {
		t.Fatalf("expected direct-call rejection, got: %v", err)
	}
}

func TestStrictModeProtoClassesOff(t *testing.T) {
	// Strict mode is untouched: `this` in a plain function stays an error.
	_, err := parseAndCompile(`
function Animal(name) { this.name = name }
new Animal("x")
`)
	if err == nil || !strings.Contains(err.Error(), "'this' is only valid") {
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
