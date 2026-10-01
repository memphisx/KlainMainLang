// perf_hooks PerformanceObserver — observe mark/measure entries. Entries
// reach the callback on a later turn of the event loop, sorted by start time.
import { PerformanceObserver } from 'perf_hooks'

const obs = new PerformanceObserver((list, observer) => {
  for (const entry of list.getEntries()) {
    console.log(entry.entryType, entry.name, "dur>=0:", entry.duration >= 0)
  }
  observer.disconnect()
})
obs.observe({ entryTypes: ['mark', 'measure'] })

performance.mark('start')
performance.mark('end')
performance.measure('start-to-end', 'start', 'end')
console.log('marked')
