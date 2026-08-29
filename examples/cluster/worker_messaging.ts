// cluster worker messaging over the real IPC channel: the primary forks a
// worker (a re-exec of this binary with NODE_CHANNEL_FD + its worker id),
// exchanges string messages both ways, and watches the exit.
import cluster from 'cluster'
import { mustCall } from 'test'

if (cluster.isPrimary) {
  const worker = cluster.fork()
  worker.on('online', mustCall(() => { console.log("worker online, id", worker.id) }))
  worker.on('message', mustCall((msg) => {
    console.log("primary received:", msg)
    worker.send("thanks, shut down")
  }))
  worker.on('exit', mustCall((code) => { console.log("worker exited with", code) }))
} else {
  process.send("greetings from worker " + cluster.workerId)
  process.on('message', (msg) => {
    console.log("worker received:", msg)
    process.exit(0)
  })
}
