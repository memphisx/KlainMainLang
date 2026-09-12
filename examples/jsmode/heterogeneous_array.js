// Vanilla, untyped JavaScript compiled natively under -compat=js: an array
// that holds more than one element type — a plain heterogeneous array in JS —
// is backed by a boxed-element array (one NaN box per slot, box-on-write /
// unbox-on-read over the normal array machinery). Every array method comes
// along: push/index/typeof, map/filter/reduce, indexOf/includes (by value),
// concat (boxes a concrete array's elements), JSON.stringify, and sort. (In
// the strict default lane a heterogeneous array is a clean compile error that
// points at the escape hatches — a tuple, an `any[]` annotation, or -compat=js
// — since strict keeps arrays uniform for native speed.)
// Run with:  klainmain -compat=js heterogeneous_array.js

// An untyped [] whose usage mixes element kinds is inferred as a boxed array.
const items = [];
items.push(42);
items.push("hello");
items.push(true);
console.log(items.length);                    // 3
console.log(JSON.stringify(items));           // [42,"hello",true]
console.log(typeof items[0], typeof items[1], typeof items[2]); // number string boolean

// A heterogeneous literal works the same way.
const mixed = [1, "two", { label: "three" }];
console.log(JSON.stringify(mixed));           // [1,"two",{"label":"three"}]
console.log(mixed[2].label);                  // three

// The full method surface flows through the box.
console.log(items.filter((x) => typeof x === "string").length); // 1
console.log(items.map((x) => String(x)).join("|"));             // 42|hello|true

// indexOf / includes compare by value, not by box identity.
const needle = "hel" + "lo";                  // a distinct "hello" string
console.log(items.indexOf(needle), items.includes(needle));     // 1 true

// concat boxes the elements of a concrete-typed array as it merges them in.
console.log(JSON.stringify(items.concat([7, 8])));              // [42,"hello",true,7,8]

// Default sort is lexicographic over each element's String() form (like JS),
// and a custom comparator receives the boxed elements.
const nums = [];
nums.push(10); nums.push(2); nums.push(1);
console.log(JSON.stringify(nums.sort()));                        // [1,10,2]
console.log(JSON.stringify(nums.sort((a, b) => a - b)));         // [1,2,10]
