// A worker whose module has a top-level `await` (see await_worker.ts): the
// worker evaluates its top level as a coroutine on its own thread, the parent
// receives the result when the worker's awaits have run, and an uncaught
// throw in a worker reaches the parent's 'error' listener as the Error object.
import { Worker } from 'worker_threads';

const w = new Worker('./await_worker.ts');
w.on('message', (n: number) => { console.log("parent: got " + n); });
w.on('error', (e: Error) => { console.log("parent: worker failed: " + e.name + ": " + e.message); });
w.on('exit', (code: number) => { console.log("parent: worker exited " + code); });
