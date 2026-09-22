// A module with a top-level `await` runs as a coroutine (TDD-00224), exactly as
// an engine evaluates it: the body runs synchronously up to its first await,
// and the code after `await p` is a reaction on p — ordered among p's other
// reactions by when each was registered.

const log: string[] = [];

function later(ms: number, value: string): Promise<string> {
  return new Promise<string>((resolve) => setTimeout(() => resolve(value), ms));
}

// 1. The continuation of `await p` runs after a `.then` registered before the
//    await, and before one registered after it.
const config = later(5, "ready");
config.then((v) => { log.push("then-before: " + v); });
const state = await config;
log.push("after await: " + state);
config.then(() => { log.push("then-after"); });

// 2. Awaiting a plain value or a settled promise still takes one tick.
Promise.resolve().then(() => { log.push("queued job"); });
await 0;
log.push("after tick");

// 3. `for await` at top level.
for await (const n of [later(1, "a"), later(1, "b")]) {
  log.push("for await: " + n);
}

// 4. Closures over top-level bindings keep working after the body has ended.
let beats = 0;
const timer = setInterval(() => {
  beats++;
  if (beats === 3) {
    clearInterval(timer);
    console.log(log.join("\n"));
    console.log("beats: " + beats);
  }
}, 2);
log.push("body done");
