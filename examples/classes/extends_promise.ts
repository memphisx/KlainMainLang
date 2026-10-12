// class X extends Promise: a promise with methods of its own. The instance
// is the promise itself: await, instanceof and console.log behave as in Node,
// and Promise's then/catch/finally and statics make instances of the class
// (species), through its constructor.

class Timed extends Promise<string> {
  label = "timed";
  describe(): string {
    return "a " + this.label + " promise";
  }
}

let built = 0;
class Counted extends Promise<number> {
  constructor(executor: (resolve: (v: number) => void, reject: (e: unknown) => void) => void) {
    built++;
    super(executor);
  }
}

async function main(): Promise<void> {
  const t = new Timed((resolve) => setTimeout(() => resolve("Thessaloniki"), 10));
  console.log(t.describe(), t instanceof Promise, t instanceof Timed);
  console.log(await t);

  const doubled = Counted.resolve(21).then((n) => n * 2);
  console.log(doubled instanceof Counted, await doubled);
  console.log(await Counted.all([Counted.resolve(1), 2, Promise.resolve(3)]));
  console.log("constructions:", built);
  console.log(t);
}

main();
