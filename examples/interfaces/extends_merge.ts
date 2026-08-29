// `interface C extends A, B` merges the base interfaces' fields and method
// signatures into C (own declarations win on a name collision).
interface Named { name: string; }
interface Aged { age: number; }
interface Person extends Named, Aged { city: string; }

const p: Person = { name: "Kyriakos", age: 40, city: "Thessaloniki" };
console.log(p.name + " (" + String(p.age) + ") from " + p.city);

// Callable interface: a lone call signature is the function type itself.
interface Formatter {
    (n: number): string;
}
function format(f: Formatter, n: number): string { return f(n); }
console.log(format((n: number): string => "#" + String(n), 9));

// Declaration merging: an interface may share a class's name (class wins).
interface Widget { hint: string; }
class Widget { id: number = 1; }
console.log(new Widget().id);

// interface + interface declaration merging: members union.
interface Options { verbose: boolean; }
interface Options { retries: number; }
const opts: Options = { verbose: true, retries: 2 };
console.log(opts.verbose, opts.retries);
