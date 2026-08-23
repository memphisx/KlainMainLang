// MessageChannel: a typed port pair; port2 crosses to the worker as
// workerData (shared by reference, like a SharedArrayBuffer) and the two
// sides talk directly over it.
import { Worker } from 'worker_threads';

const ch = new MessageChannel<string>();
const w = new Worker('./port_worker.ts', { workerData: ch.port2 });

ch.port1.onmessage = (e: { data: string }) => {
  console.log("shouted back: " + e.data);
  ch.port1.close();
  ch.port2.close();
  w.terminate();
};
setTimeout(() => { ch.port1.postMessage("hello ports"); }, 100);
