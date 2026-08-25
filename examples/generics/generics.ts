// User-defined generics (TDD-00010 V1): monomorphization for functions,
// interfaces, and classes — one specialized, mangled implementation per
// distinct concrete type actually used, the same approach the built-in
// generics (Array<T>, Map<K,V>, Promise<T>) already use by hand, now
// generalized to user code. Single, unconstrained type parameter only.

// --- Generic functions: the concrete type is inferred from an argument ---

function identity<T>(x: T): T {
  return x;
}

console.log(identity(42));       // 42
console.log(identity("hello"));  // hello

// A parameter typed `T[]` infers T from the array's own element type.
function first<T>(arr: T[]): T {
  return arr[0];
}

const nums: number[] = [10, 20, 30];
const words: string[] = ["a", "b", "c"];
console.log(first(nums));   // 10
console.log(first(words));  // a

// --- Generic interfaces: a structural object type per concrete field type ---

interface Box<T> {
  value: T;
}

const boxedNumber: Box<number> = { value: 7 };
const boxedString: Box<string> = { value: "seven" };
console.log(boxedNumber.value);  // 7
console.log(boxedString.value);  // seven

// --- Generic classes: the concrete type is explicit at each `new` site ---
// (unlike a bare generic call, `new ClassName<T>(...)` isn't ambiguous with
// a comparison operator, so V1 doesn't need inference here at all.)

class Container<T> {
  value: T;

  constructor(v: T) {
    this.value = v;
  }

  get(): T {
    return this.value;
  }

  set(v: T): void {
    this.value = v;
  }
}

const intContainer = new Container<number>(100);
const strContainer = new Container<string>("hi");
console.log(intContainer.get());  // 100
console.log(strContainer.get());  // hi

intContainer.set(200);
console.log(intContainer.get());  // 200

// Every distinct concrete type gets its own independent specialization — a
// second Container<number> doesn't interfere with the first.
const anotherIntContainer = new Container<number>(1);
console.log(anotherIntContainer.get());  // 1
console.log(intContainer.get());         // 200 (unchanged)

// ── Object type arguments (TDD-00069): a generic over a record type ──────────
interface Point { x: number; y: number; }

function identity<T>(x: T): T { return x; }
function first<T>(xs: T[]): T { return xs[0]; }

const p: Point = { x: 3, y: 4 };
console.log(identity(p).x);      // 3

const pts: Point[] = [{ x: 1, y: 2 }, { x: 5, y: 6 }];
console.log(first(pts).y);       // 2

const boxedPoint = new Container<Point>({ x: 10, y: 20 });
console.log(boxedPoint.get().x); // 10

// ── Constrained type parameters (TDD-00113): `<T extends X>` ─────────────────
// The constraint is enforced at the call/construction site, and the constrained
// shape's members are usable on a T-typed value.
interface HasId { id: number; }

function pluck<T extends HasId>(x: T): number { return x.id; }
console.log(pluck({ id: 42, name: "widget" }));  // 42

class Registry<T extends HasId> {
  last: T;
  constructor(seed: T) { this.last = seed; }
  lastId(): number { return this.last.id; }
}
const reg = new Registry<HasId>({ id: 7 });
console.log(reg.lastId());       // 7
