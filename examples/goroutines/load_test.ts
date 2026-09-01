// klainload — a concurrent load tester with a live terminal dashboard.
//
// A closed-loop load generator, built the way you'd build one in Go. A pool of
// worker goroutines drives a stream of requests through an in-process service
// as fast as it can, and a live `klain:tui` dashboard shows throughput and the
// latency distribution as the run unfolds.
//
// The whole design is channels:
//
//   - a buffered `requests` channel is the work queue: a generator goroutine
//     fills it and closes it when the traffic burst is done (natural
//     backpressure — the generator blocks when the buffer is full);
//   - CONCURRENCY worker goroutines RANGE that channel, timing each request and
//     streaming its latency back over a `results` channel;
//   - a `done` channel reports each worker's completion;
//   - the collector loop on the main thread uses `select` to fold results and
//     completions into a live histogram, then repaints the dashboard.
//
// Because the scheduler is preemptive and runs on every core, the workers peg
// the CPU in parallel while the dashboard stays fluid — a single native binary,
// no runtime, no event loop.
//
//   ./load_test              # run the load test with a live dashboard
//   ./load_test </dev/null   # non-TTY: run headless, print a final report
import { Box, Text, Progress, render, enter, leave } from "klain:tui";
import { go, Channel, select, defaultCase } from "klain:sync";
import { readKey } from "klain:tty";

// --- knobs ------------------------------------------------------------------

const CONCURRENCY = 8;     // worker goroutines (the load level, like wrk's -c)
const TOTAL = 450000;      // total requests to fire
const REQ_QUEUE = 2048;    // request-channel buffer

// --- the "service" under test ----------------------------------------------

// A deterministic pseudo-random hash of the request index — keeps every run
// reproducible while giving a realistic spread of request costs.
function hash(x: number): number {
  let h = (x ^ 0x9e3779b9) >>> 0;
  h = (h ^ (h >>> 16)) >>> 0;
  h = Math.imul(h, 0x45d9f3b) >>> 0;
  h = (h ^ (h >>> 16)) >>> 0;
  return h >>> 0;
}

// Model a request handler: a variable amount of CPU work per request, with an
// occasional heavy request so the latency distribution has a real tail (p99 ≫
// p50) — exactly what you want a load test to reveal.
function handle(id: number): number {
  const h = hash(id);
  let iters = 6000 + (h % 6000);      // base cost
  if (h % 101 === 0) iters += 90000;  // ~1% slow requests → the p99 tail
  let acc = 0;
  for (let i = 0; i < iters; i++) {
    acc = (acc + hash(i ^ h)) >>> 0;
  }
  return acc;
}

// --- shared channels (module globals, so every goroutine sees them typed) ----

const requests = new Channel<number>(REQ_QUEUE);
const results = new Channel<number>(4096); // per-request latency in microseconds
const done = new Channel<number>(0);       // one signal per finished worker

// Generator: enqueue every request id, then close — the close is the workers'
// stop signal.
go(() => {
  for (let i = 0; i < TOTAL; i++) requests.send(i);
  requests.close();
});

// Workers: range the queue, time each request, stream the latency out.
function spawnWorker(): void {
  go(() => {
    let acc = 0; // folds every handler result so the work can't be optimised away
    for (const id of requests) {
      const t0 = performance.now();
      acc = (acc + handle(id)) >>> 0;
      const micros = Math.round((performance.now() - t0) * 1000);
      results.send(micros);
    }
    done.send(acc | 1); // the value is ignored; sending it just keeps `acc` live
  });
}
for (let w = 0; w < CONCURRENCY; w++) spawnWorker();

// --- metrics: a log-bucketed latency histogram -----------------------------

const NBUCKETS = 28; // bucket b holds latencies in [2^b, 2^(b+1)) microseconds
const hist: number[] = [];
for (let i = 0; i < NBUCKETS; i++) hist.push(0);

let completed = 0;
let maxMicros = 0;

function bucketOf(micros: number): number {
  let b = 0;
  let v = micros;
  while (v > 1 && b < NBUCKETS - 1) { v = Math.floor(v / 2); b += 1; }
  return b;
}
function record(micros: number): void {
  hist[bucketOf(micros)] += 1;
  completed += 1;
  if (micros > maxMicros) maxMicros = micros;
}

