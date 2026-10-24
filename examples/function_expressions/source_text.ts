// Function.prototype.toString: a function's or class's source text, with
// its type syntax blanked to spaces as Node's type stripping leaves it.
// Object-literal accessors are own, enumerable properties.

function area(width: number, height: number): number {
  return width * height;
}
const double = (x: number) => x * 2;

class Counter<T> {
  private readonly seen: T[] = [];
  add(item: T): this {
    this.seen.push(item);
    return this;
  }
  get count(): number {
    return this.seen.length;
  }
}

console.log(String(area));
console.log(`${double}`);
console.log(String(Counter.prototype.add));
console.log(new Counter<string>().add("Thessaloniki").count);

const city = {
  name: "Thessaloniki",
  get label(): string {
    return `${this.name}, GR`;
  },
};
console.log(city, Object.keys(city), JSON.stringify(city));
console.log(Object.getOwnPropertyDescriptor(city, "label"));
