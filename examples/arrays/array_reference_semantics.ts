// Arrays are reference values (TDD-00127): mutating an array parameter in place
// grows the caller's array, exactly like JavaScript. Reassigning the parameter
// to a new array, by contrast, is a local rebind and leaves the caller's array
// untouched.

function enqueue(queue: string[], item: string): void {
  queue.push(item)
}

function drainFirst(queue: string[]): string {
  const head = queue.shift()
  return head
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
