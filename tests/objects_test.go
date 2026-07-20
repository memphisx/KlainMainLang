package tests

import (
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
`, "Alice\n30\n1")
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
for (const e of entries) {
  console.log(e.key + '=' + e.value)
}
`, "host=localhost\nport=8080")
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
`, "7\n1")
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
