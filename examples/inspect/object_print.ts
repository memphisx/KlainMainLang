// Object-to-string — Node-style structured printing (TDD-00075/ADR-00218).
//
// console.log(obj) renders a class instance / object literal the way Node's
// util.inspect does — `ClassName { field: value }` — in both -compat modes.
// String coercion (`${obj}`, `"" + obj`) uses the same view under the default
// -compat=strict; under -compat=js it's JS's `[object Object]` instead.

class Point {
  x: number = 1;
  y: number = 2;
}
console.log(new Point()); // Point { x: 1, y: 2 }

class Person {
  name: string = "Ada";
  age: number = 36;
  admin: boolean = true;
}
console.log(new Person()); // Person { name: 'Ada', age: 36, admin: true }

// Object literals print without a name; empty objects show just the braces.
console.log({ id: 7, tag: "widget" }); // { id: 7, tag: 'widget' }
console.log({ outer: 1, inner: { x: 2, y: "deep" } }); // nested, recursed

// A bigint field shows the `n` suffix, like Node inspect.
class Money {
  amount: bigint = 1000n;
}
console.log(new Money()); // Money { amount: 1000n }

// Arrays render Node-style too (and console.log(array) now works at all).
console.log([1, 2, 3]); // [ 1, 2, 3 ]
console.log(["a", "b"]); // [ 'a', 'b' ]
console.log({ tags: [10, 20], owner: "me" }); // { tags: [ 10, 20 ], owner: 'me' }

// String coercion (default -compat=strict) uses the same useful view — never
// the information-losing [object Object] (that's opt-in via -compat=js).
const p = new Point();
console.log(`point = ${p}`); // point = Point { x: 1, y: 2 }

// A user-defined toString() is honored in string coercion (both modes); a bare
// console.log still shows the structured view, like Node.
class Price {
  amount: number = 42;
  toString(): string {
    return "$" + this.amount;
  }
}
const pr = new Price();
console.log(`price = ${pr}`); // price = $42
console.log(pr); // Price { amount: 42 }
