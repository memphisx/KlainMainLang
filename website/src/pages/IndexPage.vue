<template>
  <q-page class="km-home">

    <!-- ===== HERO ===== -->
    <section class="km-hero km-on-black">
      <div class="km-hero__grain" aria-hidden="true"></div>
      <div class="km-wrap km-hero__inner">
        <div class="km-hero__copy">
          <span class="km-eyebrow km-gold">TypeScript · LLVM · clang -O2</span>
          <h1 class="km-display km-hero__title">
            TypeScript,<br>compiled<br>to <span class="km-gold">native.</span>
          </h1>
          <p class="km-hero__lede">
            You write <code class="km-mono">.ts</code>. It writes LLVM IR, hands it to
            <code class="km-mono">clang</code>, and out comes a real executable.
            Your operating system is none the wiser. No runtime. No V8. No Electron.
          </p>
          <div class="km-hero__cta">
            <router-link to="/docs/getting-started" class="km-btn km-btn--gold">Get started</router-link>
          </div>
          <p class="km-hero__note km-mono">
            One self-contained binary — for CLI tools, Docker microservices, and desktop apps.
          </p>
        </div>

        <div class="km-hero__art">
          <MedallionKM :size="320" class="km-hero__sun" title="Because native is Gold" />
          <div class="km-hero__terminal">
            <CodeBlock :code="terminal" lang="bash" terminal label="shell" />
          </div>
        </div>
      </div>
    </section>

    <!-- ===== HEADLINE STATS ===== -->
    <section class="km-stats km-on-white">
      <div class="km-wrap">
        <span class="km-eyebrow km-stats__eye">Targeted feature areas</span>
        <div class="km-stats__grid">
          <div v-for="h in headline" :key="h.label" class="km-stat">
            <span class="km-stat__value km-display">{{ h.value }}</span>
            <span class="km-stat__label">{{ h.label }}</span>
            <span class="km-stat__sub km-mono">{{ h.sub }}</span>
          </div>
        </div>
        <p class="km-stats__foot">
          These are curated feature-area checklists, “does the core case work?”, not external
          conformance. They mirror the project's own
          <router-link to="/docs/coverage" class="km-link">status matrix</router-link>, caveats and all.
        </p>

        <span class="km-eyebrow km-stats__eye km-stats__eye--2">External conformance · full public suites</span>
        <div class="km-stats__grid">
          <div v-for="c in conformance" :key="c.label" class="km-stat km-stat--conf">
            <span class="km-stat__value km-display">{{ c.value }}</span>
            <span class="km-stat__label">{{ c.label }}</span>
            <span class="km-stat__sub km-mono">{{ c.sub }}</span>
          </div>
        </div>
        <p class="km-stats__foot">
          Low on purpose, and shown on purpose. This is a <strong>typed subset</strong>: most of
          Test262 is <code>eval</code>-based untyped JS, and Node's suite is dynamic JavaScript
          against the full platform. Both largely out of scope by design, not silent failures.
          <router-link to="/docs/coverage" class="km-link">The honest breakdown.</router-link>
        </p>
      </div>
    </section>

    <!-- ===== VALUE PROPS ===== -->
    <section class="km-section km-on-black">
      <div class="km-wrap">
        <header class="km-secthead">
          <span class="km-kicker-num km-display">01</span>
          <h2 class="km-display km-secthead__title">Why it exists</h2>
        </header>
        <div class="km-props">
          <article v-for="p in props" :key="p.title" class="km-prop">
            <q-icon :name="p.icon" size="26px" class="km-prop__icon" />
            <h3 class="km-prop__title">{{ p.title }}</h3>
            <p class="km-prop__body">{{ p.body }}</p>
          </article>
        </div>
      </div>
    </section>

    <!-- ===== CODE SHOWCASE ===== -->
    <section class="km-section km-on-stone">
      <div class="km-wrap">
        <header class="km-secthead">
          <span class="km-kicker-num km-display">02</span>
          <h2 class="km-display km-secthead__title">Real TypeScript in,<br>real binaries out</h2>
        </header>
        <p class="km-showcase__lede">
          Every snippet below is an actual file under <code class="km-mono">examples/</code>.
          It compiles and runs, verified on every build. No slideware.
        </p>

        <div class="km-showcase">
          <div class="km-showcase__tabs">
            <button
              v-for="t in tabs"
              :key="t.key"
              class="km-showtab"
              :class="{ 'km-showtab--active': tab === t.key }"
              @click="tab = t.key"
            >
              <span class="km-mono">{{ t.file }}</span>
              <small>{{ t.blurb }}</small>
            </button>
          </div>
          <div class="km-showcase__code">
            <CodeBlock :code="samples[tab].code" :filename="samples[tab].filename" />
          </div>
        </div>
      </div>
    </section>

    <!-- ===== PIPELINE ===== -->
    <section class="km-section km-on-black">
      <div class="km-wrap">
        <header class="km-secthead">
          <span class="km-kicker-num km-display">03</span>
          <h2 class="km-display km-secthead__title">The pipeline,<br>in one breath</h2>
        </header>
        <PipelineFlow />
        <p class="km-pipe__tail km-mono">
          → one <code>.ll</code>, one <code>clang</code> call, one generated
          <code>main()</code>, one binary that runs unsupervised.
        </p>
      </div>
    </section>

    <!-- ===== GALLERY ===== -->
    <section class="km-section km-on-stone">
      <div class="km-wrap">
        <header class="km-secthead">
          <span class="km-kicker-num km-display">04</span>
          <h2 class="km-display km-secthead__title">Not just hello-world</h2>
        </header>
        <p class="km-gallery__lede">
          Complete little apps, each a single compiled binary — a native terminal-UI framework and a
          real desktop window, both shipped in the examples. Every screenshot is the actual program
          running.
        </p>
        <div class="km-gallery">
          <router-link v-for="g in gallery" :key="g.to" :to="g.to" class="km-gallery__card">
            <span class="km-gallery__shot"><img :src="g.img" :alt="g.alt" loading="lazy" /></span>
            <span class="km-gallery__meta">
              <span class="km-gallery__tag">{{ g.tag }}</span>
              <h3 class="km-gallery__title">{{ g.title }}</h3>
              <p class="km-gallery__body">{{ g.body }}</p>
              <span class="km-gallery__go">{{ g.cta }} →</span>
            </span>
          </router-link>
        </div>
      </div>
    </section>

    <!-- ===== COVERAGE ===== -->
    <section class="km-section km-on-white">
      <div class="km-wrap">
        <header class="km-secthead">
          <span class="km-kicker-num km-display">05</span>
          <h2 class="km-display km-secthead__title">What actually works</h2>
        </header>
        <div class="km-cov__legend">
          <span class="km-cov__key"><i class="km-cov__sw km-cov__sw--strict"></i> Exact match to JS</span>
          <span class="km-cov__key"><i class="km-cov__sw km-cov__sw--caveat"></i> Works, with minor differences</span>
        </div>
        <div class="km-cov">
          <router-link
            v-for="row in coverage"
            :key="row.area"
            to="/docs/coverage"
            class="km-cov__row"
          >
            <span class="km-cov__area">{{ row.area }}</span>
            <span class="km-cov__group km-mono">{{ row.group }}</span>
            <span class="km-cov__bar" :title="`${row.strict}% exact match · ${row.pct - row.strict}% with minor differences`">
              <span class="km-cov__fill km-cov__fill--caveat" :style="{ width: row.pct + '%' }"></span>
              <span class="km-cov__fill km-cov__fill--strict" :style="{ width: row.strict + '%' }"></span>
            </span>
            <span class="km-cov__pct km-mono">{{ row.pct }}%</span>
          </router-link>
        </div>
        <div class="km-cov__foot">
          <router-link to="/docs/coverage" class="km-btn">Full status matrix</router-link>
        </div>
      </div>
    </section>

    <!-- ===== ORIGIN / HOMAGE ===== -->
    <section class="km-origin km-on-black">
      <div class="km-wrap km-origin__grid">
        <div class="km-origin__copy">
          <span class="km-eyebrow km-gold">The name is the mission statement</span>
          <Wordmark tag="h2" :stacked="false" size="lg" class="km-origin__wm" />
          <p class="km-origin__body">
            Not German. Not a clothing brand. <strong>Klain Main</strong> isn't
            <em>“klein Main”</em> (a cute little <code>main()</code>), and it isn't
            the Augsburg
            <a href="https://klainmain.com" target="_blank" rel="noopener" class="km-origin__link">streetwear label</a>.
             <em>It's Greek</em>.
          </p>
          <p class="km-origin__body">
            <strong>Κλάιν Μάιν</strong> is slang for <em>“I don't care”</em>, the
            good kind: do the thing anyway, even when everyone tells you it's futile,
            for no better reason than <em>because you can.</em>
            That's the whole reason this compiler exists, because
            “how would I even build one” turned out to be a far better rabbit hole
            than whatever the day's actual plan was.
          </p>
          <router-link to="/docs" class="km-btn km-btn--ghost">Read the docs</router-link>
        </div>
        <div class="km-origin__flag">
          <FoilSun :size="300" title="Macedonia is Greek" />
        </div>
      </div>
    </section>

    <!-- ===== CTA ===== -->
    <section class="km-cta km-on-stone">
      <div class="km-wrap km-cta__inner">
        <h2 class="km-display km-cta__title">Write fresh TypeScript.<br>Ship a small, predictable binary.</h2>
        <div class="km-cta__btns">
          <router-link to="/docs/getting-started" class="km-btn km-btn--gold">Get started</router-link>
          <a :href="gh" target="_blank" rel="noopener" class="km-btn">Star on GitHub</a>
        </div>
      </div>
    </section>

  </q-page>
