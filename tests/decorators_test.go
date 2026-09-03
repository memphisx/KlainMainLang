package tests

import (
	"strings"
	"testing"
)

// --- Decorators, TDD-00161 Stage 1: observe-only (property + parameter)
// experimental decorators run faithfully at class-definition time; class and
// method/accessor decorators are a clean compile-time rejection (they can
// replace the target and need later stages). ---

// Property decorators run at class-definition time, in declaration order, each
// invoked with (target, propertyKey).
func TestE2EDecoratorPropertyRuns(t *testing.T) {
	assertOutput(t, `
function logProp(target: any, key: string): void {
  console.log("prop:", key)
}
class Model {
  @logProp
  name: string = "x"
  @logProp
  age: number = 0
}
console.log("defined")
const m = new Model()
console.log(m.name, m.age)
`, "prop: name\nprop: age\ndefined\nx 0")
}

// A property may carry more than one decorator; they apply bottom-up (the one
// nearest the declaration first).
func TestE2EDecoratorPropertyBottomUp(t *testing.T) {
	assertOutput(t, `
function a(t: any, k: string): void { console.log("a") }
function b(t: any, k: string): void { console.log("b") }
class C {
  @a
  @b
  field: number = 1
}
new C()
`, "b\na")
}

// Parameter decorators run with (target, key, parameterIndex): the key is the
// method name, or undefined for a constructor parameter (experimental spec).
func TestE2EDecoratorParameterRuns(t *testing.T) {
	assertOutput(t, `
function p(target: any, key: any, index: number): void {
  console.log("param", key, index)
}
class Svc {
  greet(@p name: string, @p greeting: string): void {}
  constructor(@p id: number) {}
}
new Svc(1)
`, "param undefined 0\nparam greet 0\nparam greet 1")
}

// Stage 4: an observe-only class decorator runs at class-definition time with
// the constructor; the class is otherwise unchanged.
func TestE2EDecoratorClassObserve(t *testing.T) {
	assertOutput(t, `
const registry: string[] = []
function Component(target: any): void {
  registry.push("widget")
  console.log("class decorator ran")
}
@Component
class Widget {
  render(): string { return "widget" }
}
console.log("defined", registry.length)
console.log(new Widget().render())
`, "class decorator ran\ndefined 1\nwidget")
}

// Stage 4: multiple class decorators apply bottom-up, and after member
// decorators (matching TS __decorate ordering).
func TestE2EDecoratorClassOrderingWithMember(t *testing.T) {
	assertOutput(t, `
function a(t: any): void { console.log("class a") }
function b(t: any): void { console.log("class b") }
function m(t: any, k: string, d: any): void { console.log("method m") }
@a
@b
class C {
  @m
  go(): void {}
}
new C()
`, "method m\nclass b\nclass a")
}

// Stage 4: a class decorator that returns a replacement constructor is accepted
// (compiles) — the replacement is refused at runtime (Stage 4b), not a silent
// drop and not a compile rejection.
func TestE2EDecoratorClassReplacementCompiles(t *testing.T) {
	_, err := parseAndCompile(`
function replace(target: any): any { return { replaced: true } }
@replace
class C {}
new C()
`)
	if err != nil {
		t.Fatalf("expected a class decorator returning a value to compile, got: %v", err)
	}
}

// Stage 4 + Stage 3: a decorated class with -emit-decorator-metadata exposes
// its constructor's design:paramtypes — the dependency-injection pattern.
func TestE2EDecoratorClassConstructorParamtypes(t *testing.T) {
	assertOutputWithDecoratorMetadata(t, `
class Logger {}
class Db {}
function Injectable(target: any): void {
  const deps: any = Reflect.getMetadata("design:paramtypes", target)
  console.log(deps[0].name, deps[1].name)
}
@Injectable
class Service {
  constructor(logger: Logger, db: Db) {}
}
new Service(new Logger(), new Db())
`, "Logger Db")
}

