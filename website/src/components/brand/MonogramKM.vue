<template>
  <!-- The KM monogram — high-contrast Didone letterforms struck in gold foil (identity study, third pass). Used for the favicon and small/quiet placements. -->
  <span class="km-mark" v-html="svg" role="img" :aria-label="title"></span>
</template>

<script setup>
import { useId, computed } from 'vue'

const props = defineProps({
  size: { type: [Number, String], default: 96 },
  title: { type: String, default: 'KlainMain monogram' }
})

// Unique, SSR-safe id prefix so multiple marks on a page don't collide on
// gradient/clip ids (same pattern as the retired VerginaSun component).
const p = useId()

const svg = computed(() => `<svg width="${props.size}" height="${props.size}" viewBox="-60 -57 120 116" xmlns="http://www.w3.org/2000/svg"><title>${props.title}</title><defs>
<linearGradient id="${p}foil" x1="0" y1="0" x2="0.85" y2="1">
  <stop offset="0" stop-color="#f9edc0"/><stop offset="0.28" stop-color="#e8cd78"/>
  <stop offset="0.52" stop-color="#c6a03c"/><stop offset="0.74" stop-color="#8f6d22"/>
  <stop offset="1" stop-color="#6e561c"/>
</linearGradient>
<linearGradient id="${p}foilUp" x1="0.85" y1="1" x2="0" y2="0">
  <stop offset="0" stop-color="#f9edc0"/><stop offset="0.35" stop-color="#dcbd63"/>
  <stop offset="0.7" stop-color="#a5822c"/><stop offset="1" stop-color="#7a5f1e"/>
</linearGradient>
<radialGradient id="${p}boss" cx="0.38" cy="0.32" r="0.9">
  <stop offset="0" stop-color="#faf0c8"/><stop offset="0.45" stop-color="#e0c268"/>
  <stop offset="0.8" stop-color="#a5822c"/><stop offset="1" stop-color="#6e561c"/>
</radialGradient>
<radialGradient id="${p}face" cx="0.42" cy="0.36" r="1.0">
  <stop offset="0" stop-color="#2a2411"/><stop offset="0.65" stop-color="#171307"/>
  <stop offset="1" stop-color="#050403"/>
</radialGradient>
<linearGradient id="${p}rim" x1="0" y1="0" x2="0.7" y2="1">
  <stop offset="0" stop-color="#fdf4cf"/><stop offset="0.25" stop-color="#c6a03c"/>
  <stop offset="0.5" stop-color="#8f6d22"/>
  <stop offset="0.75" stop-color="#e8cd78"/><stop offset="1" stop-color="#6e561c"/>
</linearGradient>
</defs><g><g transform="translate(-53,-50)"><g transform="translate(0,0) scale(1.0)"><g fill="#3a2f10" transform="translate(0.9,1.2)"><path d="M 1.50,2.00 L 1.50,98.00 L 10.50,98.00 L 10.50,2.00 Z"/><path d="M -0.50,0.40 L 12.50,0.40 L 12.50,2.00 L -0.50,2.00 Z"/><path d="M -0.50,98.00 L 12.50,98.00 L 12.50,99.60 L -0.50,99.60 Z"/><path d="M 12.38,52.81 L 41.38,3.81 L 38.62,2.19 L 9.62,51.19 Z"/><path d="M 10.05,49.03 L 39.55,99.03 L 46.45,94.97 L 16.95,44.97 Z"/><path d="M 35.50,0.40 L 44.50,0.40 L 44.50,2.00 L 35.50,2.00 Z"/><path d="M 36.00,98.00 L 47.00,98.00 L 47.00,99.60 L 36.00,99.60 Z"/><path d="M 52.75,2.00 L 52.75,98.00 L 61.25,98.00 L 61.25,2.00 Z"/><path d="M 96.75,2.00 L 96.75,98.00 L 105.25,98.00 L 105.25,2.00 Z"/><path d="M 57.95,3.40 L 77.95,80.40 L 81.05,79.60 L 61.05,2.60 Z"/><path d="M 94.62,2.04 L 75.62,79.04 L 83.38,80.96 L 102.38,3.96 Z"/><path d="M 51.00,0.40 L 63.00,0.40 L 63.00,2.00 L 51.00,2.00 Z"/><path d="M 51.00,98.00 L 63.00,98.00 L 63.00,99.60 L 51.00,99.60 Z"/><path d="M 95.00,0.40 L 107.00,0.40 L 107.00,2.00 L 95.00,2.00 Z"/><path d="M 95.00,98.00 L 107.00,98.00 L 107.00,99.60 L 95.00,99.60 Z"/></g><g fill="url(#${p}foil)"><path d="M 1.50,2.00 L 1.50,98.00 L 10.50,98.00 L 10.50,2.00 Z"/><path d="M -0.50,0.40 L 12.50,0.40 L 12.50,2.00 L -0.50,2.00 Z"/><path d="M -0.50,98.00 L 12.50,98.00 L 12.50,99.60 L -0.50,99.60 Z"/><path d="M 12.38,52.81 L 41.38,3.81 L 38.62,2.19 L 9.62,51.19 Z"/><path d="M 10.05,49.03 L 39.55,99.03 L 46.45,94.97 L 16.95,44.97 Z"/><path d="M 35.50,0.40 L 44.50,0.40 L 44.50,2.00 L 35.50,2.00 Z"/><path d="M 36.00,98.00 L 47.00,98.00 L 47.00,99.60 L 36.00,99.60 Z"/><path d="M 52.75,2.00 L 52.75,98.00 L 61.25,98.00 L 61.25,2.00 Z"/><path d="M 96.75,2.00 L 96.75,98.00 L 105.25,98.00 L 105.25,2.00 Z"/><path d="M 57.95,3.40 L 77.95,80.40 L 81.05,79.60 L 61.05,2.60 Z"/><path d="M 94.62,2.04 L 75.62,79.04 L 83.38,80.96 L 102.38,3.96 Z"/><path d="M 51.00,0.40 L 63.00,0.40 L 63.00,2.00 L 51.00,2.00 Z"/><path d="M 51.00,98.00 L 63.00,98.00 L 63.00,99.60 L 51.00,99.60 Z"/><path d="M 95.00,0.40 L 107.00,0.40 L 107.00,2.00 L 95.00,2.00 Z"/><path d="M 95.00,98.00 L 107.00,98.00 L 107.00,99.60 L 95.00,99.60 Z"/></g><g fill="none"><path d="M 1.50,2.00 L 1.50,98.00 L 10.50,98.00 L 10.50,2.00 Z"/><path d="M -0.50,0.40 L 12.50,0.40 L 12.50,2.00 L -0.50,2.00 Z"/><path d="M -0.50,98.00 L 12.50,98.00 L 12.50,99.60 L -0.50,99.60 Z"/><path d="M 12.38,52.81 L 41.38,3.81 L 38.62,2.19 L 9.62,51.19 Z"/><path d="M 10.05,49.03 L 39.55,99.03 L 46.45,94.97 L 16.95,44.97 Z"/><path d="M 35.50,0.40 L 44.50,0.40 L 44.50,2.00 L 35.50,2.00 Z"/><path d="M 36.00,98.00 L 47.00,98.00 L 47.00,99.60 L 36.00,99.60 Z"/><path d="M 52.75,2.00 L 52.75,98.00 L 61.25,98.00 L 61.25,2.00 Z"/><path d="M 96.75,2.00 L 96.75,98.00 L 105.25,98.00 L 105.25,2.00 Z"/><path d="M 57.95,3.40 L 77.95,80.40 L 81.05,79.60 L 61.05,2.60 Z"/><path d="M 94.62,2.04 L 75.62,79.04 L 83.38,80.96 L 102.38,3.96 Z"/><path d="M 51.00,0.40 L 63.00,0.40 L 63.00,2.00 L 51.00,2.00 Z"/><path d="M 51.00,98.00 L 63.00,98.00 L 63.00,99.60 L 51.00,99.60 Z"/><path d="M 95.00,0.40 L 107.00,0.40 L 107.00,2.00 L 95.00,2.00 Z"/><path d="M 95.00,98.00 L 107.00,98.00 L 107.00,99.60 L 95.00,99.60 Z"/></g></g></g></g></svg>`)
</script>

<style scoped>
.km-mark { display: block; line-height: 0; }
</style>
