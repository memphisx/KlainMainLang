package tests

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TDD-00097 Stage 1: WHATWG ReadableStream core.

func TestE2EStreamsStartEnqueueRead(t *testing.T) {
	assertOutput(t, `
const rs = new ReadableStream<number>({
  start: (controller) => {
    controller.enqueue(1);
    controller.enqueue(2);
    controller.close();
  }
});
const reader = rs.getReader();
const a = await reader.read();
console.log(a.value, a.done);
const b = await reader.read();
console.log(b.value, b.done);
const c = await reader.read();
console.log(c.value, c.done);
`, "1 false\n2 false\nundefined true")
}

// TDD-00196: reader.read() after close yields a real `{ value: undefined, done:
// true }` record (not the chunk type's zero), and controller.desiredSize is
// `null` once errored (vs `0` once merely closed) — the last `T | undefined`
// adopters of TDD-00187, reusing the shared sentinel.
func TestE2EStreamsReadUndefinedOnClose(t *testing.T) {
	assertOutput(t, `
const rs = new ReadableStream<number>({
  start: (c) => { c.enqueue(7); c.close(); }
});
const r = rs.getReader();
const a = await r.read();
console.log(a.value, a.done);
const b = await r.read();
console.log(b.value, b.done, b.value === undefined);
// A pointer (string) chunk reads undefined, not "", after close.
const sr = new ReadableStream<string>({ start: (c) => { c.close(); } }).getReader();
const s = await sr.read();
console.log(s.value, s.done, s.value === undefined);
`, "7 false\nundefined true true\nundefined true true")
}

func TestE2EStreamsDesiredSizeNullWhenErrored(t *testing.T) {
	assertOutput(t, `
new ReadableStream<number>({
  start: (c) => {
    console.log("readable:", c.desiredSize);
    c.close();
    console.log("closed:", c.desiredSize);
  }
});
new ReadableStream<number>({
  start: (c) => { c.error(new Error("boom")); console.log("errored:", c.desiredSize); }
});
`, "readable: 1\nclosed: 0\nerrored: null")
}

func TestE2EStreamsPullBackpressureForAwait(t *testing.T) {
	assertOutput(t, `
let n = 0;
const rs = new ReadableStream<number>({
  pull: (controller) => {
    n = n + 1;
    console.log("pull", n);
    if (n > 3) { controller.close(); } else { controller.enqueue(n * 10); }
  }
}, { highWaterMark: 1 });
for await (const chunk of rs) {
  console.log("chunk", chunk);
}
console.log("done", rs.locked);
`, "pull 1\npull 2\nchunk 10\npull 3\nchunk 20\npull 4\nchunk 30\ndone false")
}

func TestE2EStreamsErrorRejectsRead(t *testing.T) {
	assertOutput(t, `
const bad = new ReadableStream<string>({
  start: (c) => { c.error(new Error("boom")); }
});
const r = bad.getReader();
try {
  await r.read();
} catch (e) {
  console.log("caught:", (e as Error).message);
}
`, "caught: boom")
}

func TestE2EStreamsFromArrayValues(t *testing.T) {
	assertOutput(t, `
const fs = ReadableStream.from([7, 8, 9]);
for await (const v of fs.values()) { console.log("v", v); }
`, "v 7\nv 8\nv 9")
}

func TestE2EStreamsCancelRunsSourceCancel(t *testing.T) {
	assertOutput(t, `
let cancelled = false;
const cs = new ReadableStream<number>({
  pull: (c) => { c.enqueue(1); },
  cancel: () => { cancelled = true; }
});
const cr = cs.getReader();
await cr.cancel();
console.log("cancelled", cancelled);
const rec = await cr.read();
console.log("after cancel done:", rec.done);
`, "cancelled true\nafter cancel done: true")
}

