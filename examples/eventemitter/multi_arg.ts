// Node's multi-argument events (TDD-00131): an event whose payload type is a
// tuple `[A, B, …]` emits and listens with one argument per element — exactly
// like real Node's `emit('data', chunk, size)` / `on('data', (chunk, size) => …)`.
// This file runs the same way under Node.js.

class Download extends EventEmitter<{
  progress: [number, number];
  done: [string];
  error: void;
}> {
  run(): void {
    this.emit("progress", 512, 1024);
    this.emit("progress", 1024, 1024);
    this.emit("done", "/tmp/file.bin");
  }
}

const d = new Download();
d.on("progress", (loaded: number, total: number) => {
  console.log("progress: " + loaded + "/" + total);
});
d.on("done", (path: string) => {
  console.log("saved to " + path);
});
d.run();
