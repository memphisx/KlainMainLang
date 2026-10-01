// MessageChannel: a port pair; port2 moves to the worker (in the transfer
// list, with workerData naming it) and the two sides talk directly over it.
import { Worker, MessageChannel } from 'worker_threads';

const ch = new MessageChannel();
const w = new Worker('./port_worker.ts', { workerData: ch.port2, transferList: [ch.port2] });

ch.port1.onmessage = (e: MessageEvent) => {
  console.log("shouted back: " + e.data);
  ch.port1.close();
  w.terminate();
};
setTimeout(() => { ch.port1.postMessage("hello ports"); }, 100);
