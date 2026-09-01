<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">klain: namespace</span>
    <h1><code>klain:sync</code> — Go-style goroutines &amp; channels</h1>
    <p class="km-doc__lede">
      The concurrency differentiator, and the one place the project aims for
      <strong>full Go fidelity</strong>: <code>go(fn)</code> to spawn a goroutine, a typed
      <code>Channel&lt;T&gt;</code> to communicate, <code>select</code> to wait on many at once —
      all running in parallel across every core, preemptively scheduled, in a single native
      binary. This is the deliberate answer to "why not just Workers or <code>async</code>?"
    </p>

    <h2>A concurrent program, end to end</h2>
    <p>
      Here is a real fan-out / fan-in pipeline: it counts the primes below half a million across a
      pool of goroutines, streams each hit back over a channel, and coordinates completion with
      <code>select</code> — the same program you'd write in Go.
    </p>
    <CodeBlock filename="parallel_primes.ts" :code="primesCode" />
    <CodeBlock terminal filename="output" :code="primesOut" />
    <p>
      <router-link to="/docs/examples/goroutines/parallel_primes" class="km-btn km-btn--gold">
        Open in the example gallery →
      </router-link>
    </p>

    <h2>Goroutines &amp; the scheduler</h2>
    <p>
      <code>go(() =&gt; { … })</code> spawns a goroutine — far cheaper than an OS thread — onto a
      real <strong>GMP work-stealing scheduler</strong> built into an embedded C runtime that is
      linked only when you import <code>klain:sync</code> (a program that never imports it pays and
      links nothing).
    </p>
    <ul>
      <li><strong>G</strong> — a goroutine: its own stack and saved execution context.</li>
      <li><strong>M</strong> — an OS thread (<code>pthread</code>).</li>
      <li><strong>P</strong> — a logical processor with a local run queue; <code>GOMAXPROCS</code>
        defaults to the CPU count, so N runnable goroutines run on N cores — real parallelism,
        unlike <code>async</code> (single-threaded by contract).</li>
    </ul>
    <p>
      Idle processors steal half of a busy one's queue, and a global run queue backstops them, so
      work spreads evenly with no central bottleneck.
    </p>

    <h2>Channels</h2>
    <p>
      A <code>Channel&lt;T&gt;</code> is a Go channel: a <em>synchronous, blocking</em>
      <code>send</code>/<code>receive</code> that parks the <strong>goroutine</strong> — the OS
      thread keeps running other goroutines — rather than blocking a whole thread. Capacity 0 is an
      unbuffered rendezvous; a positive capacity is a buffered ring.
    </p>
    <CodeBlock filename="channels.ts" :code="channelsCode" />
    <p>
      Ranging a channel with <code>for (const v of ch)</code> receives until the channel is
      <code>close()</code>d and drained — the idiomatic way to consume a stream of values.
    </p>

    <h2><code>select</code></h2>
    <p>
      <code>select</code> waits on several channel operations at once and proceeds with whichever is
      ready; if several are ready it picks pseudo-randomly (Go's fairness), and if none are ready it
      blocks the goroutine until one fires — unless a <code>defaultCase</code> makes it
      non-blocking.
    </p>
    <CodeBlock filename="select.ts" :code="selectCode" />

    <h2>Preemptive by default</h2>
    <p>
      A busy goroutine can't starve the others. A <code>sysmon</code> helper thread flags any
      goroutine that has run too long, and the compiler inserts a tiny preempt check at
      <strong>every function entry and every loop back-edge</strong> — so even a tight compute loop
      with no channel operations yields. Because every loop is instrumented, cooperative preemption
      is complete here; there is no uninstrumented loop that Go's signal-based async preemption would
      need to catch.
    </p>
    <p>
      When a goroutine makes a genuinely blocking call, work-stealing lets the other processors keep
      draining its queue, and if every thread blocks at once a bounded rescue thread is spawned so
      the program can't deadlock.
    </p>

    <h2>Tuning</h2>
    <ul>
      <li><code>GOMAXPROCS</code> — number of logical processors (parallelism); defaults to the CPU
        count.</li>
      <li><code>KLAINSYNC_STACK_KB</code> — per-goroutine stack size in KiB (default 256); lower it
        to pack far more goroutines, raise it for deep recursion.</li>
    </ul>

    <p class="km-note">
      <strong>Scope fence.</strong> None of this touches <code>async</code>/<code>await</code>,
      Promises, or Workers — those stay JS-faithful and single-threaded so ordinary code keeps
      running under Node. Goroutines are a separate, opt-in world reached only through
      <code>klain:sync</code>, and channel operations are synchronous (Go has no <code>await</code>
      on a channel). Only the moving/growable-stack optimisation is still ahead; it shares the
      precise-stack-map work with the moving-GC backend.
    </p>

    <div class="km-doc__nextrow">
      <router-link to="/docs/klain/http" class="km-btn">← klain:http</router-link>
      <router-link to="/docs/cli" class="km-btn km-btn--gold">CLI &amp; flags →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'

const primesCode = `import { go, Channel, select, defaultCase } from 'klain:sync'

const N = 500000
const WORKERS = 8

const primes = new Channel<number>(1024)  // every prime found, streamed to the collector
const done = new Channel<number>(0)        // a worker signals when its slice is exhausted

function isPrime(n: number): boolean {
  if (n < 2) return false
  if (n % 2 === 0) return n === 2
  for (let d = 3; d * d <= n; d += 2) {
    if (n % d === 0) return false
  }
  return true
}

// Fan out: worker \`id\` tests the interleaved slice id, id+WORKERS, id+2·WORKERS…
function spawnWorker(id: number): void {
  go(() => {
    for (let n = 2 + id; n <= N; n += WORKERS) {
      if (isPrime(n)) primes.send(n)
    }
    done.send(id)
  })
}
for (let w = 0; w < WORKERS; w++) spawnWorker(w)

// Fan in: count primes as they stream in, tally completions with select, then
// drain any still-buffered primes with a non-blocking default.
let count = 0
let finished = 0
while (finished < WORKERS) {
  select(
    primes.recvCase((p: number) => { count += 1 }),
    done.recvCase((id: number) => { finished += 1 }),
  )
}
let draining = true
while (draining) {
  select(
    primes.recvCase((p: number) => { count += 1 }),
    defaultCase(() => { draining = false }),
  )
}
console.log(\`primes below \${N}: \${count}\`)`

const primesOut = `$ klainmain build parallel_primes.ts && ./parallel_primes
primes below 500000: 41538
(found by 8 goroutines running in parallel)`

const channelsCode = `const ch = new Channel<number>(0)  // unbuffered rendezvous

go(() => { ch.send(42) })          // parks the goroutine until a receiver arrives
console.log(ch.receive())          // 42

// Range a channel until it is closed and drained:
const nums = new Channel<number>(0)
go(() => {
  for (let i = 1; i <= 5; i++) nums.send(i * i)
  nums.close()
})
let sum = 0
for (const v of nums) sum += v     // 1 + 4 + 9 + 16 + 25
console.log(sum)                   // 55`

const selectCode = `const a = new Channel<number>(0)
const b = new Channel<number>(0)

go(() => { a.send(1) })
go(() => { b.send(2) })

// Take whichever is ready first; if neither is, block until one fires.
select(
  a.recvCase((v: number) => { console.log('from a:', v) }),
  b.recvCase((v: number) => { console.log('from b:', v) }),
)

// With a default, select never blocks:
select(
  a.recvCase((v: number) => { console.log('got', v) }),
  defaultCase(() => { console.log('nothing ready') }),
)`
</script>

<style scoped>
.km-note {
  border-left: 3px solid var(--km-gold, #c6a03c);
  padding: 8px 0 8px 16px;
  opacity: 0.85;
}
</style>
