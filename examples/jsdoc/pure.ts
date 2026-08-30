// The `/** @pure */` annotation (TDD-00128) asserts a function is
// side-effect-free and referentially transparent, and the compiler ENFORCES it
// with a front-end pass — zero runtime/codegen cost, byte-identical output to an
// untagged function. Inside a @pure function the compiler rejects: mutating a
// parameter (or anything reachable from it), mutating a captured/global binding,
// I/O (console/fs/fetch/…), nondeterminism (Math.random/Date.now/new Date()),
// and calling a function that is not itself @pure (purity is contagious).
//
// It applies to a declaration, an arrow binding, and a function-expression
// binding — including capturing closures.

/** @pure */
function square(n: number): number {
  return n * n;
}

/** @pure */
function scale(n: number, by: number): number {
  return n * by;
}

// An arrow binding gets the same enforcement.
/** @pure */
const cube = (n: number): number => n * n * n;

// Local mutation is fine — only *observable* effects are constrained. This
// builds and mutates its own local, and returns a fresh array; it touches
// nothing the caller can see.
/** @pure */
function scaledSquares(xs: number[], by: number): number[] {
  const out: number[] = [];
  for (const x of xs) {
    out.push(scale(square(x), by)); // out is local — mutating it is allowed
  }
  return out;
}

// Purity is contagious: scaledSquares may call square/scale only because they
// are themselves @pure.
console.log(square(6)); // 36
console.log(scale(7, 3)); // 21
console.log(cube(4)); // 64
console.log(scaledSquares([1, 2, 3], 10).join(",")); // 10,40,90

// Each of these, uncommented inside a @pure function, is a COMPILE ERROR:
//
//   /** @pure */
//   function bad(xs: number[]): number {
//     xs.push(1);            // mutating method on a parameter
//     console.log("hi");     // I/O
//     return Math.random();  // nondeterminism
//   }
