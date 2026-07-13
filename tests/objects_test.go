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
