package tests

import (
	"strings"
	"testing"
)

// --- interface / type alias ---

func TestE2EInterface(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function distance(p: Point): number {
  return Math.floor(Math.sqrt(p.x * p.x + p.y * p.y))
}
const p: Point = { x: 3, y: 4 }
console.log(distance(p))
`, "5")
}

func TestE2ETypeAlias(t *testing.T) {
	assertOutput(t, `
type Rect = { width: number; height: number }
function area(r: Rect): number { return r.width * r.height }
const r: Rect = { width: 6, height: 7 }
console.log(area(r))
`, "42")
}

func TestE2EInterfaceWithString(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number }
function greet(u: User): string { return u.name }
const u: User = { name: 'Alice', age: 30 }
console.log(greet(u))
console.log(JSON.stringify(u))
`, "Alice\n{\"name\":\"Alice\",\"age\":30}")
}

func TestE2EInterfaceFloatField(t *testing.T) {
	assertOutput(t, `
interface Point {
  x: number;
  /** @type {float64} */
  score: number;
}
const p: Point = { x: 1, score: 9.5 }
console.log(p.score)
console.log(JSON.stringify(p))
`, "9.5\n{\"x\":1,\"score\":9.5}")
}

func TestE2EInterfaceFloatFieldJSONParse(t *testing.T) {
	assertOutput(t, `
interface Point {
  x: number;
  /** @type {float64} */
  score: number;
}
const p: Point = JSON.parse('{"x":1,"score":9.5}')
console.log(p.x)
console.log(p.score)
`, "1\n9.5")
}

// TestE2EJSONParseArrayField: an array-typed interface field is now parsed via
// the tree projection (TDD-00077 P3), where it was previously a clean rejection
// ([ADR-00189]) and before that invalid LLVM IR.
func TestE2EJSONParseArrayField(t *testing.T) {
	assertOutput(t, `
interface Item { name: string; tags: string[] }
const it: Item = JSON.parse('{"name":"x","tags":["a","b","c"]}')
console.log(it.name)
console.log(it.tags.length)
console.log(it.tags[0] + it.tags[2])
`, "x\n3\nac")
}

func TestE2EInterfaceReturnType(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function origin(): Point { return { x: 0, y: 0 } }
const p = origin()
console.log(p.x)
console.log(p.y)
`, "0\n0")
}

func TestE2EUnannotatedFunctionReturnsObjectLiteral(t *testing.T) {
	assertOutput(t, `
function makePoint(x, y) { return { x: x, y: y } }
const p = makePoint(3, 4)
console.log(p.x)
console.log(p.y)
`, "3\n4")
}

func TestE2EUnannotatedArrowFunctionReturnsObjectLiteral(t *testing.T) {
	assertOutput(t, `
const makePoint = (x, y) => { return { x: x, y: y } }
const p = makePoint(5, 6)
console.log(p.x)
console.log(p.y)
`, "5\n6")
}

// --- Object literal string/numeric-literal property keys ---

func TestE2EObjectLiteralStringKeys(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { "x": 1, "y": 2 }
console.log(p.x)
console.log(p.y)
`, "1\n2")
}
func TestE2EObjectLiteralNumericKeys(t *testing.T) {
	// Numeric-literal keys (`{ 0: "a" }`) aren't dot/bracket-readable back
	// (no interface can declare a "0"-named field, and bracket access on a
	// static struct isn't supported) — verified instead through
	// JSON.stringify, which walks the field list directly by name.
	assertOutput(t, `
function makeDigits() { return { 0: "a", 1: "b" } }
console.log(JSON.stringify(makeDigits()))
`, `{"0":"a","1":"b"}`)
}
func TestE2EObjectLiteralMixedKeyForms(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number }
const u: User = { "name": "Alice", age: 30 }
console.log(u.name)
console.log(u.age)
`, "Alice\n30")
}
func TestE2EObjectLiteralStringKeyShorthandRejected(t *testing.T) {
	// A string-literal key has no shorthand form — `{ "foo" }` is invalid,
	// matching real JS (unlike a bare-identifier key, a string isn't also a
	// referenceable binding to shorthand from).
	_, err := parseAndCompile(`
const o = { "foo" }
`)
	if err == nil {
		t.Fatal("expected a parse error for a string-keyed property with no value, got none")
	}
}

// --- Object literal shorthand properties and spread ---

func TestE2EObjectShorthandProps(t *testing.T) {
	assertOutput(t, `
const x: number = 1
const y: number = 2
const obj = { x, y }
console.log(obj.x)
console.log(obj.y)
`, "1\n2")
}
func TestE2EObjectShorthandPropsMixed(t *testing.T) {
	assertOutput(t, `
const name: string = 'Alice'
const age: number = 30
const person = { name, age, active: true }
console.log(person.name)
console.log(person.age)
console.log(person.active)
`, "Alice\n30\ntrue")
}
func TestE2EObjectShorthandPropsInFunction(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function makePoint(x: number, y: number): Point {
    return { x, y }
}
const p = makePoint(3, 4)
console.log(p.x)
console.log(p.y)
`, "3\n4")
}
func TestE2EObjectSpread(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
const copy = { ...p }
console.log(copy.x)
console.log(copy.y)
`, "1\n2")
}
func TestE2EObjectSpreadOverride(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
const overridden = { ...p, y: 20 }
console.log(overridden.x)
console.log(overridden.y)
`, "1\n20")
}
func TestE2EObjectSpreadOverriddenBySpread(t *testing.T) {
	// A spread appearing AFTER an explicit property overrides it, matching JS.
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
const merged = { x: 100, ...p }
console.log(merged.x)
console.log(merged.y)
`, "1\n2")
}
func TestE2EObjectSpreadAddField(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
const withZ = { ...p, z: 3 }
console.log(withZ.x)
console.log(withZ.y)
console.log(withZ.z)
`, "1\n2\n3")
}
func TestE2EObjectSpreadIsShallow(t *testing.T) {
	assertOutput(t, `
interface Inner { v: number }
interface Outer { name: string; inner: Inner }
const o: Outer = { name: 'a', inner: { v: 1 } }
const o2 = { ...o, name: 'b' }
o2.inner.v = 99
console.log(o.inner.v)
`, "99")
}