func TestE2EStreamsSizeAlgorithmDesiredSize(t *testing.T) {
	assertOutput(t, `
const sized = new ReadableStream<string>({
  start: (c) => { console.log("ds0", c.desiredSize); c.enqueue("aa"); console.log("ds1", c.desiredSize); c.close(); }
}, { highWaterMark: 4, size: (chunk) => chunk.length });
const sr = sized.getReader();
const a = await sr.read();
console.log(a.value, a.done);
`, "ds0 4\nds1 2\naa false")
}

func TestE2EStreamsAsyncPull(t *testing.T) {
	assertOutput(t, `
const ap = new ReadableStream<number>({
  pull: async (c) => { const x = await Promise.resolve(42); c.enqueue(x); c.close(); }
});
for await (const x of ap) { console.log("async pull chunk", x); }
`, "async pull chunk 42")
}

func TestE2EStreamsByteChunksByteLengthStrategy(t *testing.T) {
	assertOutput(t, `
const bytes = new ReadableStream<Uint8Array>({
  start: (c) => { const u = new Uint8Array([1, 2, 3]); c.enqueue(u); c.close(); }
}, new ByteLengthQueuingStrategy({ highWaterMark: 16 }));
for await (const u of bytes) { console.log("bytes", u.length, u[0], u[2]); }
`, "bytes 3 1 3")
}

func TestE2EStreamsDestructuredReadClosedLock(t *testing.T) {
	assertOutput(t, `
const one = ReadableStream.from(["hello"]);
const rd = one.getReader();
const { value, done } = await rd.read();
console.log(value, done);
await rd.read();
await rd.closed;
console.log("closed ok", one.locked);
rd.releaseLock();
console.log("released", one.locked);
`, "hello false\nclosed ok true\nreleased false")
}

func TestE2EStreamsLockedGetReaderThrows(t *testing.T) {
	assertOutput(t, `
const rs = ReadableStream.from([1]);
const r1 = rs.getReader();
try {
  rs.getReader();
} catch (e) {
  console.log("locked:", (e as Error).message);
}
`, "locked: ReadableStream is already locked to a reader")
}

func TestE2EStreamsEnqueueAfterCloseThrows(t *testing.T) {
	assertOutput(t, `
const rs = new ReadableStream<number>({
  start: (c) => {
    c.close();
    try {
      c.enqueue(1);
    } catch (e) {
      console.log("throws:", (e as Error).message);
    }
  }
});
console.log("ok");
`, "throws: cannot enqueue on a closed or errored ReadableStream\nok")
}

func TestE2EStreamsParkedReadFulfilledByLaterEnqueue(t *testing.T) {
	// A read() issued while the queue is empty parks; a later enqueue from a
	// timer fulfills it — proving the pending-read promise path.
	assertOutput(t, `
const rs = new ReadableStream<number>({
  start: (c) => { setTimeout(() => { c.enqueue(99); c.close(); }, 5); }
});
const r = rs.getReader();
const rec = await r.read();
console.log("late", rec.value, rec.done);
`, "late 99 false")
}

// TDD-00097 Stage 2: WritableStream.

func TestE2EStreamsWritableDesiredSizeNullWhenErrored(t *testing.T) {
	// WritableStream's desiredSize is `null` once errored (vs a number while
	// writable) — the same `number | null` fix as the ReadableStream controller
	// (TDD-00196/ADR-00826), applied to the writer side too.
	assertOutput(t, `
const ws = new WritableStream<string>({ write: (c) => {} });
const w = ws.getWriter();
console.log("writable:", w.desiredSize);
await w.abort(new Error("boom"));
console.log("errored:", w.desiredSize, w.desiredSize === null);
`, "writable: 1\nerrored: null true")
}

func TestE2EStreamsWritableBasicWriteClose(t *testing.T) {
	assertOutput(t, `
const written: string[] = [];
const ws = new WritableStream<string>({
  write: (chunk) => { written.push(chunk); console.log("sink got", chunk); }
});
const w = ws.getWriter();
await w.write("a");
await w.write("b");
await w.close();
await w.closed;
console.log("all:", written.join(","), "locked:", ws.locked);
`, "sink got a\nsink got b\nall: a,b locked: true")
}

