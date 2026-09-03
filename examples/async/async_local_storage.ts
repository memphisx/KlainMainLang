// async_hooks AsyncLocalStorage — request-scoped context that propagates across
// `await` (TDD-00168). The store lives in the coroutine task struct, so a value
// set with run(...) survives an await/park/resume — the classic use is carrying
// a request id (or a "current user") through an async call tree without
// threading it explicitly through every function's parameters.
import { AsyncLocalStorage } from 'async_hooks'

interface RequestCtx {
  id: number
}

const store = new AsyncLocalStorage<RequestCtx>()

async function loadUser(): Promise<void> {
  // getStore() here reads the id set by the run(...) far up the call tree,
  // even though this function took no ctx parameter and awaits in between.
  const before = store.getStore()?.id
  await new Promise<number>((res) => { setTimeout(() => res(1), 5) })
  const after = store.getStore()?.id
  console.log(`req ${before}: id survived the await -> ${after}`)
}

async function handle(id: number): Promise<void> {
  await store.run({ id }, async () => {
    await loadUser()
  })
}

async function main(): Promise<void> {
  await handle(1)
  await handle(2)

  // A setTimeout scheduled inside run(...) also carries the context to its fire.
  store.run({ id: 99 }, () => {
    setTimeout(() => {
      console.log(`deferred callback still sees req ${store.getStore()?.id}`)
    }, 5)
  })

  console.log(store.getStore() === null ? 'no ambient context outside a run' : '?')
}
main()
