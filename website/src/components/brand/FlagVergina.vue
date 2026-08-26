<template>
  <!-- The Greek flag, its canton cross swapped for the gold Vergina Sun. -->
  <svg
    class="km-flag"
    :width="width"
    :height="width * (18 / 27)"
    viewBox="0 0 27 18"
    role="img"
    aria-label="Greek flag with the cross replaced by the Vergina Sun"
    xmlns="http://www.w3.org/2000/svg"
  >
    <title>Greek flag — Vergina Sun canton</title>

    <!-- 9 stripes -->
    <rect
      v-for="i in 9"
      :key="i"
      x="0"
      :y="(i - 1) * 2"
      width="27"
      height="2"
      :fill="(i - 1) % 2 === 0 ? blue : '#ffffff'"
    />

    <!-- blue canton, 5 stripes tall -->
    <rect x="0" y="0" width="10" height="10" :fill="blue" />

    <!-- Vergina sun in the canton (faithful ray geometry, scaled) -->
    <g transform="translate(5,5) scale(0.0132)" :fill="gold">
      <g v-for="rot in [0, 45, 90, 135]" :key="'r' + rot" :transform="`rotate(${rot})`">
        <path d="m-21-68.5a21,21 0 0 0 42,0l-21-217.5z m0,137a21,21 0 0 1 42,0l-21,217.5z" />
        <path transform="rotate(22.5)" d="m-16.4-118a16.4,16.4 0 0 0 32.8,0l-16.4-168z m0,236a16.4,16.4 0 0 1 32.8,0l-16.4,168z" />
      </g>
      <circle r="41.6" />
    </g>
  </svg>
</template>

<script setup>
defineProps({
  width: { type: [Number, String], default: 216 }
})
const blue = '#0d5eaf'
const gold = '#c6a03c'
</script>

<style scoped>
.km-flag { display: block; box-shadow: 0 24px 60px rgba(0,0,0,0.45); }
</style>
