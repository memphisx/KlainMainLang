// events.on — an async iterator that yields the event's args array on each
// emission, buffering events between iterations and parking the consumer when
// the queue drains (TDD-00167, built on the array-yielding generators of
// ADR-00676). The listener is attached eagerly at the on(...) call.
import { EventEmitter, on } from 'events'

async function main(): Promise<void> {
  const ee = new EventEmitter<{ tick: [number] }>()

  // Emit from a timer, after the loop below has started and parked.
  let sent = 0
  const timer = setInterval(() => {
    sent++
    ee.emit('tick', sent)
  }, 10)

  let count = 0
  for await (const [n] of on(ee, 'tick')) {
    console.log('tick', n)
    count++
    if (count >= 3) {
      clearInterval(timer)
      break
    }
  }
  console.log('done after', count, 'ticks')
}
main()
