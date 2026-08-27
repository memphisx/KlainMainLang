<template>
  <footer class="km-footer km-on-black">
    <div class="km-wrap">
      <div class="km-footer__top">
        <div class="km-footer__brand">
          <Wordmark :stacked="true" size="lg" />
          <p class="km-footer__mission km-mono">Κλάιν Μάιν — “build it in TypeScript anyway, because you can.”</p>
        </div>

        <div class="km-footer__cols">
          <div class="km-footer__col">
            <span class="km-eyebrow">Docs</span>
            <router-link to="/docs/getting-started" class="km-footer__link">Getting started</router-link>
            <router-link to="/docs/language" class="km-footer__link">Language guide</router-link>
            <router-link to="/docs/stdlib" class="km-footer__link">Standard library</router-link>
            <router-link to="/docs/cli" class="km-footer__link">CLI &amp; flags</router-link>
          </div>
          <div class="km-footer__col">
            <span class="km-eyebrow">Project</span>
            <router-link to="/docs/coverage" class="km-footer__link">Coverage</router-link>
            <router-link to="/docs/examples" class="km-footer__link">Examples</router-link>
            <a :href="gh" target="_blank" rel="noopener" class="km-footer__link">GitHub</a>
            <a :href="gh + '/blob/main/LICENSE'" target="_blank" rel="noopener" class="km-footer__link">License (AGPL-3.0)</a>
          </div>
          <div class="km-footer__col km-footer__flag">
            <FlagVergina :width="128" />
          </div>
        </div>
      </div>

      <hr class="km-rule" />

      <div class="km-footer__bottom">
        <span>© {{ year }} Kyriakos Bompotis · AGPL-3.0-or-later</span>
        <span class="km-mono">TypeScript, minus the runtime. Native binaries, no apologies.</span>
      </div>
    </div>
  </footer>
</template>

<script setup>
import Wordmark from 'components/brand/Wordmark.vue'
import FlagVergina from 'components/brand/FlagVergina.vue'
import { GITHUB_URL } from 'src/lib/content.js'

const gh = GITHUB_URL
const year = 2026
</script>

<style scoped>
.km-footer { padding-block: clamp(56px, 8vw, 96px) 40px; }
.km-footer__top {
  display: grid;
  grid-template-columns: 1.1fr 2fr;
  gap: clamp(32px, 6vw, 80px);
  align-items: start;
}
.km-footer__mission { margin-top: 20px; color: var(--km-gold); font-size: 0.82rem; }
.km-footer__cols { display: grid; grid-template-columns: repeat(3, 1fr); gap: 28px; }
.km-footer__col { display: flex; flex-direction: column; gap: 12px; }
.km-footer__col .km-eyebrow { color: #7a7a7a; margin-bottom: 6px; }
.km-footer__link {
  color: #cfcfcf; text-decoration: none; font-size: 0.9rem;
  transition: color .15s ease;
}
.km-footer__link:hover { color: var(--km-gold); }
.km-footer__flag { align-items: flex-start; }
.km-footer__flagcap { color: #6f6f6f; font-size: 0.72rem; margin-top: 4px; }
.km-footer__bottom {
  display: flex; justify-content: space-between; flex-wrap: wrap; gap: 12px;
  margin-top: 28px; color: #6f6f6f; font-size: 0.8rem;
}
@media (max-width: 860px) {
  .km-footer__top { grid-template-columns: 1fr; }
  .km-footer__cols { grid-template-columns: 1fr 1fr; }
}
@media (max-width: 520px) {
  .km-footer__cols { grid-template-columns: 1fr; }
}
</style>
