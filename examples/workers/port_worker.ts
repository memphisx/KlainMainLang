// Worker module for port_pair.ts — talks over a MessagePort instead of
// parentPort.
import { workerData } from 'worker_threads';
import type { MessagePort } from 'worker_threads';
const port: MessagePort = workerData;
port.onmessage = (e: MessageEvent) => {
  port.postMessage(String(e.data).toUpperCase());
};
