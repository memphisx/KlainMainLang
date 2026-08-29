<template>
  <q-layout view="hHh lpR fFf">
    <q-header :class="['km-header', { 'km-header--solid': scrolled }]">
      <div class="km-wrap km-header__inner">
        <router-link to="/" class="km-header__brand" aria-label="Κλάιν Μάιν — home">
          <Wordmark :stacked="false" size="sm" />
          <span class="km-header__tag km-mono">// TS → native</span>
        </router-link>

        <nav class="km-header__nav">
          <router-link to="/docs" class="km-navlink">Docs</router-link>
          <router-link to="/docs/getting-started" class="km-navlink gt-sm">Get started</router-link>
          <router-link to="/docs/coverage" class="km-navlink gt-sm">Coverage</router-link>
          <a :href="gh" target="_blank" rel="noopener" class="km-navlink">GitHub</a>
          <router-link to="/docs/install" class="km-btn km-btn--gold km-header__cta">
            <q-icon name="download" size="16px" /> Install
          </router-link>
        </nav>
      </div>
    </q-header>

    <q-page-container>
      <router-view />
    </q-page-container>

    <SiteFooter />
  </q-layout>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import Wordmark from 'components/brand/Wordmark.vue'
import SiteFooter from 'components/SiteFooter.vue'
import { GITHUB_URL } from 'src/lib/content.js'

const gh = GITHUB_URL
const scrolled = ref(false)

function onScroll () {
  scrolled.value = window.scrollY > 24
}
onMounted(() => {
  window.addEventListener('scroll', onScroll, { passive: true })
  onScroll()
})
onUnmounted(() => window.removeEventListener('scroll', onScroll))
</script>

<style scoped>
.km-header {
  background: transparent;
  box-shadow: none;
  transition: background .3s ease, border-color .3s ease, backdrop-filter .3s ease;
  border-bottom: 1px solid transparent;
  color: #fff;
}
.km-header--solid {
  background: rgba(10, 10, 10, 0.82);
  backdrop-filter: saturate(140%) blur(12px);
  border-bottom-color: var(--km-line);
}
.km-header__inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 72px;
}
.km-header__brand {
  display: inline-flex;
  align-items: baseline;
  gap: 12px;
  color: #fff;
  text-decoration: none;
}
.km-header__tag { font-size: 0.7rem; color: var(--km-gold); letter-spacing: 0.1em; }
.km-header__nav { display: flex; align-items: center; gap: clamp(14px, 2.4vw, 30px); }
.km-navlink {
  color: #fff;
  text-decoration: none;
  font-size: 0.8rem;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  opacity: 0.82;
  transition: opacity .15s ease, color .15s ease;
}
.km-navlink:hover { opacity: 1; color: var(--km-gold); }
.km-header__cta { padding: 10px 18px; }
@media (max-width: 599px) {
  .km-header__tag { display: none; }
  .km-header__cta span { display: none; }
}
</style>
