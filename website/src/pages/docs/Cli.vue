<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guide</span>
    <h1>CLI &amp; flags</h1>
    <p class="km-doc__lede">
      One binary, <code>klainmain</code>. Point it at a file; tune the trade-offs per compile.
    </p>

    <CodeBlock lang="bash" terminal label="usage" :code="usage" />

    <h2>Flags</h2>
    <table>
      <thead><tr><th>Flag</th><th>What it does</th></tr></thead>
      <tbody>
        <tr><td><code>--emit-llvm</code></td><td>Emit LLVM IR to stdout and stop — don't compile.</td></tr>
        <tr><td><code>-o &lt;name&gt;</code></td><td>Output binary name (default: input path without <code>.ts</code>).</td></tr>
        <tr><td><code>--static</code></td><td>Build a self-contained binary — the non-system libraries link statically instead of as dynamic dependencies. <strong>Linux:</strong> fully static (incl. libc), for a scratch/distroless image. <strong>Windows:</strong> statically links the mingw runtime, <code>pcre2</code>, and the <code>libcurl</code> chain, so the <code>.exe</code> needs no DLLs beside it (the UCRT and core Win32 DLLs stay dynamic — they ship with Windows 10/11). <strong>macOS:</strong> refuses cleanly (no static libSystem). Without it, builds are dynamic on every platform.</td></tr>
        <tr><td><code>-mm &lt;mode&gt;</code></td><td>Memory management: <code>manual</code> (default — <code>Memory.free(x)</code> only) or <code>gc</code> (Boehm GC, needs <code>bdw-gc</code>). Identical on Linux and macOS.</td></tr>
        <tr><td><code>-bigint &lt;lib&gt;</code></td><td>BigInt backend, linked only when used: <code>libtommath</code> (default) or <code>gmp</code>. Identical semantics — trades license/speed.</td></tr>
        <tr><td><code>-crypto &lt;lib&gt;</code></td><td><code>crypto.subtle</code> backend: <code>openssl</code> (default) or <code>commoncrypto</code> (macOS only, no OpenSSL dependency).</td></tr>
        <tr><td><code>-compat &lt;m&gt;</code></td><td><code>strict</code> (default — opinionated, safer-than-JS) or <code>js</code> (best-effort JS-faithful, e.g. global shadowing).</td></tr>
        <tr><td><code>-regex &lt;m&gt;</code></td><td>RegExp dialect: <code>es-unicode</code> (default) / <code>ecmascript</code> / <code>es-utf16</code> / <code>es-ascii</code> / <code>pcre</code>.</td></tr>
      </tbody>
    </table>

    <h2>Linking, briefly</h2>
    <p>
      Programs are pure libc by default. A binary only links an extra library when it actually
      uses the feature — <code>libcurl</code> for <code>fetch</code>/<code>http.listen</code>,
      <code>libnghttp2</code> for <code>http.listen</code>, <code>libpcre2</code> for
      <code>RegExp</code>, OpenSSL for <code>crypto.subtle</code>/<code>tls</code>. By default those
      are dynamic dependencies — closer to typical C/C++ toolchain output than a self-contained Go
      binary — so a dynamic binary assumes the target machine has them (the same assumption on every
      platform: a Linux binary needs <code>libcurl.so</code> present, a Windows one needs the DLL).
    </p>
    <p>
      Pass <code>--static</code> when you want a single self-contained binary that runs anywhere with
      no external dependency. That's the flag to build a distributable CLI/TUI tool or a desktop app
      — it's how the <router-link to="/docs/examples">showcase apps</router-link> are built. Without
      it, distributing a dynamic build is a packaging step: on Linux/macOS you declare the dependency
      (apt/Homebrew), on Windows you bundle the required DLLs alongside the <code>.exe</code> (or,
      where a license forbids redistribution, point users to the vendor's download).
    </p>

    <blockquote>
      <p>
        Left in <code>manual</code> mode, a program's memory footprint is a monotonically
        increasing function of its runtime — a <em>feature</em> for short-lived CLI tools, a
        <em>life choice</em> for anything long-running. Want automatic collection? Pass
        <code>-mm=gc</code>.
      </p>
    </blockquote>

    <div class="km-doc__nextrow">
      <router-link to="/docs/stdlib" class="km-btn">← Standard library</router-link>
      <router-link to="/docs/coverage" class="km-btn km-btn--gold">Coverage matrix →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'

const usage = `klainmain [flags] <file.ts>

# Compile to a native binary (does NOT run it)
$ klainmain app.ts

# Compile and run in one step
$ make run FILE=app.ts

# Inspect the generated LLVM IR
$ make ir FILE=app.ts`
</script>
