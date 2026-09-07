<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Start</span>
    <h1>Installation</h1>
    <p class="km-doc__lede">
      The fastest way in is a prebuilt <code>klainmain</code> binary from the latest release; if you'd
      rather build the compiler yourself, or your platform isn't in a given release, build it from
      source. Either way <code>klainmain</code> is the compiler only — it drives <code>clang</code>,
      and a compiled program links nothing beyond plain <code>libc</code> unless it actually uses a
      feature that needs more.
    </p>

    <h2>Install a prebuilt binary</h2>
    <p>
      Every release ships a <code>klainmain</code> for each platform whose test suite passed for that
      version — Linux x64/arm64, macOS x64/arm64 (Apple Silicon), Windows x64 — as
      <code>klainmain-v&lt;version&gt;-&lt;platform&gt;[.exe]</code> with a <code>checksums.txt</code>
      on the <a href="https://github.com/memphisx/KlainMainLang/releases" target="_blank" rel="noopener">GitHub&nbsp;Releases</a>
      page. The one-line installers fetch the right asset, verify its SHA-256, and print
      <code>klainmain --version</code> when done.
    </p>

    <h3>macOS / Linux</h3>
    <CodeBlock lang="bash" terminal label="shell" :code="installUnix" />

    <h3>Windows (PowerShell)</h3>
    <CodeBlock lang="bash" terminal label="powershell" :code="installWin" />

    <p class="km-doc__note">
      Both installers honour <code>KLAINMAIN_VERSION=v0.64.0</code> (pin a specific release instead of
      the latest) and <code>KLAINMAIN_INSTALL_DIR</code> (install elsewhere). If your platform's lane
      was red for a release it's left out of that release and the script says so — it returns with the
      next release that's green there, or build from source below in the meantime.
    </p>
    <p>
      <code>klainmain</code> still needs <strong>clang</strong> on <code>PATH</code> to compile your
      programs (it emits LLVM IR and hands it to clang), plus any
      <a href="#optional-feature-libraries">optional library</a> a program actually uses. On Windows
      that toolchain is the mingw-w64 UCRT sysroot from MSYS2 — see the
      <a href="https://github.com/memphisx/KlainMainLang#windows" target="_blank" rel="noopener">README's Windows section</a>
      for the exact package list.
    </p>

    <h2>Build from source</h2>
    <p>Two things are always required to build the compiler:</p>
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

    <h3>Windows</h3>
    <p>
      Windows x64 builds through the mingw-w64 UCRT toolchain from MSYS2 (Git for Windows supplies the
      Git Bash the Makefile expects). The full package list and <code>PATH</code> ordering live in the
      <a href="https://github.com/memphisx/KlainMainLang#windows" target="_blank" rel="noopener">README's Windows section</a>.
    </p>

    <h3>Clone &amp; build</h3>
    <CodeBlock lang="bash" terminal label="shell" :code="cloneCode" />
    <p>
      That produces <code>./klainmain</code> in the repo root (stamped with <code>git describe</code>;
      <code>make dist</code> cross-compiles every platform into <code>dist/</code>). Point it at a
      <code>.ts</code> file and run the binary it writes next to the source:
    </p>
    <CodeBlock lang="bash" terminal label="shell" :code="verifyCode" />

    <h2 id="optional-feature-libraries">Optional feature libraries</h2>
    <p>
      Every library below is linked <em>only when your program uses the feature</em> — the same
      conditional-linking convention throughout. A program that never touches these stays plain-libc,
      so install a library only when you hit the feature that needs it. This applies whether you
      installed a prebuilt <code>klainmain</code> or built it from source.
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
      install. GMP is an alternative bigint backend via <code>-bigint=gmp</code>. On Windows these come
      from the MSYS2 UCRT packages (see the README's Windows section).
    </p>

    <div class="km-doc__nextrow">
      <router-link to="/docs" class="km-btn">← Overview</router-link>
      <router-link to="/docs/getting-started" class="km-btn km-btn--gold">Getting started →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'

const installUnix = `# → ~/.local/bin/klainmain (no sudo)
$ curl -fsSL https://raw.githubusercontent.com/memphisx/KlainMainLang/main/install.sh | sh
$ klainmain --version`

const installWin = `# → %LOCALAPPDATA%\\Programs\\klainmain\\klainmain.exe (no elevation; added to your PATH)
PS> irm https://raw.githubusercontent.com/memphisx/KlainMainLang/main/install.ps1 | iex
PS> klainmain --version`

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
