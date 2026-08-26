<template>
  <div class="km-tree">
    <template v-for="node in nodes" :key="node.key">
      <router-link
        v-if="node.type === 'example'"
        :to="'/docs/examples/' + node.key"
        class="km-tree__leaf"
        active-class="km-tree__leaf--active"
      >{{ node.label }}</router-link>

      <div v-else class="km-tree__cat">
        <button type="button" class="km-tree__toggle" @click="toggle(node.key)" :aria-expanded="open.has(node.key)">
          <q-icon :name="open.has(node.key) ? 'expand_more' : 'chevron_right'" size="18px" />
          <span class="km-tree__label">{{ node.label }}</span>
          <small class="km-tree__count">{{ leafCount(node) }}</small>
        </button>
        <div v-show="open.has(node.key)" class="km-tree__children">
          <ExampleTree :nodes="node.children" :active="active" />
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref } from 'vue'

const props = defineProps({
  nodes: { type: Array, required: true },
  active: { type: String, default: '' }
})

// Open the branch that contains the active example.
const initial = props.nodes
  .filter((n) => n.type === 'category' && props.active &&
    (props.active === n.key || props.active.startsWith(n.key + '/')))
  .map((n) => n.key)
const open = ref(new Set(initial))

function toggle (key) {
  const s = new Set(open.value)
  s.has(key) ? s.delete(key) : s.add(key)
  open.value = s
}

function leafCount (node) {
  if (node.type === 'example') return 1
  return (node.children || []).reduce((n, c) => n + leafCount(c), 0)
}
</script>

<style scoped>
.km-tree { display: flex; flex-direction: column; }
.km-tree__leaf {
  display: block; color: #b4b4b4; text-decoration: none; font-size: 0.86rem;
  padding: 5px 12px 5px 30px; border-left: 2px solid transparent; transition: all .12s ease;
}
.km-tree__leaf:hover { color: #fff; border-left-color: #444; }
.km-tree__leaf--active { color: var(--km-gold); border-left-color: var(--km-gold); background: rgba(198,160,60,0.06); }

.km-tree__toggle {
  display: flex; align-items: center; gap: 6px; width: 100%;
  background: none; border: 0; cursor: pointer; text-align: left;
  color: #cfcfcf; font-size: 0.9rem; padding: 6px 8px; font-family: inherit;
}
.km-tree__toggle:hover { color: #fff; }
.km-tree__label { flex: 1; }
.km-tree__count { color: #5f5f5f; font-size: 0.7rem; font-family: 'JetBrains Mono', monospace; }
.km-tree__children { margin-left: 12px; border-left: 1px solid var(--km-line); }
</style>
