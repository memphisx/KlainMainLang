<template>
  <section class="km-section">
    <div class="km-wrap km-conf">
      <span class="km-eyebrow">Conformance · public test suites</span>
      <h1 class="km-display km-conf__title">How it scores on the suites everyone can check</h1>
      <p class="km-conf__lede">
        Four public suites, both compatibility lanes, per platform. Every number below is
        generated from each suite's own output by the project's conformance tool — nothing here
        is hand-typed, and a build-time guard fails if the figures drift from the last run.
        They read low on purpose: this is a <strong>typed subset</strong> compiler, and most of
        these corpora are untyped <code>eval</code>-based JavaScript, browser DOM, or the whole
        Node platform — largely out of scope by design, not silent failures.
      </p>

      <div v-if="platformsView.length" class="km-conf__tabswrap">
        <q-tabs
          v-model="activePlatform"
          class="km-conf__tabs"
          active-color="amber"
          indicator-color="amber"
          align="left"
          no-caps
          dense
        >
          <q-tab v-for="p in platformsView" :key="p.platform" :name="p.platform" :label="p.label" />
        </q-tabs>

        <q-tab-panels v-model="activePlatform" animated class="km-conf__panels">
          <q-tab-panel v-for="p in platformsView" :key="p.platform" :name="p.platform" class="km-conf__panel">
            <p class="km-conf__platnote km-mono">
              Figures for {{ p.label }}, as of that platform's last committed conformance run.
            </p>
            <div v-for="s in p.suites" :key="s.suite" class="km-conf__suite">
              <div class="km-conf__suitehead">
                <h2 class="km-conf__suitename">{{ s.label }}</h2>
                <code v-if="s.corpusCommit" class="km-conf__pin" :title="'Pinned corpus commit'">corpus {{ s.corpusCommit.slice(0, 8) }}</code>
              </div>
              <p class="km-conf__blurb">{{ s.blurb }}</p>
              <div class="km-conf__lanes">
                <div v-for="ln in s.lanes" :key="ln.flag" class="km-conf__lane">
                  <span class="km-conf__val km-display">{{ ln.value }}</span>
                  <span class="km-conf__flag km-mono">-compat={{ ln.flag }}</span>
                  <span class="km-conf__sub km-mono">{{ ln.sub }}</span>
                </div>
              </div>
            </div>
          </q-tab-panel>
        </q-tab-panels>
      </div>
      <p v-else class="km-conf__empty km-mono">Conformance figures have not been generated for this build.</p>

      <p class="km-conf__foot km-mono">
        Want the caveats behind the targeted-feature numbers, and the per-flag lane taglines?
        <router-link to="/docs/coverage" class="km-link">See the coverage matrix →</router-link>
        The repository's generated conformance reports are the source of truth.
      </p>
    </div>
  </section>
</template>

<script setup>
import { ref } from 'vue'
import { conformancePlatformsView } from 'src/lib/content.js'

const platformsView = conformancePlatformsView
const activePlatform = ref(platformsView[0]?.platform ?? '')
</script>

<style scoped>
.km-conf { max-width: 960px; }
.km-conf__title { font-size: clamp(2rem, 4vw, 3rem); line-height: 1.05; margin: 12px 0 18px; }
.km-conf__lede { color: #b6b6b6; font-size: 1.02rem; line-height: 1.6; max-width: 62ch; margin: 0 0 32px; }
.km-conf__tabs { border-bottom: 1px solid var(--km-line); margin-bottom: 8px; }
.km-conf__panels { background: transparent; }
.km-conf__panel { padding: 12px 0; }
.km-conf__platnote { color: #6a6a6a; font-size: 0.76rem; margin: 0 0 20px; }
.km-conf__suite { padding: 0 0 26px; margin: 0 0 26px; border-bottom: 1px solid var(--km-line); }
.km-conf__suite:last-child { border-bottom: none; }
.km-conf__suitehead { display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap; }
.km-conf__suitename { font-size: 1.25rem; margin: 0; }
.km-conf__pin { color: #7a7a7a; font-size: 0.72rem; background: #141414; border: 1px solid var(--km-line); padding: 2px 7px; border-radius: 3px; }
.km-conf__blurb { color: #9a9a9a; font-size: 0.9rem; line-height: 1.55; max-width: 68ch; margin: 6px 0 16px; }
.km-conf__lanes { display: grid; grid-template-columns: repeat(2, 1fr); gap: 1px; background: var(--km-line); border: 1px solid var(--km-line); }
.km-conf__lane { background: #0e0e0e; padding: 20px; display: flex; flex-direction: column; gap: 5px; }
.km-conf__val { font-size: 2.4rem; color: var(--km-gold); line-height: 1; }
.km-conf__flag { font-weight: 700; font-size: 0.82rem; color: #cfcfcf; }
.km-conf__sub { color: #7a7a7a; font-size: 0.74rem; line-height: 1.4; }
.km-conf__foot { color: #6a6a6a; font-size: 0.82rem; margin-top: 28px; line-height: 1.6; }
.km-conf__empty { color: #7a7a7a; }
@media (max-width: 600px) { .km-conf__lanes { grid-template-columns: 1fr; } }
</style>