// The pth-percentile latency, read off the cumulative histogram (2^b lower edge).
function pct(p: number): number {
  const target = p * completed;
  let cum = 0;
  for (let b = 0; b < NBUCKETS; b++) {
    cum += hist[b];
    if (cum >= target) {
      let v = 1;
      for (let i = 0; i < b; i++) v *= 2;
      return v;
    }
  }
  return maxMicros;
}

function fmtMicros(us: number): string {
  if (us >= 1000) return (us / 1000).toFixed(1) + "ms";
  return us + "µs";
}

// --- throughput sparkline ---------------------------------------------------

// Indexed as whole strings (not charAt on a multi-byte string, which would
// slice a single UTF-8 byte out of a 3-byte block glyph).
const sparkChars = ["▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"];
const rpsSamples: number[] = [];
let lastCompleted = 0;
let lastStamp = 0;
let peakRps = 0;

function sampleRps(now: number): void {
  const dt = now - lastStamp;
  if (dt <= 0) return;
  const rps = Math.round(((completed - lastCompleted) * 1000) / dt);
  rpsSamples.push(rps);
  if (rpsSamples.length > 32) rpsSamples.shift();
  if (rps > peakRps) peakRps = rps;
  lastCompleted = completed;
  lastStamp = now;
}
function sparkline(): string {
  if (rpsSamples.length === 0) return "";
  let hi = 1;
  for (let i = 0; i < rpsSamples.length; i++) if (rpsSamples[i] > hi) hi = rpsSamples[i];
  let out = "";
  for (let i = 0; i < rpsSamples.length; i++) {
    let idx = Math.floor((rpsSamples[i] * 7) / hi);
    if (idx < 0) idx = 0;
    if (idx > 7) idx = 7;
    out += sparkChars[idx];
  }
  return out;
}

// --- view -------------------------------------------------------------------

// klain:tui colors must be string literals, so these helpers branch on a color
// key rather than passing a variable through to Text/Progress.
function valueText(value: string, color: string) {
  if (color === "cyan") return Text(value, { color: "cyan", bold: true });
  if (color === "green") return Text(value, { color: "green", bold: true });
  if (color === "yellow") return Text(value, { color: "yellow", bold: true });
  if (color === "red") return Text(value, { color: "red", bold: true });
  return Text(value, { color: "white", bold: true });
}
function stat(label: string, value: string, color: string) {
  return Box({ flexDirection: "column", width: 18 }, [
    Text(label, { color: "gray", dim: true }),
    valueText(value, color),
  ]);
}

function histRow(label: string, count: number, frac: number, color: string) {
  let bar = Progress(frac, { color: "green", width: 22 });
  if (color === "yellow") bar = Progress(frac, { color: "yellow", width: 22 });
  if (color === "red") bar = Progress(frac, { color: "red", width: 22 });
  return Box({ flexDirection: "row", gap: 1 }, [
    Text(label, { color: "gray", width: 8 }),
    bar,
    Text(count + "", { color: "gray", width: 8 }),
  ]);
}