</template>

<script setup>
import { ref } from 'vue'
import CodeBlock from 'components/CodeBlock.vue'
import Wordmark from 'components/brand/Wordmark.vue'
import MedallionKM from 'components/brand/MedallionKM.vue'
import FoilSun from 'components/brand/FoilSun.vue'
import PipelineFlow from 'components/PipelineFlow.vue'
import { samples, terminal, coverage, headline, conformance, GITHUB_URL } from 'src/lib/content.js'
import klaintopImg from 'src/assets/tui/klaintop.png'
import explorerImg from 'src/assets/webview/listing.png'
import todoImg from 'src/assets/tui/todo.png'
import filesImg from 'src/assets/tui/files.png'

const gh = GITHUB_URL
const tab = ref('server')

// Showcase gallery — real example apps, each linking to its walkthrough.
const gallery = [
  {
    to: '/docs/guides/tui/live-dashboard', img: klaintopImg, tag: 'Terminal UI',
    alt: 'A terminal process manager with CPU/memory bars and a process table',
    title: 'A live process manager',
    body: 'CPU / memory bars over a sortable, scrollable process table you can kill from — an htop-lite, redrawn on a timer.',
    cta: 'Read the guide'
  },
  {
    to: '/docs/guides/webview', img: explorerImg, tag: 'Desktop',
    alt: 'A native desktop window listing a directory with file icons',
    title: 'A native desktop file explorer',
    body: 'A Quasar single-page UI in a real OS window, backed by native fs — directory listing with live text and image previews.',
    cta: 'Read the guide'
  },
  {
    to: '/docs/guides/tui/layout', img: todoImg, tag: 'Terminal UI',
    alt: 'A bordered to-do list in the terminal with a progress bar',
    title: 'A keyboard-driven to-do list',
    body: 'Flexbox layout, a selectable list, a text input, and real file persistence — the flagship klain:tui walkthrough.',
    cta: 'Read the guide'
  },
  {
    to: '/docs/examples/tui/files', img: filesImg, tag: 'Terminal UI',
    alt: 'A two-pane terminal file browser with a preview pane',
    title: 'A two-pane file browser',
    body: 'A nested layout with a live preview pane, driven by fs and path — the same immediate-mode loop, a richer view.',
    cta: 'See the example'
  }
]