// --- Object.keys / Object.values / Object.entries ---

func TestE2EObjectKeys(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 3, y: 4 }
const k = Object.keys(p)
console.log(k[0])
console.log(k[1])
`, "x\ny")
}

func TestE2EObjectValues(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number; active: boolean }
const u: User = { name: 'Alexandros', age: 25, active: true }
const v = Object.values(u)
console.log(v[0])
console.log(v[1])
console.log(v[2])
`, "Alexandros\n25\ntrue")
}

func TestE2EObjectEntries(t *testing.T) {
	assertOutput(t, `
interface Config { host: string; port: number }
const c: Config = { host: 'localhost', port: 8080 }
const entries = Object.entries(c)
for (const [k, v] of entries) {
  console.log(k + '=' + v)
}
`, "host=localhost\nport=8080")
}

func TestE2EObjectFromEntries(t *testing.T) {
	assertOutput(t, `
const entries: [string, number][] = [['a', 1], ['b', 2]]
const obj = Object.fromEntries(entries)
console.log(obj.a)
console.log(obj['b'])
`, "1\n2")
}

func TestE2EObjectFromEntriesBareLiteral(t *testing.T) {
	assertOutput(t, `
const obj = Object.fromEntries([['x', 10], ['y', 20]])
console.log(obj.x)
console.log(obj.y)
`, "10\n20")
}

func TestE2EObjectFromEntriesRoundTrip(t *testing.T) {
	assertOutput(t, `
const obj = Object.fromEntries([['a', 1], ['b', 2]])
for (const [k, v] of Object.entries(obj)) {
  console.log(k + '=' + v)
}
`, "a=1\nb=2")
}

func TestE2EObjectAssign(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number; label: string }
const target: Point = { x: 1, y: 2, label: 'a' }
const source: Point = { x: 10, y: 20, label: 'b' }
const merged = Object.assign(target, source)
console.log(merged.x)
console.log(merged.y)
console.log(merged.label)
console.log(target.x)
`, "10\n20\nb\n10")
}

func TestE2EObjectAssignPartialFields(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const target: Point = { x: 1, y: 2 }
interface XOnly { x: number }
const patch: XOnly = { x: 99 }
Object.assign(target, patch)
console.log(target.x)
console.log(target.y)
`, "99\n2")
}

func TestE2EObjectAssignMultipleSourcesLastWriteWins(t *testing.T) {
	assertOutput(t, `
interface Full { x: number; label: string }
const target: Full = { x: 0, label: '' }
const s1: Full = { x: 1, label: 'first' }
const s2: Full = { x: 2, label: 'second' }
Object.assign(target, s1, s2)
console.log(target.x)
console.log(target.label)
`, "2\nsecond")
}

func TestE2EObjectFreezeBlocksFieldWrite(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
Object.freeze(p)
try {
    p.x = 99
} catch (e) {
    console.log('caught')
}
console.log(p.x)
`, "caught\n1")
}

func TestE2EObjectFreezeBlocksCompoundAssign(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
Object.freeze(p)
try {
    p.y += 5
} catch (e) {
    console.log('caught')
}
console.log(p.y)
`, "caught\n2")
}

func TestE2EObjectFreezeTracksByValueThroughAlias(t *testing.T) {
	// Object.freeze tracks the object's own heap pointer, not the variable
	// that froze it — a mutation attempted through a function parameter
	// aliasing the same object must be caught too.
	assertOutput(t, `
interface Point { x: number; y: number }
function mutate(pt: Point): void {
    pt.x = 1000
}
const p: Point = { x: 1, y: 2 }
Object.freeze(p)
try {
    mutate(p)
} catch (e) {
    console.log('caught')
}
console.log(p.x)
`, "caught\n1")
}

func TestE2EObjectFreezeDoesNotAffectOtherObjects(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const frozen: Point = { x: 1, y: 2 }
const plain: Point = { x: 5, y: 6 }
Object.freeze(frozen)
plain.x = 50
console.log(plain.x)
`, "50")
}

func TestE2EObjectFreezeReturnsSameObject(t *testing.T) {
	// Object.freeze returns the exact same reference it was given (not a
	// copy) — confirmed by reading a field through the returned value
	// before freezing actually blocks anything further.
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 7, y: 8 }
const same = Object.freeze(p)
console.log(same.x)
console.log(same === p)
`, "7\ntrue")
}

func TestE2EObjectSealAllowsFieldMutation(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
Object.seal(p)
p.x = 70
console.log(p.x)
`, "70")
}

func TestE2EObjectAssignOnFrozenTargetThrows(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const target: Point = { x: 1, y: 2 }
const source: Point = { x: 5, y: 6 }
Object.freeze(target)
try {
    Object.assign(target, source)
} catch (e) {
    console.log('caught')
}
console.log(target.x)
`, "caught\n1")
}

func TestE2EObjectFreezeWithNoSourcesDoesNotThrow(t *testing.T) {
	// Object.assign(frozenObj) with no sources never attempts a write, so
	// it must not throw, matching real JS.
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
Object.freeze(p)
const same = Object.assign(p)
console.log(same.x)
`, "1")
}

func TestE2EObjectAssignUnknownFieldRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface A { x: number }
interface B { x: number; z: number }
const a: A = { x: 1 }
const b: B = { x: 2, z: 3 }
Object.assign(a, b)
`)
	if err == nil {
		t.Fatal("expected a compile error for Object.assign with a source field not present on target's type, got none")
	}
}

