<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guides · Concurrency</span>
    <h1>Build a concurrent load tester</h1>
    <p class="km-doc__lede">
      A load tester is the perfect shape for <code>klain:sync</code>: a pool of goroutine "agents"
      hammering a real endpoint in parallel, a live view of throughput and latency, and a report you
      keep. We'll build <code>klainload</code> — point it at any URL, set how many agents and for how
      long (or run until you stop it), watch a <code>klain:tui</code> dashboard, and get a summary
      that stays on screen. One native binary, no runtime, no event loop.
    </p>

    <Shot :src="loadImg"
      alt="A terminal dashboard: throughput in requests per second, success/failure counts, p50/p90/p99 latency, a status-code breakdown, and an RPS sparkline"
      caption="klainload's live dashboard — throughput, success rate, latency percentiles, a status-code breakdown, and an RPS sparkline, redrawn as the agents drive requests at the target. Press s at any time to stop." />

    <h2>1 · The agents</h2>
    <p>
      Each agent is a goroutine running a closed loop: check a shared <code>stop</code> channel
      (without blocking), fire one <strong>synchronous</strong> request at the target, and stream a
      <code>Result</code> out. There's no fixed request count and no work queue — a load tester runs
      for a duration or until stopped, as fast as the target answers:
    </p>
    <CodeBlock filename="engine.ts" :code="engine" />
    <p>
      <code>xhr.open(method, url, false)</code> is a real blocking HTTP request; on a goroutine it
      parks that agent while the transfer is in flight, so all the agents' requests genuinely
      overlap. <code>go(fn)</code> puts each agent on the M:N scheduler — with
      <code>GOMAXPROCS</code> defaulting to your core count they run in parallel across cores.
    </p>

    <h2>2 · Stopping: a broadcast close</h2>
    <p>
      This is the Go idiom that makes "stop at any time" trivial. Closing a channel wakes every
      receiver, and in a <code>select</code> a <code>recvCase</code> on a closed channel fires
      immediately — so a single <code>stop.close()</code> halts <em>all</em> the agents at once.
      Never send on it; only close it:
    </p>
    <CodeBlock filename="load_test.ts" :code="stopcode" />
    <p>
      The same close ends a fixed-duration run (fired at a <code>performance.now()</code> deadline)
      and an infinite one (fired by a keypress). Workers never touch <code>results</code> after
      stopping; the collector drains what's left with a non-blocking <code>select</code>.
    </p>

    <h2>3 · Fan in: fold results into stats</h2>
    <p>
      The collector runs on the main thread. Each pass drains every result currently available
      (non-blocking <code>select</code> with a <code>defaultCase</code>), then — in the live UI —
      polls the keyboard for the stop key and repaints. <code>readKey(60)</code> both paces the loop
      and reads a keystroke if one is waiting:
    </p>
    <CodeBlock filename="load_test.ts" :code="collector" />
    <p>
      A <code>Channel&lt;Result&gt;</code> carries the whole per-request object by pointer, so one
      pass folds latency, HTTP status class, and byte count together. Latencies land in a
      log-bucketed histogram — all you need to read percentiles off the cumulative counts without
      storing every sample:
    </p>
    <CodeBlock filename="stats.ts" :code="pct" />

    <h2>4 · Configuring the run</h2>
    <p>
      The target and parameters come from the command line <em>or</em> the first TUI screen. With no
      URL, a config form opens (edit the URL, method, agent count, duration or infinite, headers,
      body, then Start); a URL pre-fills it, and <code>--run</code> skips it:
    </p>
    <CodeBlock filename="terminal" :code="cli" />
    <p>
      The config screen is a small component built the same way as the <code>klain:tui</code> form
      guide — you own the field values and route each keystroke; <code>TextInput</code> just draws
      the text and a cursor.
    </p>

    <h2>5 · The results that stay</h2>
    <p>
      When the run ends, the results screen shows the success rate and latency summary, and the same
      report is printed to stdout after the TUI exits — so it survives in your scrollback instead of
      vanishing with the alt-screen. Non-interactive runs (piped, or in CI) skip the TUI entirely
      and just print it:
    </p>
    <CodeBlock filename="report (stdout)" :code="report" />

    <h2>Split by concern</h2>
    <p>
      The tool is a handful of small modules, each a real concern — a working demonstration of the
      module system:
    </p>
    <ul>
      <li><code>config.ts</code> — the <code>Config</code> shape and CLI parsing.</li>
      <li><code>engine.ts</code> — the agent goroutines and the stop wiring.</li>
      <li><code>stats.ts</code> — the per-request <code>Result</code> and the aggregation.</li>
      <li><code>report.ts</code> — the persistent text summary.</li>
      <li><code>screens.ts</code> — the live dashboard and results screen.</li>
      <li><code>configform.ts</code> — the interactive config screen.</li>
      <li><code>load_test.ts</code> — the orchestrator that wires them together.</li>
    </ul>

    <h2>Good to know</h2>
    <ul>
      <li><strong>Goroutines are a separate world from <code>async</code>.</strong> An agent body is
        synchronous — channel ops and the blocking request park the goroutine, not the thread, and
        there is no <code>await</code>. Stopping is a channel <code>close()</code>, not a cancelled
        Promise.</li>
      <li><strong>A synchronous request is what a closed-loop generator wants.</strong>
        <code>XMLHttpRequest</code> with <code>async: false</code> (a real, faithful web API) blocks
        the agent until the response is in, and yields cooperatively on a goroutine so the agents
        overlap.</li>
      <li><strong>It's all tunable.</strong> Agent count and duration are parameters;
        <code>GOMAXPROCS</code> sets the parallelism and <code>KLAINSYNC_STACK_KB</code> the
        per-goroutine stack — dial the load and watch the throughput and the tail move.</li>
      <li><strong>Colours are compile-time literals</strong> in <code>klain:tui</code>, so the
        helpers branch on a colour key rather than passing one through a variable.</li>
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