func TestE2EStreamsWritableSlowSinkSerializes(t *testing.T) {
	assertOutput(t, `
let active = 0;
const slow = new WritableStream<number>({
  write: async (chunk) => {
    active = active + 1;
    console.log("start", chunk, "active", active);
    await new Promise<void>((res) => setTimeout(() => res(), 5));
    active = active - 1;
    console.log("end", chunk);
  },
  close: () => { console.log("sink closed"); }
});
const w = slow.getWriter();
w.write(1);
w.write(2);
console.log("desired after 2 writes:", w.desiredSize);
await w.close();
console.log("closed");
`, "desired after 2 writes: -1\nstart 1 active 1\nend 1\nstart 2 active 1\nend 2\nsink closed\nclosed")
}

func TestE2EStreamsWritableReadyBackpressure(t *testing.T) {
	assertOutput(t, `
const bp = new WritableStream<number>({
  write: async (c) => { await new Promise<void>((res) => setTimeout(() => res(), 3)); }
}, { highWaterMark: 1 });
const bw = bp.getWriter();
console.log("desired0:", bw.desiredSize);
bw.write(1);
bw.write(2);
console.log("desired mid:", bw.desiredSize);
await bw.ready;
console.log("ready resolved, desired:", bw.desiredSize);
await bw.close();
`, "desired0: 1\ndesired mid: -1\nready resolved, desired: 1")
}

func TestE2EStreamsWritableSinkRejectionErrors(t *testing.T) {
	assertOutput(t, `
const bad = new WritableStream<number>({
  write: async (c) => { await Promise.resolve(); throw new Error("sinkfail"); }
});
const w = bad.getWriter();
try {
  await w.write(1);
} catch (e) {
  console.log("write rejected:", (e as Error).message);
}
try {
  await w.closed;
} catch (e) {
  console.log("closed rejected:", (e as Error).message);
}
`, "write rejected: sinkfail\nclosed rejected: sinkfail")
}

func TestE2EStreamsWritableAbortRejectsQueued(t *testing.T) {
	assertOutput(t, `
let aborted = false;
const ab = new WritableStream<number>({
  write: async (c) => { await new Promise<void>((r) => setTimeout(() => r(), 50)); },
  abort: () => { aborted = true; }
});
const aw = ab.getWriter();
const p1 = aw.write(1);
const p2 = aw.write(2);
await aw.abort();
console.log("aborted flag:", aborted);
try { await p2; } catch (e) { console.log("queued write rejected"); }
console.log("done");
`, "aborted flag: true\nqueued write rejected\ndone")
}

func TestE2EStreamsMethodShorthandSource(t *testing.T) {
	assertOutput(t, `
const rs = new ReadableStream<number>({
  start(c) { c.enqueue(5); c.close(); }
});
for await (const v of rs) { console.log(v); }
`, "5")
}

// TDD-00097 Stage 3: pipeTo/pipeThrough, TransformStream, tee().

func TestE2EStreamsPipeToBackpressure(t *testing.T) {
	assertOutput(t, `
let pulls = 0;
const src = new ReadableStream<number>({
  pull: (c) => {
    pulls = pulls + 1;
    if (pulls > 3) { c.close(); } else { c.enqueue(pulls); }
  }
}, { highWaterMark: 1 });
const out: number[] = [];
const dst = new WritableStream<number>({
  write: async (chunk) => {
    await new Promise<void>((r) => setTimeout(() => r(), 2));
    out.push(chunk);
    console.log("wrote", chunk);
  },
  close: () => { console.log("sink close"); }
}, { highWaterMark: 1 });
await src.pipeTo(dst);
console.log("piped:", out.join(","));
`, "wrote 1\nwrote 2\nwrote 3\nsink close\npiped: 1,2,3")
}

