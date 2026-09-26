// A property access on a run-time undefined/null value throws a TypeError —
// catchable, `instanceof TypeError`, with Node's exact message — instead of
// crashing the program.

interface User { name: string; manager?: User }

function managerName(u: User): string {
  // `u.manager` may be absent; the `!` tells TypeScript to trust it, and
  // reading `.name` off an absent one throws.
  return u.manager!.name
}

const boss: User = { name: "Ada" }
const dev: User = { name: "Lin", manager: boss }

console.log(managerName(dev))

try {
  console.log(managerName(boss))
} catch (err) {
  console.log(err instanceof TypeError)
  console.log((err as Error).message)
}

function shout(s: string | null): string {
  return s!.toUpperCase()
}

try {
  shout(null)
} catch (err) {
  console.log((err as Error).message)
}

// `?.` is the way to read through a value that may be absent.
console.log(boss.manager?.name)
