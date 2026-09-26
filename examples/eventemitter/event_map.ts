// Event-map EventEmitter (TDD-00097 Stage 7): one emitter, per-event argument
// tuples — `[]` for an event with no arguments — declared as an object type
// argument, @types/node's EventMap shape, checked at compile time against
// string-literal event names.
import { EventEmitter } from 'events';
class Downloader extends EventEmitter<{ progress: [pct: number]; chunk: [part: string]; done: []; error: [err: Error] }> {
  fetchAll(): void {
    this.emit("progress", 0);
    this.emit("chunk", "first part");
    this.emit("progress", 50);
    this.emit("chunk", "second part");
    this.emit("progress", 100);
    this.emit("done");
  }
}

const d = new Downloader();
let received = "";
d.on("progress", (pct) => { console.log("progress: " + pct + "%"); });
d.on("chunk", (part) => { received = received + part + " "; });
d.on("done", () => { console.log("received:", received.trim()); });
d.fetchAll();

console.log("is an EventEmitter:", d instanceof EventEmitter);
