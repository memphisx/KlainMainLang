// `var` is function-scoped and hoisted: the binding exists for the whole
// function, and on a path where its declaration never ran it holds a real
// `undefined` — not a zero value. TypeScript rejects these reads (a variable
// used before being assigned), so this runs as JavaScript, as Node runs it.

function pick(cond) {
  if (cond) {
    var n = 42;
    var s = "hello";
    var o = { x: 1 };
    var xs = [1, 2, 3];
  }
  console.log(n, s, o, xs);                                     // 42 hello { x: 1 } [ 1, 2, 3 ]  /  undefined ×4
  console.log(typeof n, typeof s, typeof o, typeof xs);         // number string object object  /  undefined ×4
  if (n !== undefined) console.log("narrowed", n + 1);          // narrowed 43  /  (nothing)
  return n;                                                     // 42  /  undefined
}
console.log(pick(true));
console.log(pick(false));

// Dead code after a `break` still declares its `var`s.
do { var a = 1; break; var b = "never"; } while (0);
console.log(a, b, typeof b);                                    // 1 undefined undefined

// A `for` init `var` runs whenever the loop does; a body `var` leaks out too.
for (var i = 0; i < 3; i++) { var last = i * 10; }
console.log(i, last);                                           // 3 20

// A module-level `var` inside a block is a module binding: a function
// reads it, undefined until (and unless) its declaration runs.
if (Math.random() > 2) { var never = "set"; }
for (let k = 0; k < 3; k++) { var seen = k; }
function readBoth() { return [never, seen]; }
console.log(readBoth());                                        // [ undefined, 2 ]

// A `for (var k in/of …)` head declares the function's own k: a nested
// function declaration reads it, an earlier `var k` is the same binding, and
// it shadows an outer k before and after the loop.
function heads() {
  var key = 0;
  for (var key in { a: 1, b: 2 }) report();
  for (var item of [7, 8]) {}
  function report() { return key; }
  return [report(), key, item];
}
const item = "outer";
console.log(heads(), item);                                     // [ 'b', 'b', 8 ] outer

// A local written inside a `try` keeps its value after a throw.
let count = 0;
try { count = 1; throw new Error("boom"); } catch (e) { console.log("caught with count =", count); } // 1
console.log(count);                                             // 1

// Unary + is ToNumber, exactly like Number().
const nothing = null, missing = undefined;
console.log(+"  12 ", +"0x10", +"junk", +true, +nothing, +missing, +[], +[5]); // 12 16 NaN 1 0 NaN 0 5
