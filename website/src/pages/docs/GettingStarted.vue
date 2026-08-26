<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Start</span>
    <h1>Getting started</h1>
    <p class="km-doc__lede">
      Build the compiler, point it at a <code>.ts</code> file, and run the native binary it writes.
    </p>

    <h2>Requirements</h2>
    <ul>
      <li><strong>Go 1.26+</strong> — to build the compiler itself.</li>
      <li><strong>clang</strong> (LLVM 15+, opaque-pointer support) — the backend that turns emitted IR into a binary.</li>
      <li>Optional, linked <em>only when your program uses the feature</em>: <code>libcurl</code> (for <code>fetch</code> / <code>http.listen</code>), <code>libnghttp2</code> (for <code>http.listen</code>), <code>libpcre2</code> (for <code>RegExp</code>), OpenSSL 3 (for <code>crypto.subtle</code> / <code>tls</code>), <code>bdw-gc</code> (only for <code>-mm=gc</code>).</li>
    </ul>

    <h2>Build the compiler</h2>
    <CodeBlock lang="bash" terminal label="shell" :code="buildCode" />

    <h2>Compile and run a file</h2>
    <p>Compiling produces a native binary next to the source — it does <em>not</em> run it.</p>
    <CodeBlock lang="bash" terminal label="shell" :code="runCode" />

    <h2>Your first program</h2>
    <CodeBlock filename="hello.ts" :code="helloCode" />
    <p>Then:</p>
    <CodeBlock lang="bash" terminal label="shell" :code="'$ ./klainmain hello.ts\n$ ./hello\nhello, native world'" />

    <h2>Handy make targets</h2>
    <table>
      <thead><tr><th>Target</th><th>Description</th></tr></thead>
      <tbody>
        <tr><td><code>make build</code></td><td>Compile the compiler to <code>./klainmain</code></td></tr>
        <tr><td><code>make run FILE=f.ts</code></td><td>Compile <strong>and</strong> run a single file</td></tr>
        <tr><td><code>make compile FILE=f.ts</code></td><td>Compile a file to a binary (don't run it)</td></tr>
        <tr><td><code>make ir FILE=f.ts</code></td><td>Emit LLVM IR only, no binary</td></tr>
        <tr><td><code>make examples</code></td><td>Compile and run every example — the readable regression suite</td></tr>
      </tbody>
    </table>

    <div class="km-doc__nextrow">
      <router-link to="/docs" class="km-btn">← Overview</router-link>
      <router-link to="/docs/language" class="km-btn km-btn--gold">Language guide →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'

const buildCode = `$ make build            # produces ./klainmain`

const runCode = `$ ./klainmain examples/basics/basics.ts   # → examples/basics/basics
$ ./examples/basics/basics                # run it yourself
$ ./klainmain -o myapp app.ts             # custom output name`

const helloCode = `const who: string = "native world";
console.log("hello, " + who);`
</script>
