// util.promisify: a callback-last function becomes a Promise-returning one.
// The callback's first argument rejects, its second resolves.
import { promisify } from 'util'

function lookupUser(id: number, cb: (err: Error | null, name: string) => void): void {
  setTimeout(() => {
    if (id === 0) cb(new Error("no user 0"), "")
    else cb(null, "user-" + id)
  }, 1)
}

const lookup = promisify(lookupUser)

async function main(): Promise<void> {
  console.log(await lookup(7))
  try {
    await lookup(0)
  } catch (e) {
    console.log("rejected:", (e as Error).message)
  }
}

main()
