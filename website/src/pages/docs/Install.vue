<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Start</span>
    <h1>Installation</h1>
    <p class="km-doc__lede">
      There's no installer and no package to download — you build the compiler from source. It's a
      single Go binary once built, and a compiled program links nothing beyond plain <code>libc</code>
      unless it actually uses a feature that needs more.
    </p>

    <h2>Core toolchain</h2>
    <p>Two things are always required:</p>
    <ul>
      <li><strong>Go 1.26+</strong> — builds the compiler itself (see <code>go.mod</code> for the exact pinned version).</li>
      <li><strong>clang</strong> (LLVM 15+, opaque-pointer support) — the backend that turns emitted LLVM IR into a native binary.</li>
    </ul>

    <h3>macOS (Apple Silicon or Intel)</h3>
    <CodeBlock lang="bash" terminal label="shell" :code="macCore" />

    <h3>Debian / Ubuntu</h3>
    <CodeBlock lang="bash" terminal label="shell" :code="debCore" />

    <h3>Alpine</h3>
    <CodeBlock lang="bash" terminal label="shell" :code="alpineCore" />

    <h2>Clone &amp; build</h2>
    <CodeBlock lang="bash" terminal label="shell" :code="cloneCode" />
    <p>
      That produces <code>./klainmain</code> in the repo root. Point it at a <code>.ts</code> file and
      run the binary it writes next to the source:
    </p>
    <CodeBlock lang="bash" terminal label="shell" :code="verifyCode" />

    <h2>Optional feature libraries</h2>
    <p>
      Every library below is linked <em>only when your program uses the feature</em> — the same
      conditional-linking convention throughout. A program that never touches these stays plain-libc,
      so install a library only when you hit the feature that needs it.
    </p>
    <table>
      <thead><tr><th>Feature</th><th>Library</th><th>Install</th></tr></thead>
      <tbody>
        <tr><td><code>fetch</code> / <code>http.listen</code></td><td>libcurl</td><td><code>brew install curl</code> · <code>apt-get install libcurl4-openssl-dev</code> · <code>apk add curl-dev</code></td></tr>
        <tr><td><code>http.listen</code> (h2c)</td><td>libnghttp2</td><td><code>brew install nghttp2</code> · <code>apt-get install libnghttp2-dev</code> · <code>apk add nghttp2-dev</code></td></tr>
        <tr><td><code>RegExp</code></td><td>libpcre2-8</td><td><code>brew install pcre2</code> · <code>apt-get install libpcre2-dev</code> · <code>apk add pcre2-dev</code></td></tr>
        <tr><td><code>crypto.subtle</code> / <code>tls</code> / <code>wss://</code></td><td>OpenSSL 3</td><td><code>brew install openssl@3</code> · <code>apt-get install libssl-dev</code> · <code>apk add openssl-dev</code></td></tr>
        <tr><td><code>bigint</code></td><td>libtommath (default)</td><td><code>brew install libtommath</code> · <code>apt-get install libtommath-dev</code> · <code>apk add libtommath-dev</code></td></tr>
        <tr><td><code>-mm=gc</code> (opt-in GC)</td><td>bdw-gc / libgc</td><td><code>brew install bdw-gc</code> · <code>apt-get install libgc-dev</code> · <code>apk add gc-dev</code></td></tr>
      </tbody>
    </table>
    <p class="km-doc__note">
      <code>crypto.getRandomValues</code> / <code>randomUUID</code> use the OS CSPRNG directly and need
      no library. On macOS, <code>-crypto=commoncrypto</code> uses the built-in CommonCrypto with zero
      install. GMP is an alternative bigint backend via <code>-bigint=gmp</code>.
    </p>

    <div class="km-doc__nextrow">
      <router-link to="/docs" class="km-btn">← Overview</router-link>
      <router-link to="/docs/getting-started" class="km-btn km-btn--gold">Getting started →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'

const macCore = `# Homebrew — https://brew.sh
$ brew install go llvm
# clang from the llvm formula, or use Xcode's:  xcode-select --install`

const debCore = `$ sudo apt-get update
$ sudo apt-get install golang clang`

const alpineCore = `$ apk add go clang`

const cloneCode = `$ git clone https://github.com/memphisx/KlainMainLang
$ cd KlainMainLang
$ make build            # → ./klainmain`

const verifyCode = `$ ./klainmain examples/basics/basics.ts   # → examples/basics/basics
$ ./examples/basics/basics                # run it yourself`
</script>
