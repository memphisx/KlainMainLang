<template>
  <q-layout view="lHh LpR lFf">
    <q-header class="km-docheader">
      <div class="km-docheader__inner">
        <q-btn flat dense round icon="menu" class="lt-md km-docheader__burger" @click="drawer = !drawer" aria-label="Menu" />
        <router-link to="/" class="km-docheader__brand" aria-label="Home">
          <Wordmark :stacked="false" size="sm" />
        </router-link>
        <q-space />
        <router-link to="/docs/getting-started" class="km-navlink gt-sm">Get started</router-link>
        <a :href="gh" target="_blank" rel="noopener" class="km-navlink">GitHub</a>
      </div>
    </q-header>

    <q-drawer
      v-model="drawer"
      show-if-above
      :width="288"
      :breakpoint="1023"
      class="km-docdrawer"
    >
      <q-scroll-area class="fit">
        <nav class="km-docnav">
          <div v-for="group in nav" :key="group.label" class="km-docnav__group">
            <span class="km-eyebrow km-docnav__label">{{ group.label }}</span>
            <router-link
              v-for="item in group.items"
              :key="item.to"
              :to="item.to"
              class="km-docnav__link"
              active-class="km-docnav__link--active"
              exact-active-class="km-docnav__link--active"
            >{{ item.text }}</router-link>
          </div>

          <div class="km-docnav__group">
            <span class="km-eyebrow km-docnav__label">Examples</span>
            <router-link
              to="/docs/examples"
              class="km-docnav__link"
              active-class="km-docnav__link--active"
              exact-active-class="km-docnav__link--active"
            >Overview</router-link>
            <ExampleTree :nodes="examplesTree" :active="activeExample" />
          </div>
        </nav>
      </q-scroll-area>
    </q-drawer>

    <q-page-container>
      <q-page class="km-docpage">
        <div class="km-docpage__inner">
          <router-view />
        </div>
      </q-page>
    </q-page-container>
  </q-layout>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRoute } from 'vue-router'
import Wordmark from 'components/brand/Wordmark.vue'
import ExampleTree from 'components/docs/ExampleTree.vue'
import examplesTree from 'src/data/examples-tree.json'
import { GITHUB_URL } from 'src/lib/content.js'

const gh = GITHUB_URL
const drawer = ref(false)
const route = useRoute()

// Current example key, e.g. "async/promise_all" — drives tree auto-expand.
const activeExample = computed(() => {
  const m = route.path.match(/^\/docs\/examples\/(.+)$/)
  return m ? m[1] : ''
})

const nav = [
  {
    label: 'Start',
    items: [
      { to: '/docs', text: 'Overview' },
      { to: '/docs/getting-started', text: 'Getting started' }
    ]
  },
  {
    label: 'Guide',
    items: [
      { to: '/docs/language', text: 'Language guide' },
      { to: '/docs/stdlib', text: 'Standard library' },
      { to: '/docs/cli', text: 'CLI & flags' }
    ]
  },
  {
    label: 'Reference',
    items: [
      { to: '/docs/coverage', text: 'Coverage matrix' }
    ]
  }
]
</script>

<style scoped>
.km-docheader {
  background: rgba(10,10,10,0.9);
  backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--km-line);
  box-shadow: none;
}
.km-docheader__inner { display: flex; align-items: center; gap: 14px; height: 64px; padding: 0 20px; }
.km-docheader__brand { color: #fff; text-decoration: none; }
.km-navlink {
  color: #fff; text-decoration: none; font-size: 0.78rem; font-weight: 600;
  letter-spacing: 0.12em; text-transform: uppercase; opacity: 0.8; margin-left: 22px;
}
.km-navlink:hover { color: var(--km-gold); opacity: 1; }

.km-docdrawer { background: #0c0c0c; border-right: 1px solid var(--km-line); }
.km-docnav { padding: 88px 20px 60px; }
.km-docnav__group { margin-bottom: 30px; }
.km-docnav__label { display: block; color: #6a6a6a; margin-bottom: 12px; }
.km-docnav__link {
  display: block; color: #c2c2c2; text-decoration: none; font-size: 0.93rem;
  padding: 7px 12px; border-left: 2px solid transparent; transition: all .14s ease;
}
.km-docnav__link:hover { color: #fff; border-left-color: #444; }
.km-docnav__link--active { color: var(--km-gold); border-left-color: var(--km-gold); background: rgba(198,160,60,0.06); }

.km-docpage { background: var(--km-black); }
.km-docpage__inner { max-width: 820px; margin: 0 auto; padding: clamp(32px, 5vw, 64px) clamp(20px, 4vw, 48px) 120px; }
</style>
