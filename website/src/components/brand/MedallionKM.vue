<template>
  <!-- The medallion — a struck gold coin: beveled rim, dark polished face, the faithful sixteen-ray sun (8 major + 8 minor, true anatomy) as fine engraving around the KM monogram. The primary mark. -->
  <span class="km-mark" v-html="svg" role="img" :aria-label="title"></span>
</template>

<script setup>
import { useId, computed } from 'vue'

const props = defineProps({
  size: { type: [Number, String], default: 96 },
  title: { type: String, default: 'KlainMain medallion' }
})

// Unique, SSR-safe id prefix so multiple marks on a page don't collide on
// gradient/clip ids (same pattern as the retired VerginaSun component).
const p = useId()

const svg = computed(() => `<svg width="${props.size}" height="${props.size}" viewBox="-110 -110 220 220" xmlns="http://www.w3.org/2000/svg"><title>${props.title}</title><defs>
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
</defs><g><circle r="96" fill="url(#${p}rim)"/><circle r="90" fill="url(#${p}boss)"/><circle r="84" fill="url(#${p}face)"/><circle r="84" fill="none" stroke="#000" stroke-opacity="0.6" stroke-width="1"/><circle r="86.8" fill="none" stroke="#f9edc0" stroke-opacity="0.35" stroke-width="0.8"/><defs><path id="${p}egM" d="M -6.76,-22.03 A 6.76,6.76 0 0 0 6.76,-22.03 L 0,-92.00 Z"/><path id="${p}egN" d="M -5.28,-37.96 A 5.28,5.28 0 0 0 5.28,-37.96 L 0,-92.00 Z"/></defs><mask id="${p}ann"><rect x="-110" y="-110" width="220" height="220" fill="#fff"/><circle r="30" fill="#000"/></mask><g mask="url(#${p}ann)"><g transform="scale(0.80)" stroke="url(#${p}foilUp)" stroke-width="1.1" fill="none" stroke-linejoin="round"><use href="#${p}egM" transform="rotate(0)"/><use href="#${p}egM" transform="rotate(45)"/><use href="#${p}egM" transform="rotate(90)"/><use href="#${p}egM" transform="rotate(135)"/><use href="#${p}egM" transform="rotate(180)"/><use href="#${p}egM" transform="rotate(225)"/><use href="#${p}egM" transform="rotate(270)"/><use href="#${p}egM" transform="rotate(315)"/><use href="#${p}egN" transform="rotate(22.5)"/><use href="#${p}egN" transform="rotate(67.5)"/><use href="#${p}egN" transform="rotate(112.5)"/><use href="#${p}egN" transform="rotate(157.5)"/><use href="#${p}egN" transform="rotate(202.5)"/><use href="#${p}egN" transform="rotate(247.5)"/><use href="#${p}egN" transform="rotate(292.5)"/><use href="#${p}egN" transform="rotate(337.5)"/></g><g transform="scale(0.80)" stroke="#000" stroke-opacity="0.55" stroke-width="0.4" fill="none"><use href="#${p}egM" transform="rotate(0)"/><use href="#${p}egM" transform="rotate(45)"/><use href="#${p}egM" transform="rotate(90)"/><use href="#${p}egM" transform="rotate(135)"/><use href="#${p}egM" transform="rotate(180)"/><use href="#${p}egM" transform="rotate(225)"/><use href="#${p}egM" transform="rotate(270)"/><use href="#${p}egM" transform="rotate(315)"/><use href="#${p}egN" transform="rotate(22.5)"/><use href="#${p}egN" transform="rotate(67.5)"/><use href="#${p}egN" transform="rotate(112.5)"/><use href="#${p}egN" transform="rotate(157.5)"/><use href="#${p}egN" transform="rotate(202.5)"/><use href="#${p}egN" transform="rotate(247.5)"/><use href="#${p}egN" transform="rotate(292.5)"/><use href="#${p}egN" transform="rotate(337.5)"/></g></g><circle r="24" fill="url(#${p}face)"/><circle r="24" fill="none" stroke="url(#${p}foilUp)" stroke-width="1.4"/><g transform="translate(-13.8,-13) scale(0.26)"><g fill="#0c0a04" transform="translate(0.6,0.8)"><path d="M 1.50,2.00 L 1.50,98.00 L 10.50,98.00 L 10.50,2.00 Z"/><path d="M -0.50,0.40 L 12.50,0.40 L 12.50,2.00 L -0.50,2.00 Z"/><path d="M -0.50,98.00 L 12.50,98.00 L 12.50,99.60 L -0.50,99.60 Z"/><path d="M 12.38,52.81 L 41.38,3.81 L 38.62,2.19 L 9.62,51.19 Z"/><path d="M 10.05,49.03 L 39.55,99.03 L 46.45,94.97 L 16.95,44.97 Z"/><path d="M 35.50,0.40 L 44.50,0.40 L 44.50,2.00 L 35.50,2.00 Z"/><path d="M 36.00,98.00 L 47.00,98.00 L 47.00,99.60 L 36.00,99.60 Z"/><path d="M 52.75,2.00 L 52.75,98.00 L 61.25,98.00 L 61.25,2.00 Z"/><path d="M 96.75,2.00 L 96.75,98.00 L 105.25,98.00 L 105.25,2.00 Z"/><path d="M 57.95,3.40 L 77.95,80.40 L 81.05,79.60 L 61.05,2.60 Z"/><path d="M 94.62,2.04 L 75.62,79.04 L 83.38,80.96 L 102.38,3.96 Z"/><path d="M 51.00,0.40 L 63.00,0.40 L 63.00,2.00 L 51.00,2.00 Z"/><path d="M 51.00,98.00 L 63.00,98.00 L 63.00,99.60 L 51.00,99.60 Z"/><path d="M 95.00,0.40 L 107.00,0.40 L 107.00,2.00 L 95.00,2.00 Z"/><path d="M 95.00,98.00 L 107.00,98.00 L 107.00,99.60 L 95.00,99.60 Z"/></g><g fill="url(#${p}foil)"><path d="M 1.50,2.00 L 1.50,98.00 L 10.50,98.00 L 10.50,2.00 Z"/><path d="M -0.50,0.40 L 12.50,0.40 L 12.50,2.00 L -0.50,2.00 Z"/><path d="M -0.50,98.00 L 12.50,98.00 L 12.50,99.60 L -0.50,99.60 Z"/><path d="M 12.38,52.81 L 41.38,3.81 L 38.62,2.19 L 9.62,51.19 Z"/><path d="M 10.05,49.03 L 39.55,99.03 L 46.45,94.97 L 16.95,44.97 Z"/><path d="M 35.50,0.40 L 44.50,0.40 L 44.50,2.00 L 35.50,2.00 Z"/><path d="M 36.00,98.00 L 47.00,98.00 L 47.00,99.60 L 36.00,99.60 Z"/><path d="M 52.75,2.00 L 52.75,98.00 L 61.25,98.00 L 61.25,2.00 Z"/><path d="M 96.75,2.00 L 96.75,98.00 L 105.25,98.00 L 105.25,2.00 Z"/><path d="M 57.95,3.40 L 77.95,80.40 L 81.05,79.60 L 61.05,2.60 Z"/><path d="M 94.62,2.04 L 75.62,79.04 L 83.38,80.96 L 102.38,3.96 Z"/><path d="M 51.00,0.40 L 63.00,0.40 L 63.00,2.00 L 51.00,2.00 Z"/><path d="M 51.00,98.00 L 63.00,98.00 L 63.00,99.60 L 51.00,99.60 Z"/><path d="M 95.00,0.40 L 107.00,0.40 L 107.00,2.00 L 95.00,2.00 Z"/><path d="M 95.00,98.00 L 107.00,98.00 L 107.00,99.60 L 95.00,99.60 Z"/></g></g></g></svg>`)
</script>

<style scoped>
.km-mark { display: block; line-height: 0; }
</style>
