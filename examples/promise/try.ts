// Promise.try: run a callback that may throw synchronously, return a
// value, or return a promise — every outcome arrives as one promise.

function parsePort(text: string): number {
  const n = Number(text)
  if (!Number.isInteger(n) || n <= 0 || n > 65535) {
    throw new RangeError(`bad port: ${text}`)
  }
  return n
}

async function main(): Promise<void> {
  for (const text of ["8080", "http", "70000"]) {
    try {
      const port = await Promise.try(parsePort, text)
      console.log("port", port)
    } catch (e: any) {
      console.log(e.name, e.message)
    }
  }
  const city = await Promise.try(() => Promise.resolve("Thessaloniki"))
  console.log("city", city)
}

main()
