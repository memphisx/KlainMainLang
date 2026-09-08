// process.env and process.argv absences are real `string | undefined` values:
// a missing environment variable or an out-of-range argv index reads back as
// `undefined`, exactly as in Node. Strict mode requires narrowing, `??`, or
// `!` before using the result where a bare string is expected.

const missing = process.env.KLAIN_NO_SUCH_VAR;
console.log(missing);                         // undefined
console.log(missing === undefined);           // true
console.log(missing ?? "default-value");      // default-value

process.env.KLAIN_DEMO = "set-from-code";
console.log(process.env.KLAIN_DEMO);          // set-from-code

const home = process.env.HOME;
if (home !== undefined) {
  console.log(home.length > 0);               // true on any normal shell
}

console.log(process.argv[99]);                // undefined
console.log(process.argv[99] ?? "no-arg");    // no-arg
console.log(process.argv[99] === undefined);  // true

// The classic subcommand pattern keeps working — undefined is falsy and
// never string-equal to a real value.
if (!process.argv[2]) {
  console.log("no subcommand given");
}
