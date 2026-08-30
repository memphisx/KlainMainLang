<template>
  <q-layout view="hHh LpR lFf">
    <q-header class="km-docheader">
      <div class="km-docheader__inner">
        <q-btn flat dense round icon="menu" class="lt-md km-docheader__burger" @click="drawer = !drawer" aria-label="Menu" />
        <router-link to="/" class="km-docheader__brand" aria-label="Home">
          <MonogramKM :size="28" />
          <Wordmark :stacked="false" size="sm" />
        </router-link>
        <q-space />
        <button type="button" class="km-searchbtn" @click="searchOpen = true" aria-label="Search documentation">
          <q-icon name="search" size="16px" />
          <span class="km-searchbtn__label gt-sm">Search</span>
          <kbd class="km-searchbtn__kbd gt-sm">⌘F</kbd>
        </button>
        <router-link to="/docs/getting-started" class="km-navlink gt-sm">Get started</router-link>
        <a :href="gh" target="_blank" rel="noopener" class="km-navlink">GitHub</a>
      </div>
    </q-header>

    <ReferenceSearch v-model="searchOpen" />

    <q-drawer
      v-model="drawer"
      show-if-above
      :width="288"
      :breakpoint="1023"
      class="km-docdrawer"
    >
      <q-scroll-area class="fit" :visible="false">
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
            <button type="button" class="km-docnav__treetoggle" @click="refOpen = !refOpen" :aria-expanded="refOpen">
              <q-icon :name="refOpen ? 'expand_more' : 'chevron_right'" size="16px" />
              <span class="km-eyebrow km-docnav__label km-docnav__label--inline">API reference</span>
            </button>
            <div v-show="refOpen" class="km-docnav__tree">
              <template v-for="group in refGroups" :key="group.label">
                <span class="km-docnav__subhead">{{ group.label }}</span>
                <router-link
                  v-for="item in group.items"
                  :key="item.to"
                  :to="item.to"
                  class="km-docnav__link km-docnav__link--nested"
                  active-class="km-docnav__link--active"
                  exact-active-class="km-docnav__link--active"
                >{{ item.text }}</router-link>
              </template>
            </div>
          </div>

          <div class="km-docnav__group">
            <button type="button" class="km-docnav__treetoggle" @click="klainOpen = !klainOpen" :aria-expanded="klainOpen">
              <q-icon :name="klainOpen ? 'expand_more' : 'chevron_right'" size="16px" />
              <span class="km-eyebrow km-docnav__label km-docnav__label--inline">klain:</span>
            </button>
            <div v-show="klainOpen" class="km-docnav__tree">
              <router-link
                v-for="item in klainTree"
                :key="item.to"
                :to="item.to"
                class="km-docnav__link km-docnav__link--nested"
                active-class="km-docnav__link--active"
                exact-active-class="km-docnav__link--active"
              >{{ item.text }}</router-link>
            </div>
          </div>

          <div class="km-docnav__group">
            <button type="button" class="km-docnav__treetoggle" @click="guidesOpen = !guidesOpen" :aria-expanded="guidesOpen">
              <q-icon :name="guidesOpen ? 'expand_more' : 'chevron_right'" size="16px" />
              <span class="km-eyebrow km-docnav__label km-docnav__label--inline">Guides</span>
            </button>
            <div v-show="guidesOpen" class="km-docnav__tree">
              <router-link
                v-for="item in guidesTree"
                :key="item.to"
                :to="item.to"
                class="km-docnav__link km-docnav__link--nested"
                active-class="km-docnav__link--active"
                exact-active-class="km-docnav__link--active"
              >{{ item.text }}</router-link>
            </div>
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
import MonogramKM from 'components/brand/MonogramKM.vue'
import ExampleTree from 'components/docs/ExampleTree.vue'
import ReferenceSearch from 'components/docs/ReferenceSearch.vue'
import examplesTree from 'src/data/examples-tree.json'
import { GITHUB_URL } from 'src/lib/content.js'
import { index as referenceIndex } from 'src/data/reference-index.js'

const gh = GITHUB_URL
const drawer = ref(false)
const searchOpen = ref(false)
const route = useRoute()

// Current example key, e.g. "async/promise_all" — drives tree auto-expand.
const activeExample = computed(() => {
  const m = route.path.match(/^\/docs\/examples\/(.+)$/)
  return m ? m[1] : ''
})

