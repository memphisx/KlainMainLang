// A class whose base is a value known only at run time: a conditional
// over classes, or a mixin over a constructor-typed parameter. Each class
// the value can hold gets its own subclass, chosen by the value at run time.
class Circle { area(r: number) { return Math.round(Math.PI * r * r); } kind() { return "circle"; } }
class Square { area(s: number) { return s * s; } kind() { return "square"; } }

const shapes = [Circle, Square];
for (const Shape of shapes) {
  class Labeled extends Shape {
    label(n: number) { return `${this.kind()} of ${n}: ${this.area(n)}`; }
  }
  const l = new Labeled();
  console.log(l.label(3), l instanceof Shape, l instanceof Labeled);
}

type Named = new (...a: any[]) => { kind(): string };
function Shouting(Base: Named) {
  return class extends Base { shout() { return this.kind().toUpperCase() + "!"; } };
}
const fromConfig = (useCircle: boolean): Named => useCircle ? Circle : Square;
console.log(new (Shouting(fromConfig(true)))().shout(), new (Shouting(fromConfig(false)))().shout());