// Stage 2: an observe-only method decorator runs at class-definition time with
// (target, key, descriptor); the method keeps its original behavior (calls
// route through the descriptor's unchanged value).
func TestE2EDecoratorMethodObserve(t *testing.T) {
	assertOutput(t, `
function loud(target: any, key: string, desc: any): void {
  console.log("decorating:", key)
}
class Greeter {
  @loud
  greet(name: string): string { return "hi " + name }
}
console.log("defined")
console.log(new Greeter().greet("ada"))
`, "decorating: greet\ndefined\nhi ada")
}

// Stage 2: a method decorator that replaces descriptor.value re-routes calls to
// the replacement.
func TestE2EDecoratorMethodReplaceViaValue(t *testing.T) {
	assertOutput(t, `
function stub(target: any, key: string, desc: any): void {
  desc.value = function (): number { return 999 }
}
class C {
  @stub
  f(): number { return 1 }
}
console.log(new C().f())
`, "999")
}

// Stage 2: a method decorator that returns a new descriptor replaces the member.
func TestE2EDecoratorMethodReplaceViaReturn(t *testing.T) {
	assertOutput(t, `
function ret(target: any, key: string, desc: any): any {
  const d: any = { value: function (): number { return 42 }, writable: true, enumerable: false, configurable: true }
  return d
}
class C {
  @ret
  f(): number { return 1 }
}
console.log(new C().f())
`, "42")
}

// Stage 2: multiple method decorators apply bottom-up.
func TestE2EDecoratorMethodBottomUp(t *testing.T) {
	assertOutput(t, `
function a(t: any, k: string, d: any): void { console.log("a") }
function b(t: any, k: string, d: any): void { console.log("b") }
class C {
  @a
  @b
  m(): void {}
}
new C().m()
`, "b\na")
}

