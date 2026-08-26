<template>
  <article v-if="ex" class="km-doc">
    <nav class="km-crumbs">
      <router-link to="/docs/examples" class="km-crumbs__link">Examples</router-link>
      <template v-for="(c, i) in ex.category" :key="i">
        <span class="km-crumbs__sep">/</span>
        <span class="km-crumbs__cur">{{ c }}</span>
      </template>
    </nav>

    <h1>{{ ex.title }}</h1>

    <!-- Description harvested from the file's leading comment (literate). -->
    <div v-if="blocks.length" class="km-doc__lede km-example__desc">
      <template v-for="(b, i) in blocks" :key="i">
        <ul v-if="b.type === 'ul'"><li v-for="(it, j) in b.items" :key="j">{{ it }}</li></ul>
        <ol v-else-if="b.type === 'ol'"><li v-for="(it, j) in b.items" :key="j">{{ it }}</li></ol>
        <p v-else>{{ b.text }}</p>
      </template>
    </div>

    <CodeBlock :code="ex.code" :filename="ex.file" />

    <div class="km-example__foot">
      <a :href="ghUrl" target="_blank" rel="noopener" class="km-btn">View source on GitHub</a>
      <span class="km-mono km-example__path">examples/{{ ex.file }}</span>
    </div>

    <div class="km-doc__nextrow">
      <router-link v-if="prev" :to="'/docs/examples/' + prev.key" class="km-btn">← {{ prev.label }}</router-link>
      <span v-else></span>
      <router-link v-if="next" :to="'/docs/examples/' + next.key" class="km-btn km-btn--gold">{{ next.label }} →</router-link>
    </div>
  </article>

  <article v-else class="km-doc">
    <h1>Example not found</h1>
    <router-link to="/docs/examples" class="km-btn km-btn--gold">All examples</router-link>
  </article>
</template>

<script setup>
import { computed } from 'vue'
import CodeBlock from 'components/CodeBlock.vue'
import content from 'src/data/examples-content.json'
import tree from 'src/data/examples-tree.json'
import { GITHUB_URL } from 'src/lib/content.js'

const props = defineProps({ exampleKey: { type: String, required: true } })

const ex = computed(() => content[props.exampleKey] || null)
const ghUrl = computed(() => ex.value ? `${GITHUB_URL}/blob/main/examples/${ex.value.file}` : GITHUB_URL)

// Flatten tree to an ordered list for prev/next.
const ordered = (() => {
  const acc = []
  const walk = (nodes) => nodes.forEach((n) => n.type === 'example' ? acc.push(n) : walk(n.children || []))
  walk(tree)
  return acc
})()
const idx = computed(() => ordered.findIndex((n) => n.key === props.exampleKey))
const prev = computed(() => idx.value > 0 ? ordered[idx.value - 1] : null)
const next = computed(() => idx.value >= 0 && idx.value < ordered.length - 1 ? ordered[idx.value + 1] : null)

// Parse the harvested description into paragraphs and lists.
const blocks = computed(() => {
  if (!ex.value?.description) return []
  return ex.value.description.split(/\n{2,}/).map((raw) => {
    const lines = raw.split('\n')
    const isOl = lines.every((l) => /^\s*\d+[.)]\s/.test(l))
    const isUl = !isOl && lines.every((l) => /^\s*[-*•]\s/.test(l))
    if (isOl) return { type: 'ol', items: lines.map((l) => l.replace(/^\s*\d+[.)]\s/, '').trim()) }
    if (isUl) return { type: 'ul', items: lines.map((l) => l.replace(/^\s*[-*•]\s/, '').trim()) }
    return { type: 'p', text: lines.join(' ').trim() }
  })
})
</script>

<style scoped>
.km-crumbs { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; font-size: 0.74rem; text-transform: uppercase; letter-spacing: 0.12em; margin-bottom: 6px; }
.km-crumbs__link { color: var(--km-gold); text-decoration: none; border: 0 !important; }
.km-crumbs__sep { color: #555; }
.km-crumbs__cur { color: #8a8a8a; }
.km-example__desc { white-space: normal; }
.km-example__desc :deep(ol), .km-example__desc :deep(ul) { font-size: 1rem; color: #b6b6b6; margin: 0 0 16px; }
.km-example__foot { display: flex; align-items: center; gap: 18px; flex-wrap: wrap; margin: 4px 0 0; }
.km-example__path { color: #6a6a6a; font-size: 0.78rem; }
</style>
