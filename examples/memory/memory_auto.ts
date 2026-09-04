// -mm=auto — compiler-inserted frees (TDD-00173), the third memory mode
// alongside "manual" (memory_free.ts) and "gc" (memory_gc.ts). Compile with:
//
//   klainmain -mm=auto examples/memory/memory_auto.ts
//
// Three layers, from most to least explicit:
//
//   /** @free */   — free this binding at every exit of its declaring block
//                    (fall-through, return, break, continue). A conservative
//                    escape check must PROVE the value never leaves the block
//                    (not returned, stored, passed to a retaining call, or
//                    captured by a closure) — an annotation it can't prove is
//                    a compile error, never a silent no-op.
//   /** @owned */  — free at the value's statically-determined *last use*
//                    instead of block exit. On a parameter (function-level
//                    `@owned name` tag), the callee frees its argument, and
//                    every call site must pass a value the caller provably
//                    no longer needs.
//   (nothing)      — under -mm=auto only, every unannotated local that
//                    passes the same escape check is freed at block exit
//                    automatically. Values the analysis can't prove safe
//                    simply leak, exactly as all values do in manual mode.
//
// The annotations are honored in every mode — they are compiler-checked,
// compiler-placed Memory.free calls — so this file also compiles and runs
// identically under the default manual mode (that's what `make examples`
// does). Under -mm=auto, Memory.free itself becomes a compile error: the
// compiler owns every free there.

// @free: the throwaway buffer is freed at each loop iteration's end —
// including iterations that leave via `continue`.
let total = 0;
for (let i = 0; i < 10000; i++) {
  /** @free */ let chunk: number[] = [i, i * 2, i * 3];
  if (i % 2 === 0) { continue; }
  total = total + chunk[2];
}
console.log("total:", total);

// @owned on a parameter: transform() takes ownership of its argument and
// frees it right after the last statement that uses it — the caller hands
// over a fresh array and never touches it again.
/** @owned input */
function transform(input: number[]): number {
  const doubled = input[0] * 2; // last use of input — freed right after
  console.log("transforming");
  return doubled;
}
const result = transform([21]);
console.log("doubled:", result);

// @owned on a local: freed immediately after its last use (the .length
// read), not at the end of the program.
/** @owned */ let label: string = "run-" + total;
console.log("label length:", label.length);

// Unannotated: under -mm=auto this array is freed automatically at the end
// of the program (the escape check proves it never leaves), with zero
// annotations. Under manual mode it leaks — same output either way.
let plain: number[] = [1, 2, 3, 4];
console.log("plain sum:", plain[0] + plain[3]);
