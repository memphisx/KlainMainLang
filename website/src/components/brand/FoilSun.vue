<template>
  <!-- The foil sun — the faithful sixteen-ray sun geometry kept whole (8+8, two sizes, concave bases), struck in gold foil with a polished boss replacing the rosette. Respectful homage in metal; hero/section-scale art. -->
  <span class="km-mark" v-html="svg" role="img" :aria-label="title"></span>
</template>

<script setup>
import { useId, computed } from 'vue'

const props = defineProps({
  size: { type: [Number, String], default: 96 },
  title: { type: String, default: 'Gold sun' }
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
</defs><g><defs><path id="${p}fsM" d="M -6.76,-22.03 A 6.76,6.76 0 0 0 6.76,-22.03 L 0,-92.00 Z"/><path id="${p}fsN" d="M -5.28,-37.96 A 5.28,5.28 0 0 0 5.28,-37.96 L 0,-92.00 Z"/></defs><g transform="translate(1.4,1.8)" fill="#000" fill-opacity="0.5"><use href="#${p}fsM" transform="rotate(0)"/><use href="#${p}fsM" transform="rotate(45)"/><use href="#${p}fsM" transform="rotate(90)"/><use href="#${p}fsM" transform="rotate(135)"/><use href="#${p}fsM" transform="rotate(180)"/><use href="#${p}fsM" transform="rotate(225)"/><use href="#${p}fsM" transform="rotate(270)"/><use href="#${p}fsM" transform="rotate(315)"/><use href="#${p}fsN" transform="rotate(22.5)"/><use href="#${p}fsN" transform="rotate(67.5)"/><use href="#${p}fsN" transform="rotate(112.5)"/><use href="#${p}fsN" transform="rotate(157.5)"/><use href="#${p}fsN" transform="rotate(202.5)"/><use href="#${p}fsN" transform="rotate(247.5)"/><use href="#${p}fsN" transform="rotate(292.5)"/><use href="#${p}fsN" transform="rotate(337.5)"/></g><g fill="url(#${p}foil)" stroke="#57430f" stroke-width="0.8" stroke-linejoin="round"><use href="#${p}fsM" transform="rotate(0)"/><use href="#${p}fsM" transform="rotate(45)"/><use href="#${p}fsM" transform="rotate(90)"/><use href="#${p}fsM" transform="rotate(135)"/><use href="#${p}fsM" transform="rotate(180)"/><use href="#${p}fsM" transform="rotate(225)"/><use href="#${p}fsM" transform="rotate(270)"/><use href="#${p}fsM" transform="rotate(315)"/><use href="#${p}fsN" transform="rotate(22.5)"/><use href="#${p}fsN" transform="rotate(67.5)"/><use href="#${p}fsN" transform="rotate(112.5)"/><use href="#${p}fsN" transform="rotate(157.5)"/><use href="#${p}fsN" transform="rotate(202.5)"/><use href="#${p}fsN" transform="rotate(247.5)"/><use href="#${p}fsN" transform="rotate(292.5)"/><use href="#${p}fsN" transform="rotate(337.5)"/></g><g transform="scale(0.985)" fill="url(#${p}foilUp)" fill-opacity="0.35"><use href="#${p}fsM" transform="rotate(0)"/><use href="#${p}fsM" transform="rotate(45)"/><use href="#${p}fsM" transform="rotate(90)"/><use href="#${p}fsM" transform="rotate(135)"/><use href="#${p}fsM" transform="rotate(180)"/><use href="#${p}fsM" transform="rotate(225)"/><use href="#${p}fsM" transform="rotate(270)"/><use href="#${p}fsM" transform="rotate(315)"/><use href="#${p}fsN" transform="rotate(22.5)"/><use href="#${p}fsN" transform="rotate(67.5)"/><use href="#${p}fsN" transform="rotate(112.5)"/><use href="#${p}fsN" transform="rotate(157.5)"/><use href="#${p}fsN" transform="rotate(202.5)"/><use href="#${p}fsN" transform="rotate(247.5)"/><use href="#${p}fsN" transform="rotate(292.5)"/><use href="#${p}fsN" transform="rotate(337.5)"/></g><circle r="13.4" fill="none" stroke="url(#${p}foilUp)" stroke-width="1.2"/><circle r="11.5" fill="none" stroke="url(#${p}foilUp)" stroke-width="0.8"/><circle r="9.7" fill="url(#${p}boss)" stroke="#57430f" stroke-width="0.6"/></g></svg>`)
</script>

<style scoped>
.km-mark { display: block; line-height: 0; }
</style>
