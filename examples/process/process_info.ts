// process introspection, scheduling, env, and lifecycle.
console.log("platform:", process.platform, "arch:", process.arch);
console.log("pid:", process.pid > 0);

process.env.GREETING = "hi";
console.log("env:", process.env.GREETING);

process.nextTick(() => {
  console.log("nextTick ran after synchronous code");
});

const start = process.hrtime.bigint();
let sum = 0;
for (let i = 0; i < 1000; i++) sum += i;
const elapsed = process.hrtime.bigint() - start;
console.log("did work, elapsed ns >= 0:", elapsed >= 0n);
console.log("uptime >= 0:", process.uptime() >= 0);

process.on('exit', (code: number) => {
  console.log("exiting with code", code);
});
process.exitCode = 0;
console.log("main finished");
