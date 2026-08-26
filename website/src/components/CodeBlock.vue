<template>
  <div class="km-codeblock" :class="{ 'km-codeblock--terminal': terminal }">
    <div v-if="label || terminal" class="km-codeblock__bar">
      <span v-if="terminal" class="km-codeblock__dots" aria-hidden="true">
        <i></i><i></i><i></i>
      </span>
      <span class="km-codeblock__label km-mono">{{ label || filename }}</span>
    </div>
    <pre class="km-code"><code class="hljs" v-html="rendered"></code></pre>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { highlight } from 'src/lib/hl.js'

const props = defineProps({
  code: { type: String, required: true },
  lang: { type: String, default: 'typescript' },
  label: { type: String, default: '' },
  filename: { type: String, default: '' },
  terminal: { type: Boolean, default: false }
})

const rendered = computed(() => highlight(props.code.replace(/\s+$/, ''), props.lang))
</script>

<style scoped>
.km-codeblock {
  background: #0e0e0e;
  border: 1px solid var(--km-line);
  overflow: hidden;
}
.km-codeblock__bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--km-line);
  background: #131313;
}
.km-codeblock__dots { display: inline-flex; gap: 6px; }
.km-codeblock__dots i {
  width: 10px; height: 10px; border-radius: 50%;
  background: #2f2f2f; display: inline-block;
}
.km-codeblock__label {
  font-size: 0.72rem;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: #7a7a7a;
}
pre {
  margin: 0;
  padding: 22px 24px;
  overflow-x: auto;
}
.km-codeblock--terminal pre { color: #d6d6d6; }
</style>
