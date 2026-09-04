// console/util formatting fidelity — printf-style substitution, undefined,
// Map/Set inspection, and the Buffer console form (ADR-00690), all matching
// Node.js.

// A string-literal first argument is a util.format format string. %s inserts
// String(arg), %d/%i an integer, %f a float, %j JSON, %o/%O util.inspect.
console.log("%s!", "hi"); // hi!
console.log("%d + %d = %d", 2, 3, 5); // 2 + 3 = 5
console.log("progress: %f%%", 12.5); // progress: 12.5%

// %% is a literal percent; %c consumes its argument (a browser CSS string) and
// emits nothing; unconsumed arguments are appended space-separated.
console.log("100%% done", "ok"); // 100% done ok
console.log("%c styled", "color:red", "then this"); //  styled then this

// undefined prints as the keyword (not a blank line); null and undefined stay
// distinct.
console.log(undefined); // undefined
console.log(null, undefined); // null undefined

// Map and Set inspect the way Node shows them.
console.log(new Map([["a", 1], ["b", 2]])); // Map(2) { 'a' => 1, 'b' => 2 }
console.log(new Set([1, 2, 3])); // Set(3) { 1, 2, 3 }
console.log(new Map()); // Map(0) {}

// A Buffer uses Node's `<Buffer ..>` hex form, distinct from a Uint8Array.
console.log(Buffer.from("hi")); // <Buffer 68 69>
console.log(Buffer.from([255, 16, 0])); // <Buffer ff 10 00>
