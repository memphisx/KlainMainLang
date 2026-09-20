// One event loop: every wait either parks a coroutine or, at module top level,
// runs the loop — so nothing that waits ever stops anything else from running.
//
// A heartbeat ticks every 20 ms for the whole program. Each wait below — a
// top-level `await`, an async arrow, an async method, an async timer callback,
// a `.then` chain that returns a promise — lasts ~100 ms, and the heartbeat
// keeps beating through all of them, exactly as it does under Node.
//
// Self-contained: the "slow operation" is a timer promise and a child process,
// so it needs no network.
import { spawn } from 'node:child_process'

let beats = 0
const heart = setInterval(() => { beats = beats + 1 }, 20)

function sleep(ms: number): Promise<void> {
  return new Promise<void>((res) => { setTimeout(() => { res() }, ms) })
}

// Reports how many heartbeats happened during a wait. A starved loop scores 0.
function report(what: string, before: number): void {
  console.log(what + ": heartbeat kept going = " + (beats - before >= 2))
}

// 1. A top-level await runs the loop: the child's output arrives during it.
let childSaid = ""
const child = spawn('sh', ['-c', 'echo hello-from-child'])
child.stdout.on('data', (d: string) => { childSaid = childSaid + d })
child.on('exit', (code: number) => { console.log("child exit " + code) })
child.on('close', (code: number) => { console.log("child close " + code) })
let mark = beats
await sleep(100)
report("top-level await", mark)
console.log("child output seen during the await: " + childSaid.trim())

// 2. An async arrow returns its promise at the first await.
const viaArrow = async (): Promise<string> => {
  await sleep(100)
  return "arrow done"
}
mark = beats
console.log(await viaArrow())
report("async arrow", mark)

// 3. So does an async method.
class Job {
  async run(): Promise<string> {
    await sleep(100)
    return "method done"
  }
}
mark = beats
console.log(await new Job().run())
report("async method", mark)

// 4. A `.then` callback that returns a promise is flattened into the chain, and
//    reactions on one promise run in the order they were registered.
mark = beats
const chained = Promise.resolve(20).then((n: number) => sleep(100).then(() => n + 22))
chained.then((v: number) => { console.log("first reaction " + v) })
chained.then((v: number) => { console.log("second reaction " + v) })
console.log("flattened to " + (await chained))
report(".then chain", mark)

// 5. A timer callback may be async; its await parks like any other.
mark = beats
setTimeout(async () => {
  await sleep(100)
  report("async timer callback", mark)
  clearInterval(heart)
}, 1)