const tabs = [
  { key: 'server', file: 'http_server.ts', blurb: 'An HTTP/1.1 + HTTP/2 server' },
  { key: 'desktop', file: 'embedded.ts', blurb: 'A single-file desktop app' },
  { key: 'fetch', file: 'fetch.ts', blurb: 'async / await + typed JSON' },
  { key: 'generics', file: 'generics.ts', blurb: 'Generics & interfaces' },
  { key: 'numbers', file: 'jsdoc-widths.ts', blurb: 'JSDoc numeric widths' }
]

const props = [
  { icon: 'bolt', title: 'No runtime, no VM', body: 'The output is a native executable from clang -O2, not a bundled interpreter. Zero startup warmup, small binaries, plain libc by default.' },
  { icon: 'dns', title: 'Real servers', body: 'http.listen speaks HTTP/1.1 and HTTP/2 (h2c) on one port. fs, worker_threads and cluster give you real OS threads and processes. TLS on both ends.' },
  { icon: 'public', title: 'Browser-shaped APIs, off-browser', body: 'fetch, URL, WebSocket, Web Crypto, Streams, AbortController, timers, the web APIs that make sense without a DOM, and none of the ones that don\'t.' },
  { icon: 'desktop_windows', title: 'Desktop apps, one file', body: 'klain:webview opens a real window over the system browser engine and calls straight into typed native code. Any SPA (React/Vue/Svelte/Quasar) embeds into the binary with new Webview({ serve }) — a single-file app, packaged to a .app or .desktop.' },
  { icon: 'tune', title: 'You pick the trade-offs', body: 'number is a JS-faithful 64-bit float by default, opt into sized machine ints (int8…uint64) with a JSDoc width when you want them. Memory is manual by default (never frees, perfect for short-lived CLIs); pass -mm=gc for a real Boehm collector. Crypto / bigint / regex backends chosen per compile.' },
  { icon: 'inventory_2', title: 'Ships in a scratch image', body: 'One statically-linkable binary, no interpreter and no node_modules to copy in. A Docker microservice fits in a FROM scratch image measured in megabytes, boots instantly, and has nothing extra to patch or CVE-scan.' }
]

