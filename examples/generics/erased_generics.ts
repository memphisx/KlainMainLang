// User-defined generics, V2 (TDD-00010): `/** @erased */` opts a generic
// function out of V1's default monomorphization (one specialized copy per
// concrete type) into type erasure instead — the function's body is
// compiled exactly once, with every bare-T parameter/return position
// treated as `any`/`unknown` under the hood (the same boxed-value machinery
// `any`/`unknown` already use elsewhere). Best suited to pass-through
// generics — containers and identity-shaped functions that store/retrieve
// T without doing arithmetic on it; see the file's last section for why.

/** @erased */
function identity<T>(x: T): T {
  return x;
}

console.log(identity(42));      // 42
console.log(identity("hello")); // hello
console.log(identity(true));    // true

// A mixed signature — some parameters concrete, some erased — works the
// same way; only the T-typed positions are erased.
/** @erased */
function labeled<T>(label: string, x: T): T {
  console.log(label);
  return x;
}

console.log(labeled("num:", 7));
console.log(labeled("str:", "seven"));

// Unlike V1, calling an `@erased` function with several different concrete
// types never generates several specialized copies — there's exactly one
// compiled `identity`/`labeled`, regardless of how many distinct types call
// it. That's the real tradeoff V2 makes: no monomorphization blowup, but a
// small per-call boxing/unboxing cost, and only "pass-through" bodies are
// supported — `T[]` positions and arithmetic on `T` are both out of scope
// (a clean compile error, not a silent miscompile): the commented-out line
// below would fail to compile.
//
// /** @erased */
// function add<T>(a: T, b: T): T { return a + b; }

// Explicit call-site type arguments pick the instantiation directly —
// the way to call a generic with nothing to infer from.
function emptyOf<T>(): T[] { return []; }
const nums = emptyOf<number>();
console.log(nums.length); // 0
