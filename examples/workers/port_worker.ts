// Worker module for port_pair.ts — talks over a MessagePort instead of
// parentPort.
import { parentPort, workerData } from 'worker_threads';
const port: MessagePort<string> = workerData;
port.onmessage = (e: { data: string }) => {
  port.postMessage(e.data.toUpperCase());
};
parentPort.on('message', (go: number) => {});
