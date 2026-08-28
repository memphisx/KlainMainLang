<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guide</span>
    <h1>Standard library</h1>
    <p class="km-doc__lede">
      The APIs that make sense for CLI tools and microservices — real servers, real files,
      real crypto — plus the browser-shaped ones that work off-browser.
    </p>

    <h2>HTTP server</h2>
    <p>
      <code>http.listen</code> is at 100% coverage. It transparently accepts cleartext HTTP/2
      (h2c) alongside HTTP/1.1 on the same port.
    </p>
    <CodeBlock filename="http_server.ts" :code="samples.server.code" />

    <h2>fetch</h2>
    <p>
      <code>fetch</code>, <code>Request</code>, <code>Response</code> and <code>Headers</code> are
      in. <code>.text()</code>/<code>.json()</code>/<code>.arrayBuffer()</code> are synchronous,
      and <code>.json()</code> parses a body straight into a declared type.
    </p>
    <CodeBlock filename="fetch.ts" :code="samples.fetch.code" />

    <h2>File system</h2>
    <p>
      <code>fs</code> reads and writes (~93%). Sync and async-shaped variants exist; async runs
      blocking I/O under the hood. <code>readFileSync</code>/<code>writeFileSync</code> are
      text-first — use the binary-aware <code>readFileSyncBytes</code> for binary data.
    </p>
    <CodeBlock filename="fs.ts" :code="fsCode" />

    <h2>Concurrency &amp; workers</h2>
    <p>
      <code>worker_threads</code> and <code>cluster</code> give you real OS threads and processes,
      each running its own event loop and talking over message channels. Shared memory
      (<code>SharedArrayBuffer</code>/<code>Atomics</code>) is available too.
    </p>

    <h2>Web Crypto</h2>
    <p>
      A complete <code>crypto.subtle</code> surface over a selectable backend (OpenSSL or Apple
      CommonCrypto): digest, HMAC, AES-GCM/CBC, RSA-OAEP/PSS, ECDSA, PBKDF2/HKDF, key formats
      raw/pkcs8/spki/jwk.
    </p>

    <h2>The rest</h2>
    <table>
      <thead><tr><th>Area</th><th>Notes</th></tr></thead>
      <tbody>
        <tr><td>Streams</td><td>Web + Node streams (options form and <code>class X extends Readable/Writable</code>), 100%</td></tr>
        <tr><td><code>URL</code> / <code>URLSearchParams</code></td><td>100% (one value per key)</td></tr>
        <tr><td><code>WebSocket</code> / SSE</td><td>Client &amp; server; client speaks <code>wss://</code></td></tr>
        <tr><td><code>events</code> (EventEmitter)</td><td>100%, single payload type per emitter</td></tr>
        <tr><td><code>path</code> / <code>os</code></td><td>100% (POSIX; Linux + Apple Silicon verified)</td></tr>
        <tr><td><code>net</code> / <code>dns</code> / <code>dgram</code> / <code>tls</code> / <code>http2</code> / <code>https</code> / <code>zlib</code></td><td>Done (<code>tls</code> = client + server; <code>http2</code> = h2c server + client sessions; <code>https</code> = client); <code>vm</code> not started</td></tr>
        <tr><td><code>node:test</code></td><td>The test runner — <code>test</code>/<code>describe</code>, subtests, hooks, TAP output, non-zero exit on failure</td></tr>
        <tr><td><code>diagnostics_channel</code></td><td>Named pub/sub (string messages)</td></tr>
      </tbody>
    </table>

    <div class="km-doc__nextrow">
      <router-link to="/docs/language" class="km-btn">← Language guide</router-link>
      <router-link to="/docs/cli" class="km-btn km-btn--gold">CLI &amp; flags →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'
import { samples } from 'src/lib/content.js'

const fsCode = `import { readFileSync, writeFileSync } from 'fs'

const config: string = readFileSync('config.json')
writeFileSync('out.txt', 'done\\n')`
</script>
