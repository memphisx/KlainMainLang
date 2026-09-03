// perf_hooks PerformanceObserver — observe mark/measure entries (TDD-00166).
import { PerformanceObserver } from 'perf_hooks'

const obs = new PerformanceObserver((list) => {
  for (const entry of list.getEntries()) {
    console.log(entry.entryType, entry.name, "dur>=0:", entry.duration >= 0)
  }
})
obs.observe({ entryTypes: ['measure'] })

performance.mark('start')
performance.mark('end')
performance.measure('start-to-end', 'start', 'end')
obs.disconnect()
