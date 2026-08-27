<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Documentation</span>
    <h1>KlainMainLang</h1>
    <p class="km-doc__lede">
      A TypeScript-to-native compiler, written in Go, that emits LLVM IR and hands it to
      <code>clang</code>. You write <code>.ts</code>, it writes <code>.ll</code>,
      <code>clang</code> writes a real executable.
    </p>

    <blockquote>
      <p>
        <strong>Personal / experimental project.</strong> One person, building this for fun and
        to learn how compilers actually work. Not audited, not hardened, no stability guarantees
        between commits. Perfect for tinkering, small CLI toys, and Docker microservices — bring
        your own garbage collector, or pass <code>-mm=gc</code> and let it bring one for you.
      </p>
    </blockquote>

    <h2>Where to go next</h2>
    <div class="km-cards">
      <router-link v-for="c in cards" :key="c.to" :to="c.to" class="km-card2">
        <q-icon :name="c.icon" size="24px" class="km-card2__icon" />
        <h3>{{ c.title }}</h3>
        <p>{{ c.body }}</p>
      </router-link>
    </div>

    <h2>At a glance</h2>
    <ul>
      <li><strong>The language is basically all there (~95%).</strong> Classes, generics, closures, <code>async</code>/<code>await</code>, unions, generics with constraints, mapped &amp; conditional types.</li>
      <li><strong>You can write a real server.</strong> <code>http.listen</code> speaks HTTP/1.1 and HTTP/2, <code>fs</code> reads and writes, <code>worker_threads</code> and <code>cluster</code> give you real OS threads and processes, TLS on both ends.</li>
      <li><strong>Browser-shaped APIs that make sense off-browser.</strong> <code>fetch</code>, <code>URL</code>, <code>WebSocket</code>, Web Crypto, Streams, <code>AbortController</code>, timers.</li>
      <li><strong>What'll bite you.</strong> A bare <code>: number</code> is a JS-faithful double; reach for a sized <code>int8</code>…<code>uint64</code> type when you want integer semantics. Concurrency is cooperative. Nothing is dynamic. All deliberate — see the <router-link to="/docs/coverage">coverage matrix</router-link>.</li>
    </ul>

    <div class="km-doc__nextrow">
      <span></span>
      <router-link to="/docs/getting-started" class="km-btn km-btn--gold">Getting started →</router-link>
    </div>
  </article>
</template>

<script setup>
const cards = [
  { to: '/docs/getting-started', icon: 'rocket_launch', title: 'Getting started', body: 'Build the compiler, compile your first .ts file, run the binary.' },
  { to: '/docs/language', icon: 'code', title: 'Language guide', body: 'Types, generics, classes, modules, async/await — and the sharp edges.' },
  { to: '/docs/stdlib', icon: 'library_books', title: 'Standard library', body: 'http, fetch, fs, workers, crypto, streams, process.' },
  { to: '/docs/cli', icon: 'terminal', title: 'CLI & flags', body: 'Commands, memory modes, backends, --static and friends.' }
]
</script>

<style scoped>
.km-cards { display: grid; grid-template-columns: 1fr 1fr; gap: 1px; background: var(--km-line); border: 1px solid var(--km-line); margin: 0 0 24px; }
.km-card2 { background: #0e0e0e; padding: 24px; text-decoration: none; border: 0 !important; transition: background .15s ease; }
.km-card2:hover { background: #151515; }
.km-card2__icon { color: var(--km-gold); margin-bottom: 12px; }
.km-card2 h3 { margin: 0 0 6px; }
.km-card2 p { color: #a0a0a0; font-size: 0.9rem; margin: 0; }
@media (max-width: 600px) { .km-cards { grid-template-columns: 1fr; } }
</style>
