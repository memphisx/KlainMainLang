<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guides</span>
    <h1>Guides &amp; walkthroughs</h1>
    <p class="km-doc__lede">
      Longer-form, build-it-with-me tutorials that stitch several features together into a
      complete, runnable program — the kind of thing the per-API reference and the short
      examples don't cover on their own. Each guide ends with source you can compile and run.
    </p>

    <div class="km-guidegrid">
      <router-link to="/docs/guides/tui-app" class="km-guidecard">
        <span class="km-guidecard__tag">Terminal UI</span>
        <h3>Build a terminal app with <code>klain:tui</code></h3>
        <p>
          From an empty screen to a complete, keyboard-driven task manager with a flexbox
          layout, list selection, a text input, a progress summary, and real file
          persistence — then a live self-refreshing dashboard.
        </p>
        <span class="km-guidecard__go">Start the walkthrough →</span>
      </router-link>

      <div class="km-guidecard km-guidecard--soon">
        <span class="km-guidecard__tag">Desktop</span>
        <h3>Ship a desktop app with <code>klain:webview</code></h3>
        <p>
          A single-file desktop application: an SPA frontend calling straight into typed
          native code. (Planned — for now see the
          <router-link to="/docs/klain/webview">klain:webview</router-link> reference.)
        </p>
        <span class="km-guidecard__go km-guidecard__go--soon">Coming soon</span>
      </div>
    </div>

    <p class="km-note">
      More guides will land here over time — desktop apps, HTTP microservices, and other
      end-to-end patterns. Each is a standalone page; the sidebar lists them under
      <strong>Guides</strong>.
    </p>
  </article>
</template>

<script setup>
// A static hub; the individual guides are their own routed pages.
</script>

<style scoped>
.km-guidegrid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 1rem;
  margin: 1.5rem 0;
}
.km-guidecard {
  display: block;
  padding: 1.25rem;
  border: 1px solid var(--km-border, rgba(255, 255, 255, 0.12));
  border-radius: 12px;
  text-decoration: none;
  color: inherit;
  transition: border-color 0.15s ease, transform 0.15s ease;
}
.km-guidecard:hover { border-color: var(--km-gold, #d4af37); transform: translateY(-2px); }
.km-guidecard--soon { opacity: 0.6; }
.km-guidecard__tag {
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--km-gold, #d4af37);
}
.km-guidecard h3 { margin: 0.4rem 0 0.5rem; font-size: 1.05rem; }
.km-guidecard p { margin: 0 0 0.75rem; font-size: 0.9rem; opacity: 0.85; }
.km-guidecard__go { font-weight: 600; color: var(--km-gold, #d4af37); }
.km-guidecard__go--soon { color: inherit; opacity: 0.6; }
</style>
