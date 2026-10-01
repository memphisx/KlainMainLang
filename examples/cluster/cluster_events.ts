import cluster from 'cluster';

// Cluster-level events: instead of wiring listeners onto each Worker handle,
// register once on the cluster module — every listener receives the Worker
// as its first argument: 'fork' on the next tick after cluster.fork(),
// 'online' once the worker reports in, 'message' and 'exit' as each worker
// sends and ends. cluster.workers maps each live worker's id to its Worker.
if (cluster.isPrimary) {
  cluster.on('fork', (w) => { console.log("forked worker " + w.id); });
  cluster.on('online', (w) => { console.log("worker " + w.id + " online"); });
  cluster.on('message', (w, msg: string) => {
    console.log("worker " + w.id + " says: " + msg);
    w.send("stop");
  });
  cluster.on('exit', (w, code) => { console.log("worker " + w.id + " exited " + code); });

  cluster.fork();
  const workers = cluster.workers!;
  console.log("pool size: " + Object.keys(workers).length);
  for (const id in workers) {
    console.log("registered: worker " + workers[id]!.id);
  }
} else {
  process.send!("ready");
  process.on('message', (msg) => {
    if (msg === "stop") { process.exit(0); }
  });
}