// --- enum ---

func TestE2EEnumNumeric(t *testing.T) {
	assertOutput(t, `
enum Direction { Up, Down, Left, Right }
console.log(Direction.Up)
console.log(Direction.Down)
console.log(Direction.Right)
`, "0\n1\n3")
}

func TestE2EEnumExplicitValues(t *testing.T) {
	assertOutput(t, `
enum Status { Active = 1, Inactive = 2, Pending = 10 }
console.log(Status.Active)
console.log(Status.Inactive)
console.log(Status.Pending)
`, "1\n2\n10")
}

func TestE2EEnumAutoIncrementAfterExplicit(t *testing.T) {
	assertOutput(t, `
enum Level { Low = 1, Medium, High, Critical = 100, Fatal }
console.log(Level.Low)
console.log(Level.Medium)
console.log(Level.High)
console.log(Level.Critical)
console.log(Level.Fatal)
`, "1\n2\n3\n100\n101")
}

func TestE2EEnumString(t *testing.T) {
	assertOutput(t, `
enum Suit { Clubs = 'C', Diamonds = 'D', Hearts = 'H', Spades = 'S' }
console.log(Suit.Hearts)
console.log(Suit.Spades)
`, "H\nS")
}

// A string enum's member assigned to a variable/param/field/array *explicitly
// typed with the enum* must allocate a string (ptr) slot, not the numeric-enum
// i64 default — otherwise storing the member's string-constant pointer into an
// i64 slot is a hard clang error. ADR-00247: the enum name (and its `[]` array
// form) as a type annotation resolves to the enum's backing type. Covers typed var,
// comparison, param+return, interface field, switch, and a typed array.
func TestE2EStringEnumTypedVariable(t *testing.T) {
	assertOutput(t, `
enum Color { Red = "RED", Green = "GREEN", Blue = "BLUE" }
const c: Color = Color.Green
console.log(c)
console.log(c === Color.Green)
console.log(c === Color.Red)
function pick(): Color { return Color.Blue }
const p: Color = pick()
console.log(p)
function label(x: Color): string {
  switch (x) { case Color.Red: return "R"; case Color.Green: return "G"; default: return "?" }
}
console.log(label(Color.Green))
console.log(label(Color.Blue))
interface Shape { color: Color }
const s: Shape = { color: Color.Red }
console.log(s.color)
const cs: Color[] = [Color.Red, Color.Blue]
console.log(cs[1])
`, "GREEN\ntrue\nfalse\nBLUE\nG\n?\nRED\nBLUE")
}

func TestE2EConstEnum(t *testing.T) {
	assertOutput(t, `
const enum Color { Red = 0, Green = 1, Blue = 2 }
function paintIt(c: number): string {
  if (c === Color.Red) { return 'red' }
  if (c === Color.Green) { return 'green' }
  return 'blue'
}
console.log(paintIt(Color.Green))
console.log(paintIt(Color.Blue))
`, "green\nblue")
}

// --- enum used in a switch statement ---

func TestE2EEnumInSwitch(t *testing.T) {
	assertOutput(t, `
enum Op { Add = 0, Sub = 1, Mul = 2 }
function calc(op: number, a: number, b: number): number {
  switch (op) {
    case Op.Add: return a + b
    case Op.Sub: return a - b
    case Op.Mul: return a * b
  }
  return 0
}
console.log(calc(Op.Add, 3, 4))
console.log(calc(Op.Sub, 10, 3))
console.log(calc(Op.Mul, 5, 6))
`, "7\n7\n30")
}

// --- .length on a non-variable expression (Object.keys()) ---

func TestE2ELengthOnObjectKeys(t *testing.T) {
	assertOutput(t, `
const obj = { a: 1, b: 2, c: 3 }
console.log(Object.keys(obj).length)
`, "3")
}

// --- Indexing into a non-variable expression (Object.keys()) ---

func TestE2EIndexOnObjectKeys(t *testing.T) {
	assertOutput(t, `
const obj = { x: 1, y: 2, z: 3 }
console.log(Object.keys(obj)[0])
console.log(Object.keys(obj)[2])
`, "x\nz")
}

// --- Array-typed interface/object fields (ADR-00061) ---

func TestE2EArrayTypedFieldLengthAndIndex(t *testing.T) {
	assertOutput(t, `
interface Container { items: number[] }
const items: number[] = [10, 20, 30]
const c: Container = { items: items }
console.log(c.items.length)
console.log(c.items[1])
`, "3\n20")
}

func TestE2EArrayTypedFieldForOf(t *testing.T) {
	assertOutput(t, `
interface Container { items: number[] }
const items: number[] = [10, 20, 30]
const c: Container = { items: items }
for (const x of c.items) { console.log(x) }
`, "10\n20\n30")
}

func TestE2EArrayTypedFieldSpread(t *testing.T) {
	assertOutput(t, `
interface Container { items: number[]; label: string }
const items: number[] = [10, 20, 30]
const c: Container = { items: items, label: "orig" }
const c2 = { ...c, label: "copy" }
console.log(c2.items.length)
console.log(c2.items[2])
`, "3\n30")
}

func TestE2EArrayTypedFieldDestructuring(t *testing.T) {
	assertOutput(t, `
interface Container { items: number[] }
const items: number[] = [10, 20, 30]
const c: Container = { items: items }
const { items: destructured } = c
console.log(destructured.length)
destructured.push(40)
console.log(destructured.length)
console.log(c.items.length)
`, "3\n4\n3")
}

