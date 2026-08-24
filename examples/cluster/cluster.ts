import cluster from 'cluster';

// The Node cluster idiom: the primary forks a pool of workers, each of which
// re-runs this program from the top with cluster.isWorker true. Here each
// worker does a small unit of work and exits; the primary waits for them all
// (so it stays alive while they run) and then exits — a self-contained demo.
// A real service would have each worker call http.listen(port) instead, all
// sharing the port via SO_REUSEPORT.
if (cluster.isPrimary) {
  console.log("primary: forking 3 workers");
  for (let i = 0; i < 3; i++) {
    const w = cluster.fork();
    console.log("primary: started worker " + w.id);
  }
} else {
  console.log("worker " + cluster.workerId + ": doing work, then exiting");
}
