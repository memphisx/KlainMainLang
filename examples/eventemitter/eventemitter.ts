// EventEmitter (TDD-00023): a class extending EventEmitter gets a
// real on/once/emit/off/removeListener/removeAllListeners/listenerCount/
// eventNames surface for free, plus a standalone composed emitter for code
// that doesn't want (or can't have, given single inheritance) its own class
// hierarchy rooted in EventEmitter. With no type argument, an event takes
// any arguments, as in @types/node's default event map.

import { EventEmitter } from 'events';

class Downloader extends EventEmitter {
  name: string;
  bytesDone: number;

  constructor(name: string) {
    super();
    this.name = name;
    this.bytesDone = 0;
  }

  progress(chunk: number): void {
    this.bytesDone = this.bytesDone + chunk;
    this.emit("progress", this.name + ": " + this.bytesDone + " bytes");
  }

  finish(): void {
    this.emit("complete", this.name);
  }
}

const dl = new Downloader("archive.zip");
dl.on("progress", (msg: string): void => {
  console.log(msg);
});
dl.once("complete", (name: string): void => {
  console.log("done: " + name);
});

dl.progress(1024);   // archive.zip: 1024 bytes
dl.progress(2048);   // archive.zip: 3072 bytes
dl.finish();          // done: archive.zip
dl.finish();          // (once listener already fired — no further output)

// Unlistened 'error' events throw, matching real Node's one specially
// treated event name: an Error argument is thrown as is, anything else is
// wrapped in "Unhandled error. (…)".
try {
  dl.emit("error", "disk full");
} catch (e) {
  console.log("caught: " + (e as Error).message); // caught: Unhandled error. ('disk full')
}

// A standalone, composed EventEmitter — for code that doesn't want its own
// class hierarchy rooted in EventEmitter (single inheritance means a class
// can extend EventEmitter XOR some other base, not both).
const bus = new EventEmitter();
const onTick = (n: number): void => {
  console.log("tick: " + n);
};
bus.on("tick", onTick);
bus.emit("tick", 1);   // tick: 1
bus.off("tick", onTick);
bus.emit("tick", 2);   // (no listener left — no output)

console.log(bus.listenerCount("tick")); // 0
console.log(dl.listenerCount("progress")); // 1
