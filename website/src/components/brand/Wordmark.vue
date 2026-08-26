<template>
  <!-- The Klain Main lockup, set in Jost (Futura) — the Calvin Klein register. -->
  <component
    :is="tag"
    class="km-wordmark"
    :class="[stacked ? 'km-wordmark--stack' : 'km-wordmark--inline', `km-wordmark--${size}`]"
    aria-label="Klain Main — KlainMainLang"
  >
    <span>Klain</span><span>Main</span>
  </component>
</template>

<script setup>
defineProps({
  tag: { type: String, default: 'span' },
  stacked: { type: Boolean, default: true },
  size: { type: String, default: 'md' } // sm | md | lg | xl
})
</script>

<style scoped>
.km-wordmark { color: inherit; }
.km-wordmark--inline { flex-direction: row; gap: 0.34em; line-height: 1; }
.km-wordmark--inline span { display: inline; }

.km-wordmark--sm { font-size: 1.05rem; }
.km-wordmark--md { font-size: 1.6rem; }
.km-wordmark--lg { font-size: clamp(2.4rem, 6vw, 4rem); }
.km-wordmark--xl { font-size: clamp(3.4rem, 12vw, 9.5rem); }
</style>