func TestE2EArrayTypedFieldObjectAssign(t *testing.T) {
	assertOutput(t, `
interface Container { items: number[]; label: string }
const items: number[] = [1, 2]
const newItems: number[] = [9, 8, 7, 6]
const c: Container = { items: items, label: "x" }
Object.assign(c, { items: newItems, label: "y" })
console.log(c.items.length)
`, "4")
}

func TestE2EArrayTypedFieldOptionalChaining(t *testing.T) {
	assertOutput(t, `
interface Container { items: number[] }
function printLen(cc: Container | null): void {
  console.log(cc?.items.length)
}
const items: number[] = [1, 2, 3]
const c: Container = { items: items }
printLen(c)
printLen(null)
`, "3\n0")
}

func TestE2EArrayTypedFieldReturnedFromFunction(t *testing.T) {
	assertOutput(t, `
interface Container { items: number[] }
function getItems(cc: Container): number[] {
  return cc.items
}
const items: number[] = [1, 2, 3]
const c: Container = { items: items }
const returned = getItems(c)
console.log(returned.length)
`, "3")
}

// --- computed property keys (docs/tdd/TDD-00012.md) ---

func TestE2EComputedPropertyKeyBasic(t *testing.T) {
	assertOutput(t, `
const k = "b"
const obj = { a: 1, [k]: 2 }
console.log(obj.a)
console.log(obj[k])
console.log(obj["a"])
`, "1\n2\n1")
}

func TestE2EComputedPropertyKeyWriteAndCompoundAssign(t *testing.T) {
	assertOutput(t, `
const k = "b"
const obj = { a: 1, [k]: 2 }
obj.a = 10
obj[k] += 5
obj["c"] = 99
console.log(obj.a)
console.log(obj.b)
console.log(obj.c)
`, "10\n7\n99")
}

func TestE2EComputedPropertyKeyObjectKeysValuesEntries(t *testing.T) {
	assertOutput(t, `
const k = "b"
const obj = { a: 1, [k]: 2, c: 3 }
for (const key of Object.keys(obj)) {
  console.log(key)
}
for (const v of Object.values(obj)) {
  console.log(v)
}
for (const [k, v] of Object.entries(obj)) {
  console.log(k + "=" + v)
}
`, "a\nb\nc\n1\n2\n3\na=1\nb=2\nc=3")
}

func TestE2EComputedPropertyKeyStringValues(t *testing.T) {
	assertOutput(t, `
const k = "y"
const obj = { x: "hello", [k]: "world" }
console.log(obj.x)
console.log(obj[k])
`, "hello\nworld")
}

func TestE2EComputedPropertyKeyNumericStringifies(t *testing.T) {
	// ADR-00461: a numeric computed key stringifies (JS object keys are
	// strings) instead of being rejected.
	assertOutput(t, `
const k = 5
const obj = { [k]: 1 }
console.log(obj["5"])
`, "1")
}

func TestE2EComputedPropertyKeySpreadCombinationRejected(t *testing.T) {
	_, err := parseAndCompile(`
const k = "b"
const src = { a: 1 }
const obj = { ...src, [k]: 2 }
`)
	if err == nil {
		t.Fatal("expected a compile error for combining object spread with a computed property key, got none")
	}
}

// --- Object literal field coercion against a declared type (TDD-00007) ---
//
// Each of these checks an object literal's fields are coerced against the
// separately-declared expected type, not just the literal's own self-inferred
// type (TDD-00007). Since `number` is a float64 (TDD-00123), a fractional value
// into a `number` field is preserved exactly (not truncated).

func TestE2EObjectLiteralVarDeclPreservesFloatField(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 40.6 }
console.log(p.y)
`, "40.6")
}

func TestE2EObjectLiteralVarDeclCoercesIntFieldToFloat(t *testing.T) {
	assertOutput(t, `
interface Player {
  name: string
  /** @type {float64} */
  score: number
}
const pl: Player = { name: "a", score: 5 }
console.log(pl.score)
`, "5")
}

func TestE2EObjectLiteralFunctionArgumentCoercion(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function printY(p: Point): void {
  console.log(p.y)
}
printY({ x: 1, y: 40.6 })
`, "40.6")
}

func TestE2EObjectLiteralClosureArgumentCoercion(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const printY = (p: Point): void => {
  console.log(p.y)
}
printY({ x: 1, y: 40.6 })
`, "40.6")
}

func TestE2EObjectLiteralDefaultParamCoercion(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function printY(p: Point = { x: 1, y: 40.6 }): void {
  console.log(p.y)
}
printY()
`, "40.6")
}

func TestE2EObjectLiteralReturnValueCoercion(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function makePoint(): Point {
  return { x: 1, y: 40.6 }
}
console.log(makePoint().y)
`, "40.6")
}

func TestE2EObjectLiteralArrayElementCoercion(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const pts: Point[] = [{ x: 1, y: 2.9 }, { x: 3, y: 4.1 }]
console.log(pts[0].y)
console.log(pts[1].y)
`, "2.9\n4.1")
}

func TestE2ENestedObjectLiteralFieldCoercion(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
interface Address { city: string; coords: Point }
const addr: Address = { city: "Thessaloniki", coords: { x: 1, y: 40.6 } }
console.log(addr.coords.y)
`, "40.6")
}

func TestE2EObjectSpreadPreservesCoercedSourceField(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 40.6 }
const q: Point = { ...p, x: 5 }
console.log(q.x)
console.log(q.y)
`, "5\n40.6")
}

