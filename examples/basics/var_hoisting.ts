// `var` is function-scoped and hoisted: the binding exists for the whole
// function, and on a path where its declaration never ran it holds a real
// `undefined` — not its type's zero value. TypeScript keeps the declared
// type (`number`, `string`, …); the value is what Node produces.

function pick(cond: boolean) {
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

// A local written inside a `try` keeps its value after a throw.
let count = 0;
try { count = 1; throw new Error("boom"); } catch (e) { console.log("caught with count =", count); } // 1
console.log(count);                                             // 1

// Unary + is ToNumber, exactly like Number().
console.log(+"  12 ", +"0x10", +"junk", +true, +null, +undefined, +[], +[5]); // 12 16 NaN 1 0 NaN 0 5
