// Worker module for top_level_await_worker.ts — compiled into that example's
// binary via new Worker('./await_worker.ts'), never as a standalone entry.
//
// A worker module with a top-level `await` runs as a coroutine on the worker's
// own thread (TDD-00224 Stage 2), exactly like the entry program: the code
// after `await p` is a reaction on p, ordered among p's other reactions.
import { parentPort } from 'worker_threads';

function later(ms: number, value: number): Promise<number> {
  return new Promise<number>((resolve) => setTimeout(() => resolve(value), ms));
}

const warmup = later(10, 40);
warmup.then((v) => { console.log("worker: then-before " + v); });
const base = await warmup;
console.log("worker: after await " + base);
warmup.then(() => { console.log("worker: then-after"); });

// A second await; the worker's own timers keep running in between.
const extra = await later(5, 2);
parentPort.postMessage(base + extra);
