<template>
  <!-- Faithful Vergina Sun: 8 major + 8 minor rays with concave circular
       bases, concentric central rings, and a 10-petal rosette. Geometry
       reproduced from the public-domain WIPO/Wikimedia reference drawing. -->
  <svg
    class="km-vergina"
    :width="size"
    :height="size"
    viewBox="-292 -292 584 584"
    role="img"
    :aria-label="title"
    xmlns="http://www.w3.org/2000/svg"
  >
    <title>{{ title }}</title>
    <defs>
      <clipPath :id="id('m1')"><path :id="id('ray1')" d="m-21-68.5a21,21 0 0 0 42,0l-21-217.5z m0,137a21,21 0 0 1 42,0l-21,217.5z" stroke-linejoin="round" /></clipPath>
      <clipPath :id="id('m2')"><path :id="id('ray2')" d="m-16.4-118a16.4,16.4 0 0 0 32.8,0l-16.4-168z m0,236a16.4,16.4 0 0 1 32.8,0l-16.4,168z" transform="rotate(22.5)" stroke-linejoin="round" /></clipPath>
      <clipPath :id="id('m3')"><path :id="id('flow1')" d="m0,0 -7-22.5a7,7 0 0 1 14,0 l-14,45a7,7 0 0 0 14,0z" /></clipPath>
      <clipPath :id="id('m4')"><path :id="id('flow2')" d="m0,0 -5.797159-17.84182a5.4,6.2 0 0 1 11.594318,0 l-11.594318,35.68364a5.4,6.2 0 0 0 11.594318,0z" /></clipPath>
    </defs>

    <!-- rays -->
    <g v-for="rot in [0, 45, 90, 135]" :key="'r' + rot" :transform="`rotate(${rot})`">
      <g :clip-path="`url(#${id('m1')})`">
        <use :href="`#${id('ray1')}`" :fill="cLight" :stroke="cMid" stroke-width="23" />
        <use :href="`#${id('ray1')}`" fill="none" :stroke="cDeep" stroke-width="10" />
      </g>
      <use :href="`#${id('ray1')}`" fill="none" :stroke="cLine" />
      <g :clip-path="`url(#${id('m2')})`">
        <use :href="`#${id('ray2')}`" :fill="cLight" :stroke="cMid" stroke-width="23" />
        <use :href="`#${id('ray2')}`" fill="none" :stroke="cDeep" stroke-width="10" />
      </g>
      <use :href="`#${id('ray2')}`" fill="none" :stroke="cLine" />
    </g>

    <!-- central rings -->
    <g fill="none" :stroke="cLine">
      <circle :fill="cDeep" r="30.2" />
      <circle stroke-width="3" r="35.8" />
      <circle :stroke="cDeep" r="35.8" />
      <circle stroke-width="3" r="41.6" />
      <circle :stroke="cDeep" r="41.6" />
    </g>

    <!-- rosette: 10 larger petals -->
    <g v-for="rot in [0, 36, 72, 108, 144]" :key="'p' + rot" :transform="`rotate(${rot - 18})`">
      <g :clip-path="`url(#${id('m3')})`">
        <use :href="`#${id('flow1')}`" :fill="cLight" :stroke="cMid" stroke-width="7" />
        <use :href="`#${id('flow1')}`" fill="none" :stroke="cDeep" stroke-width="4" />
      </g>
      <use :href="`#${id('flow1')}`" fill="none" :stroke="cLine" />
    </g>
    <!-- rosette: 10 smaller petals -->
    <g v-for="rot in [0, 36, 72, 108, 144]" :key="'q' + rot" :transform="`rotate(${rot})`">
      <g :clip-path="`url(#${id('m4')})`">
        <use :href="`#${id('flow2')}`" :fill="cLight" :stroke="cMid" stroke-width="6" />
        <use :href="`#${id('flow2')}`" fill="none" :stroke="cDeep" stroke-width="3" />
      </g>
      <use :href="`#${id('flow2')}`" fill="none" :stroke="cLine" />
    </g>
  </svg>
</template>

<script setup>
import { useId } from 'vue'

defineProps({
  size: { type: [Number, String], default: 96 },
  title: { type: String, default: 'Vergina Sun' }
})

// Unique, SSR-safe id prefix so multiple suns on a page don't collide.
const uid = useId()
const id = (k) => `${uid}${k}`

// Brand-tuned gold, keeping the 3-tone dimensionality of the original.
const cLight = '#e8cd78'
const cMid = '#c6a03c'
const cDeep = '#a5822c'
const cLine = '#6e561c'
</script>

<style scoped>
.km-vergina { display: block; }
</style>
