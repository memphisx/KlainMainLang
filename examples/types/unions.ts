// General union types beyond T | null (TDD-00043) — string | number | boolean
// combinations, including with null/undefined, at the same runtime-tagged
// { i8, i64 } box any/unknown already use, but statically restricted to the
// declared member set at every assignment/call/return boundary. V1 scope:
// scalar members only (number/string/boolean/null/undefined) — a union
// nested inside an array element or object field, or a member that's itself
// an interface/array, is not yet supported and gives a clean compile-time
// error instead. See docs/status/TYPE-SYSTEM.md.

// --- declare, print, typeof, reassign across members ---
let x: string | number = "hello"
console.log(x)           // hello
console.log(typeof x)    // string
x = 42
console.log(x)           // 42
console.log(typeof x)    // number

// --- a third member, plus null ---
let y: number | boolean | null = 5
console.log(y)           // 5
y = true
console.log(y)           // true
y = null
console.log(y)           // null

// --- functions: a union param is checked at every call site, a union return
// is boxed like any other dynamic value. Arithmetic and member access on a
// union-typed value are still a clean compile error (Staged V1, same as
// any/unknown) — typeof/===/!== and printing are what's usable without first
// narrowing to a concrete type, which this compiler doesn't do yet either. ---
function describe(value: string | number): string | number {
	if (typeof value === "string") {
		return "matched a string"
	}
	return value
}
console.log(describe("hi"))  // matched a string
console.log(describe(21))    // 21

// --- arrow functions work the same way ---
const toDisplay = (n: number | boolean): string | number => {
	return n
}
console.log(toDisplay(7))     // 7
console.log(toDisplay(false)) // false

// --- equality reuses any/unknown's own tag-aware comparison ---
let a: string | number = 5
let b: string | number = 5
console.log(a === b)     // 1
let c: string | number = "5"
console.log(a === c)     // 0

// --- flow narrowing (TDD-00114): typeof/truthiness refine a union in-branch ---
function describeVal(x: string | number): string {
	if (typeof x === "string") {
		return "str:" + x.toUpperCase();   // x is string here
	} else {
		return "num:" + (x + 1);           // x is number here
	}
}
console.log(describeVal("hi"));  // str:HI
console.log(describeVal(41));    // num:42

// nested else-if refines the remaining members; early return narrows the rest
function label(v: string | number | boolean): string {
	if (typeof v === "boolean") {
		return v ? "yes" : "no";
	}
	if (typeof v === "number") {
		return "n" + (v * 2);
	}
	return "s" + v.length;             // v is string here
}
console.log(label(true));   // yes
console.log(label(10));     // n20
console.log(label("abc"));  // s3

// --- object union members (TDD-00115): one object member, usable via narrowing ---
interface Point { x: number; y: number; }
function render(v: string | Point): string {
	if (typeof v === "object") {
		return "(" + v.x + "," + v.y + ")";   // v is Point here
	}
	return v.toUpperCase();                  // v is string here
}
console.log(render({ x: 3, y: 4 }));  // (3,4)
console.log(render("hi"));            // HI

// Point | null with truthiness narrowing + object rest
function sumRest(p: Point | null): number {
	if (p) {
		const { x, ...rest } = p;
		return x + rest.y;
	}
	return -1;
}
console.log(sumRest({ x: 10, y: 20 }));  // 30
console.log(sumRest(null));              // -1

// --- discriminated unions (TDD-00116): a shared literal tag field ---
interface Circle { kind: "circle"; r: number; }
interface Square { kind: "square"; side: number; }
function area(sh: Circle | Square): number {
	if (sh.kind === "circle") {
		return 3 * sh.r * sh.r;   // sh is Circle here
	}
	return sh.side * sh.side;    // sh is Square here (early-return narrowed)
}
console.log(area({ kind: "circle", r: 2 }));    // 12
console.log(area({ kind: "square", side: 3 })); // 9