</script>

<style scoped>
code { font-family: 'JetBrains Mono', monospace; font-size: 0.9em; }

/* ---- HERO ---- */
.km-hero { position: relative; overflow: hidden; padding-top: 104px; padding-bottom: clamp(48px, 6vw, 88px); }
.km-hero__grain {
  position: absolute; inset: 0;
  background:
    radial-gradient(1200px 600px at 80% -10%, rgba(198,160,60,0.16), transparent 60%),
    radial-gradient(900px 500px at -10% 110%, rgba(198,160,60,0.08), transparent 60%);
  pointer-events: none;
}
.km-hero__inner {
  position: relative;
  display: grid; grid-template-columns: 1.15fr 0.85fr;
  gap: clamp(32px, 6vw, 72px); align-items: center;
}
/* Keep long code lines from stretching the grid tracks past the wrap. */
.km-hero__copy, .km-hero__art { min-width: 0; }
.km-hero__terminal { max-width: 100%; overflow: hidden; }
.km-hero__title { font-size: clamp(3rem, 8vw, 6.2rem); margin: 14px 0 22px; }
.km-hero__lede { max-width: 30rem; font-size: 1.05rem; color: #cfcfcf; }
.km-hero__cta { display: flex; gap: 12px; flex-wrap: wrap; margin: 26px 0 16px; }
.km-hero__note { color: #6f6f6f; font-size: 0.76rem; letter-spacing: 0.04em; }
.km-hero__art { position: relative; display: flex; flex-direction: column; align-items: center; }
.km-hero__sun { color: var(--km-gold); opacity: 0.9; filter: drop-shadow(0 0 40px rgba(198,160,60,0.25)); }
.km-hero__terminal { width: 100%; margin-top: -60px; }
/* On desktop, widen the terminal and pull it left so long commands (git clone)
   fit on one line instead of forcing a scrollbar. */
@media (min-width: 901px) {
  .km-hero__terminal { width: calc(100% + 180px); margin-left: -180px; }
  .km-hero__terminal :deep(.km-code) { font-size: 0.72rem; }
  .km-hero__terminal :deep(pre) { padding: 20px 20px; }
}
@media (max-width: 900px) {
  .km-hero__inner { grid-template-columns: 1fr; }
  .km-hero__sun { display: none; }
  .km-hero__terminal { margin-top: 8px; }
}

/* ---- STATS ---- */
.km-stats { padding-block: clamp(48px, 7vw, 88px); }
.km-stats__eye { display: block; color: #8a8a82; margin-bottom: 22px; }
.km-stats__eye--2 { margin-top: 56px; padding-top: 34px; border-top: 1px solid rgba(0,0,0,0.12); }
.km-stats__grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 20px; }
.km-stat { display: flex; flex-direction: column; gap: 6px; padding: 8px 0; border-top: 2px solid var(--km-black); }
.km-stat__value { font-size: clamp(2.6rem, 6vw, 4.4rem); line-height: 1; }
.km-stat__label { font-weight: 700; letter-spacing: 0.04em; }
.km-stat__sub { color: #6a6a6a; font-size: 0.78rem; }
.km-stat--conf { border-top-color: var(--km-gold); }
.km-stat--conf .km-stat__value { color: var(--km-gold-dk); }
.km-stats__foot { margin-top: 24px; color: #444; font-size: 0.9rem; max-width: 46rem; }
.km-stats__foot code { color: var(--km-gold-dk); font-family: 'JetBrains Mono', monospace; font-size: 0.85em; }
.km-stats__foot strong { color: var(--km-black); }
@media (max-width: 640px) { .km-stats__grid { grid-template-columns: 1fr; } }

/* ---- SECTION HEADINGS ---- */
.km-secthead { display: flex; align-items: flex-start; gap: 20px; margin-bottom: clamp(32px, 5vw, 56px); }
.km-kicker-num { font-size: clamp(1.4rem, 3vw, 2.2rem); line-height: 1; }
.km-secthead__title { font-size: clamp(2rem, 5.5vw, 4rem); }

/* ---- PROPS ---- */
.km-props { display: grid; grid-template-columns: repeat(2, 1fr); gap: 1px; background: var(--km-line); border: 1px solid var(--km-line); }
.km-prop { background: var(--km-black); padding: clamp(24px, 3.4vw, 44px); }
.km-prop__icon { color: var(--km-gold); margin: 0 0 14px; display: block; }
.km-prop__title { font-size: 1.34rem; font-weight: 800; margin-bottom: 10px; }
.km-prop__body { color: #b6b6b6; font-size: 0.98rem; }
@media (max-width: 700px) { .km-props { grid-template-columns: 1fr; } }

/* ---- SHOWCASE ---- */
.km-showcase__lede { max-width: 40rem; color: #4a4a44; margin-bottom: 34px; }
.km-showcase { display: grid; grid-template-columns: 260px 1fr; gap: 0; border: 1px solid rgba(0,0,0,0.14); }
.km-showcase__tabs { display: flex; flex-direction: column; background: #efece4; border-right: 1px solid rgba(0,0,0,0.12); }
.km-showtab {
  text-align: left; background: transparent; border: 0; cursor: pointer;
  padding: 18px 20px; border-bottom: 1px solid rgba(0,0,0,0.08);
  border-left: 3px solid transparent; transition: background .15s ease, border-color .15s ease;
  display: flex; flex-direction: column; gap: 4px;
}
.km-showtab span { font-size: 0.88rem; font-weight: 600; color: #1a1a1a; }
.km-showtab small { color: #6a6a64; font-size: 0.76rem; }
.km-showtab:hover { background: #e7e3d8; }
/* On hover the tab background goes light, so force the labels dark — otherwise
   the active tab's white filename stays white and becomes unreadable. */
.km-showtab:hover span { color: #1a1a1a; }
.km-showtab:hover small { color: #6a6a64; }
.km-showtab--active { background: var(--km-black); border-left-color: var(--km-gold); }
.km-showtab--active span { color: #fff; }
.km-showtab--active small { color: var(--km-gold); }
.km-showcase__code { background: #0e0e0e; }

/* ---- GALLERY (on stone / light) ---- */
.km-gallery__lede { max-width: 42rem; color: #4a4a44; margin-bottom: 34px; }
.km-gallery {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: clamp(16px, 2.4vw, 28px);
}
.km-gallery__card {
  display: flex; flex-direction: column;
  text-decoration: none; color: inherit;
  background: #fff;
  border: 1px solid rgba(0, 0, 0, 0.12);
  border-radius: 14px; overflow: hidden;
  transition: border-color 0.15s ease, transform 0.15s ease, box-shadow 0.15s ease;
}
.km-gallery__card:hover {
  border-color: var(--km-gold);
  transform: translateY(-3px);
  box-shadow: 0 12px 30px rgba(0, 0, 0, 0.10);
}
.km-gallery__shot { display: block; background: #0c0c0c; border-bottom: 1px solid rgba(0, 0, 0, 0.08); }
.km-gallery__shot img { display: block; width: 100%; height: 190px; object-fit: cover; object-position: top left; }
.km-gallery__meta { display: flex; flex-direction: column; flex: 1; padding: 18px; }
.km-gallery__tag { font-size: 0.7rem; text-transform: uppercase; letter-spacing: 0.08em; color: #9a7b1e; }
.km-gallery__title { margin: 0.4rem 0 0.5rem; font-size: 1.05rem; color: #1b1b18; }
.km-gallery__body { margin: 0 0 1rem; font-size: 0.9rem; color: #5a5a52; flex: 1; }
.km-gallery__go { font-weight: 600; color: #9a7b1e; font-size: 0.9rem; }
.km-showcase__code :deep(.km-codeblock) { border: 0; height: 100%; }
@media (max-width: 720px) {
  .km-showcase { grid-template-columns: 1fr; }
  .km-showcase__tabs { flex-direction: row; overflow-x: auto; }
  .km-showtab { border-bottom: 0; border-left: 0; border-top: 3px solid transparent; min-width: 180px; }
  .km-showtab--active { border-left: 0; border-top-color: var(--km-gold); }
}

/* ---- PIPELINE ---- */
.km-pipe__tail { margin-top: 26px; color: var(--km-gold); font-size: 0.9rem; }
.km-pipe__tail code { color: #fff; }

/* ---- COVERAGE ---- */
.km-cov { border-top: 1px solid rgba(0,0,0,0.14); }
.km-cov__row {
  display: grid; grid-template-columns: 1.4fr 0.8fr 3fr auto; gap: 18px; align-items: center;
  padding: 13px 0; border-bottom: 1px solid rgba(0,0,0,0.08);
  text-decoration: none; color: inherit; cursor: pointer;
  transition: background .15s ease;
}
.km-cov__row:hover { background: rgba(0,0,0,0.03); }
.km-cov__row:hover .km-cov__area { color: var(--km-gold-dk); }
.km-cov__area { font-weight: 600; }
.km-cov__group { color: #8a8a82; font-size: 0.74rem; text-transform: uppercase; letter-spacing: 0.08em; }
.km-cov__bar { height: 8px; background: rgba(0,0,0,0.08); position: relative; overflow: hidden; }
.km-cov__fill { position: absolute; inset: 0 auto 0 0; }
/* Caveat layer (wider) sits under the strict layer (narrower), so the bar reads
   as: gold = faithful, amber = works-with-caveats, empty = not implemented. */
.km-cov__fill--caveat { background: var(--km-gold); opacity: 0.42; z-index: 1; }
.km-cov__fill--strict { background: var(--km-gold-dk); z-index: 2; }
.km-cov__legend { display: flex; flex-wrap: wrap; gap: 8px 26px; margin-bottom: 18px; }
.km-cov__key { display: inline-flex; align-items: center; gap: 8px; font-size: 0.8rem; color: #55524a; }
.km-cov__sw { width: 14px; height: 10px; display: inline-block; border-radius: 2px; }
.km-cov__sw--strict { background: var(--km-gold-dk); }
.km-cov__sw--caveat { background: var(--km-gold); opacity: 0.42; }
.km-cov__pct { font-weight: 700; min-width: 3.2em; text-align: right; }
.km-cov__foot { display: flex; align-items: center; gap: 24px; margin-top: 32px; flex-wrap: wrap; }
.km-cov__foot p { color: #55524a; font-size: 0.82rem; max-width: 34rem; }
.km-cov__foot code { color: var(--km-gold-dk); }
@media (max-width: 640px) {
  .km-cov__row { grid-template-columns: 1fr auto; gap: 6px 12px; }
  .km-cov__group { display: none; }
  .km-cov__bar { grid-column: 1 / -1; }
}

/* ---- ORIGIN ---- */
.km-origin { padding-block: clamp(64px, 9vw, 130px); border-top: 1px solid var(--km-line); }
.km-origin__grid { display: grid; grid-template-columns: 1fr 0.8fr; gap: clamp(32px, 6vw, 80px); align-items: center; }
.km-origin__wm {
  font-size: clamp(2.8rem, 7vw, 5rem) !important;
  margin: 14px 0 26px;
}
.km-origin__body { color: #b6b6b6; max-width: 34rem; margin-bottom: 18px; }
.km-origin__body em { color: var(--km-gold); font-style: normal; }
.km-origin__body strong { color: #fff; }
.km-origin__body code { color: #fff; }
.km-origin__link { color: var(--km-gold); text-decoration: underline; text-underline-offset: 2px; }
.km-origin__link:hover { color: #fff; }
.km-origin__flag { display: flex; justify-content: center; }
@media (max-width: 820px) { .km-origin__grid { grid-template-columns: 1fr; } .km-origin__flag { order: -1; } }

/* ---- CTA ---- */
.km-cta { padding-block: clamp(64px, 10vw, 140px); }
.km-cta__inner { text-align: center; display: flex; flex-direction: column; align-items: center; gap: 34px; }
.km-cta__title { font-size: clamp(2rem, 6vw, 4.6rem); }
.km-cta__btns { display: flex; gap: 14px; flex-wrap: wrap; justify-content: center; }
</style>
