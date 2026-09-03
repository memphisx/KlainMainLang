// Vanilla, untyped JavaScript compiled natively under -compat=js: a variable
// that holds different types over its lifetime — a plain "dynamic variable" in
// JS — is backed by the NaN-boxed any value, so reassigning it across number,
// string, and boolean just works. (In the strict default lane this is a clean
// compile error, since there a binding's type is fixed at its declaration.)
// A variable assigned only one kind stays a plain unboxed value, so ordinary
// numeric code keeps its speed. Run with:  klainmain -compat=js dynamic_variable.js

// A binding reassigned across scalar kinds becomes dynamic.
let value = 42;
console.log(typeof value, value);   // number 42

value = "now a string";
console.log(typeof value, value);   // string now a string

value = true;
console.log(typeof value, value);   // boolean true

// A dynamic module-level variable is visible to a named function too.
let state = 0;
function report() {
  console.log("state is", state);
}
report();                            // state is 0
state = "ready";
report();                            // state is ready

// A single-kind binding is NOT widened — it stays a fast unboxed number.
let total = 0;
for (let i = 1; i <= 5; i++) {
  total = total + i;
}
console.log("total", total);         // total 15
