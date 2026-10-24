// Own keys list in creation order, and a key exists only once written.
interface Point { x: number; y: number; label?: string }

const p: Point = { y: 2, x: 1 };
console.log(Object.keys(p), JSON.stringify(p));

const q: Point = JSON.parse('{"label":"Thessaloniki","y":5,"x":4}');
console.log(q);

delete q.label;
q.label = "again";
console.log(Object.keys(q));

class Lazy {
  declare cache: string;
  name = "lazy";
  fill() { this.cache = "ready"; }
}
const l = new Lazy();
console.log(l, "cache" in l);
l.fill();
console.log(l);
