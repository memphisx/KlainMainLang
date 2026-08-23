// The browser Worker surface: ambient `new Worker` (no import), handler
// properties, and MessageEvent-style `.data` payloads.
const w = new Worker('./shout_worker.ts');
w.onmessage = (e) => {
  console.log("worker says: " + e.data);
  w.terminate();
};
w.postMessage("hello from Thessaloniki");
