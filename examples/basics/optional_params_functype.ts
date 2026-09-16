// Optional-parameter markers (`x?: T`) in a function *type* annotation and on
// an arrow-function expression's parameters (ADR-00963). An omitted argument is
// a real `undefined`, so the body reads the parameter as `T | undefined`
// (TDD-00187) — narrow it or supply a fallback with `??`.

// A function type may mark parameters optional. A binding of that type holds an
// arrow whose parameters carry the same markers, so the two agree.
type Cb = (x?: number, y?: string) => void;
const log: Cb = (x?: number, y?: string) => { console.log(x ?? -1, y ?? "none"); };
log(5, "hi");   // 5 hi
log(3);         // 3 none  (y omitted)
log();          // -1 none  (both omitted)

// An arrow-function expression with an optional first parameter is recognized
// as an arrow (not a parenthesized ternary), with or without a return type.
const orDefault = (x?: number): number => x ?? 0;
console.log(orDefault());   // 0
console.log(orDefault(7));  // 7

// A callback typed with optional parameters may be written more plainly at the
// call site — the plainer arrow still satisfies the optional-typed slot.
function run(fn: (a?: number) => number): number { return fn(10); }
console.log(run((a) => (a ?? 0) + 1));  // 11

// The skew also works the other way: an arrow may mark a parameter optional
// that its declared type left required. The closure adopts the slot's ABI
// contextually, so calls through the binding — or the argument slot — pass the
// value through correctly (ADR-00963).
type Strict = (x: number) => number;
const lenient: Strict = (x?: number): number => x ?? -1;
console.log(lenient(8));  // 8

function apply(fn: (a: number) => number): number { return fn(4); }
console.log(apply((a?: number) => (a ?? 0) + 100));  // 104
