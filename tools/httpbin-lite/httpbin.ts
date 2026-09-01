// httpbin-lite — a tiny, local, dependency-free stand-in for httpbin.org,
// started by `make examples` (ADR-00096) so the fetch / async / eventsource /
// streams examples don't depend on a third-party website's uptime. It serves
// only the handful of endpoints those examples exercise — not a general clone.
//
// Written in KlainMainLang and compiled by this project's own compiler
// (dogfooding what the examples demonstrate), replacing the earlier Go version.
// Every route is one `http.listen` handler that routes on `req.method`/
// `req.path`. Binary and streamed bodies use a `ReadableStream<Uint8Array>`
// response body (the `string | ReadableStream` union): that carries a real byte
// count, so `/bytes/{n}`'s embedded null byte survives (ADR-00094/TDD-00026),
// and `/chunked` + the SSE endpoints stream chunk-at-a-time. A slow response
// (`/delay`, `/chunked`) awaits inside the stream's `pull`, so it never blocks
// the event loop — concurrent requests still overlap.

import http from "http";

interface Res {
  status: number;
  body: string | ReadableStream<Uint8Array>;
  headers: Map<string, string>;
}

const enc = new TextEncoder();

function plain(): Map<string, string> {
  const h: Map<string, string> = new Map<string, string>();
  h.set("Content-Type", "text/plain");
  return h;
}

// The last path segment of e.g. "/status/404" or "/bytes/16".
function lastSegment(path: string): string {
  const parts = path.split("/");
  return parts[parts.length - 1];
}

// GET /bytes/{n}: n bytes (0x01, 0x02, …) with a guaranteed embedded null byte
// at offset 5 — the whole point of the route (exercises .arrayBuffer()'s
// byte-count-survives-a-null-byte path, ADR-00094). One enqueue, then close.
function bytesStream(n: number): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start: (c) => {
      const u = new Uint8Array(n);
      for (let i = 0; i < n; i++) u[i] = (i + 1) & 255;
      if (n > 5) u[5] = 0;
      c.enqueue(u);
      c.close();
    },
  });
}

// GET /chunked: three text chunks, each flushed separately after a small delay,
// so a streaming client observes them incrementally (fetch Response.body).
function chunkedStream(): ReadableStream<Uint8Array> {
  let n = 0;
  return new ReadableStream<Uint8Array>({
    pull: async (c) => {
      // Built locally each call — a closure can't capture an array variable.
      const parts = ["alpha ", "beta ", "gamma"];
      if (n >= parts.length) { c.close(); return; }
      await new Promise<void>((r) => setTimeout(() => r(), 30));
      c.enqueue(enc.encode(parts[n]));
      n = n + 1;
    },
  });
}

// GET /delay/{n}: respond after `secs` seconds. The wait is inside pull (an
// awaited setTimeout), so it yields the connection's fiber rather than blocking
// the whole loop — concurrent /delay requests overlap, as httpbin's does.
function delayStream(secs: number): ReadableStream<Uint8Array> {
  // Sleep as a loop of fixed 100ms ticks: the tick count is a captured local,
  // but the setTimeout delay stays a literal — a variable timeout can't be
  // captured through the nested Promise executor. Non-blocking (each await
  // yields the fiber), so concurrent /delay requests still overlap.
  const ticks = Math.round(secs * 10);
  let sent = false;
  return new ReadableStream<Uint8Array>({
    pull: async (c) => {
      if (sent) { c.close(); return; }
      sent = true;
      for (let i = 0; i < ticks; i++) {
        await new Promise<void>((r) => setTimeout(() => r(), 100));
      }
      c.enqueue(enc.encode("delayed"));
    },
  });
}