// The klain: namespace tree — one leaf per module, grows as modules land.
const klainTree = [
  { to: '/docs/klain', text: 'Overview' },
  { to: '/docs/klain/webview', text: 'klain:webview' },
  { to: '/docs/klain/http', text: 'klain:http' },
  { to: '/docs/klain/sync', text: 'klain:sync' }
]
const klainOpen = ref(true)
// Auto-expand when viewing any klain page.
if (route.path.startsWith('/docs/klain')) klainOpen.value = true

// The Guides tree — long-form walkthroughs, grows as guides land.
const guidesTree = [
  { to: '/docs/guides', text: 'Overview' },
  { to: '/docs/guides/tui-app', text: 'Build a TUI app' }
]
const guidesOpen = ref(true)
if (route.path.startsWith('/docs/guides')) guidesOpen.value = true

// The per-API reference tree — from the generated index (ordered by kind, then
// title), grouped under kind sub-headers so a 32-surface list stays scannable.
// A new surface JSON appears here automatically.
const KIND_LABELS = { language: 'Language', web: 'Web APIs', node: 'Node.js' }
const refGroups = ['language', 'web', 'node']
  .map((k) => ({
    label: KIND_LABELS[k],
    items: referenceIndex.filter((m) => m.kind === k).map((m) => ({ to: '/docs/reference/' + m.surface, text: m.title }))
  }))
  .filter((g) => g.items.length)
const refOpen = ref(true)

const nav = [
  {
    label: 'Start',
    items: [
      { to: '/docs', text: 'Overview' },
      { to: '/docs/install', text: 'Installation' },
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
  background: #0a0a0a;
  border-bottom: 1px solid var(--km-line);
  box-shadow: none;
  z-index: 2001; /* above the drawer, so the bar reads as a solid full-width top */
}
.km-docheader__inner { display: flex; align-items: center; gap: 14px; height: 64px; padding: 0 20px; }
.km-docheader__brand { color: #fff; text-decoration: none; display: inline-flex; align-items: center; gap: 10px; }
.km-docheader__brand .km-mark { display: inline-flex; }
.km-navlink {
  color: #fff; text-decoration: none; font-size: 0.78rem; font-weight: 600;
  letter-spacing: 0.12em; text-transform: uppercase; opacity: 0.8; margin-left: 22px;
}
.km-navlink:hover { color: var(--km-gold); opacity: 1; }

.km-searchbtn {
  display: inline-flex; align-items: center; gap: 8px; margin-left: 22px;
  background: rgba(255,255,255,0.05); border: 1px solid var(--km-line); color: #b6b6b6;
  border-radius: 6px; padding: 6px 10px; cursor: pointer; transition: all .14s ease;
}
.km-searchbtn:hover { color: #fff; border-color: #555; }
.km-searchbtn__label { font-size: 0.82rem; }
.km-searchbtn__kbd {
  font-size: 0.66rem; color: #8a8a8a; background: #0d0d0d;
  border: 1px solid var(--km-line); border-radius: 4px; padding: 1px 5px;
}

.km-docdrawer { background: #0c0c0c; border-right: 1px solid var(--km-line); }
.km-docnav { padding: 28px 20px 30px; }
.km-docnav__group { margin-bottom: 30px; }
.km-docnav__label { display: block; color: #6a6a6a; margin-bottom: 12px; }
.km-docnav__link {
  display: block; color: #c2c2c2; text-decoration: none; font-size: 0.93rem;
  padding: 7px 12px; border-left: 2px solid transparent; transition: all .14s ease;
}
.km-docnav__link:hover { color: #fff; border-left-color: #444; }
.km-docnav__link--active { color: var(--km-gold); border-left-color: var(--km-gold); background: rgba(198,160,60,0.06); }

/* Collapsible klain: tree group */
.km-docnav__treetoggle {
  display: flex; align-items: center; gap: 4px;
  background: none; border: 0; padding: 0 0 12px; cursor: pointer;
}
.km-docnav__treetoggle .q-icon { color: #6a6a6a; }
.km-docnav__label--inline { display: inline; margin-bottom: 0; }
.km-docnav__tree { display: flex; flex-direction: column; }
.km-docnav__link--nested { padding-left: 26px; font-size: 0.88rem; }
.km-docnav__subhead {
  display: block; font-size: 0.64rem; letter-spacing: 0.14em; text-transform: uppercase;
  color: #5f5f5f; font-weight: 700; padding: 14px 0 5px 12px;
}
.km-docnav__subhead:first-child { padding-top: 4px; }

.km-docpage { background: var(--km-black); }
.km-docpage__inner { max-width: 820px; margin: 0 auto; padding: clamp(32px, 5vw, 64px) clamp(20px, 4vw, 48px) 120px; }
</style>