function view(elapsedMs: number, finished: number) {
  const rps = elapsedMs > 0 ? Math.round((completed * 1000) / elapsedMs) : 0;
  const prog = TOTAL > 0 ? (completed * 1.0) / TOTAL : 0;

  // Aggregate the fine buckets into a few readable latency bands.
  const bands = [
    { label: "<128µs", lo: 0, hi: 7, color: "green" },
    { label: "<512µs", lo: 7, hi: 9, color: "green" },
    { label: "<2ms", lo: 9, hi: 11, color: "yellow" },
    { label: "<8ms", lo: 11, hi: 13, color: "yellow" },
    { label: "≥8ms", lo: 13, hi: NBUCKETS, color: "red" },
  ];
  const rows = bands.map((b) => {
    let c = 0;
    for (let k = b.lo; k < b.hi; k++) c += hist[k];
    const frac = completed > 0 ? (c * 1.0) / completed : 0;
    return histRow(b.label, c, frac, b.color);
  });

  const header = Box({ flexDirection: "row", justifyContent: "space-between" }, [
    Text("klainload", { color: "green", bold: true }),
    Text(CONCURRENCY + " workers · " + TOTAL + " reqs", { color: "gray", dim: true }),
  ]);

  const progressBar = Box({ flexDirection: "row", gap: 1 }, [
    Progress(prog, { color: "cyan", width: 34 }),
    Text(Math.round(prog * 100) + "%", { color: "cyan", width: 5 }),
  ]);

  const statsRow = Box({ flexDirection: "row", gap: 2 }, [
    stat("throughput", rps + " rps", "cyan"),
    stat("completed", completed + " / " + TOTAL, "white"),
    stat("elapsed", (elapsedMs / 1000).toFixed(1) + "s", "white"),
  ]);
  const latRow = Box({ flexDirection: "row", gap: 2 }, [
    stat("p50", fmtMicros(pct(0.5)), "green"),
    stat("p90", fmtMicros(pct(0.9)), "yellow"),
    stat("p99", fmtMicros(pct(0.99)), "red"),
  ]);

  const sparkRow = Box({ flexDirection: "row", gap: 1 }, [
    Text("rps", { color: "gray", width: 4 }),
    Text(sparkline(), { color: "cyan" }),
    Text("peak " + peakRps, { color: "gray", dim: true }),
  ]);

  const footer = finished >= CONCURRENCY
    ? Text("done — " + completed + " requests · press any key to exit", { color: "green", bold: true })
    : Text("load test running…", { color: "gray", dim: true });

  const children = [
    header,
    Box({ height: 1 }, []),
    progressBar,
    Box({ height: 1 }, []),
    statsRow,
    Box({ height: 1 }, []),
    latRow,
    Box({ height: 1 }, []),
    sparkRow,
    Box({ height: 1 }, []),
    Text("latency distribution", { color: "cyan", bold: true }),
  ];
  for (let i = 0; i < rows.length; i++) children.push(rows[i]);
  children.push(Box({ height: 1 }, []));
  children.push(footer);

  return Box(
    { flexDirection: "column", width: 52, border: "round", borderColor: "cyan", padding: 1 },
    children,
  );
}

// --- collector loop ---------------------------------------------------------

// These are declared as literal-initialised `let`s (so they promote to module
// globals the collector functions can see) and then assigned their real values.
let isTTY = false;
isTTY = process.stdin.isTTY;
let start = 0;
start = performance.now();
lastStamp = start;

if (isTTY) enter();

// Repaint roughly every FRAME requests so the dashboard updates smoothly
// regardless of throughput.
let FRAME = 1;
FRAME = Math.floor(TOTAL / 240) + 1;
let finished = 0;

function maybeRender(): void {
  const now = performance.now();
  sampleRps(now);
  if (isTTY) render(view(now - start, finished));
}

// select folds the two live channels — latencies and worker completions — into
// the running histogram; whichever is ready fires, so the loop never blocks on
// one while the other has data.
while (finished < CONCURRENCY) {
  select(
    results.recvCase((micros: number) => {
      record(micros);
      if (completed % FRAME === 0) maybeRender();
    }),
    done.recvCase((_: number) => { finished += 1; }),
  );
}

// Drain any latencies still buffered behind the last completion signals — a
// non-blocking select with a default that stops the loop when nothing is left.
let draining = true;
while (draining) {
  select(
    results.recvCase((micros: number) => { record(micros); }),
    defaultCase(() => { draining = false; }),
  );
}

// Final frame.
const totalMs = performance.now() - start;
if (isTTY) {
  render(view(totalMs, CONCURRENCY));
  readKey(0);   // wait for a keystroke, then restore the screen
  leave();
} else {
  const rps = totalMs > 0 ? Math.round((completed * 1000) / totalMs) : 0;
  console.log(
    "klainload: " + completed + " requests · " + CONCURRENCY + " workers · " +
    (totalMs / 1000).toFixed(2) + "s · " + rps + " rps",
  );
  console.log(
    "  latency  p50 " + fmtMicros(pct(0.5)) + "  p90 " + fmtMicros(pct(0.9)) +
    "  p99 " + fmtMicros(pct(0.99)) + "  max " + fmtMicros(maxMicros),
  );
}
