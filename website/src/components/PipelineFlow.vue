<template>
  <!-- The compiler pipeline as a single flowing conveyor. The source file enters
       from the top-left and the native binary drops out the bottom-right, so the
       five stages themselves stay on one compact line. Curved gold connectors
       carry the artifact from stage to stage. Bespoke to this section, but keeps
       the site's gold-on-black language. -->
  <div class="km-flow">

    <!-- input — floats above the track, at the start -->
    <div class="km-flow__io km-flow__io--in">
      <span class="km-flow__io-tag km-mono">source</span>
      <code class="km-flow__io-file km-mono">{{ source }}</code>
      <svg class="km-flow__arc km-flow__arc--in" viewBox="0 0 44 46" fill="none" aria-hidden="true">
        <path d="M6 2 C 6 26 10 34 40 40" />
        <path class="km-flow__arrowhead" d="M33 33 L41 40 L31 42" />
      </svg>
    </div>

    <!-- the stages -->
    <div class="km-flow__track" role="list">
      <template v-for="(s, i) in stages" :key="s.name">
        <article class="km-flow__stage" role="listitem">
          <span class="km-flow__ghost km-display" aria-hidden="true">{{ pad(i + 1) }}</span>
          <span class="km-flow__dot" aria-hidden="true"></span>
          <code v-if="s.tool" class="km-flow__tool km-mono">{{ s.tool }}</code>
          <h3 class="km-flow__name">{{ s.name }}</h3>
          <p class="km-flow__desc">{{ s.desc }}</p>
          <span class="km-flow__emits km-mono">
            <span class="km-flow__emits-verb">emits</span> {{ s.out }}
          </span>
        </article>

        <svg
          v-if="i < stages.length - 1"
          class="km-flow__link"
          viewBox="0 0 48 40"
          fill="none"
          aria-hidden="true"
        >
          <path d="M2 20 Q 24 5 46 20" />
          <path class="km-flow__arrowhead" d="M39 14 L46 20 L39 26" />
        </svg>
      </template>
    </div>

    <!-- output — floats below the track, at the end -->
    <div class="km-flow__io km-flow__io--out">
      <svg class="km-flow__arc km-flow__arc--out" viewBox="0 0 44 46" fill="none" aria-hidden="true">
        <path d="M38 44 C 38 20 34 12 4 6" />
        <path class="km-flow__arrowhead" d="M11 13 L3 6 L13 4" />
      </svg>
      <span class="km-flow__io-tag km-mono">runs</span>
      <code class="km-flow__io-file km-mono">{{ output }}</code>
    </div>

  </div>
</template>

<script setup>
defineProps({
  source: { type: String, default: 'app.ts' },
  output: { type: String, default: './app' },
  stages: {
    type: Array,
    default: () => [
      { name: 'Lexer', tool: 'lexer', out: 'tokens', desc: 'Source text becomes a flat token stream.' },
      { name: 'Parser', tool: 'Pratt descent', out: 'AST', desc: 'Recursive descent with precedence climbing builds the tree.' },
      { name: 'Resolver', tool: 'ResolveProgram', out: 'one AST', desc: 'Every transitive import is parsed and merged into one program.' },
      { name: 'LLVM emitter', tool: '~60 emit_*.go', out: '.ll', desc: 'Small domain files write LLVM IR text, one main().' },
      { name: 'clang -O2', tool: 'clang', out: 'binary', desc: 'The real backend turns IR into a host-arch executable.' }
    ]
  }
})

const pad = (n) => String(n).padStart(2, '0')
</script>

<style scoped>
.km-flow {
  display: flex;
  flex-direction: column;
}

