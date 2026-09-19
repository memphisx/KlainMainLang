// Arrays are reference values (TDD-00127): mutating an array parameter in place
// grows the caller's array, exactly like JavaScript. Reassigning the parameter
// to a new array, by contrast, is a local rebind and leaves the caller's array
// untouched.

function enqueue(queue: string[], item: string): void {
  queue.push(item)
}

function drainFirst(queue: string[]): string {
  const head = queue.shift()
  return head!
}

const jobs: string[] = ["build"]
enqueue(jobs, "test")
enqueue(jobs, "deploy")
console.log("after enqueue:", jobs.join(" -> "))   // build -> test -> deploy
console.log("length:", jobs.length)                // 3

const first = drainFirst(jobs)
console.log("drained:", first)                     // build
console.log("remaining:", jobs.join(" -> "))       // test -> deploy

// Reassigning the parameter is local — the caller's array is unchanged.
function withoutDeploys(queue: string[]): number {
  queue = queue.filter((j) => j !== "deploy")
  return queue.length
}
console.log("filtered length:", withoutDeploys(jobs))  // 1
console.log("caller intact:", jobs.join(" -> "))       // test -> deploy

// Propagation is transitive through nested calls.
function pipeline(stages: number[]): void {
  addStage(stages, 4)
}
function addStage(stages: number[], n: number): void {
  stages.push(n)
}
const stages: number[] = [1, 2, 3]
pipeline(stages)
console.log("stages:", stages.join(","))           // 1,2,3,4

// The same reference semantics hold for every argument shape, not just a
// named variable: an object field, a nested element, and a HOF callback
// element all pass the SAME array, so in-place mutation propagates.
const registry = { tags: ["a"] }
enqueue(registry.tags, "b")                        // through an object field
console.log("field:", registry.tags.join(","))     // a,b

const matrix: number[][] = [[1], [10, 20]]
addStage(matrix[0], 2)                             // through a nested element
console.log("element:", matrix[0].join(","))       // 1,2

matrix.forEach((row) => row.push(0))               // through a HOF element
console.log("rows:", matrix[0].join(","), matrix[1].join(",")) // 1,2,0 10,20,0

const row = matrix.find((r) => r.length === 3)     // a found element aliases
if (row) { row.push(99) }
console.log("found:", matrix[0].join(","))         // 1,2,0,99

// A transient copy stays detached: slice() hands the callee a new array.
enqueue2(matrix[1].slice())
console.log("detached:", matrix[1].length)         // 3
function enqueue2(xs: number[]): void { xs.push(-1) }
