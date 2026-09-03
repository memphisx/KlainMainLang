<template>
  <teleport to="body">
    <div v-if="modelValue" class="km-search" @click.self="close">
      <div class="km-search__panel" role="dialog" aria-modal="true" aria-label="Search documentation">
        <div class="km-search__bar">
          <q-icon name="search" size="20px" class="km-search__icon" />
          <input
            ref="input"
            v-model="query"
            type="text"
            class="km-search__input"
            placeholder="Search the docs…"
            aria-label="Search the documentation"
            @keydown.down.prevent="move(1)"
            @keydown.up.prevent="move(-1)"
            @keydown.enter.prevent="choose(results[active])"
            @keydown.esc="close"
          />
          <kbd class="km-search__esc">esc</kbd>
        </div>

        <div class="km-search__body">
          <ul v-if="results.length" class="km-search__list">
            <li
              v-for="(r, i) in results"
              :key="r.kind + ':' + r.route + '#' + r.hash + ':' + r.name"
              class="km-search__item"
              :class="{ 'km-search__item--active': i === active }"
              @mousedown.prevent="choose(r)"
              @mouseenter="active = i"
            >
              <div class="km-search__row">
                <span class="km-search__tag" :class="'km-search__tag--' + r.kind">{{ KIND_LABEL[r.kind] }}</span>
                <span class="km-search__name" :class="{ 'km-mono': r.kind === 'api' }">{{ r.name }}</span>
                <span v-if="r.kind === 'api'" class="km-search__dot" :class="'km-search__dot--' + r.badge"></span>
                <span class="km-search__crumb">{{ r.title }}</span>
              </div>
              <p v-if="r.desc" class="km-search__desc">{{ r.desc }}</p>
            </li>
          </ul>
          <div v-else-if="query.trim()" class="km-search__empty">No matches for “{{ query.trim() }}”</div>
          <div v-else class="km-search__hint">
            Search all of the docs — {{ stats.pages }} pages, {{ stats.references }} API surfaces,
            {{ stats.api }} API entries, and {{ stats.examples }} examples. Try <em>readFile</em>,
            <em>klain:sync</em>, <em>CLI flags</em>, or <em>load tester</em>.
          </div>
        </div>
      </div>
    </div>
  </teleport>
</template>

<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { searchRecords, searchStats } from 'src/data/search-index.js'

const props = defineProps({ modelValue: { type: Boolean, default: false } })
const emit = defineEmits(['update:modelValue'])

const router = useRouter()
const query = ref('')
const active = ref(0)
const input = ref(null)

const KIND_LABEL = { page: 'Page', reference: 'Reference', api: 'API', example: 'Example' }
// Kind ordering for the tie-break: a page/surface beats an individual method
// which beats an example when relevance is otherwise equal.
const KIND_RANK = { page: 0, reference: 1, api: 2, example: 3 }

const all = searchRecords
const stats = searchStats

const results = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return []
  const scored = []
  for (const r of all) {
    const inName = r.name.toLowerCase().includes(q)
    if (!inName && !r.hay.includes(q)) continue
    const score = r.name.toLowerCase() === q ? 0 : inName ? 1 : 2
    scored.push({ r, score })
  }
  scored.sort((a, b) =>
    a.score - b.score ||
    (KIND_RANK[a.r.kind] - KIND_RANK[b.r.kind]) ||
    a.r.name.length - b.r.name.length
  )
  return scored.slice(0, 24).map((s) => s.r)
})

function move (d) {
  if (!results.value.length) return
  active.value = (active.value + d + results.value.length) % results.value.length
}
function choose (r) {
  if (!r) return
  close()
  const target = r.hash ? { path: r.route, hash: '#' + r.hash } : { path: r.route }
  router.push(target).then(() => {
    if (!r.hash) return
    setTimeout(() => {
      const el = document.getElementById(r.hash)
      if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }, 60)
  })
}
function close () { emit('update:modelValue', false) }