/* ---- input / output pills, floated to the corners ---- */
.km-flow__io {
  position: relative;
  flex: 0 0 auto;
  display: inline-flex;
  align-items: baseline;
  gap: 10px;
  padding: 9px 18px;
  border-radius: 999px;
  border: 1px dashed rgba(198,160,60,0.5);
  background: rgba(198,160,60,0.06);
}
.km-flow__io--in { align-self: flex-start; margin: 0 0 14px 8px; }
.km-flow__io--out {
  align-self: flex-end;
  margin: 14px 8px 0 0;
  border-style: solid;
  background: linear-gradient(120deg, rgba(198,160,60,0.14), rgba(198,160,60,0.05));
}
.km-flow__io-tag { font-size: 0.6rem; letter-spacing: 0.16em; text-transform: uppercase; color: #8a8378; }
.km-flow__io-file { font-size: 0.92rem; font-weight: 700; color: var(--km-gold); }
.km-flow__io--out .km-flow__io-file { color: #fff; }

/* the little curved tails linking a pill to the track */
.km-flow__arc {
  position: absolute;
  width: 44px; height: 46px;
  stroke: var(--km-gold);
  stroke-width: 2;
  stroke-linecap: round;
  opacity: 0.75;
}
.km-flow__arc--in { left: 18px; top: 100%; }
.km-flow__arc--out { right: 18px; bottom: 100%; }

/* ---- the one-line track of stages ---- */
.km-flow__track {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  gap: 4px;
}

/* ---- stage node ---- */
.km-flow__stage {
  flex: 1 1 0;
  min-width: 0;
  position: relative;
  overflow: hidden;
  border-radius: 20px;
  background:
    radial-gradient(120% 80% at 50% -20%, rgba(198,160,60,0.14), transparent 60%),
    linear-gradient(180deg, #161616, var(--km-black));
  border: 1px solid var(--km-line);
  padding: 20px 20px 18px;
  display: flex;
  flex-direction: column;
  box-shadow: 0 18px 40px -28px rgba(0,0,0,0.9), inset 0 1px 0 rgba(255,255,255,0.03);
}
/* soft gold crescent at the top edge, purely decorative */
.km-flow__stage::before {
  content: '';
  position: absolute;
  left: 20px; right: 20px; top: 0; height: 2px;
  border-radius: 999px;
  background: linear-gradient(90deg, transparent, var(--km-gold), transparent);
  opacity: 0.7;
}
.km-flow__ghost {
  position: absolute;
  top: -16px; right: 6px;
  font-size: 4.4rem;
  line-height: 1;
  color: rgba(198,160,60,0.08);
  pointer-events: none;
}
.km-flow__dot {
  width: 9px; height: 9px;
  border-radius: 50%;
  background: var(--km-gold);
  box-shadow: 0 0 0 4px rgba(198,160,60,0.14);
  margin-bottom: 12px;
}
.km-flow__tool {
  color: var(--km-gold);
  font-size: 0.68rem;
  letter-spacing: 0.02em;
  margin-bottom: 4px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.km-flow__name { font-size: 1.04rem; font-weight: 800; margin: 0 0 6px; }
.km-flow__desc { color: #a4a4a4; font-size: 0.8rem; margin: 0 0 14px; }
.km-flow__emits {
  margin-top: auto;
  align-self: flex-start;
  font-size: 0.68rem;
  color: #efe6cf;
  background: rgba(198,160,60,0.12);
  border: 1px solid rgba(198,160,60,0.3);
  border-radius: 999px;
  padding: 3px 11px;
}
.km-flow__emits-verb { color: #8a8378; }

/* ---- curved connectors between stages ---- */
.km-flow__link {
  flex: 0 0 26px;
  width: 26px; height: 40px;
  stroke: var(--km-gold);
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.km-flow__arrowhead { stroke-width: 2; }

/* ---- mobile: stack vertically ---- */
@media (max-width: 860px) {
  .km-flow__track { flex-direction: column; align-items: stretch; gap: 0; }
  .km-flow__stage { flex-basis: auto; }
  /* keep the pills at their intrinsic width and centered */
  .km-flow__io--in, .km-flow__io--out { align-self: center; margin: 0; }
  .km-flow__io--in { margin-bottom: 60px; }
  .km-flow__io--out { margin-top: 60px; }
  /* re-center the curved tails so they drop straight into / out of the stack */
  .km-flow__arc--in { left: 50%; transform: translateX(-50%); }
  .km-flow__arc--out { right: auto; left: 50%; transform: translateX(-50%); }
  /* connector rotates to point downward between stacked cards */
  .km-flow__link {
    flex-basis: 30px;
    width: 100%; height: 30px;
    transform: rotate(90deg);
    align-self: center;
    margin: 2px 0;
  }
}
</style>
