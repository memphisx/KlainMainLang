// Worker module for shared_counter.ts — bumps the shared counter with
// Atomics and wakes the main thread's waiter when done.
import { parentPort, workerData } from 'worker_threads';
const sab: SharedArrayBuffer = workerData;
parentPort.on('message', (rounds: number) => {
  const cells = new Int32Array(sab);
  for (let i = 0; i < rounds; i++) {
    Atomics.add(cells, 0, 1);
  }
  Atomics.store(cells, 1, 1);
  Atomics.notify(cells, 1);
  parentPort.postMessage(1);
});
