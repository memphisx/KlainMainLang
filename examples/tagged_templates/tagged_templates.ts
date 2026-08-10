// --- A tagged template desugars to a plain call: tag(["a","b","c"], 1, 2) ---
function tag(strings: string[], ...values: number[]): string {
    let result = strings[0];
    for (let i = 0; i < values.length; i++) {
        result += values[i];
        result += strings[i + 1];
    }
    return result;
}
console.log(tag`a${1}b${2}c`);  // a1b2c

// --- No interpolation at all ---
function plain(strings: string[]): string {
    return strings[0];
}
console.log(plain`hello world`);  // hello world

// --- Interpolated values reach the tag function as real typed values, not
// implicitly stringified the way a plain (un-tagged) template literal's own
// interpolation already is ---
function sumTag(strings: string[], ...values: number[]): number {
    let s = 0;
    for (const v of values) { s += v; }
    return s;
}
console.log(sumTag`x${10}y${20}z${12}`);  // 42

// --- A class method works as a tag too, as long as it takes only the
// strings array (see the note at the bottom about array-typed closure
// parameters, which also rules out an arrow function as a tag) ---
class Fmt {
    build(strings: string[]): string {
        return strings[0];
    }
}
const f = new Fmt();
console.log(f.build`hi`);  // hi

// --- The tag function's own signature drives normal call-argument
// coercion/type-checking, same as a hand-written call would ---
function unannotated(strings: string[], v: number) { return strings[0] + v; }
const r = unannotated`v=${99}`;
console.log(r);  // v=99

// --- Closures over an enclosing scope still work — it's the *call to the
// tag function* that's sugar, not anything about the surrounding code ---
function make(): () => number {
    let base = 100;
    return (): number => sumTag`x${base}y`;
}
const closure = make();
console.log(closure());  // 100

// --- V1 scope notes (see docs/tdd/TDD-00059.md):
// - No `.raw` property on the `strings` array — this compiler's arrays are
//   fixed-shape with no room for an extra property, and real `String.raw`
//   was already scoped as separate, larger work (ADR-00028).
// - An arrow function can't be used as a tag: its first parameter (the
//   strings array) is unavoidably array-typed, and array-typed closure
//   parameters aren't supported at all yet — a broader, pre-existing gap,
//   not specific to tagged templates. A named function or a class method
//   (as long as it takes only the strings array) both work fine, since
//   neither compiles through the closure/env-struct machinery.
