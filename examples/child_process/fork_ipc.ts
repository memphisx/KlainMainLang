// child_process.fork — the self-fork idiom with a real IPC channel: the
// child is this same program re-executed (NODE_CHANNEL_FD, Node's own
// mechanism), detected via `if (process.send)`. Messages are strings framed
// as JSON lines on a socketpair.
import { fork } from 'child_process'
import { mustCall } from 'test'

if (process.send) {
  // child: greet, echo one message, hang up
  process.send("child up")
  process.on('message', (msg) => {
    process.send("echo:" + msg)
    process.disconnect()
  })
} else {
  const child = fork(__filename)
  console.log("forked pid > 0:", child.pid > 0)
  child.on('message', mustCall((msg) => {
    console.log("from child:", msg)
    if (msg === "child up") {
      child.send("kalimera thessaloniki")
    }
  }, 2))
  child.on('exit', mustCall((code) => {
    console.log("child exited:", code)
  }))
}
