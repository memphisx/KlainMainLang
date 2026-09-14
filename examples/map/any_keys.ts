// Any-keyed Map — a bare `new Map()` whose keys are heterogeneous widens to
// the any-keyed runtime, holding string, number, NaN, boolean, object, and
// null keys together and reading each back by SameValueZero (TDD-00211).
const m = new Map();

const key = { id: 1 };
m.set("name", "thessaloniki");
m.set(42, "the answer");
m.set(true, "flag");
m.set(NaN, "not a number");
m.set(key, "an object");
m.set(null, "nothing");

console.log(m.size); // 6
console.log(m.get("name")); // thessaloniki
console.log(m.get(42)); // the answer
console.log(m.get(true)); // flag
console.log(m.get(NaN)); // not a number  (NaN is SameValueZero-equal to NaN)
console.log(m.get(key)); // an object     (objects key by reference)
console.log(m.get(null)); // nothing

// +0 and -0 collapse to a single normalized key.
m.set(-0, "zero");
console.log(m.get(0)); // zero
console.log(m.size); // 7

// A miss reads back as undefined.
console.log(m.has("missing")); // false
console.log(m.get("missing")); // undefined