func TestE2EStreamsPipeThroughTransform(t *testing.T) {
	assertOutput(t, `
const upper = new TransformStream<string, string>({
  transform: (chunk, controller) => { controller.enqueue(chunk.toUpperCase()); }
});
const words = ReadableStream.from(["klain", "main", "lang"]);
for await (const w of words.pipeThrough(upper)) {
  console.log(w);
}
`, "KLAIN\nMAIN\nLANG")
}

func TestE2EStreamsTransformTypeChangeFlush(t *testing.T) {
	assertOutput(t, `
const lens = new TransformStream<string, number>({
  transform: (chunk, c) => { c.enqueue(chunk.length); },
  flush: (c) => { c.enqueue(-1); }
});
for await (const n of ReadableStream.from(["a", "bb", "ccc"]).pipeThrough(lens)) {
  console.log("len", n);
}
`, "len 1\nlen 2\nlen 3\nlen -1")
}

func TestE2EStreamsTee(t *testing.T) {
	assertOutput(t, `
const [t1, t2] = ReadableStream.from([10, 20]).tee();
for await (const x of t1) { console.log("b1", x); }
for await (const y of t2) { console.log("b2", y); }
`, "b1 10\nb1 20\nb2 10\nb2 20")
}

func TestE2EStreamsChainedPipeThroughPipeTo(t *testing.T) {
	assertOutput(t, `
const double = new TransformStream<number, number>({
  transform: (c2, ctl) => { ctl.enqueue(c2 * 2); }
});
const collected: number[] = [];
const sink = new WritableStream<number>({ write: (v) => { collected.push(v); } });
await ReadableStream.from([1, 2, 3]).pipeThrough(double).pipeTo(sink);
console.log("chain:", collected.join(","));
`, "chain: 2,4,6")
}

func TestE2EStreamsPipePropagationRules(t *testing.T) {
	assertOutput(t, `
let sinkAborted = false;
const badSrc = new ReadableStream<number>({
  pull: (c) => { c.error(new Error("srcboom")); }
});
const d1 = new WritableStream<number>({
  write: (v) => {},
  abort: () => { sinkAborted = true; }
});
try {
  await badSrc.pipeTo(d1);
} catch (e) {
  console.log("pipe rejected:", (e as Error).message, "sinkAborted:", sinkAborted);
}
let srcCancelled = false;
const s2 = new ReadableStream<number>({
  pull: (c) => { c.enqueue(1); },
  cancel: () => { srcCancelled = true; }
});
const badDst = new WritableStream<number>({
  write: async (v) => { await Promise.resolve(); throw new Error("dstboom"); }
});
try {
  await s2.pipeTo(badDst);
} catch (e) {
  console.log("pipe rejected:", (e as Error).message, "srcCancelled:", srcCancelled);
}
let aborted2 = false;
const badSrc2 = new ReadableStream<number>({
  pull: (c) => { c.error(new Error("boom2")); }
});
const d2 = new WritableStream<number>({ write: (v) => {}, abort: () => { aborted2 = true; } });
try {
  await badSrc2.pipeTo(d2, { preventAbort: true });
} catch (e) {
  console.log("preventAbort kept sink:", !aborted2);
}
`, "pipe rejected: srcboom sinkAborted: true\npipe rejected: dstboom srcCancelled: true\npreventAbort kept sink: true")
}

func TestE2EStreamsPipeToAbortSignal(t *testing.T) {
	assertOutput(t, `
const ac = new AbortController();
const slowSrc = new ReadableStream<number>({
  pull: async (c) => { await new Promise<void>((r) => setTimeout(() => r(), 30)); c.enqueue(1); }
});
const d3 = new WritableStream<number>({ write: (v) => {} });
setTimeout(() => { ac.abort(); }, 5);
try {
  await slowSrc.pipeTo(d3, { signal: ac.signal });
} catch (e) {
  console.log("signal aborted the pipe");
}
console.log("done");
`, "signal aborted the pipe\ndone")
}

// TDD-00097 Stage 4: fetch Response.body streaming.