func TestE2EUntypedObjectLiteralStillInfersOwnFieldType(t *testing.T) {
	// No declared type anywhere — the literal's own inferred type (float64
	// for y, since 40.6 has a decimal point) must still apply unchanged.
	assertOutput(t, `
const p = { x: 1, y: 40.6 }
console.log(p.y)
`, "40.6")
}

// --- `in` operator ---

func TestE2EInOperatorObjectLiteral(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
console.log("x" in p)
console.log("z" in p)
`, "true\nfalse")
}

func TestE2EInOperatorClassInstance(t *testing.T) {
	assertOutput(t, `
class Foo {
  a: number;
  constructor() { this.a = 1 }
}
const f = new Foo()
console.log("a" in f)
console.log("b" in f)
`, "true\nfalse")
}

func TestE2EInOperatorInIfCondition(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
if ("x" in p) {
  console.log("has x")
} else {
  console.log("no x")
}
`, "has x")
}

func TestE2EInOperatorForInStillWorks(t *testing.T) {
	// Regression check: `in` becoming a binary operator (contextual keyword,
	// not a reserved token) must not break the pre-existing for...in loop,
	// which recognizes "in" the same contextual way.
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
for (const k in p) {
  console.log(k)
}
`, "x\ny")
}

func TestE2EInOperatorNonObjectIsError(t *testing.T) {
	_, err := parseAndCompile(`console.log("x" in 5)`)
	if err == nil {
		t.Fatal("expected a compile error for 'in' against a non-object")
	}
}

func TestE2EInOperatorDynamicKeyIsError(t *testing.T) {
	_, err := parseAndCompile(`
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
const k = "x"
console.log(k in p)
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-literal 'in' key")
	}
}

// --- Uninitialized field safety ---
//
// Object literal allocation must zero-fill, not just malloc: a field
// omitted from a given literal (an optional `?:` interface field) never
// gets its own storeField call, so it must read back a deterministic zero,
// not whatever garbage was already in that heap slot. Real bug found
// investigating destructuring defaults; see ADR-00157.

func TestE2EOptionalFieldOmittedReadsZero(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y?: number }
let p: Point = { x: 1 };
console.log(p.x, p.y);
`, "1 0")
}

func TestE2EOptionalFieldOmittedReadsZeroAfterHeapChurn(t *testing.T) {
	// A fresh, never-reused heap page can look zeroed by pure luck even
	// with plain malloc — churn the heap with unrelated allocations first
	// (not asserted on, purely to disturb whatever memory a naive fix might
	// still hand back unzeroed) so this test would have caught the bug for
	// real, not just by accident of allocator behavior on a small/first
	// allocation.
	assertOutput(t, `
interface Point { x: number; y?: number }
for (let i = 0; i < 50; i++) {
  const s = "filler-" + i.toString();
}
let p: Point = { x: 1 };
console.log(p.y);
`, "0")
}

func TestE2EClassFieldUnassignedInConstructorReadsZero(t *testing.T) {
	// This compiler doesn't verify every field is assigned on every path
	// through the constructor (no definite-assignment check) — an
	// under-assigned field must read back a deterministic zero, same
	// reasoning and fix as the optional-object-field case above.
	assertOutput(t, `
class Box {
  x: number;
  y: number;
  constructor() { this.x = 1; }
}
const b = new Box();
console.log(b.x, b.y);
`, "1 0")
}

// --- Object destructuring default values (`{a = expr} = obj`, ADR-00158) ---
//
// Only accepted for a pointer-backed nullable field (T | null where T is
// string/array/object/class) — the one field shape with a real, safe null
// check (`icmp eq ptr %v, null`). A nullable *scalar* field (number|null,
// boolean|null) fakes its null via an in-band sentinel (0/false) that
// collides with a legitimate value of the same spelling, and a
// non-nullable (including merely-optional `?:`) field has no signal at
// all — both are a clean compile-time rejection instead.

func TestE2EObjectDestructuringDefaultUsedWhenNull(t *testing.T) {
	assertOutput(t, `
interface User { name: string | null }
let u: User = { name: null };
let { name = "anon" } = u;
console.log(name);
`, "anon")
}

func TestE2EObjectDestructuringDefaultNotUsedWhenPresent(t *testing.T) {
	assertOutput(t, `
interface User { name: string | null }
let u: User = { name: "Alice" };
let { name = "anon" } = u;
console.log(name);
`, "Alice")
}

func TestE2EObjectDestructuringDefaultOnNullableArrayField(t *testing.T) {
	assertOutput(t, `
interface Box { items: number[] | null }
let empty: Box = { items: null };
let { items = [1, 2, 3] } = empty;
console.log(items.length, items[0]);
`, "3 1")
}

func TestE2EDestructuredObjectParamDefault(t *testing.T) {
	assertOutput(t, `
interface Opts { label: string | null }
function f({ label = "default" }: Opts): void {
  console.log(label);
}
f({ label: null });
f({ label: "set" });
`, "default\nset")
}

func TestE2EObjectDestructuringDefaultOnNonNullableFieldRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface Point { x: number }
let p: Point = { x: 1 };
let { x = 5 } = p;
`)
	if err == nil {
		t.Fatal("expected a compile error for a destructuring default on a non-nullable field")
	}
	if !strings.Contains(err.Error(), "nullable reference type") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EObjectDestructuringDefaultOnNullableScalarFieldRejected(t *testing.T) {
	// number | null fakes its null via an in-band 0 sentinel — indistinct
	// from a real 0, so a default here would silently override a
	// legitimate value. Rejected, not allowed to be unsound.
	_, err := parseAndCompile(`
interface Point { x: number | null }
let p: Point = { x: 0 };
let { x = 5 } = p;
`)
	if err == nil {
		t.Fatal("expected a compile error for a destructuring default on a nullable scalar field")
	}
}