// GET /stream, /stream-named: text/event-stream. Enqueue the event(s), then
// hold the connection open (a long-idle pull) until the client disconnects —
// the SSE client's own .close() ends it. /stream-retry instead closes after one
// event (a simulated drop); on the auto-reconnect it echoes Last-Event-ID.
function sseStream(initial: string, hold: boolean): ReadableStream<Uint8Array> {
  const payload = initial; // local copy — a closure captures locals, not params
  const holdOpen = hold;
  let started = false;
  return new ReadableStream<Uint8Array>({
    pull: async (c) => {
      if (!started) {
        started = true;
        c.enqueue(enc.encode(payload));
        if (!holdOpen) { c.close(); return; }
        return;
      }
      // Hold the connection open with short, repeated pulls rather than one
      // long sleep: the event loop wakes on each tick, so other connections
      // are still served promptly (a single long timer would let this stream
      // monopolise the loop's select() wait). The client's own .close() ends
      // it; the process exit tears down whatever is left.
      await new Promise<void>((r) => setTimeout(() => r(), 250));
    },
  });
}

const portEnv = process.env.HTTPBIN_LITE_PORT;
const PORT: number = portEnv !== undefined && portEnv !== "" ? parseInt(portEnv, 10) : 8765;

console.error("httpbin-lite (klain) listening on 127.0.0.1:" + PORT);

http.listen(PORT, (req: HttpRequest): Res => {
  const path = req.path;
  const method = req.method;

  // --- JSON identity ---
  if (path === "/get" || path === "/ip") {
    return { status: 200, body: '{"origin":"127.0.0.1"}', headers: plain() };
  }

  // --- arbitrary status: /status/{code} ---
  if (path.indexOf("/status/") === 0) {
    let code = parseInt(lastSegment(path), 10);
    if (Number.isNaN(code)) code = 500;
    return { status: code, body: "status " + code, headers: plain() };
  }

  // --- redirect: /redirect-to?url= ---
  if (path === "/redirect-to") {
    const target = req.query.has("url") ? req.query.get("url") : "/get";
    const h: Map<string, string> = new Map<string, string>();
    h.set("Location", target);
    return { status: 302, body: "", headers: h };
  }

  // --- binary body with an embedded null: /bytes/{n} ---
  if (path.indexOf("/bytes/") === 0) {
    let n = parseInt(lastSegment(path), 10);
    if (Number.isNaN(n) || n <= 0) n = 16;
    return { status: 200, body: bytesStream(n), headers: plain() };
  }

  // --- echo POST body ---
  if (method === "POST" && path === "/post") {
    return { status: 200, body: req.body, headers: plain() };
  }

  // --- DELETE ---
  if (method === "DELETE" && path === "/delete") {
    return { status: 200, body: "deleted", headers: plain() };
  }

  // --- echo request headers ---
  if (path === "/headers") {
    let out = "";
    const keys = req.headers.keys();
    for (const name of keys) {
      out = out + name + ": " + req.headers.get(name) + "\n";
    }
    return { status: 200, body: out, headers: plain() };
  }

  // --- delayed response: /delay/{n} ---
  if (path.indexOf("/delay/") === 0) {
    let secs = parseFloat(lastSegment(path));
    if (Number.isNaN(secs) || secs < 0) secs = 1;
    return { status: 200, body: delayStream(secs), headers: plain() };
  }

  // --- incrementally-flushed chunked body ---
  if (path === "/chunked") {
    return { status: 200, body: chunkedStream(), headers: plain() };
  }

  // --- Server-Sent Events ---
  if (path === "/stream") {
    const h: Map<string, string> = new Map<string, string>();
    h.set("Content-Type", "text/event-stream");
    return { status: 200, body: sseStream("data: hello\n\n", true), headers: h };
  }
  if (path === "/stream-named") {
    const h: Map<string, string> = new Map<string, string>();
    h.set("Content-Type", "text/event-stream");
    return { status: 200, body: sseStream("event: greeting\ndata: hi there\n\ndata: plain\n\n", true), headers: h };
  }
  if (path === "/stream-retry") {
    const h: Map<string, string> = new Map<string, string>();
    h.set("Content-Type", "text/event-stream");
    const lastId = req.headers.has("last-event-id") ? req.headers.get("last-event-id") : "";
    if (lastId === "") {
      return { status: 200, body: sseStream("retry: 100\nid: first-attempt\ndata: dropping soon\n\n", false), headers: h };
    }
    return { status: 200, body: sseStream("data: reconnected, last id was " + lastId + "\n\n", true), headers: h };
  }

  return { status: 404, body: "not found: " + path, headers: plain() };
});