func TestE2EStreamsFetchBodyStreaming(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		for _, part := range []string{"alpha ", "beta ", "gamma"} {
			fmt.Fprint(w, part)
			fl.Flush()
			time.Sleep(30 * time.Millisecond)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	src := fmt.Sprintf(`
const res = await fetch("%s/chunked");
console.log("status", res.status);
const decoder = new TextDecoder();
let text = "";
let chunks = 0;
for await (const chunk of res.body) {
  chunks = chunks + 1;
  text = text + decoder.decode(chunk);
}
console.log("multiple chunks:", chunks >= 2, "text:", text);
`, srv.URL)
	assertOutput(t, src, "status 200\nmultiple chunks: true text: alpha beta gamma")
}

func TestE2EStreamsFetchBodyThenTextStillWorks(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
const res = await fetch("%s/flat");
console.log("ok", res.ok);
const body = await res.text();
console.log("len>0", body.length > 0);
console.log("again", (await res.text()).length > 0);
`, srv.URL)
	assertOutput(t, src, "ok true\nlen>0 true\nagain true")
}

func TestE2EStreamsFetchBodyReplayAfterText(t *testing.T) {
	// .body on a Response whose transfer already completed (or was consumed
	// buffered) replays the buffered bytes as one chunk.
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
const res = await fetch("%s/flat");
const t1 = await res.text();
const decoder = new TextDecoder();
let text = "";
for await (const chunk of res.body) { text = text + decoder.decode(chunk); }
console.log(text === t1);
`, srv.URL)
	assertOutput(t, src, "true")
}

// TDD-00097 Stage 5: chunked http responses from a ReadableStream body.

func TestE2EStreamsHTTPChunkedResponse(t *testing.T) {
	src := `
import http from 'klain:http';
http.listen(18631, (req: HttpRequest) => {
  let n = 0;
  const body = new ReadableStream<string>({
    pull: async (c) => {
      n = n + 1;
      if (n > 3) { c.close(); return; }
      await new Promise<void>((r) => setTimeout(() => r(), 5));
      c.enqueue("part" + n + " ");
    }
  });
  return { status: 200, body: body };
});
`
	port := startHTTPServer(t, src, 18631)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if len(resp.TransferEncoding) == 0 || resp.TransferEncoding[0] != "chunked" {
		t.Fatalf("expected chunked transfer encoding, got %v", resp.TransferEncoding)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != "part1 part2 part3 " {
		t.Fatalf("body = %q, want %q", got, "part1 part2 part3 ")
	}
}

func TestE2EStreamsHTTPChunkedBinaryBody(t *testing.T) {
	src := `
import http from 'klain:http';
http.listen(18632, (req: HttpRequest) => {
  const body = new ReadableStream<Uint8Array>({
    start: (c) => {
      const a = new Uint8Array([104, 105]);
      c.enqueue(a);
      c.close();
    }
  });
  return { status: 200, body: body };
});
`
	port := startHTTPServer(t, src, 18632)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != "hi" {
		t.Fatalf("body = %q, want %q", got, "hi")
	}
}

// TDD-00097 Stage 5b: streaming http request bodies.

func TestE2EStreamsRequestBodyStream(t *testing.T) {
	// 12 MiB — beyond the buffered path's 10 MiB request cap — consumed
	// chunk-at-a-time by a coroutine task via req.stream().
	src := `
import http from 'klain:http';
async function count(req: HttpRequest): Promise<string> {
  let total = 0;
  let chunks = 0;
  for await (const chunk of req.stream()) {
    total = total + chunk.length;
    chunks = chunks + 1;
  }
  return total + " in >=2 chunks: " + (chunks >= 2);
}
http.listen(18651, async (req: HttpRequest) => {
  return { status: 200, body: await count(req) };
});
`
	port := startHTTPServer(t, src, 18651)
	body := bytes.Repeat([]byte("y"), 12*1024*1024)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	want := "12582912 in >=2 chunks: true"
	if string(got) != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestE2EStreamsRequestBodyEcho(t *testing.T) {
	// req.stream() piped straight into a chunked response — the body flows
	// through the server without ever being fully buffered, fed by the event
	// loop's pump (the stream is consumed outside the connection fiber).
	src := `
import http from 'klain:http';
http.listen(18652, (req: HttpRequest) => {
  return { status: 200, body: req.stream() };
});
`
	port := startHTTPServer(t, src, 18652)
	body := bytes.Repeat([]byte("z"), 4*1024*1024)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("echo mismatch: got %d bytes, want %d", len(got), len(body))
	}
}

func TestE2EStreamsRequestBufferedAccessorsStillWork(t *testing.T) {
	// A streaming-mode program (req.stream() used somewhere) keeps the
	// buffered accessors working — they drain the remaining body in place.
	src := `
import http from 'klain:http';
async function streamed(req: HttpRequest): Promise<string> {
  let total = 0;
  for await (const c of req.stream()) { total = total + c.length; }
  return "" + total;
}
http.listen(18653, async (req: HttpRequest) => {
  if (req.path === "/streamed") {
    return { status: 200, body: await streamed(req) };
  }
  return { status: 200, body: "buffered:" + req.body };
});
`
	port := startHTTPServer(t, src, 18653)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/plain", port), "text/plain", strings.NewReader("hello body"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "buffered:hello body" {
		t.Fatalf("buffered = %q", got)
	}
	resp2, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/streamed", port), "text/plain", strings.NewReader("0123456789"))
	if err != nil {
		t.Fatalf("POST2: %v", err)
	}
	got2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if string(got2) != "10" {
		t.Fatalf("streamed = %q", got2)
	}
}

