// FinalizationRegistry: run a labeled cleanup callback when a target object
// dies. Mode-dependent timing, identical API:
//   - default (-mm=manual): fires deterministically when Memory.free(target)
//     runs, plus an exit flush for anything never freed;
//   - -mm=gc: fires after the collector proves the target unreachable;
//   - -finalizers=report additionally prints a leak line at exit for every
//     registration whose target was never freed.
import Memory from 'memory'

interface Conn { fd: number }

const registry = new FinalizationRegistry((label: string) => {
  console.log("cleanup:", label)
})

let a: Conn = { fd: 3 }
let b: Conn = { fd: 4 }
const token: Conn = { fd: -1 }

registry.register(a, "connection-a")
registry.register(b, "connection-b", token)

// unregister via token: connection-b's cleanup never runs for the free below.
console.log("unregistered b:", registry.unregister(token))

Memory.free(a) // deterministic death point: enqueue "connection-a"
Memory.free(b) // b was unregistered — nothing enqueued

let leaked: Conn = { fd: 5 }
registry.register(leaked, "never-freed") // flushed at exit
console.log("end of script")