// --- Object destructuring with string/numeric-literal keys (TDD-00065 Stage 3a) ---

func TestE2EObjectDestructuringStringKeyOrdinaryField(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
let p: Point = { x: 3, y: 4 };
let { "x": px, "y": py } = p;
console.log(px, py);
`, "3 4")
}

func TestE2EObjectDestructuringStringKeyNonIdentifierField(t *testing.T) {
	// A field whose name isn't a valid identifier is reachable only through a
	// string-literal key on both the literal and the pattern side.
	assertOutput(t, `
let p = { "first-name": "Ada", "age": 36 };
let { "first-name": fn, "age": a } = p;
console.log(fn, a);
`, "Ada 36")
}

func TestE2EObjectDestructuringNumericKey(t *testing.T) {
	assertOutput(t, `
let p = { 0: "zero", 1: "one" };
let { 0: first, 1: second } = p;
console.log(first, second);
`, "zero one")
}

func TestE2EObjectDestructuringStringKeyInForOf(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; label: string }
let pts: Point[] = [{ x: 1, label: "a" }, { x: 2, label: "b" }];
for (const { "x": vx, label } of pts) {
  console.log(vx, label);
}
`, "1 a\n2 b")
}

func TestE2EObjectDestructuringStringKeyNestedPattern(t *testing.T) {
	assertOutput(t, `
interface Inner { a: number; b: number }
interface Wrap { inner: Inner }
let w: Wrap = { inner: { a: 10, b: 20 } };
let { "inner": { a, b } } = w;
console.log(a, b);
`, "10 20")
}

func TestE2EObjectDestructuringStringKeyWithoutBindingRejected(t *testing.T) {
	// A non-identifier key has no shorthand form, so it must bind through an
	// explicit `: name`.
	_, err := parseAndCompile(`
let p = { x: 1 };
let { "x" } = p;
`)
	if err == nil {
		t.Fatal("expected a compile error for a string key with no `: name` binding")
	}
	if !strings.Contains(err.Error(), "must be bound with") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- Object destructuring rest `{ ...rest }` (TDD-00065 Stage 3b) ---

func TestE2EObjectDestructuringRestBasic(t *testing.T) {
	assertOutput(t, `
interface Rec { a: number; b: number; c: string }
let r: Rec = { a: 1, b: 2, c: "three" };
let { a, ...rest } = r;
console.log(a);
console.log(rest.b, rest.c);
`, "1\n2 three")
}

func TestE2EObjectDestructuringRestExcludesRenamedSourceKey(t *testing.T) {
	// The rest excludes the source *key* (b), not the local name (bb).
	assertOutput(t, `
interface Rec { a: number; b: number; c: number }
let r: Rec = { a: 1, b: 2, c: 3 };
let { b: bb, ...others } = r;
console.log(bb);
console.log(others.a, others.c);
`, "2\n1 3")
}

func TestE2EObjectDestructuringRestWithStringKey(t *testing.T) {
	assertOutput(t, `
interface Rec { a: number; b: number }
let r: Rec = { a: 1, b: 2 };
let { "a": aa, ...tail } = r;
console.log(aa, tail.b);
`, "1 2")
}

func TestE2EObjectDestructuringRestInForOf(t *testing.T) {
	assertOutput(t, `
interface Rec { a: number; b: number; c: string }
let recs: Rec[] = [{ a: 1, b: 2, c: "x" }, { a: 10, b: 20, c: "y" }];
for (const { a, ...more } of recs) {
  console.log(a, more.b, more.c);
}
`, "1 2 x\n10 20 y")
}

func TestE2EObjectDestructuringRestInParameter(t *testing.T) {
	assertOutput(t, `
interface Rec { a: number; b: number; c: number }
function f({ a, ...rest }: Rec): void {
  console.log(a);
  console.log(rest.b, rest.c);
}
f({ a: 1, b: 2, c: 3 });
`, "1\n2 3")
}

func TestE2EObjectDestructuringRestEmptyResidual(t *testing.T) {
	assertOutput(t, `
interface Rec { a: number; b: number }
let r: Rec = { a: 1, b: 2 };
let { a, b, ...rest } = r;
console.log(JSON.stringify(rest));
`, "{}")
}

func TestE2EObjectDestructuringRestShallowCopiesArrayField(t *testing.T) {
	assertOutput(t, `
interface WithArr { id: number; tags: number[] }
let w: WithArr = { id: 7, tags: [1, 2, 3] };
let { id, ...more } = w;
console.log(more.tags.length, more.tags[0]);
`, "3 1")
}

func TestE2EObjectDestructuringRestIsRealObject(t *testing.T) {
	// The residual is a first-class object: spreadable and JSON-serializable.
	assertOutput(t, `
interface Rec { a: number; b: number; c: string }
let r: Rec = { a: 1, b: 2, c: "three" };
let { a, ...rest } = r;
let copy = { ...rest };
console.log(copy.c);
console.log(JSON.stringify(rest));
`, "three\n{\"b\":2,\"c\":\"three\"}")
}

func TestE2EObjectDestructuringRestOfClassInstanceIsPlainObject(t *testing.T) {
	// A class instance's rest yields a plain object of its visible fields only
	// (hidden class metadata stripped), matching JS's own-enumerable copy.
	assertOutput(t, `
class P { x: number; y: number; constructor(x: number, y: number) { this.x = x; this.y = y; } }
let p = new P(3, 4);
let { x, ...rest } = p;
console.log(x);
console.log(rest.y);
console.log(JSON.stringify(rest));
`, "3\n4\n{\"y\":4}")
}

func TestE2EObjectDestructuringRestNotLastRejected(t *testing.T) {
	_, err := parseAndCompile(`
let r = { a: 1, b: 2 };
let { ...rest, a } = r;
`)
	if err == nil {
		t.Fatal("expected a compile error for a rest element that isn't last")
	}
	if !strings.Contains(err.Error(), "must be the last property") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- Object destructuring assignment (`({ a, b } = expr)`, ADR-00160) ---

func TestE2EObjectDestructuringAssignmentShorthand(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
let x: number = 0, y: number = 0;
let p: Point = { x: 5, y: 6 };
({x, y} = p);
console.log(x, y);
`, "5 6")
}

func TestE2EObjectDestructuringAssignmentRenamed(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
let px: number = 0, py: number = 0;
let p: Point = { x: 10, y: 20 };
({x: px, y: py} = p);
console.log(px, py);
`, "10 20")
}

func TestE2EObjectDestructuringAssignmentClosureCapture(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
function make(): () => number {
  let px: number = 0;
  let py: number = 0;
  let p: Point = { x: 10, y: 20 };
  ({x: px, y: py} = p);
  return () => px + py;
}
const fn = make();
console.log(fn());
`, "30")
}

func TestE2EObjectDestructuringAssignmentConstTargetRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface Point { x: number; y: number }
let x: number = 0;
const y: number = 0;
let p: Point = { x: 5, y: 6 };
({x, y} = p);
`)
	if err == nil {
		t.Fatal("expected a compile error assigning into a const destructuring target")
	}
}

