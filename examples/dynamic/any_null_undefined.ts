// null and undefined survive being boxed into an `any` slot as their own
// distinct values (ADR-00958): `=== null` / `=== undefined` and `typeof` all
// behave exactly as in Node, whether the box is an any[] element, a function's
// `any` return, a Map value, or an object's `any` field. (Previously such a box
// stored a raw 0, so every comparison read false and a boxed null rendered as
// undefined.) Runs the same under Node.js.

const values: any[] = [undefined, 42, "hello", null];

console.log("undefined element:", values[0] === undefined, "| typeof:", typeof values[0]);
console.log("null element:     ", values[3] === null, "| typeof:", typeof values[3]);
console.log("null !== undefined:", values[3] !== values[0]);

// A function typed to return `any`.
function lookup(key: string): any {
  if (key === "missing") return undefined;
  if (key === "empty") return null;
  return key.length;
}
console.log("missing is undefined:", lookup("missing") === undefined);
console.log("empty is null:       ", lookup("empty") === null);

// A Map whose values are `any`.
const cache = new Map<string, any>();
cache.set("pending", undefined);
cache.set("cleared", null);
console.log("map undefined:", cache.get("pending") === undefined);
console.log("map null:     ", cache.get("cleared") === null);

// JSON.stringify of an array boxes undefined to null, matching Node.
console.log("json:", JSON.stringify(values));
