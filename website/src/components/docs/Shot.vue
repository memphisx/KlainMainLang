<template>
  <figure class="km-shot">
    <img :src="src" :alt="alt" loading="lazy" decoding="async" />
    <figcaption v-if="caption" class="km-shot__cap">{{ caption }}</figcaption>
  </figure>
</template>

<script setup>
// A framed screenshot with an optional caption — used by the guides to show the
// real, captured output of a compiled example (see scripts/gen-tui-screenshots.sh).
defineProps({
  src: { type: String, required: true },
  alt: { type: String, default: '' },
  caption: { type: String, default: '' }
})
</script>

<style scoped>
.km-shot {
  margin: 1.5rem 0;
}
.km-shot img {
  display: block;
  width: 100%;
  height: auto;
  max-width: 720px;
  border: 1px solid var(--km-line, rgba(255, 255, 255, 0.12));
  border-radius: 12px;
}
.km-shot__cap {
  margin-top: 0.55rem;
  font-size: 0.82rem;
  opacity: 0.7;
  max-width: 720px;
}
</style>
