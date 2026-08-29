<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">klain: namespace</span>
    <h1><code>klain:http</code> — handler-returns-response server</h1>
    <p class="km-doc__lede">
      Node's <code>http.createServer</code> is also implemented (under the <code>http</code> name),
      but <code>klain:http</code> is this project's own model: <code>listen(port, req ⇒ response)</code>,
      where the handler simply returns a typed <code>{ status, body, headers }</code> object. Same
      runtime, explicitly non-Node shape.
    </p>
    <CodeBlock filename="server.ts" :code="httpCode" />
    <p>
      The server transparently accepts cleartext HTTP/2 (h2c) alongside HTTP/1.1 on the same port,
      and the handler's return type is checked at compile time. Reach for the Node-shaped
      <code>http.createServer</code> when you want the streaming <code>res</code> API; reach for
      <code>klain:http</code> when a pure request→response function is all you need.
    </p>

    <div class="km-doc__nextrow">
      <router-link to="/docs/klain/webview" class="km-btn">← klain:webview</router-link>
      <router-link to="/docs/klain/sync" class="km-btn km-btn--gold">klain:sync →</router-link>
    </div>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'

const httpCode = `import http from 'klain:http'

interface Res { status: number; body: string; headers: Map<string, string> }

http.listen(8080, (req: HttpRequest): Res => {
  return { status: 200, body: 'hello ' + req.path, headers: new Map() }
})`
</script>
