// Promise.withResolvers: settle a promise from outside its executor — here
// a tiny one-shot event that callers await and a timer fires.

function once<T>(): { wait: Promise<T>; fire: (value: T) => void } {
  const { promise, resolve } = Promise.withResolvers<T>()
  return { wait: promise, fire: resolve }
}

async function main(): Promise<void> {
  const ready = once<string>()
  setTimeout(() => ready.fire("Thessaloniki"), 20)
  console.log("waiting...")
  const city = await ready.wait
  console.log("ready:", city)

  const failing: PromiseWithResolvers<number> = Promise.withResolvers()
  failing.reject(new Error("no answer"))
  try {
    await failing.promise
  } catch (e: any) {
    console.log("rejected:", e.message)
  }
}

main()
