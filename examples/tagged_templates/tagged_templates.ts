// --- A tagged template desugars to a plain call: tag(["a","b","c"], 1, 2) ---
function tag(strings: TemplateStringsArray, ...values: number[]): string {
    let result = strings[0];
    for (let i = 0; i < values.length; i++) {
        result += values[i];
        result += strings[i + 1];
    }
    return result;
}
console.log(tag`a${1}b${2}c`);  // a1b2c

// --- No interpolation at all ---
function plain(strings: TemplateStringsArray): string {
    return strings[0];
}
console.log(plain`hello world`);  // hello world

// --- Interpolated values reach the tag function as real typed values, not
// implicitly stringified the way a plain (un-tagged) template literal's own
// interpolation already is ---
function sumTag(strings: TemplateStringsArray, ...values: number[]): number {
    let s = 0;
    for (const v of values) { s += v; }
    return s;
}
console.log(sumTag`x${10}y${20}z${12}`);  // 42

// --- A class method works as a tag too ---
class Fmt {
    build(strings: TemplateStringsArray): string {
        return strings[0];
    }
}
const f = new Fmt();
console.log(f.build`hi`);  // hi

// --- So does an arrow function ---
const count = (strings: TemplateStringsArray, ...values: number[]): number => strings.length + values.length;
console.log(count`x${1}y${2}z`);  // 5

// --- The tag function's own signature drives normal call-argument
// coercion/type-checking, same as a hand-written call would ---
function unannotated(strings: TemplateStringsArray, v: number) { return strings[0] + v; }
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

// --- A generic tag takes explicit type arguments, as a call does ---
function joinTag<T>(strings: TemplateStringsArray, ...values: T[]): string {
    return strings.join("|") + ":" + values.join(",");
}
console.log(joinTag<number>`a${1}b${2}c`);  // a|b|c:1,2

// --- String.raw (ADR-00562): the raw, undecoded quasi text is preserved, so
// escape sequences appear verbatim rather than being interpreted.
console.log(String.raw`a\nb`);             // a\nb  (not a real newline)
console.log(String.raw`C:\path\to\file`);  // C:\path\to\file
const port = 8080;
console.log(String.raw`http://localhost:${port}\api`);  // http://localhost:8080\api

// --- Scope note (see docs/tdd/TDD-00059.md): a user-defined tag function
// can't read the `strings` array's `.raw` property yet — this compiler's
// arrays are fixed-shape with no room for an extra property; only the
// built-in `String.raw` tag exposes raw text (via a dedicated codegen path,
// ADR-00562).
