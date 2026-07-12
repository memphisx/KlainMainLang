interface Point {
  x: number;
  y: number;
}

interface User {
  name: string;
  age: number;
}

// type alias — same power, different syntax
type Rect = { width: number; height: number }

function distance(p: Point): number {
  return Math.floor(Math.sqrt(p.x * p.x + p.y * p.y))
}

function greet(u: User): string {
  return `Hello ${u.name}, age ${u.age}`
}

function area(r: Rect): number {
  return r.width * r.height
}

const p: Point = { x: 3, y: 4 }
console.log(distance(p))     // 5

const u: User = { name: 'Alice', age: 30 }
console.log(greet(u))        // Hello Alice, age 30
console.log(JSON.stringify(u)) // {"name":"Alice","age":30}

const r: Rect = { width: 6, height: 7 }
console.log(area(r))         // 42

// Inline interface type in var decl
const p2: Point = { x: 0, y: 0 }
p2.x = 5
p2.y = 12
console.log(distance(p2))    // 13
