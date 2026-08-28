// `typeof value` derives a type from an existing value without restating its
// shape (ADR-00389).

// From an object value — reuse its shape as a named type.
const config = { host: "localhost", port: 8080 };
type Config = typeof config;
const staging: Config = { host: "staging", port: 9090 };
console.log(staging.port); // 9090

// Inline, on a scalar — the new binding takes the value's type.
const defaultRetries = 3;
let retries: typeof defaultRetries = 5;
console.log(retries); // 5

// As a parameter type — accept anything shaped like `settings`.
const settings = { name: "svc", verbose: true };
function describe(s: typeof settings): string {
  return s.name;
}
console.log(describe({ name: "worker", verbose: false })); // worker

// From a function — `typeof fn` is the function's type.
function triple(n: number): number {
  return n * 3;
}
let op: typeof triple = triple;
console.log(op(4)); // 12
