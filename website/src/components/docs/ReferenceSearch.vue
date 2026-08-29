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
            placeholder="Search the API reference…"
            aria-label="Search the API reference"
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
              :key="r.surface + '#' + r.id"
              class="km-search__item"
              :class="{ 'km-search__item--active': i === active }"
              @mousedown.prevent="choose(r)"
              @mouseenter="active = i"
            >
              <div class="km-search__row">
                <span class="km-search__name km-mono">{{ r.name }}</span>
                <span class="km-search__dot" :class="'km-search__dot--' + r.badge"></span>
                <span class="km-search__crumb">{{ r.surfaceTitle }}</span>
              </div>
              <p v-if="r.desc" class="km-search__desc">{{ r.desc }}</p>
            </li>
          </ul>
          <div v-else-if="query.trim()" class="km-search__empty">No matches for “{{ query.trim() }}”</div>
          <div v-else class="km-search__hint">
            Search {{ all.length }} API reference entries across {{ surfaceCount }} surfaces — jump straight to a
            method's signature, caveats, and example. Try <em>readFile</em>, <em>fetch</em>, or <em>toSorted</em>.
          </div>
        </div>
      </div>
    </div>
  </teleport>
</template>

<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { surfaces } from 'src/data/reference-index.js'

const props = defineProps({ modelValue: { type: Boolean, default: false } })
const emit = defineEmits(['update:modelValue'])

const router = useRouter()
const query = ref('')
const active = ref(0)
const input = ref(null)

// Flatten every surface's entries once — ~500 small records, cheap to scan.
const all = []
for (const [surface, data] of Object.entries(surfaces)) {
  for (const e of data.entries) {
    all.push({
      surface,
      surfaceTitle: data.title,
      id: e.id,
      name: e.name,
      badge: e.badge,
      desc: (e.description || '').slice(0, 120),
      hay: (e.name + ' ' + data.title + ' ' + (e.description || '')).toLowerCase()
    })
  }
}
const surfaceCount = Object.keys(surfaces).length

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
  scored.sort((a, b) => a.score - b.score || a.r.name.length - b.r.name.length)
  return scored.slice(0, 20).map((s) => s.r)
})

function move (d) {
  if (!results.value.length) return
  active.value = (active.value + d + results.value.length) % results.value.length
}
function choose (r) {
  if (!r) return
  close()
  router.push({ path: '/docs/reference/' + r.surface, hash: '#' + r.id }).then(() => {
    setTimeout(() => {
      const el = document.getElementById(r.id)
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
