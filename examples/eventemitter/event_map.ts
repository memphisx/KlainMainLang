// Event-map EventEmitter (TDD-00097 Stage 7): one emitter, per-event payload
// types — including payload-less `void` events — declared as an object type
// argument, checked at compile time against string-literal event names.
class Downloader extends EventEmitter<{ progress: number; chunk: string; done: void; error: Error }> {
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
