// Classes and builtin constructors are values: stored, passed, picked at
// run time, and constructed with `new` through any expression.

class Shape {
  static count = 0;
  name = "shape";
  size: number;
  constructor(size = 0) {
    this.size = size;
    Shape.count++;
  }
  area(): number {
    return 0;
  }
  static unit(): Shape {
    const s = new Shape(1);
    s.name = "unit";
    return s;
  }
}

class Square extends Shape {
  constructor(side: number) {
    super(side);
    this.name = "square";
  }
  area(): number {
    return this.size * this.size;
  }
}

// A class through a `typeof` binding, a parameter and an array.
let Kind: typeof Shape = Square;
console.log(new Kind(3).area(), Kind.name, Kind.unit().name);

function build(C: typeof Shape, n: number): Shape {
  return new C(n);
}
console.log(build(Shape, 1).name, build(Square, 4).area());

const kinds: (typeof Shape)[] = [Shape, Square];
for (const k of kinds) console.log(k.name, new k(2).area());

// A constructor-typed parameter infers the instance type.
function make<T>(ctor: new () => T): T {
  return new ctor();
}
console.log(make(Shape).name, make(Map).size);

// `new` through a member, an index and a call.
class Factory {
  Product: typeof Shape = Square;
  create(): Shape {
    return new this.Product(5);
  }
}
console.log(new Factory().create().area());
const registry = { Shape, Square };
console.log(new registry.Square(6).area());
const pick = (big: boolean) => (big ? Square : Shape);
console.log(new (pick(true))(7).area());

// Builtin constructors as values.
const M = Map;
const m = new M<string, number>([["Thessaloniki", 1]]);
console.log(m.get("Thessaloniki"), M.name);
const ctors: any[] = [Set, Date, Error];
console.log(new ctors[0]([1, 2, 2]).size, new ctors[1](0).getTime(), new ctors[2]("boom").message);

// Statics through the class value.
Kind.count = 100;
console.log(Shape.count);

try {
  const notACtor: any = 5;
  new notACtor();
} catch (e) {
  console.log((e as Error).message);
}
