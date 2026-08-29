<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guide</span>
    <h1>The <code>klain:</code> namespace</h1>
    <p class="km-doc__lede">
      Node and the web give us the names to match — <code>fs</code>, <code>http</code>,
      <code>crypto</code>. The <code>klain:</code> namespace is where this compiler ships
      capabilities that have <em>no</em> Node or browser equivalent: bespoke, native-first
      reimaginings, kept behind an explicitly non-Node specifier so portable code stays portable.
    </p>

    <div class="km-cards">
      <router-link to="/docs/klain/webview" class="km-card">
        <h3><code>klain:webview</code></h3>
        <p>Desktop apps over the system webview — any SPA as the frontend of a compiled binary,
          calling straight into native code.</p>
        <span class="km-card__status km-card__status--ok">Stages 0–7 shipped</span>
      </router-link>
      <router-link to="/docs/klain/http" class="km-card">
        <h3><code>klain:http</code></h3>
        <p>The bespoke <code>listen(handler ⇒ response)</code> server model — a pure
          request→response function, no streaming <code>res</code> object.</p>
        <span class="km-card__status km-card__status--ok">Shipped</span>
      </router-link>
      <router-link to="/docs/klain/sync" class="km-card">
        <h3><code>klain:sync</code></h3>
        <p>Go-style CSP — <code>go</code>, typed channels, <code>select</code> — with real
          parallelism across cores. The concurrency differentiator.</p>
        <span class="km-card__status">Planned</span>
      </router-link>
    </div>

    <p>
      Pick a module from the list on the left, or the cards above. Each is an opt-in: a program that
      never imports a <code>klain:</code> module links nothing extra and stays portable.
    </p>

    <div class="km-doc__nextrow">
      <router-link to="/docs/stdlib" class="km-btn">← Standard library</router-link>
      <router-link to="/docs/klain/webview" class="km-btn km-btn--gold">klain:webview →</router-link>
    </div>
  </article>
</template>

<script setup>
</script>

<style scoped>
.km-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px;
  margin: 28px 0;
}
.km-card {
  display: block;
  padding: 18px 20px;
  border: 1px solid #2a2a2a;
  border-radius: 10px;
  text-decoration: none;
  color: inherit;
  transition: border-color .14s ease, transform .14s ease;
}
.km-card:hover { border-color: var(--km-gold, #c6a03c); transform: translateY(-2px); }
.km-card h3 { margin: 0 0 8px; }
.km-card p { margin: 0 0 14px; opacity: 0.82; font-size: 0.92rem; }
.km-card__status {
  font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.06em;
  color: #8a8a8a;
}
.km-card__status--ok { color: var(--km-gold, #c6a03c); }
</style>