// Reset + focus each time it opens.
watch(() => props.modelValue, (open) => {
  if (open) {
    query.value = ''
    active.value = 0
    nextTick(() => input.value && input.value.focus())
  }
})

// Hijack Cmd/Ctrl+F to open the palette; Esc handled on the input.
function onKeydown (e) {
  if ((e.metaKey || e.ctrlKey) && (e.key === 'f' || e.key === 'F')) {
    e.preventDefault()
    emit('update:modelValue', true)
  }
}
onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<style scoped>
.km-search {
  position: fixed; inset: 0; z-index: 3000;
  background: rgba(0,0,0,0.6); backdrop-filter: blur(3px);
  display: flex; justify-content: center; align-items: flex-start;
  padding: clamp(60px, 12vh, 160px) 20px 40px;
}
.km-search__panel {
  width: 100%; max-width: 640px;
  background: #111; border: 1px solid var(--km-line); border-radius: 8px;
  box-shadow: 0 24px 60px rgba(0,0,0,0.6); overflow: hidden;
}
.km-search__bar { display: flex; align-items: center; gap: 12px; padding: 16px 18px; border-bottom: 1px solid var(--km-line); }
.km-search__icon { color: #7a7a7a; }
.km-search__input {
  flex: 1; background: none; border: none; outline: none;
  color: #eee; font-size: 1.02rem;
}
.km-search__input::placeholder { color: #6a6a6a; }
.km-search__esc {
  font-size: 0.66rem; color: #8a8a8a; border: 1px solid var(--km-line);
  border-radius: 4px; padding: 2px 6px; background: #0d0d0d;
}

.km-search__body {
  max-height: min(56vh, 480px);
  overflow-y: auto;
  /* Scrollbar stays hidden until the pointer is over the list (approximates
     "show only when scrolling"; native overlay scrollbars already do this). */
  scrollbar-width: thin;
  scrollbar-color: transparent transparent;
}
.km-search__body:hover { scrollbar-color: #3a3a3a transparent; }
.km-search__body::-webkit-scrollbar { width: 8px; }
.km-search__body::-webkit-scrollbar-thumb { background: transparent; border-radius: 8px; }
.km-search__body:hover::-webkit-scrollbar-thumb { background: #3a3a3a; }
.km-search__list { list-style: none; margin: 0; padding: 6px; }
.km-search__item { padding: 9px 12px; border-radius: 5px; cursor: pointer; }
.km-search__item--active { background: rgba(198,160,60,0.12); }
.km-search__row { display: flex; align-items: center; gap: 10px; }
.km-search__tag {
  flex: none; font-size: 0.6rem; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase;
  padding: 2px 6px; border-radius: 4px; border: 1px solid var(--km-line); color: #9a9a9a; background: #0d0d0d;
  min-width: 62px; text-align: center;
}
.km-search__tag--page { color: #8fb3e0; border-color: rgba(143,179,224,0.35); }
.km-search__tag--reference { color: #c6a03c; border-color: rgba(198,160,60,0.4); }
.km-search__tag--api { color: #9a9a9a; }
.km-search__tag--example { color: #6fd18a; border-color: rgba(111,209,138,0.35); }
.km-search__name { color: #eaeaea; font-size: 0.9rem; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.km-search__dot { width: 8px; height: 8px; border-radius: 50%; flex: none; }
.km-search__dot--exact { background: #6fd18a; }
.km-search__dot--caveats { background: #e2b95a; }
.km-search__dot--missing { background: #9a9a9a; }
.km-search__crumb { margin-left: auto; color: #7a7a7a; font-size: 0.74rem; white-space: nowrap; }
.km-search__desc {
  margin: 3px 0 0; color: #8f8f8f; font-size: 0.78rem; line-height: 1.4;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.km-search__empty, .km-search__hint { padding: 20px 22px; color: #8a8a8a; font-size: 0.86rem; line-height: 1.6; }
.km-search__hint em { color: var(--km-gold); font-style: normal; }
</style>
