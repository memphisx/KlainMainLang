// fs.watch — native, event-driven file-change watching folded into the event
// loop. The OS change source is native per platform (Linux inotify, macOS
// kqueue/EVFILT_VNODE, Windows ReadDirectoryChangesW) — no stat-polling.
//
// fs.watch(path, listener) returns an FSWatcher that fires (eventType,
// filename) on each change; .close() stops watching and drops the keepalive so
// the program can exit.

import fs from "fs";

const path = "__demo_watch.txt";
fs.writeFileSync(path, "initial");

const watcher = fs.watch(path, (eventType: string, filename: string) => {
  console.log("watch fired:", eventType);
  watcher.close();
  fs.unlinkSync(path);
  console.log("watcher closed, program can now exit");
});

// Mutate the file on a later loop turn so the watcher observes a real change.
setTimeout(() => {
  fs.writeFileSync(path, "changed");
}, 100);
