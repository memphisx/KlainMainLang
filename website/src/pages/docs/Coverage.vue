<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Reference</span>
    <h1>Coverage matrix</h1>
    <p class="km-doc__lede">
      Two different things live on this page, and it's worth not confusing them. First: how much
      of each <strong>targeted feature area</strong> works. Second: how the compiler scores against
      <strong>full external test suites</strong> — a much harsher, much lower number.
    </p>

    <h2>Targeted feature areas</h2>
    <p>
      These count a feature that works for its core, documented case, mirrored from the project's
      own status matrix (the repository tracks a stricter “zero-caveat” number per page too). They
      say <em>“the paths this compiler targets work”</em> — not <em>“it runs all TypeScript.”</em>
    </p>
    <div class="km-headline">
      <div v-for="h in headline" :key="h.label" class="km-headline__item">
        <span class="km-headline__val km-display">{{ h.value }}</span>
        <span class="km-headline__lbl">{{ h.label }}</span>
        <span class="km-headline__sub km-mono">{{ h.sub }}</span>
      </div>
    </div>

    <h2>External conformance</h2>
    <p>
      Run unfiltered against the public suites, the numbers are low — and shown anyway, because
      hiding them would be dishonest. This is a <strong>typed subset</strong> compiler: most of
      Test262 uses <code>eval</code> as its own assertion mechanism, and Node's tests are dynamic,
      untyped JavaScript against the entire platform. Those files don't fail so much as fall
      <em>outside what this project set out to compile</em>.
    </p>
    <div class="km-headline km-headline--conf">
      <div v-for="c in conformance" :key="c.label" class="km-headline__item">
        <span class="km-headline__val km-display">{{ c.value }}</span>
        <span class="km-headline__lbl">{{ c.label }}</span>
        <span class="km-headline__sub km-mono">{{ c.sub }}</span>
      </div>
    </div>
    <ul>
      <li><strong>Test262</strong> — 6,029 / 53,578 (11.3%) of the whole corpus; 5,236 / 34,334 (15.3%) once the out-of-scope tags (Intl, Temporal, dynamic <code>import()</code>, Proxy/Reflect, module/async/eval flags) are filtered out. The core-<em>language</em> category alone runs at ~21%.</li>
      <li><strong>TypeScript</strong> — accept/reject agreement with <code>tsc</code> over ~9.3k single-file cases; disagreements are dominated by valid TS features this narrow subset doesn't implement.</li>
      <li><strong>Node.js</strong> — of Node's full <code>test/parallel</code> suite, ~2,451 files compile far enough to run and 35 pass. It's a floor on “how much of Node's own suite runs verbatim,” not a module-correctness score.</li>
    </ul>

    <div class="km-covcell__legend">
      <span class="km-covcell__key"><i class="km-covcell__sw km-covcell__sw--strict"></i> Exact match to JS</span>
      <span class="km-covcell__key"><i class="km-covcell__sw km-covcell__sw--caveat"></i> Works, with documented differences</span>
    </div>
    <template v-for="group in groups" :key="group">
      <h2>{{ group }}</h2>
      <table>
        <thead><tr><th>Area</th><th>Coverage</th></tr></thead>
        <tbody>
          <tr v-for="row in rowsFor(group)" :key="row.area">
            <td>{{ row.area }}</td>
            <td>
              <span class="km-covcell" :title="`${row.strict}% exact match · ${row.pct - row.strict}% with documented differences`">
                <span class="km-covcell__bar">
                  <span class="km-covcell__fill km-covcell__fill--caveat" :style="{ width: row.pct + '%' }"></span>
                  <span class="km-covcell__fill km-covcell__fill--strict" :style="{ width: row.strict + '%' }"></span>
                </span>
                <span class="km-mono">{{ row.pct }}%</span>
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </template>

    <h2>Known sharp edges</h2>
    <ul>
      <li>Compiling to native doesn't make <code>: number</code> a machine integer — it stays an IEEE-754 double, exactly as in JS. For real integer semantics reach for a sized <code>int8</code>…<code>uint64</code> type (or a JSDoc width) — a KlainMainLang extension that standard <code>tsc</code> doesn't recognize.</li>
      <li>Concurrency is cooperative — one fiber at a time per thread, no preemption.</li>
      <li>Nothing is dynamic — no <code>Proxy</code>, no runtime property add/delete.</li>
      <li>Whole-program compile only — there's no separate compilation or link step.</li>
    </ul>

    <p class="km-mono km-covnote">Figures as of 2026-08-27. The repository's status pages are the source of truth.</p>

    <div class="km-doc__nextrow">
      <router-link to="/docs/cli" class="km-btn">← CLI &amp; flags</router-link>
      <router-link to="/docs/examples" class="km-btn km-btn--gold">Examples →</router-link>
    </div>
  </article>
</template>

<script setup>
import { coverage, headline, conformance } from 'src/lib/content.js'

const groups = ['Language', 'Web platform', 'Node.js']
function rowsFor (g) { return coverage.filter(r => r.group === g) }
</script>

<style scoped>
.km-headline { display: grid; grid-template-columns: repeat(3, 1fr); gap: 1px; background: var(--km-line); border: 1px solid var(--km-line); margin: 0 0 24px; }
.km-headline__item { background: #0e0e0e; padding: 22px; display: flex; flex-direction: column; gap: 4px; }
.km-headline--conf { margin-top: 4px; }
.km-headline__val { font-size: 2.4rem; color: var(--km-gold); line-height: 1; }
.km-headline__lbl { font-weight: 700; font-size: 0.9rem; }
.km-headline__sub { color: #7a7a7a; font-size: 0.74rem; }
.km-covcell { display: inline-flex; align-items: center; gap: 12px; min-width: 180px; }
.km-covcell__bar { flex: 1; height: 6px; background: #262626; position: relative; overflow: hidden; }
/* Two layers: the wide caveat layer (dim) sits under the narrow strict layer
   (solid), so the bar reads as solid = exact match, dim = works-with-documented
   -differences, empty = not implemented — mirroring the landing page. */
.km-covcell__fill { position: absolute; inset: 0 auto 0 0; background: var(--km-gold); }
.km-covcell__fill--caveat { opacity: 0.4; z-index: 1; }
.km-covcell__fill--strict { opacity: 1; z-index: 2; }
.km-covcell__legend { display: flex; flex-wrap: wrap; gap: 8px 26px; margin: 0 0 16px; }
.km-covcell__key { display: inline-flex; align-items: center; gap: 8px; font-size: 0.8rem; color: #9a9a9a; }
.km-covcell__sw { width: 14px; height: 10px; display: inline-block; border-radius: 2px; background: var(--km-gold); }
.km-covcell__sw--strict { opacity: 1; }
.km-covcell__sw--caveat { opacity: 0.4; }
.km-covnote { color: #6a6a6a; font-size: 0.78rem; margin-top: 18px; }
@media (max-width: 600px) { .km-headline { grid-template-columns: 1fr; } }
</style>
