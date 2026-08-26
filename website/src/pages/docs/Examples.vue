<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Reference</span>
    <h1>Examples</h1>
    <p class="km-doc__lede">
      Every language feature has a runnable example under <code>examples/</code> — no README
      snippets to go stale, just <code>.ts</code> files that compile and run, verified on every
      build. All {{ total }} of them are here, grouped by area; pick one from the tree on the left.
    </p>

    <div class="km-excat">
      <section v-for="cat in categories" :key="cat.key" class="km-excat__group">
        <h2 class="km-excat__title">{{ cat.label }} <small>{{ cat.items.length }}</small></h2>
        <div class="km-excat__items">
          <router-link
            v-for="it in cat.items"
            :key="it.key"
            :to="'/docs/examples/' + it.key"
            class="km-excat__item"
          >{{ it.label }}</router-link>
        </div>
      </section>
    </div>
  </article>
</template>

<script setup>
import tree from 'src/data/examples-tree.json'

// Flatten one level for the landing grid: each top category with its leaf
// examples (descending into sub-categories, labelled by their path).
function leavesOf (node, trail) {
  const out = []
  for (const c of node.children || []) {
    if (c.type === 'example') out.push({ key: c.key, label: c.label })
    else out.push(...leavesOf(c, [...trail, c.label]))
  }
  return out
}

const categories = tree.map((c) => ({ key: c.key, label: c.label, items: leavesOf(c, []) }))
  .filter((c) => c.items.length)
const total = categories.reduce((n, c) => n + c.items.length, 0)
</script>

<style scoped>
.km-excat { display: flex; flex-direction: column; gap: 30px; margin-top: 10px; }
.km-excat__title { font-size: 1.1rem; border-top: 1px solid var(--km-line); padding-top: 16px; margin: 0 0 12px; }
.km-excat__title small { color: #5f5f5f; font-size: 0.72rem; font-family: 'JetBrains Mono', monospace; margin-left: 6px; }
.km-excat__items { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 6px 14px; }
.km-excat__item {
  color: #b6b6b6; text-decoration: none; font-size: 0.9rem; padding: 4px 0;
  border: 0 !important; transition: color .12s ease;
}
.km-excat__item:hover { color: var(--km-gold); }
</style>
