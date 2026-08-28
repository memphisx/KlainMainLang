<template>
  <div class="km-404 km-on-black">
    <div class="km-wrap km-404__inner">
      <MedallionKM :size="120" class="km-404__sun" />
      <h1 class="km-display km-404__code">404</h1>
      <p class="km-404__msg km-mono">This route was never emitted. // Κλάιν Μάιν — I don't care, let's go home.</p>
      <router-link to="/" class="km-btn km-btn--gold">Back to start</router-link>
    </div>
  </div>
</template>

<script setup>
import MedallionKM from 'components/brand/MedallionKM.vue'
</script>

<style scoped>
.km-404 { min-height: 100vh; display: flex; align-items: center; }
.km-404__inner { text-align: center; display: flex; flex-direction: column; align-items: center; gap: 20px; }
.km-404__sun { color: var(--km-gold); }
.km-404__code { font-size: clamp(5rem, 20vw, 12rem); line-height: 1; }
.km-404__msg { color: #8a8a8a; }
</style>
