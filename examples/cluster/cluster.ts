import cluster from 'cluster';

// The Node cluster idiom: the primary forks a pool of workers, each of which
// re-runs this program from the top with cluster.isWorker true. Here each
// worker does a small unit of work and disconnects (a connected worker stays
// alive, waiting for the primary); the primary exits once they all have — a
// self-contained demo.
// A real service would have each worker listen on one port instead: the
// primary accepts the connections and hands them to the workers in turn.
if (cluster.isPrimary) {
  console.log("primary: forking 3 workers");
  for (let i = 0; i < 3; i++) {
    const w = cluster.fork();
    console.log("primary: started worker " + w.id);
  }
} else {
  console.log("worker " + cluster.worker!.id + ": doing work, then exiting");
  cluster.worker!.disconnect();
}
