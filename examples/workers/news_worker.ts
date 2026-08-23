// Worker module for broadcast.ts — subscribes to the 'news' channel.
import { parentPort } from 'worker_threads';
const bc = new BroadcastChannel('news');
bc.onmessage = (e: { data: string }) => {
  parentPort.postMessage("subscriber read: " + e.data);
  bc.close();
};
parentPort.on('message', (go: number) => {});
