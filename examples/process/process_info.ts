// process introspection, scheduling, env, and lifecycle.
console.log("platform:", process.platform, "arch:", process.arch);
console.log("pid:", process.pid > 0);
console.log("execPath is absolute:", process.execPath.startsWith("/"));
console.log("node compat:", process.version, "klain:", process.versions.klain);

// emitWarning writes Node's `(node:<pid>) <type>: <message>` line to stderr.
process.emitWarning("this is a demo warning");
process.emitWarning("legacy call site", "DeprecationWarning");

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

// memoryUsage() returns Node's shape; rss is a real value (peak resident set,
// from getrusage). The V8-heap fields have no native analogue and report 0.
const mem = process.memoryUsage();
console.log("rss > 0:", mem.rss > 0, "heapUsed:", mem.heapUsed);

process.on('exit', (code: number) => {
  console.log("exiting with code", code);
});
process.exitCode = 0;
console.log("main finished");
