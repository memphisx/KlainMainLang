// A class extending Function: each instance is a callable function, built
// from the constructor's source strings, that also carries the class's
// methods and fields.
class Formula extends Function {
  label: string;
  constructor(label: string) {
    super("x", "return x * x + 1");
    this.label = label;
  }
  table(xs: number[]): string {
    return xs.map((x) => `${this.label}(${x}) = ${this(x)}`).join(", ");
  }
}

const f = new Formula("f");
console.log(f(3));
console.log(f.table([0, 1, 2]));
console.log(f instanceof Formula, f instanceof Function, typeof f);