// Accessor, static, and generator method decorators remain a clean rejection.
func TestE2EDecoratorAccessorRejected(t *testing.T) {
	_, err := parseAndCompile(`
function d(t: any, k: string, desc: any): void {}
class C { @d get x(): number { return 1 } }
`)
	if err == nil {
		t.Fatal("expected a compile error: accessor decorators not yet supported")
	}
	if !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestE2EDecoratorStaticMethodRejected(t *testing.T) {
	_, err := parseAndCompile(`
function d(t: any, k: string, desc: any): void {}
class C { @d static f(): void {} }
`)
	if err == nil {
		t.Fatal("expected a compile error: static method decorators not yet supported")
	}
}

// Stage 3: with -emit-decorator-metadata, a decorated method's decorator can
// read design:type / design:paramtypes / design:returntype off the target.
func TestE2EDecoratorMetadataMethod(t *testing.T) {
	assertOutputWithDecoratorMetadata(t, `
function inspect(target: any, key: string, desc: any): void {
  const t: any = Reflect.getMetadata("design:type", target, key)
  const rt: any = Reflect.getMetadata("design:returntype", target, key)
  const pts: any = Reflect.getMetadata("design:paramtypes", target, key)
  console.log(t.name, rt.name, pts[0].name, pts[1].name)
}
class Svc {
  @inspect
  greet(name: string, age: number): boolean { return true }
}
new Svc()
`, "Function Boolean String Number")
}

// Stage 3: property design:type, including a class-typed field (the source
// class name, demangled).
func TestE2EDecoratorMetadataProperty(t *testing.T) {
	assertOutputWithDecoratorMetadata(t, `
class Dep {}
function prop(target: any, key: string): void {
  const t: any = Reflect.getMetadata("design:type", target, key)
  console.log(key, t.name)
}
class C {
  @prop count: number = 0
  @prop label: string = ""
  @prop dep: Dep = new Dep()
}
new C()
`, "count Number\nlabel String\ndep Dep")
}

// Stage 5: the standard (TC39) dialect calls decorators (value, context).
// Class and method decorators run; returning `value` (identity) keeps the
// member, and context carries kind/name.
func TestE2EStandardDecoratorObserve(t *testing.T) {
	assertOutputStandardDecorators(t, `
function logged(value: any, context: any): any {
  console.log("decorating", context.kind, context.name)
  return value
}
@logged
class C {
  @logged
  greet(): string { return "hi" }
}
console.log("defined")
console.log(new C().greet())
`, "decorating method greet\ndecorating class C\ndefined\nhi")
}

// Stage 5: a standard method decorator that returns a replacement re-routes the
// method's calls.
func TestE2EStandardDecoratorMethodReplace(t *testing.T) {
	assertOutputStandardDecorators(t, `
function stub(value: any, context: any): any {
  return function (): string { return "replaced" }
}
class C {
  @stub
  m(): string { return "original" }
}
console.log(new C().m())
`, "replaced")
}

// Stage 5: parameter decorators don't exist in the standard dialect; field and
// accessor decorators are a clean rejection (their construction-time semantics
// are the standard dialect's remaining sub-stage).
func TestE2EStandardDecoratorParamRejected(t *testing.T) {
	err := compileStandardDecorators(`
function d(v: any, c: any): void {}
class C { m(@d x: number): void {} }
`)
	if err == nil || !strings.Contains(err.Error(), "parameter decorators do not exist") {
		t.Fatalf("expected a parameter-decorator rejection, got: %v", err)
	}
}

// Stage 5: a standard field decorator returns an initializer that transforms
// the field's initial value per-instance (with a typed parameter — the dynamic
// function now binds declared parameter types).
func TestE2EStandardDecoratorFieldInitializer(t *testing.T) {
	assertOutputStandardDecorators(t, `
function double(value: any, context: any): any {
  return function (initial: number): number { return initial * 2 }
}
class C {
  @double
  x: number = 21
}
console.log(new C().x)
`, "42")
}

// Stage 5: context.addInitializer registers a callback that runs per-instance
// during construction.
func TestE2EStandardDecoratorAddInitializer(t *testing.T) {
	assertOutputStandardDecorators(t, `
const log: string[] = []
function track(value: any, context: any): void {
  context.addInitializer(function (): void { log.push("init") })
}
class C {
  @track
  greet(): string { return "hi" }
}
new C(); new C()
console.log(log.length)
`, "2")
}

// Stage 5: an addInitializer callback that captures the decorator's `context`
// works — the dynamic function carries its captures (ADR-00647).
func TestE2EStandardDecoratorAddInitializerCapturing(t *testing.T) {
	assertOutputStandardDecorators(t, `
function track(value: any, context: any): void {
  context.addInitializer(function (): void { console.log("init for", context.name) })
}
class C {
  @track
  greet(): string { return "hi" }
}
new C()
`, "init for greet")
}

// Stage 5: getter and setter decorators run (observe) and route accessor
// access through the accessor's slot.
func TestE2EStandardDecoratorGetterSetter(t *testing.T) {
	assertOutputStandardDecorators(t, `
function logged(value: any, context: any): any {
  console.log("decorating", context.kind, context.name)
  return value
}
class Temp {
  _c: number = 0
  @logged
  get celsius(): number { return this._c }
  @logged
  set celsius(v: number) { this._c = v }
}
const t = new Temp()
t.celsius = 25
console.log(t.celsius)
`, "decorating getter celsius\ndecorating setter celsius\n25")
}

// A *static* field decorator and static/generic-class accessor decorators
// remain clean rejections under the standard dialect.
func TestE2EStandardDecoratorStaticFieldRejected(t *testing.T) {
	err := compileStandardDecorators(`
function d(v: any, c: any): void {}
class C { @d static x: number = 0 }
`)
	if err == nil || !strings.Contains(err.Error(), "static field decorator") {
		t.Fatalf("expected a static-field-decorator rejection, got: %v", err)
	}
}

// A non-decorated `accessor x` auto-field is recognized and behaves as an
// ordinary field.
func TestE2EAccessorAutoField(t *testing.T) {
	assertOutput(t, `
class C { accessor x: number = 5 }
const c = new C()
c.x = 9
console.log(c.x)
`, "9")
}

// Stage 5: a decorator on an `accessor` auto-field follows the TC39
// { get, set, init } protocol — observe leaves the accessor working, and it
// routes get/set through the desugared backing field.
func TestE2EStandardDecoratorAccessorObserve(t *testing.T) {
	assertOutputStandardDecorators(t, `
function observed(value: any, context: any): any {
  console.log("decorating accessor", context.name)
  return value
}
class C {
  @observed
  accessor x: number = 10
}
const c = new C()
console.log(c.x)
c.x = 99
console.log(c.x)
`, "decorating accessor x\n10\n99")
}

// A returned { init } transforms the backing field's initial value.
func TestE2EStandardDecoratorAccessorInit(t *testing.T) {
	assertOutputStandardDecorators(t, `
function doubled(value: any, context: any): any {
  return { init: function (initial: number): number { return initial * 2 } }
}
class C {
  @doubled
  accessor x: number = 21
}
console.log(new C().x)
`, "42")
}

// A returned { get } replaces the accessor's getter.
func TestE2EStandardDecoratorAccessorGetReplace(t *testing.T) {
	assertOutputStandardDecorators(t, `
function fixedGet(value: any, context: any): any {
  return { get: function (): number { return 777 } }
}
class C {
  @fixedGet
  accessor x: number = 5
}
console.log(new C().x)
`, "777")
}

// `accessor` on a method is rejected at parse; an accessor auto-field decorator
// under the experimental dialect is rejected (it's a TC39 decorator).
func TestE2EAccessorOnMethodRejected(t *testing.T) {
	_, err := parseAndCompile(`class C { accessor foo(): void {} }`)
	if err == nil || !strings.Contains(err.Error(), "'accessor' is only valid on a class field") {
		t.Fatalf("expected accessor-on-method rejection, got: %v", err)
	}
}

// Reflect.defineMetadata / getMetadata / hasMetadata work directly, independent
// of the flag (no design:* auto-emission).
func TestE2EReflectUserMetadata(t *testing.T) {
	assertOutput(t, `
const obj: any = {}
Reflect.defineMetadata("role", "admin", obj, "user")
console.log(Reflect.getMetadata("role", obj, "user"))
console.log(Reflect.hasMetadata("role", obj, "user"))
console.log(Reflect.hasMetadata("missing", obj, "user"))
Reflect.defineMetadata("v", 42, obj)
console.log(Reflect.getMetadata("v", obj))
`, "admin\ntrue\nfalse\n42")
}

// A `@dec(...)` factory-call decorator invokes the *returned* value as the
// decorator: the factory runs first, and its returned function decorates the
// member. Works across placements — property, method, and class.
func TestE2EDecoratorFactoryProperty(t *testing.T) {
	assertOutput(t, `
function decorate(target: any, key: string): void {
  console.log("decorate on", key)
}
function tag(label: string): any {
  console.log("factory", label)
  return decorate
}
class C {
  @tag("meta")
  x: number = 0
}
new C()
`, "factory meta\ndecorate on x")
}

func TestE2EDecoratorFactoryMethodAndClass(t *testing.T) {
	assertOutput(t, `
function mdec(t: any, k: string, d: any): void { console.log("method dec", k) }
function route(path: string): any { return mdec }
function cdec(t: any): void { console.log("class dec") }
function Controller(prefix: string): any { return cdec }
@Controller("/api")
class C {
  @route("/users")
  list(): string { return "x" }
}
new C()
`, "method dec list\nclass dec")
}

// A factory whose returned decorator is a typed closure capturing the factory's
// arguments works (the returned function value routes through the closure ABI).
func TestE2EDecoratorFactoryTypedClosure(t *testing.T) {
	assertOutput(t, `
function typedFactory(n: number): (t: any, k: string) => void {
  return function (t: any, k: string): void { console.log("dec", k, n) }
}
class C { @typedFactory(5) y: number = 0 }
new C()
`, "dec y 5")
}

// A factory whose returned decorator is an *any-typed* closure capturing the
// factory's argument now works too — a dynamic function carries its captures in
// its tag-12 env (ADR-00647).
func TestE2EDecoratorFactoryAnyCapturingClosure(t *testing.T) {
	assertOutput(t, `
function tag(label: string): any {
  return function (target: any, key: string): void {
    console.log("tagged", key, "with", label)
  }
}
class C { @tag("meta") x: number = 0 }
new C()
`, "tagged x with meta")
}

// Regression: a static field initializer calling a top-level function now
// resolves it (its name was not rewritten to the mangled form before the
// decorator work touched rewriteClassDecl).
func TestE2EStaticFieldInitializerCallsFunction(t *testing.T) {
	assertOutput(t, `
function five(): number { return 5 }
class C { static x: number = five() }
console.log(C.x)
`, "5")
}
