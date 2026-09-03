// events.once — a Promise that resolves with the event's args the first time it
// fires (TDD-00167). Depends on the Promise<T[]> value fix (ADR-00674).
import { EventEmitter, once } from 'events'

async function main(): Promise<void> {
  const ee = new EventEmitter<{ ready: [number] }>()
  setTimeout(() => { ee.emit('ready', 42) }, 0)
  const args = await once(ee, 'ready')
  console.log('ready fired with', args[0])
}
main()
