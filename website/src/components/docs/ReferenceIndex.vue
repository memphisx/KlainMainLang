<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">API reference</span>
    <h1>{{ data.title }}</h1>

    <div class="km-refidx__cov">
      <span class="km-refidx__covitem">
        <span class="km-refidx__covnum">{{ data.coverage.loose }}</span>
        <span class="km-refidx__covlabel">Coverage</span>
      </span>
      <span class="km-refidx__covitem">
        <span class="km-refidx__covnum">{{ data.coverage.strict }}</span>
        <span class="km-refidx__covlabel">Strict (zero caveats)</span>
      </span>
    </div>

    <p v-if="data.lede" class="km-doc__lede">{{ data.lede }}</p>

    <div class="km-refidx__legend">
      <span class="km-refidx__lg"><i class="km-refidx__dot--exact"></i> Exact</span>
      <span class="km-refidx__lg"><i class="km-refidx__dot--caveats"></i> Works, with caveats</span>
      <span class="km-refidx__lg"><i class="km-refidx__dot--missing"></i> Not supported</span>
    </div>

    <nav class="km-refidx__jump">
      <a
        v-for="e in data.entries"
        :key="e.id"
        :href="'#' + e.id"
        class="km-refidx__jumplink km-mono"
        :class="'km-refidx__jumplink--' + e.badge"
      >{{ e.name }}</a>
    </nav>

    <ReferenceEntry
      v-for="e in data.entries"
      :key="e.id"
      :entry="e"
      :names="names"
    />
  </article>
</template>

<script setup>
import { computed } from 'vue'
import ReferenceEntry from './ReferenceEntry.vue'

const props = defineProps({
  data: { type: Object, required: true }
})

// id → display name, for "See also" chips.
const names = computed(() => {
  const m = {}
  for (const e of props.data.entries) m[e.id] = e.name
  return m
})
</script>

<style scoped>
.km-refidx__cov { display: flex; gap: 32px; margin: 6px 0 20px; }
.km-refidx__covitem { display: flex; flex-direction: column; }
.km-refidx__covnum { font-size: 1.5rem; font-weight: 700; color: var(--km-gold); font-variant-numeric: tabular-nums; }
.km-refidx__covlabel { font-size: 0.72rem; letter-spacing: 0.1em; text-transform: uppercase; color: #7a7a7a; }

.km-refidx__legend { display: flex; gap: 20px; flex-wrap: wrap; margin: 18px 0 24px; }
.km-refidx__lg { display: inline-flex; align-items: center; gap: 7px; font-size: 0.8rem; color: #b0b0b0; }
.km-refidx__legend i { width: 9px; height: 9px; border-radius: 50%; display: inline-block; }
.km-refidx__dot--exact { background: #6fd18a; }
.km-refidx__dot--caveats { background: #e2b95a; }
.km-refidx__dot--missing { background: #9a9a9a; }

.km-refidx__jump {
  display: flex; flex-wrap: wrap; gap: 8px;
  padding: 18px; margin-bottom: 8px;
  background: #0e0e0e; border: 1px solid var(--km-line);
}
.km-refidx__jumplink {
  text-decoration: none; font-size: 0.8rem; color: #c2c2c2;
  padding: 4px 10px; border-radius: 3px; border: 1px solid transparent;
  border-left-width: 3px;
}
.km-refidx__jumplink:hover { color: #fff; border-color: #333; }
.km-refidx__jumplink--exact { border-left-color: #6fd18a; }
.km-refidx__jumplink--caveats { border-left-color: #e2b95a; }
.km-refidx__jumplink--missing { border-left-color: #9a9a9a; opacity: 0.72; }
</style>
