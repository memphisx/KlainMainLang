// A class extending Array or Promise picks the constructor its derived
// results use with a static [Symbol.species] getter.

class Plain extends Array<number> {
  static get [Symbol.species]() { return Array; }
}
class Tagged extends Array<number> {
  tag(): string { return "tagged"; }
}
class ToTagged extends Array<number> {
  static get [Symbol.species]() { return Tagged; }
}
class Child extends Plain {}

const p = new Plain();
p.push(1, 2, 3);
const doubled = p.map((x) => x * 2);
console.log(doubled instanceof Plain, Array.isArray(doubled), doubled);

const t = new ToTagged();
t.push(4, 5);
const big = t.filter((x) => x > 4);
console.log(big instanceof Tagged, big.length);

const c = new Child();
c.push(9);
console.log(c.slice(0) instanceof Child);

class Task extends Promise<number> {
  static get [Symbol.species]() { return Promise; }
}
const task = new Task((resolve) => resolve(20));
const next = task.then((x) => x + 1);
console.log(next instanceof Task, next instanceof Promise);
next.then((v) => console.log("settled", v));