func TestE2EObjectDestructuringAssignmentUnknownFieldRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface Point { x: number; y: number }
let x: number = 0, z: number = 0;
let p: Point = { x: 5, y: 6 };
({x, z} = p);
`)
	if err == nil {
		t.Fatal("expected a compile error for a destructuring assignment field the source object doesn't have")
	}
}

// --- Object literal method shorthand (`{ foo() {...} }`) ---

func TestE2EObjectLiteralMethodShorthand(t *testing.T) {
	assertOutput(t, `
const obj = {
    valueOf() {
        return 42;
    }
};
console.log(obj.valueOf());
`, "42")
}

func TestE2EObjectLiteralMethodShorthandWithParamsAndReturnType(t *testing.T) {
	assertOutput(t, `
const calc = {
    add(a: number, b: number): number {
        return a + b;
    },
    label: "calculator"
};
console.log(calc.add(2, 3));
console.log(calc.label);
`, "5\ncalculator")
}

func TestE2EObjectLiteralMethodShorthandStringKey(t *testing.T) {
	assertOutput(t, `
const obj = {
    "greet"(): string {
        return "hi";
    }
};
console.log(obj.greet());
`, "hi")
}

func TestE2EObjectLiteralMethodShorthandNamedGet(t *testing.T) {
	// "get"/"set" are contextual, not reserved — a plain method literally
	// named get/set (not an accessor, no getter/setter support on object
	// literals at all) must still parse as an ordinary method.
	assertOutput(t, `
const obj = {
    get(): number { return 5; }
};
console.log(obj.get());
`, "5")
}

func TestE2EObjectLiteralMethodShorthandThisRejected(t *testing.T) {
	// V1 scope: a method-shorthand value desugars to a plain anonymous
	// function expression (TDD-00060), which has no `this` binding at all —
	// unlike a class method, an object literal has no static nominal type to
	// give `this` a known shape, and no dynamic this-binding machinery
	// exists yet. Confirmed this fails cleanly (not a silent miscompile).
	_, err := parseAndCompile(`
const counter = {
    count: 0,
    increment() {
        this.count = this.count + 1;
        return this.count;
    }
};
console.log(counter.increment());
`)
	if err == nil {
		t.Fatal("expected a compile error for 'this' inside an object-literal method, got none")
	}
	if !strings.Contains(err.Error(), "'this' is only valid inside a method or constructor body") {
		t.Fatalf("expected the 'this' is only valid... error, got: %v", err)
	}
}

// A nullable-scalar object/interface field (TDD-00064 Stage 3) carries a
// presence bit, so a null field is distinguishable from a present 0 across
// reads, `??`, `=== null`, assignment, JSON, and template interpolation.
func TestE2ENullableScalarField(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number | null }
const u: User = { name: "A", age: 0 }
console.log(u.age)
console.log(u.age ?? 99)
console.log(u.age === null)
console.log(` + "`age=${u.age}`" + `)
const u2: User = { name: "B", age: null }
console.log(u2.age)
console.log(u2.age ?? 99)
console.log(u2.age === null)
console.log(JSON.stringify(u))
console.log(JSON.stringify(u2))
u2.age = 42
console.log(u2.age ?? 0)
u2.age = null
console.log(u2.age ?? 7)
`, "0\n0\nfalse\nage=0\nnull\n99\ntrue\n"+`{"name":"A","age":0}`+"\n"+`{"name":"B","age":null}`+"\n42\n7")
}

// A nullable-scalar field survives JSON.parse: a literal null stays null, a 0
// stays a present 0.
func TestE2ENullableScalarFieldJSONParse(t *testing.T) {
	assertOutput(t, `
interface User { name: string; age: number | null }
const a: User = JSON.parse('{"name":"P","age":null}')
console.log(a.age === null)
console.log(a.age ?? 7)
const b: User = JSON.parse('{"name":"Q","age":0}')
console.log(b.age === null)
console.log(b.age ?? 7)
`, "true\n7\nfalse\n0")
}

// A nullable-scalar class field, set from a nullable-scalar constructor
// parameter, keeps its null-ness (not a present 0).
func TestE2ENullableScalarClassField(t *testing.T) {
	assertOutput(t, `
class Account {
  balance: number | null
  constructor(b: number | null) { this.balance = b }
}
console.log(new Account(0).balance ?? -1)
console.log(new Account(null).balance ?? -1)
console.log(new Account(5).balance === null)
`, "0\n-1\nfalse")
}

