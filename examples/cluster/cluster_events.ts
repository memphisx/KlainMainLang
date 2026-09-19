import cluster from 'cluster';

// Cluster-level events: instead of wiring listeners onto each Worker handle,
// register once on the cluster module — every listener receives the Worker
// as its first argument. 'fork' fires synchronously inside cluster.fork(),
// 'online' from a queued microtask, and 'message'/'exit' as each worker's
// IPC/reap path reports. cluster.workers lists every forked Worker handle.
if (cluster.isPrimary) {
  cluster.on('fork', (w) => { console.log("forked worker " + w.id); });
  cluster.on('online', (w) => { console.log("worker " + w.id + " online"); });
  cluster.on('message', (w, msg: string) => {
    console.log("worker " + w.id + " says: " + msg);
    w.send("stop");
  });
  cluster.on('exit', (w, code) => { console.log("worker " + w.id + " exited " + code); });

  cluster.fork();
  console.log("pool size: " + cluster.workers.length);
  for (const w of cluster.workers) {
    console.log("registered: worker " + w.id);
  }
} else {
  process.send("ready");
  process.on('message', (msg) => {
    if (msg === "stop") { process.exit(0); }
  });
}
