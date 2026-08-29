// Object-type method signatures and bare call signatures. A method
// signature `name(params): R` is TS shorthand for a function-typed
// property; a lone call signature `{ (n: number): string }` is the
// callable-object form of a plain function type.

type Ops = {
    double(n: number): number;
    describe(s: string): string;
};

function useOps(ops: Ops): string {
    return ops.describe("result") + String(ops.double(21));
}

function apply(fn: { (n: number): string }, n: number): string {
    return fn(n);
}

console.log(useOps({
    double: (n: number): number => n * 2,
    describe: (s: string): string => s + ": ",
}));
console.log(apply((n: number): string => "applied to " + String(n), 7));
