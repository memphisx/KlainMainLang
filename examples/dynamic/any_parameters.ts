// --- Bare `any` / `unknown` as a function parameter and return type ---
// TDD-00062 (Staged V2): a bare any/unknown parameter is boxed at the call
// boundary and usable via print, `typeof`, and `===`/`!==`. Arithmetic,
// field access, indexing, and passing an array value into an `any` slot stay
// clean compile errors (the value is opaque until narrowed by other means).

// A parameter typed `any` accepts a value of any (boxable) type; printing it
// dispatches on the runtime tag.
function show(x: any): void {
    console.log(x);
}
show(42);        // 42
show("hello");   // hello
show(true);      // true
show(null);      // null

// `unknown` behaves identically here; `typeof` reflects the boxed value's tag.
function kindOf(x: unknown): void {
    console.log(typeof x);
}
kindOf(42);      // number
kindOf("hi");    // string
kindOf(false);   // boolean

// `===` on two `any` values compares by the runtime tag: same tag + same
// payload is equal, a tag mismatch is never equal (matching JS strict
// equality — e.g. the number 1 is never `===` the string "1").
function compare(a: any, b: any): void {
    console.log(a === b ? "equal" : "not equal");
}
compare(1, 1);       // equal
compare(1, "1");     // not equal
compare("x", "x");   // equal

// Objects box by reference, so `===` is reference identity — again matching JS.
const point = { x: 1, y: 2 };
compare(point, point);        // equal
compare(point, { x: 1, y: 2 }); // not equal

// A bare `any` return type round-trips the same box back to the caller.
function pick(flag: boolean, a: any, b: any): any {
    return flag ? a : b;
}
console.log(pick(true, "chosen", 0));   // chosen
console.log(pick(false, "chosen", 0));  // 0

// Arrow functions and function expressions get the same support — an `any`
// parameter is boxed at the call site regardless of the function's shape.
const arrowCompare = (a: any, b: any): void => {
    console.log(a === b ? "equal" : "not equal");
};
arrowCompare(1, 1);    // equal
arrowCompare(1, "1");  // not equal

const exprShow = function (x: any): void {
    console.log(x);
};
exprShow("from a function expression");  // from a function expression

// Objects and arrays are reference types: boxing keeps their identity, so
// `===` is reference equality (not a content comparison) — matching JS.
const list = [1, 2, 3];
arrowCompare(list, list);   // equal   (same array)
arrowCompare([1], [1]);     // not equal (two distinct arrays)

// A boxed reference type stringifies to its `[object …]` tag. (An object's
// fields and an array's elements aren't recoverable from the boxed pointer,
// so the contents aren't shown.)
show({ a: 1 });  // [object Object]
show(list);      // [object Array]