const engine = `go(() => {
  for (;;) {
    // Non-blocking stop check: once stop is closed, its recvCase fires
    // for every agent, so they all halt together.
    let stopped = false
    select(
      stop.recvCase((_: number) => { stopped = true }),
      defaultCase(() => {}),
    )
    if (stopped) break

    const t0 = performance.now()
    const xhr = new XMLHttpRequest()
    xhr.open(method, url, false)   // async: false — a real blocking request
    xhr.send()
    const latencyUs = Math.round((performance.now() - t0) * 1000)
    // status is 0 on a connection failure; send() never throws.
    results.send({ latencyUs, status: xhr.status, bytes: xhr.responseText.length })
  }
  done.send(1)
})`

const stopcode = `const stop = new Channel<number>(0)

// From the collector: close once — every agent's recvCase fires.
if (!cfg.infinite && now >= deadline) stop.close()   // duration reached
if (key === 's' || key === 'q') stop.close()          // manual stop`

const collector = `while (finished < cfg.concurrency) {
  // Drain every result currently available, without blocking.
  let draining = true
  while (draining) {
    select(
      results.recvCase((r: Result) => { record(stats, r) }),
      done.recvCase((_: number) => { finished += 1 }),
      defaultCase(() => { draining = false }),
    )
  }
  const key: string = readKey(60)        // pace ~60ms AND poll for a stop key
  if (key === 's' || key === 'q') { stop.close() }
  if (now - lastRender > 100) render(renderRunning(cfg, stats, now - start))
}`

const pct = `// The pth-percentile latency, read off the cumulative histogram.
export function pct(s: Stats, p: number): number {
  const target = p * s.completed
  let cum = 0
  for (let b = 0; b < NBUCKETS; b++) {
    cum += s.hist[b]
    if (cum >= target) return 1 << b     // 2^b µs — the bucket's lower edge
  }
  return s.maxUs
}`

const cli = `klainload                                 # opens the config screen
klainload http://host/path                # pre-fills the config screen
klainload http://host/path --run -c 32 -d 30   # skip it: 30s, 32 agents
klainload http://host/path -d inf         # run until you press s`

const report = `klainload — GET http://127.0.0.1:8080/
  16 agents · 30s

Summary
  requests    482913
  throughput  16097 req/s
  success     482910 (100.0%)
  failed      3 (0.0%)

Latency
  mean    980µs   min 210µs   max 44.1ms
  p50     0.8ms   p90 1.6ms   p99 4.1ms

Status codes
  2xx 482910   3xx 0   4xx 0   5xx 0   errors 3`
</script>
