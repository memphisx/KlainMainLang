<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guide</span>
    <h1>Language guide</h1>
    <p class="km-doc__lede">
      If it's core TypeScript, it probably compiles. Here's the shape of what's supported —
      and the deliberate sharp edges worth knowing before you lean on them.
    </p>

    <h2>Numbers: JS-faithful by default, sized when you want</h2>
    <p>
      A bare <code>: number</code> is a JS-faithful IEEE-754 double, so untyped arithmetic
      matches JavaScript exactly — <code>0.1 + 0.2</code> yields
      <code>0.30000000000000004</code>, <code>10 / 3</code> yields
      <code>3.3333333333333335</code>. Reach for a sized integer type
      (<code>int8</code>…<code>int64</code>, <code>uint8</code>…<code>uint64</code>), or a JSDoc
      width override, when you want real machine-integer semantics.
    </p>
    <CodeBlock filename="numbers.ts" :code="numbersCode" />

    <h2>Generics &amp; interfaces</h2>
    <p>
      Generic functions, interfaces and classes work, including object/interface/class type
      arguments and <code>&lt;T extends X&gt;</code> constraints. A function's type arguments are
      inference-only today (no explicit <code>identity&lt;number&gt;(5)</code>).
    </p>
    <CodeBlock filename="generics.ts" :code="samples.generics.code" />

    <h2>Classes &amp; OOP</h2>
    <p>
      Classes, inheritance, <code>#private</code> fields, getters/setters, static members and
      <code>[Symbol.iterator]</code> all work. Parameter properties
      (<code>constructor(public x: number)</code>) and the <code>readonly</code> field modifier
      aren't parsed yet — declare the field explicitly. Built-in types aren't valid
      <code>extends</code> targets (no <code>class X extends Error</code>).
    </p>

    <h2>Async / await</h2>
    <p>
      Full marks — <code>async</code>/<code>await</code> and <code>Promise</code> are at 100%
      coverage. Concurrency is cooperative: exactly one fiber runs at a time per thread, no
      preemption, same single-threaded model as JS. Real parallelism lives one level up in
      <code>worker_threads</code>, which spawns actual pthreads.
    </p>
    <CodeBlock filename="fetch.ts" :code="samples.fetch.code" />

    <h2>Unions &amp; narrowing</h2>
    <p>
      Union types allow scalar members and object members (one, or ≥2 as a discriminated union
      with a first-position string-literal tag). Flow narrowing works for a union local or field —
      <code>typeof</code>, truthiness, <code>== null</code>, <code>tag === "literal"</code>, with
      if/else and early return.
    </p>

    <h2>Modules</h2>
    <p>
      Named <code>import</code>/<code>export</code> plus static CommonJS <code>require('&lt;literal&gt;')</code>
      work. There's no real linker: every module you touch is flattened into one AST and one
      <code>main()</code>. Imported files run their top-level code once, in dependency order;
      only import <em>cycles</em> are held to declarations-only.
    </p>

    <h2>What's deliberately out</h2>
    <ul>
      <li><code>Proxy</code> / <code>Reflect</code> — dynamic property intercept.</li>
      <li>Decorators — need metadata reflection.</li>
      <li><code>eval</code> — an opt-in embedded-engine path, not started.</li>
      <li>Runtime property add/delete — objects are fixed-shape heap structs.</li>
    </ul>

    <div class="km-doc__nextrow">
      <router-link to="/docs/getting-started" class="km-btn">← Getting started</router-link>
      <router-link to="/docs/stdlib" class="km-btn km-btn--gold">Standard library →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'
import { samples } from 'src/lib/content.js'

const numbersCode = `const ratio: number = 0.1 + 0.2;   // 0.30000000000000004 (JS-faithful)
const q: number = 10 / 3;          // 3.3333333333333335
let count: int32 = 7;              // opt-in integer semantics: 7 / 2 -> 3
/** @type {uint8} */
let byte = 255;                    // exact width via JSDoc: 255 + 1 -> 0`
</script>
