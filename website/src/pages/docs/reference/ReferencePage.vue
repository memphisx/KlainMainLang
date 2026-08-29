<template>
  <ReferenceIndex v-if="data" :data="data" />
  <p v-else>Reference not found.</p>
</template>

<script setup>
import { computed } from 'vue'
import ReferenceIndex from 'components/docs/ReferenceIndex.vue'
import { surfaces } from 'src/data/reference-index.js'

const props = defineProps({
  surface: { type: String, required: true }
})

const data = computed(() => surfaces[props.surface] || null)
</script>
