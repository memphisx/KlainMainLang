// BroadcastChannel: name-keyed pub/sub across threads. Every subscriber
// gets its own deep-copied delivery.
import { Worker } from 'worker_threads';

const w = new Worker('./news_worker.ts');
w.on('message', (line: string) => {
  console.log(line);
  w.terminate();
});

const bc = new BroadcastChannel('news');
setTimeout(() => { bc.postMessage("markets up in Thessaloniki"); }, 100);
