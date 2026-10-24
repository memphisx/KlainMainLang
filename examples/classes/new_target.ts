// new.target names the class `new` was called with: a base class can tell
// which subclass is being built, and refuse to be built directly.

abstract class Shape {
  readonly kind: string;
  constructor() {
    if (new.target === Shape) throw new TypeError("Shape is abstract");
    this.kind = new.target.name.toLowerCase();
  }
}

class Circle extends Shape {
  r: number;
  constructor(r: number) {
    super();
    this.r = r;
  }
}

class Square extends Shape {
  side: number;
  constructor(side: number) {
    super();
    this.side = side;
  }
}

const shapes: Shape[] = [new Circle(1), new Square(2)];
for (const s of shapes) console.log(s.kind);

function describe(): string {
  return new.target === undefined ? "called" : "constructed";
}
console.log(describe());
try {
  new (Shape as any)();
} catch (e) {
  console.log((e as Error).message);
}