// Homogeneous fixed-shape objects keep real typed values in
// Object.values/Object.entries (ADR-00492) — usable as numbers, not
// stringified. Heterogeneous shapes still stringify (covered above).
func TestE2EObjectValuesEntriesTyped(t *testing.T) {
	assertOutput(t, `
interface Scores { math: number; physics: number }
const s: Scores = { math: 90, physics: 82 }
let total: number = 0
for (const v of Object.values(s)) {
  total = total + v
}
console.log(total)
for (const [k, v] of Object.entries(s)) {
  console.log(k + ":" + (v + 1))
}
`, "172\nmath:91\nphysics:83")
}

// TDD-00153: object-literal getters/setters are lowered to a synthetic
// anonymous class, so `this` in the accessor body reads/writes the object and
// `o.x` / `o.x = v` dispatch through the getter/setter.
func TestE2EObjectLiteralGetterSetter(t *testing.T) {
	assertOutput(t, `
const o = {
  _x: 10,
  get x(): number { return this._x; },
  set x(v: number) { this._x = v; }
};
console.log(o.x);
o.x = 20;
console.log(o.x);
`, "10\n20")
}

// A getter may compute over multiple fields; a data initializer may reference
// an enclosing local; compound assignment through a setter works.
func TestE2EObjectLiteralAccessorsMixed(t *testing.T) {
	assertOutput(t, `
const base = 5;
const o = {
  _first: "Ada",
  _last: "Lovelace",
  scale: base,
  get fullName(): string { return this._first + " " + this._last; },
  set fullName(v: string) { this._first = v; },
  get scaled(): number { return this.scale * 2; }
};
console.log(o.fullName);
o.fullName = "Grace";
console.log(o.fullName);
console.log(o.scaled);
`, "Ada Lovelace\nGrace Lovelace\n10")
}

// An unannotated getter infers its return type from the field it reads.
func TestE2EObjectLiteralUnannotatedGetter(t *testing.T) {
	assertOutput(t, `
const o = { _n: 42, get n() { return this._n; } };
console.log(o.n);
`, "42")
}

// Assigning to a property that has no setter is a clean compile error.
func TestE2EObjectLiteralGetterNoSetterRejected(t *testing.T) {
	_, err := parseAndCompile(`
const o = { _n: 1, get n(): number { return this._n; } };
o.n = 5;
`)
	if err == nil {
		t.Fatal("expected a compile error assigning a getter-only property, got none")
	}
	if !strings.Contains(err.Error(), "no setter") {
		t.Fatalf("expected 'no setter', got: %v", err)
	}
}

// V1 boundary: an accessor-bearing literal can't be assigned to a differently
// shaped structural object type (its accessors are methods, not fields).
func TestE2EObjectLiteralAccessorStructuralAssignRejected(t *testing.T) {
	_, err := parseAndCompile(`
const o = { _n: 1, get n(): number { return this._n; } };
const p: { n: number } = o;
console.log(p.n);
`)
	if err == nil {
		t.Fatal("expected a compile error for the structural assignment, got none")
	}
	if !strings.Contains(err.Error(), "getter/setter") {
		t.Fatalf("expected the accessor-shape error, got: %v", err)
	}
}

// ADR-00608: constant-key bracket access reads/writes a fixed object's field.
func TestE2EObjectConstantKeyBracketAccess(t *testing.T) {
	assertOutput(t, `
const o = { a: 1, b: 2 };
console.log(o["a"]);
o["a"] = 10;
o["b"] += 5;
console.log(o["a"], o["b"]);
const n = { 0: "x", 1: "y" };
console.log(n[0], n[1]);
`, "1\n10 7\nx y")
}

// A class instance's field is reachable by a constant string key too.
func TestE2EClassConstantKeyBracketAccess(t *testing.T) {
	assertOutput(t, `
class Box { v: number = 3; }
const b = new Box();
console.log(b["v"]);
b["v"] = 8;
console.log(b["v"]);
`, "3\n8")
}

// ADR-00608: a string-literal or numeric field name is declarable in an
// interface / object type annotation and read back via constant-key access.
func TestE2EObjectTypeQuotedFieldName(t *testing.T) {
	assertOutput(t, `
interface Person { "first-name": string; age: number }
const p: Person = { "first-name": "Ada", age: 36 };
console.log(p["first-name"], p.age);
type Pair = { 0: string; 1: number };
const pr: Pair = { 0: "x", 1: 2 };
console.log(pr[0], pr[1]);
`, "Ada 36\nx 2")
}

// ADR-00609: a constant computed key in object destructuring resolves to the
// matching field, the same as a plain `{ key: local }`.
func TestE2EObjectDestructuringComputedConstKey(t *testing.T) {
	assertOutput(t, `
const obj = { a: 1, b: "two" };
const { ["a"]: x, ["b"]: y } = obj;
console.log(x, y);
const n = { 0: "zero", 1: "one" };
const { [0]: first } = n;
console.log(first);
`, "1 two\nzero")
}

// A runtime-valued computed key has no static field to bind — a clean rejection.
func TestE2EObjectDestructuringComputedRuntimeKeyRejected(t *testing.T) {
	_, err := parseAndCompile(`
const key = "a";
const obj = { a: 1 };
const { [key]: x } = obj;
console.log(x);
`)
	if err == nil {
		t.Fatal("expected a compile error for a runtime computed destructuring key, got none")
	}
	if !strings.Contains(err.Error(), "constant string or number") {
		t.Fatalf("expected the constant-key error, got: %v", err)
	}
}
