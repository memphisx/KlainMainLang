<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guides · Concurrency</span>
    <h1>Build a concurrent load tester</h1>
    <p class="km-doc__lede">
      A load tester is the perfect shape for <code>klain:sync</code>: many workers hammering a
      service in parallel, a live view of throughput and latency. We'll build a closed-loop load
      generator — a pool of goroutines driving a stream of requests as fast as they can — with a
      self-refreshing <code>klain:tui</code> dashboard. The whole design is channels, and it
      compiles to one native binary with no runtime and no event loop.
    </p>

    <Shot :src="loadImg"
      alt="A terminal dashboard: a progress bar, throughput in requests per second, p50/p90/p99 latency, an RPS sparkline, and a colored latency-distribution histogram"
      caption="load_test.ts — a live load-test dashboard: throughput, latency percentiles, an RPS sparkline, and a latency histogram, redrawn as eight worker goroutines drive 450k requests through the service in parallel." />

    <h2>1 · The architecture is channels</h2>
    <p>
      Three channels wire the whole thing together, exactly as you'd sketch it in Go — a work
      queue in, a results stream out, and a completion signal:
    </p>
    <CodeBlock filename="load_test.ts" :code="chans" />
    <p>
      The <code>requests</code> channel is buffered, so it doubles as a backpressure valve: the
      generator blocks when the buffer is full and resumes as workers drain it. Declaring the
      channels at the top level makes them module globals every goroutine can see with their full
      type intact — the idiomatic way to share a channel with a worker function.
    </p>

    <h2>2 · The service under test</h2>
    <p>
      A real request handler does a variable amount of work, and every so often a slow one drags
      the tail. We model that with a deterministic per-request cost plus an occasional heavy
      request, so the run is reproducible <em>and</em> its latency distribution has a genuine
      <code>p99 ≫ p50</code> tail — the thing a load test exists to reveal:
    </p>
    <CodeBlock filename="load_test.ts" :code="handle" />

    <h2>3 · Fan out: the worker pool</h2>
    <p>
      A generator goroutine fills the queue and <code>close()</code>s it; that close is the
      workers' stop signal. Each worker <strong>ranges</strong> the queue — <code>for (const id of
      requests)</code> receives until the channel is closed and drained — times each request, and
      streams the latency out over <code>results</code>:
    </p>
    <CodeBlock filename="load_test.ts" :code="workers" />
    <p>
      <code>go(fn)</code> puts each worker on the M:N scheduler, so with
      <code>GOMAXPROCS</code> defaulting to your core count they run genuinely in parallel. Because
      the scheduler is preemptive — the compiler plants a yield check at every function entry and
      loop back-edge — no worker can monopolise a core and starve the dashboard.
    </p>

    <h2>4 · Fan in: <code>select</code> and a live histogram</h2>
    <p>
      The collector runs on the main thread. <code>select</code> folds the two live channels —
      per-request latencies and worker completions — into a running histogram, taking whichever is
      ready so it never blocks on one while the other has data:
    </p>
    <CodeBlock filename="load_test.ts" :code="collector" />
    <p>
      Latencies land in a log-bucketed histogram, which is all you need to read percentiles off the
      cumulative counts — no need to store every sample:
    </p>
    <CodeBlock filename="load_test.ts" :code="pct" />
    <p>
      When the last worker signals done there may still be latencies buffered in
      <code>results</code>; a non-blocking <code>select</code> with a <code>defaultCase</code>
      drains them and stops:
    </p>
    <CodeBlock filename="load_test.ts" :code="drain" />

    <h2>5 · The dashboard</h2>
    <p>
      Repaint every few hundred requests and the same <code>Box</code> / <code>Text</code> /
      <code>Progress</code> components from the <code>klain:tui</code> guides give you throughput,
      the percentile row, an RPS sparkline, and the latency histogram — a live view of a native,
      multi-core workload:
    </p>
    <CodeBlock filename="load_test.ts" :code="view" />

    <h2>Good to know</h2>
    <ul>
      <li><strong><code>klain:sync</code> and <code>klain:tui</code> compose cleanly.</strong> The
        worker goroutines run on the scheduler's threads while the render loop runs on the main
        thread; they only ever meet over channels, so there's no shared mutable state to guard.</li>
      <li><strong>Goroutines are a separate world from <code>async</code>.</strong> A goroutine body
        is synchronous — channel sends and receives block the goroutine (not the thread), and there
        is no <code>await</code>. That's why this is a closed-loop generator against an in-process
        service rather than something built on Promises.</li>
      <li><strong>It's all tunable.</strong> <code>GOMAXPROCS</code> sets the parallelism and
        <code>KLAINSYNC_STACK_KB</code> the per-goroutine stack size — dial the worker count and
        watch the throughput and the tail move.</li>
      <li><strong>Colours are compile-time literals</strong> in <code>klain:tui</code>, so the
        helpers here branch on a colour key rather than passing one through a variable.</li>
    </ul>

    <div class="km-doc__nextrow">
      <router-link to="/docs/klain/sync" class="km-btn">← klain:sync reference</router-link>
      <router-link to="/docs/examples/goroutines/load_test" class="km-btn km-btn--gold">Run the example →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'
import Shot from 'components/docs/Shot.vue'
import loadImg from 'src/assets/tui/loadtest.png'

const chans = `const requests = new Channel<number>(2048)  // the work queue
const results  = new Channel<number>(4096)  // per-request latency, in microseconds
const done     = new Channel<number>(0)      // one signal per finished worker`

const handle = `function handle(id: number): number {
  const h = hash(id)
  let iters = 6000 + (h % 6000)       // base cost
  if (h % 101 === 0) iters += 90000   // ~1% slow requests → the p99 tail
  let acc = 0
  for (let i = 0; i < iters; i++) acc = (acc + hash(i ^ h)) >>> 0
  return acc
}`

const workers = `// Generator: fill the queue, then close it — the close is the stop signal.
go(() => {
  for (let i = 0; i < TOTAL; i++) requests.send(i)
  requests.close()
})

// Workers: range the queue, time each request, stream the latency out.
function spawnWorker(): void {
  go(() => {
    for (const id of requests) {
      const t0 = performance.now()
      handle(id)
      results.send(Math.round((performance.now() - t0) * 1000))
    }
    done.send(1)
  })
}
for (let w = 0; w < CONCURRENCY; w++) spawnWorker()`

const collector = `let finished = 0
while (finished < CONCURRENCY) {
  select(
    results.recvCase((micros: number) => {
      record(micros)
      if (completed % FRAME === 0) render(view())
    }),
    done.recvCase((_: number) => { finished += 1 }),
  )
}`

const pct = `// The pth-percentile latency, read off the cumulative histogram.
function pct(p: number): number {
  const target = p * completed
  let cum = 0
  for (let b = 0; b < NBUCKETS; b++) {
    cum += hist[b]
    if (cum >= target) return 1 << b   // 2^b µs — the bucket's lower edge
  }
  return maxMicros
}`

const drain = `let draining = true
while (draining) {
  select(
    results.recvCase((micros: number) => { record(micros) }),
    defaultCase(() => { draining = false }),
  )
}`

const view = `Box({ flexDirection: 'row', gap: 2 }, [
  stat('p50', fmtMicros(pct(0.5)), 'green'),
  stat('p90', fmtMicros(pct(0.9)), 'yellow'),
  stat('p99', fmtMicros(pct(0.99)), 'red'),
])`
</script>
