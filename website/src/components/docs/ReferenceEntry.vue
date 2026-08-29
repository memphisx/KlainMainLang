<template>
  <section :id="entry.id" class="km-ref">
    <header class="km-ref__head">
      <h3 class="km-ref__name km-mono">{{ entry.name }}</h3>
      <span class="km-ref__badge" :class="'km-ref__badge--' + entry.badge">{{ badgeLabel }}</span>
      <span
        v-if="entry.extension"
        class="km-ref__ext"
        title="This project's own extension — not part of the standard API"
      >Compiler extension</span>
    </header>

    <div class="km-ref__sig km-mono">{{ entry.signature }}</div>

    <p v-if="entry.description" class="km-ref__desc">{{ entry.description }}</p>

    <template v-if="entry.params && entry.params.length">
      <span class="km-eyebrow km-ref__section">Parameters</span>
      <dl class="km-ref__params">
        <template v-for="p in entry.params" :key="p.name">
          <dt class="km-mono">{{ p.name }}<span class="km-ref__ptype">{{ p.type }}</span></dt>
          <dd>{{ p.desc }}</dd>
        </template>
      </dl>
    </template>

    <template v-if="entry.returns">
      <span class="km-eyebrow km-ref__section">Returns</span>
      <p class="km-ref__returns"><code>{{ entry.returns.type }}</code> — {{ entry.returns.desc }}</p>
    </template>

    <CodeBlock v-if="entry.snippet" :code="entry.snippet" class="km-ref__code" />
    <router-link
      v-if="entry.exampleKey"
      :to="'/docs/examples/' + entry.exampleKey"
      class="km-ref__examplelink"
    >See it in a full example →</router-link>

    <div v-if="entry.differences && entry.differences.length" class="km-ref__diff">
      <span class="km-ref__difftitle">⚠ Differences from standard</span>
      <ul>
        <li v-for="(d, i) in entry.differences" :key="i" v-html="renderInline(d)"></li>
      </ul>
    </div>

    <div v-if="hasFooter" class="km-ref__footer">
      <div v-if="seeAlso.length" class="km-ref__see">
        <span class="km-ref__seelabel">See also</span>
        <a
          v-for="s in seeAlso"
          :key="s.id"
          :href="'#' + s.id"
          class="km-ref__seechip km-mono"
        >{{ s.name }}</a>
      </div>
      <a
        v-if="entry.spec && entry.spec.mdn"
        :href="entry.spec.mdn"
        target="_blank"
        rel="noopener"
        class="km-ref__spec"
      >MDN ↗</a>
    </div>
  </section>
</template>

<script setup>
import { computed } from 'vue'
import CodeBlock from 'components/CodeBlock.vue'

const props = defineProps({
  entry: { type: Object, required: true },
  // id → name lookup so "See also" chips can show the method name.
  names: { type: Object, default: () => ({}) }
})

const BADGE_LABELS = { exact: 'Exact', caveats: 'Works, with caveats', missing: 'Not supported' }
const badgeLabel = computed(() => BADGE_LABELS[props.entry.badge] || props.entry.badge)

const seeAlso = computed(() =>
  (props.entry.seeAlso || []).map((id) => ({ id, name: props.names[id] || id }))
)

const hasFooter = computed(() => seeAlso.value.length || (props.entry.spec && props.entry.spec.mdn))

// Render inline `code` spans in caveat text without a full markdown pass.
function renderInline (text) {
  const esc = text
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  return esc.replace(/`([^`]+)`/g, '<code>$1</code>')
}
</script>

<style scoped>
.km-ref {
  padding: 52px 0 44px;
  border-top: 1px solid var(--km-line);
  scroll-margin-top: 84px;
}
.km-ref:first-of-type { padding-top: 40px; }
.km-ref__head { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; }
.km-ref__name { font-size: 1.28rem; margin: 0; color: #fff; }
.km-ref__badge {
  font-size: 0.66rem; letter-spacing: 0.1em; text-transform: uppercase;
  font-weight: 700; padding: 3px 9px; border-radius: 3px; white-space: nowrap;
}
.km-ref__badge--exact { color: #6fd18a; background: rgba(111,209,138,0.12); border: 1px solid rgba(111,209,138,0.3); }
.km-ref__badge--caveats { color: #e2b95a; background: rgba(226,185,90,0.12); border: 1px solid rgba(226,185,90,0.3); }
.km-ref__badge--missing { color: #9a9a9a; background: rgba(154,154,154,0.1); border: 1px solid rgba(154,154,154,0.28); }
.km-ref__ext {
  font-size: 0.66rem; letter-spacing: 0.1em; text-transform: uppercase; font-weight: 700;
  padding: 3px 9px; border-radius: 3px; white-space: nowrap;
  color: #9db7e8; background: rgba(120,150,220,0.12); border: 1px solid rgba(120,150,220,0.32);
}

.km-ref__sig {
  margin-top: 14px; padding: 10px 14px; font-size: 0.9rem;
  background: #0e0e0e; border: 1px solid var(--km-line); color: #cfcfcf; overflow-x: auto;
}
.km-ref__desc { margin: 16px 0; color: #c8c8c8; line-height: 1.65; }

.km-ref__section { display: block; color: #6a6a6a; margin: 20px 0 8px; }
.km-ref__params { margin: 0; display: grid; grid-template-columns: max-content 1fr; gap: 6px 18px; }
.km-ref__params dt { color: #e6e6e6; font-size: 0.88rem; }
.km-ref__ptype { color: #7a7a7a; margin-left: 10px; }
.km-ref__params dd { margin: 0; color: #b8b8b8; line-height: 1.55; }
.km-ref__returns { margin: 0; color: #c8c8c8; }
.km-ref__returns code { color: #e6e6e6; }

.km-ref__code { margin-top: 16px; }
.km-ref__examplelink {
  display: inline-block; margin-top: 10px; color: var(--km-gold);
  text-decoration: none; font-size: 0.85rem; font-weight: 600;
}
.km-ref__examplelink:hover { text-decoration: underline; }

.km-ref__diff {
  margin-top: 20px; padding: 16px 18px;
  background: rgba(226,185,90,0.06); border: 1px solid rgba(226,185,90,0.28);
  border-left: 3px solid #e2b95a;
}
.km-ref__difftitle { display: block; color: #e2b95a; font-weight: 700; font-size: 0.82rem; letter-spacing: 0.04em; margin-bottom: 8px; }
.km-ref__diff ul { margin: 0; padding-left: 18px; }
.km-ref__diff li { color: #d4c69c; line-height: 1.6; margin-bottom: 6px; }
.km-ref__diff li:last-child { margin-bottom: 0; }
.km-ref__diff code { color: #f0e2b8; background: rgba(226,185,90,0.12); padding: 1px 5px; border-radius: 3px; font-size: 0.85em; }

.km-ref__footer { display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap; margin-top: 22px; }
.km-ref__see { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.km-ref__seelabel { color: #6a6a6a; font-size: 0.78rem; }
.km-ref__seechip { color: #c2c2c2; text-decoration: none; font-size: 0.82rem; padding: 3px 9px; border: 1px solid var(--km-line); border-radius: 3px; }
.km-ref__seechip:hover { color: var(--km-gold); border-color: var(--km-gold); }
.km-ref__spec { color: #8a8a8a; text-decoration: none; font-size: 0.82rem; }
.km-ref__spec:hover { color: #fff; }
</style>
