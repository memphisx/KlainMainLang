// Optional call `f?.(...)`: a nullish callee short-circuits the whole call to
// `undefined` — the arguments are not evaluated either.

function run(label: string, cb?: (n: number) => number): void {
  const r = cb?.(21);
  if (r === undefined) {
    console.log(label, "-> undefined");
  } else {
    console.log(label, "->", r);
  }
}

run("with callback", (n) => n * 2);
run("without callback");

// The arguments of a skipped call are not evaluated.
let evaluated = 0;
function arg(): number {
  evaluated++;
  return 1;
}
function maybe(cb?: (n: number) => void): void {
  cb?.(arg());
}
maybe();
maybe((n) => console.log("called with", n));
console.log("argument evaluations:", evaluated);

// A function-typed field.
interface Hooks {
  onDone?: (msg: string) => void;
}
const quiet: Hooks = {};
const loud: Hooks = { onDone: (msg) => console.log("done:", msg) };
quiet.onDone?.("never printed");
loud.onDone?.("printed");

// A declared function is never nullish: `?.()` is an ordinary call.
function always(): string {
  return "always";
}
console.log(always?.());

// Optional element access, and a chain short-circuiting as a whole: with a
// nullish `r`, neither `.tags`, `[0]` nor `.toUpperCase()` runs.
interface Row {
  name: string;
  tags: string[];
}
function describe(r?: Row): void {
  console.log(r?.tags?.[0], r?.name.toUpperCase().length, r?.name.length ?? "no row");
}
describe({ name: "abc", tags: ["x", "y"] });
describe();

// Node defines process.getuid only on POSIX; on Windows it is `undefined`, so
// the portable spelling is the optional call.
const uid = process.getuid?.();
console.log("uid is", uid === undefined ? "undefined (Windows)" : "a number");
