// A primitive's wrapper object: `new Number(3)` is an object holding 3.
const n = new Number(2.5);
const s = new String("hello");
const b = new Boolean(false);

console.log(typeof n, n instanceof Number, n === n, new Number(1) === new Number(1));
console.log(n, s, b);
console.log(n.toFixed(2), s.toUpperCase(), s.length, s[1], b.valueOf());
console.log(b ? "a wrapper is always truthy" : "unreachable");
console.log(`${n} ${s} ${b}`, JSON.stringify({ n, s, b }));
console.log(Object.prototype.toString.call(s), Object.keys(s));

// Object(primitive) wraps it too, and a wrapper takes properties.
const w: any = Object(42);
w.label = "answer";
console.log(w, w.label, w + 1);
export {};
