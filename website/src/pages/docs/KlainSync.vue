<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">klain: namespace</span>
    <h1><code>klain:sync</code> — structured concurrency <small>(planned)</small></h1>
    <p class="km-doc__lede">
      The concurrency differentiator: Go-style CSP — <code>go(fn)</code> to spawn a task,
      typed <code>Channel&lt;T&gt;</code> to communicate, <code>select</code> to wait on many.
      Designed, not yet built. This is the deliberate answer to "why not just Workers or
      <code>async</code>?"
    </p>
    <CodeBlock filename="pipeline.ts" :code="syncCode" />

    <h2>The goal: match Go, not approximate it</h2>
    <p>
      This is the one place the project aims for <strong>full Go fidelity</strong>, because a
      watered-down version would defeat the point. Goroutines are unique to Go among mainstream
      runtimes — millions of them, cheap stacks, and a scheduler that <em>preempts</em> a goroutine
      that won't yield so one busy task can't starve the rest. That is exactly what
      <code>klain:sync</code> targets:
    </p>
    <ul>
      <li><strong>A real GMP scheduler.</strong> Goroutines (G) run on OS threads (M), each holding
        a logical processor (P) with its own run queue; idle Ps steal work. <code>GOMAXPROCS</code>
        defaults to CPU count, so N runnable goroutines run on N cores — real parallelism, unlike
        <code>async</code> (single-threaded by contract).</li>
      <li><strong>Preemptive scheduling.</strong> The compiler inserts preempt checks at function
        entry and every loop back-edge, so even a tight compute loop yields — a busy goroutine can't
        starve the others. Full signal-based asynchronous preemption (Go 1.14+, for code with no
        reachable check at all) is the final fidelity stage.</li>
      <li><strong>Go channels &amp; <code>select</code>.</strong> Buffered/unbuffered, close, nil,
        range, and pseudo-random <code>select</code> fairness — the real semantics, blocking the
        goroutine (not the OS thread).</li>
    </ul>
    <p class="km-note">
      <strong>Scope fence.</strong> None of this touches <code>async</code>/<code>await</code>,
      Promises, or Workers — those stay JS-faithful and single-threaded so ordinary code keeps
      running under Node. Goroutines are a separate, opt-in world reached only through
      <code>klain:sync</code>. Delivered in stages — scheduler + channels first, comprehensive
      cooperative preemption next, then signal-based async preemption and growable stacks (which
      share the precise-stack-map work with the moving-GC backend) — each shippable with its
      fidelity gap disclosed.
    </p>

    <div class="km-doc__nextrow">
      <router-link to="/docs/klain/http" class="km-btn">← klain:http</router-link>
      <router-link to="/docs/cli" class="km-btn km-btn--gold">CLI &amp; flags →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'

const syncCode = `import { go, Channel } from 'klain:sync'

const ch = new Channel<number>(0)   // unbuffered rendezvous channel

// Spawn producers onto the M:N thread pool — real parallelism.
for (let i = 0; i < 4; i++) {
  go(async () => { await ch.send(i * i) })
}

let sum = 0
for (let i = 0; i < 4; i++) {
  sum += await ch.receive()
}
console.log(sum)`
</script>

<style scoped>
.km-note {
  border-left: 3px solid var(--km-gold, #c6a03c);
  padding: 8px 0 8px 16px;
  opacity: 0.85;
}
</style>
