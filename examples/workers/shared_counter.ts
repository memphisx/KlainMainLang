// SharedArrayBuffer + Atomics: two threads bump one shared counter with
// Atomics.add (no messages carrying the value — the memory itself is
// shared), while the main thread blocks in Atomics.wait until the worker
// flips the done flag and notifies.
import { Worker } from 'worker_threads';

const sab = new SharedArrayBuffer(8); // [0] counter, [1] done flag
const cells = new Int32Array(sab);

const w = new Worker('./counter_worker.ts', { workerData: sab });
w.on('message', (ok: number) => { w.terminate(); });
w.postMessage(1000);

for (let i = 0; i < 1000; i++) {
  Atomics.add(cells, 0, 1);
}
const r: string = Atomics.wait(cells, 1, 0, 2000);
console.log("wait: " + r + ", counter: " + Atomics.load(cells, 0));
console.log("4-byte ops lock-free: " + Atomics.isLockFree(4));