// TestE2EStreamsRequestBodyAfterStreamThrows (ADR-00362): reading req.body after
// req.stream() has consumed the same request's body throws a catchable
// TypeError (WHATWG "body already disturbed"), rather than silently returning the
// pre-stream prefix.
func TestE2EStreamsRequestBodyAfterStreamThrows(t *testing.T) {
	src := `
import http from 'klain:http';
async function handle(req: HttpRequest): Promise<string> {
  let total = 0;
  for await (const c of req.stream()) { total = total + c.length; }
  try {
    const leftover = req.body;
    return "no-throw:" + leftover;
  } catch (e) {
    return "threw:" + (e as Error).name;
  }
}
http.listen(18654, async (req: HttpRequest) => {
  return { status: 200, body: await handle(req) };
});
`
	port := startHTTPServer(t, src, 18654)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "text/plain", strings.NewReader("0123456789"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "threw:TypeError" {
		t.Fatalf("body-after-stream = %q, want %q (a catchable TypeError)", got, "threw:TypeError")
	}
}

// TestE2EHTTPUnionResponseBody (TDD-00119): a handler whose declared response
// `body` field is `string | ReadableStream<Uint8Array>` may return either shape
// across branches — the response writer branches on the union tag at runtime,
// buffered write for a string and chunked transfer for a stream.
func TestE2EHTTPUnionResponseBodyString(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string | ReadableStream<Uint8Array> }
http.listen(18655, (req: HttpRequest): Res => {
  if (req.path === '/stream') {
    return { status: 200, body: req.stream() }
  }
  return { status: 201, body: "plain body" }
})
`
	port := startHTTPServer(t, src, 18655)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/plain", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}
	if string(got) != "plain body" {
		t.Errorf("string branch body = %q, want %q", got, "plain body")
	}
}

func TestE2EHTTPUnionResponseBodyStream(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string | ReadableStream<Uint8Array> }
http.listen(18656, (req: HttpRequest): Res => {
  if (req.path === '/stream') {
    return { status: 200, body: req.stream() }
  }
  return { status: 200, body: "plain" }
})
`
	port := startHTTPServer(t, src, 18656)
	payload := bytes.Repeat([]byte("q"), 3*1024*1024)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/stream", port), "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("stream branch echo mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

// TDD-00097 Stage 6: CompressionStream / DecompressionStream (zlib).

func TestE2EStreamsGzipRoundtrip(t *testing.T) {
	assertOutput(t, `
const enc = new TextEncoder();
const dec = new TextDecoder();
const original = "streams compress everything, ".repeat(200);
const src = new ReadableStream<Uint8Array>({
  start: (c) => { const b = enc.encode(original); c.enqueue(b); c.close(); }
});
const [rawBranch, decodeBranch] = src.pipeThrough(new CompressionStream("gzip")).tee();
let gzLen = 0;
let first = -1;
let second = -1;
for await (const chunk of rawBranch) {
  if (first < 0) { first = chunk[0]; second = chunk[1]; }
  gzLen = gzLen + chunk.length;
}
console.log("gzip magic:", first === 0x1f && second === 0x8b);
console.log("smaller:", gzLen < original.length);
let out = "";
for await (const chunk of decodeBranch.pipeThrough(new DecompressionStream("gzip"))) {
  out = out + dec.decode(chunk);
}
console.log("roundtrip:", out === original);
`, "gzip magic: true\nsmaller: true\nroundtrip: true")
}

func TestE2EStreamsDeflateFormatsRoundtrip(t *testing.T) {
	assertOutput(t, `
async function roundtripDeflate(): Promise<boolean> {
  const enc = new TextEncoder();
  const dec = new TextDecoder();
  const original = "deflate me ".repeat(100);
  const src = new ReadableStream<Uint8Array>({
    start: (c) => { const b = enc.encode(original); c.enqueue(b); c.close(); }
  });
  let out = "";
  for await (const chunk of src.pipeThrough(new CompressionStream("deflate")).pipeThrough(new DecompressionStream("deflate"))) {
    out = out + dec.decode(chunk);
  }
  return out === original;
}
async function roundtripRaw(): Promise<boolean> {
  const enc = new TextEncoder();
  const dec = new TextDecoder();
  const original = "deflate me raw ".repeat(100);
  const src = new ReadableStream<Uint8Array>({
    start: (c) => { const b = enc.encode(original); c.enqueue(b); c.close(); }
  });
  let out = "";
  for await (const chunk of src.pipeThrough(new CompressionStream("deflate-raw")).pipeThrough(new DecompressionStream("deflate-raw"))) {
    out = out + dec.decode(chunk);
  }
  return out === original;
}
console.log("deflate:", await roundtripDeflate());
console.log("deflate-raw:", await roundtripRaw());
`, "deflate: true\ndeflate-raw: true")
}

func TestE2EStreamsGunzipGoInterop(t *testing.T) {
	// Decompress a body produced by Go's compress/gzip, streamed through
	// fetch's Response.body — real cross-implementation validation.
	mux := http.NewServeMux()
	payload := strings.Repeat("interop payload line\n", 500)
	mux.HandleFunc("/gz", func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		zw.Write([]byte(payload))
		zw.Close()
		w.Write(buf.Bytes())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	src := fmt.Sprintf(`
const res = await fetch("%s/gz");
const dec = new TextDecoder();
let out = "";
for await (const chunk of res.body.pipeThrough(new DecompressionStream("gzip"))) {
  out = out + dec.decode(chunk);
}
console.log("lines:", out.split("\n").length - 1, "match:", out.length === %d);
`, srv.URL, len(payload))
	assertOutput(t, src, "lines: 500 match: true")
}

func TestE2EStreamsGzipGoInterop(t *testing.T) {
	// The mirror direction: a compiled server streams a gzip-compressed
	// chunked response; Go's gzip reader decodes it.
	src := `
import http from 'klain:http';
http.listen(18661, (req: HttpRequest) => {
  const enc = new TextEncoder();
  let n = 0;
  const source = new ReadableStream<Uint8Array>({
    pull: (c) => {
      n = n + 1;
      if (n > 50) { c.close(); return; }
      const b = enc.encode("row " + n + "\n");
      c.enqueue(b);
    }
  });
  return { status: 200, body: source.pipeThrough(new CompressionStream("gzip")) };
});
`
	port := startHTTPServer(t, src, 18661)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !strings.HasPrefix(string(got), "row 1\n") || !strings.Contains(string(got), "row 50\n") {
		t.Fatalf("decoded = %q...", string(got)[:40])
	}
}

func TestE2EBareReturnClosureIsVoid(t *testing.T) {
	// Regression: an unannotated closure whose only return is a bare
	// `return;` used to infer the scalar default, emitting `ret i64 0` for
	// the bare return and a runtime-reachable `unreachable` at the
	// fall-through end — a crash. Found via Stage 6's early-return pull
	// callbacks (ADR-00302).
	assertOutput(t, `
const f = (x: number) => {
  if (x > 3) { return; }
  console.log("small", x);
};
f(1);
f(5);
const g = function(x: number) {
  if (x > 3) { return; }
  console.log("g", x);
};
g(2);
g(7);
console.log("done");
`, "small 1\ng 2\ndone")
}

func TestE2ERecordStringBracketParity(t *testing.T) {
	// ADR-00485: Record<string, V> is the index-signature dict — bracket
	// read/write, Object.keys, and JSON.stringify all work.
	assertOutput(t, `
const r: Record<string, number> = {};
r["k"] = 1;
r["j"] = 2;
console.log(r["k"], Object.keys(r).length);
console.log(JSON.stringify(r));
`, "1 2\n{\"k\":1,\"j\":2}")
}

// --- TDD-00195 Stage 1: server req as a Node Readable ---

// `for await (const chunk of req)` iterates the request body directly (not
// req.stream()) — req is a Node Readable over its own body.
func TestE2EReqAsReadableForAwait(t *testing.T) {
	src := `
import http from 'klain:http';
async function count(req: HttpRequest): Promise<string> {
  let total = 0;
  let chunks = 0;
  for await (const chunk of req) {
    total = total + chunk.length;
    chunks = chunks + 1;
  }
  return "bytes=" + total + " multi=" + (chunks >= 2);
}
http.listen(18661, async (req: HttpRequest) => {
  return { status: 200, body: await count(req) };
});
`
	port := startHTTPServer(t, src, 18661)
	body := bytes.Repeat([]byte("q"), 3*1024*1024)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	want := fmt.Sprintf("bytes=%d multi=true", len(body))
	if string(got) != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// req.on('data')/req.on('end') flowing-mode listeners fire, while the handler
// awaits until 'end' (keeping the response open past the synchronous return).
func TestE2EReqAsReadableOnData(t *testing.T) {
	src := `
import http from 'klain:http';
http.listen(18662, async (req: HttpRequest) => {
  let total = 0;
  await new Promise<void>((resolve) => {
    req.on('data', (c: Uint8Array) => { total = total + c.length; });
    req.on('end', () => { resolve(); });
  });
  return { status: 200, body: "on=" + total };
});
`
	port := startHTTPServer(t, src, 18662)
	body := []byte("abcdefghij-1234567890")
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "text/plain", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	want := fmt.Sprintf("on=%d", len(body))
	if string(got) != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// req.pipe(writable) — the request body pipes into a Node Writable (here a file
// write stream), and the handler awaits the pipe's 'finish' before responding.
func TestE2EReqPipeToWriteStream(t *testing.T) {
	out := filepath.ToSlash(filepath.Join(tempDir(t), "klain_reqpipe_test_out.bin"))
	src := fmt.Sprintf(`
import http from 'klain:http';
import fs from 'fs';
http.listen(18663, async (req: HttpRequest) => {
  const ws = fs.createWriteStream(%q);
  await new Promise<void>((resolve) => {
    ws.on('finish', () => { resolve(); });
    req.pipe(ws);
  });
  const back: string = fs.readFileSync(%q, 'utf8');
  return { status: 200, body: "piped=" + back.length };
});
`, out, out)
	port := startHTTPServer(t, src, 18663)
	body := []byte("PIPE-THIS-BODY-0123456789")
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	want := fmt.Sprintf("piped=%d", len(body))
	if string(got) != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}
