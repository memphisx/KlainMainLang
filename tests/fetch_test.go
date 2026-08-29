package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- fetch / Response ---
//
// These spin up a local httptest.Server rather than hitting a real external
// URL, so the suite stays deterministic and offline-capable — but they still
// exercise the real libcurl HTTP client path end to end (a local server is a
// real TCP connection with real HTTP framing, not a mocked-out call site).

func newFetchTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/flat", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"title":"hello","count":42,"active":true}`)
	})
	mux.HandleFunc("/jsonarray", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[10,20,30]`)
	})
	mux.HandleFunc("/notfound", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "not found")
	})
	mux.HandleFunc("/servererror", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/flat", http.StatusFound)
	})
	mux.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) {
		body := strings.Repeat("x", 40000)
		fmt.Fprint(w, body)
	})
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) {
		// Embedded null NOT at the end (byte 2 of 6) — a null-at-the-end body
		// could hide an off-by-one in the length threading (ADR-00094).
		w.Write([]byte{'h', 'i', 0, 'b', 'y', 'e'})
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, "done")
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Method  string `json:"method"`
			Body    string `json:"body"`
			XCustom string `json:"x_custom"`
			Auth    string `json:"authorization"`
		}{
			Method:  r.Method,
			Body:    string(body),
			XCustom: r.Header.Get("X-Custom-Header"),
			Auth:    r.Header.Get("Authorization"),
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestE2EFetchStatusAndText(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/flat")
    console.log(r.status)
    console.log(r.ok)
    const body: string = r.text()
    console.log(body)
}
main2()
`, srv.URL)
	assertOutput(t, src, "200\ntrue\n"+`{"title":"hello","count":42,"active":true}`)
}

// Awaiting the (synchronous) body accessors used to hard compile-crash and free
// the live buffer — `await` treated the string/buffer as a Promise slot. `await`
// of a non-thenable is identity, so these must work exactly as the un-awaited
// forms and preserve the buffer (ADR-00241).
func TestE2EFetchAwaitText(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/flat")
    const body: string = await r.text()
    console.log(body)
}
main2()
`, srv.URL)
	assertOutput(t, src, `{"title":"hello","count":42,"active":true}`)
}

func TestE2EFetchAwaitJSONProjection(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
interface Payload { title: string; count: number; active: boolean }
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/flat")
    const p: Payload = await r.json()
    console.log(p.title + " " + p.count + " " + p.active)
}
main2()
`, srv.URL)
	assertOutput(t, src, "hello 42 true")
}

func TestE2EFetchAwaitJSONArray(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/jsonarray")
    const xs: number[] = await r.json()
    console.log(xs[0] + " " + xs[1] + " " + xs[2] + " len=" + xs.length)
}
main2()
`, srv.URL)
	assertOutput(t, src, "10 20 30 len=3")
}

func TestE2EFetchAwaitArrayBufferAndReuse(t *testing.T) {
	srv := newFetchTestServer(t)
	// Also reuses the response after the awaited accessor — a regression guard
	// against the old free-the-live-buffer behavior.
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/flat")
    const buf = await r.arrayBuffer()
    console.log(buf.byteLength)
    const body: string = await r.text()
    console.log(body)
}
main2()
`, srv.URL)
	assertOutput(t, src, "42\n"+`{"title":"hello","count":42,"active":true}`)
}

// A may-suspend async function (one that awaits a fetch) is compiled as a
// coroutine task (TDD-00083 Stage 2): calling it spawns a task that runs to its
// first await and returns a pending promise, so two such calls made before
// either is awaited run their fetches concurrently and compose correctly.
func TestE2EAsyncFnComposition(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function grabStatus(u: string): Promise<number> {
    const r = await fetch(u)
    return r.status
}
async function grabBody(u: string): Promise<string> {
    const r = await fetch(u)
    return await r.text()
}
async function main2(): Promise<void> {
    const p1 = grabStatus("%s/flat")
    const p2 = grabStatus("%s/notfound")
    const a: number = await p1
    const b: number = await p2
    console.log(a + " " + b)
    const body: string = await grabBody("%s/flat")
    console.log(body)
}
main2()
`, srv.URL, srv.URL, srv.URL)
	assertOutput(t, src, "200 404\n"+`{"title":"hello","count":42,"active":true}`)
}

// Promise.all/.race/.allSettled over an array of task promises (may-suspend
// async fns) — the combinators drive the shared scheduler, so the members run
// concurrently and are collected/picked correctly (TDD-00083 Stage 2).
func TestE2EPromiseCombinatorsOverTasks(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function grab(u: string): Promise<number> {
    const r = await fetch(u)
    return r.status
}
async function main2(): Promise<void> {
    const all: number[] = await Promise.all([grab("%s/flat"), grab("%s/notfound"), grab("%s/flat")])
    console.log(all[0] + " " + all[1] + " " + all[2] + " len=" + all.length)
    const first: number = await Promise.race([grab("%s/flat"), grab("%s/notfound")])
    console.log("race>=200:" + (first >= 200))
}
main2()
`, srv.URL, srv.URL, srv.URL, srv.URL, srv.URL)
	assertOutput(t, src, "200 404 200 len=3\nrace>=200:true")
}

// A may-suspend async fn that throws rejects its promise; awaiting it re-throws
// at the awaiter (so try/catch works), and Promise.all rejects on the first
// rejection — including under concurrency, which exercises the per-task jmpbuf
// stacks (TDD-00083 Stage 2, fiber-safe exceptions).
func TestE2ETaskRejection(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function bad(tag: string): Promise<number> {
    const r = await fetch("%s/flat")
    throw new Error("nope-" + tag)
}
async function grab(u: string): Promise<number> {
    const r = await fetch(u)
    return r.status
}
async function main2(): Promise<void> {
    const p1 = bad("A")
    const p2 = grab("%s/flat")
    try { const s = await p1; console.log("no throw " + s) } catch (e) { console.log("caught: " + e.message) }
    console.log("sibling ok: " + (await p2))
    try {
        const xs: number[] = await Promise.all([grab("%s/flat"), bad("B")])
        console.log("all no throw")
    } catch (e) { console.log("all caught: " + e.message) }
}
main2()
`, srv.URL, srv.URL, srv.URL)
	assertOutput(t, src, "caught: nope-A\nsibling ok: 200\nall caught: nope-B")
}

// Promise.any over task promises resolves to the first fulfilled member
// (TDD-00083 Stage 2).
func TestE2EPromiseAnyOverTasks(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function grab(u: string): Promise<number> {
    const r = await fetch(u)
    return r.status
}
async function main2(): Promise<void> {
    const s: number = await Promise.any([grab("%s/flat"), grab("%s/notfound")])
    console.log("any>=200:" + (s >= 200))
}
main2()
`, srv.URL, srv.URL)
	assertOutput(t, src, "any>=200:true")
}

// Promise.any skips rejected members and resolves to the first *fulfilled* one;
// when every member rejects it throws an AggregateError whose `.errors` carries
// each rejection reason (TDD-00083). Both over real suspending task promises.
func TestE2EPromiseAnySkipRejectedAndAggregateError(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function bad(u: string): Promise<number> {
    const r = await fetch(u)
    throw new Error("nope")
}
async function good(u: string): Promise<number> {
    const r = await fetch(u)
    return r.status
}
async function main2(): Promise<void> {
    const s: number = await Promise.any([bad("%s/flat"), good("%s/flat"), bad("%s/flat")])
    console.log("skip:" + (s >= 200))
    try {
        const t: number = await Promise.any([bad("%s/flat"), bad("%s/flat")])
        console.log("no throw")
    } catch (e) {
        console.log("name:" + e.name)
        console.log("count:" + e.errors.length)
        console.log("first:" + e.errors[0].message)
    }
}
main2()
`, srv.URL, srv.URL, srv.URL, srv.URL, srv.URL)
	assertOutput(t, src, "skip:true\nname:AggregateError\ncount:2\nfirst:nope")
}

// Promise.prototype.then/.catch/.finally over task promises run their callbacks
// as microtasks after the current synchronous run (TDD-00083 Stage 3). Each is
// checked in isolation — the relative order of reactions across *independent*
// promises is scheduler-timing-dependent, so only one reaction per program here.
func TestE2EPromiseThenCatchFinally(t *testing.T) {
	srv := newFetchTestServer(t)
	assertOutput(t, fmt.Sprintf(`
async function grab(u: string): Promise<number> { const r = await fetch(u); return r.status }
grab("%s/flat").then((s: number) => { console.log("then " + s) })
console.log("sync")
`, srv.URL), "sync\nthen 200")
	assertOutput(t, fmt.Sprintf(`
async function bad(u: string): Promise<number> { const r = await fetch(u); throw new Error("x") }
bad("%s/flat").catch((e) => { console.log("catch ran") })
console.log("sync")
`, srv.URL), "sync\ncatch ran")
	assertOutput(t, fmt.Sprintf(`
async function grab(u: string): Promise<number> { const r = await fetch(u); return r.status }
grab("%s/flat").finally(() => { console.log("finally ran") })
console.log("sync")
`, srv.URL), "sync\nfinally ran")
}

// .then(f).then(g) chains f's return value into the next promise; .catch(h)
// recovers a rejection into a value the following .then sees; .finally passes the
// source value straight through (TDD-00083 value-chaining).
func TestE2EPromiseThenValueChaining(t *testing.T) {
	srv := newFetchTestServer(t)
	// then→then value chain
	assertOutput(t, fmt.Sprintf(`
async function grab(u: string): Promise<number> { const r = await fetch(u); return r.status }
grab("%s/flat").then((n: number) => n * 2).then((m: number) => { console.log("chain " + m) })
console.log("sync")
`, srv.URL), "sync\nchain 400")
	// catch recovers into a value the next then observes
	assertOutput(t, fmt.Sprintf(`
async function bad(u: string): Promise<number> { const r = await fetch(u); throw new Error("x") }
bad("%s/flat").catch((e) => 99).then((n: number) => { console.log("recovered " + n) })
console.log("sync")
`, srv.URL), "sync\nrecovered 99")
	// finally passes the fulfilled value through unchanged
	assertOutput(t, fmt.Sprintf(`
async function grab(u: string): Promise<number> { const r = await fetch(u); return r.status }
grab("%s/flat").finally(() => { console.log("fin") }).then((n: number) => { console.log("after " + n) })
console.log("sync")
`, srv.URL), "sync\nfin\nafter 200")
}

func TestE2EFetchNotFoundHasOkFalse(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/notfound")
    console.log(r.status)
    console.log(r.ok)
}
main2()
`, srv.URL)
	assertOutput(t, src, "404\nfalse")
}

func TestE2EFetchServerErrorHasOkFalse(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/servererror")
    console.log(r.status)
    console.log(r.ok)
}
main2()
`, srv.URL)
	assertOutput(t, src, "500\nfalse")
}

func TestE2EFetchFollowsRedirects(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/redirect")
    console.log(r.status)
}
main2()
`, srv.URL)
	assertOutput(t, src, "200")
}

func TestE2EFetchJSONIntoTypedTarget(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
interface FlatData { title: string; count: number; active: boolean }

async function main2(): Promise<void> {
    const data: FlatData = (await fetch("%s/flat")).json()
    console.log(data.title)
    console.log(data.count)
    console.log(data.active)
}
main2()
`, srv.URL)
	assertOutput(t, src, "hello\n42\ntrue")
}

// --- fetch(url, init): custom method, headers, body (ADR-00074, TDD-00017) ---

func TestE2EFetchCustomMethodHeadersAndBody(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
interface EchoResp { method: string; body: string; x_custom: string; authorization: string }

async function main2(): Promise<void> {
    const headers: Map<string, string> = new Map<string, string>()
    headers.set("X-Custom-Header", "hello")
    headers.set("Authorization", "Bearer abc123")
    const r: Response = await fetch("%s/echo", { method: "POST", headers: headers, body: "payload-data" })
    const data: EchoResp = r.json()
    console.log(data.method)
    console.log(data.body)
    console.log(data.x_custom)
    console.log(data.authorization)
}
main2()
`, srv.URL)
	assertOutput(t, src, "POST\npayload-data\nhello\nBearer abc123")
}

func TestE2EFetchCustomMethodOnlyNoBody(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
interface EchoResp { method: string; body: string }

async function main2(): Promise<void> {
    const r: Response = await fetch("%s/echo", { method: "DELETE" })
    const data: EchoResp = r.json()
    console.log(data.method)
    console.log(data.body)
}
main2()
`, srv.URL)
	assertOutput(t, src, "DELETE\n")
}

func TestE2EFetchHeadersOnlyDefaultsToGet(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
interface EchoResp { method: string; x_custom: string }

async function main2(): Promise<void> {
    const headers: Map<string, string> = new Map<string, string>()
    headers.set("X-Custom-Header", "just-a-header")
    const r: Response = await fetch("%s/echo", { headers: headers })
    const data: EchoResp = r.json()
    console.log(data.method)
    console.log(data.x_custom)
}
main2()
`, srv.URL)
	assertOutput(t, src, "GET\njust-a-header")
}

func TestE2EFetchPlainCallStillWorksUnchanged(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/flat")
    console.log(r.status)
}
main2()
`, srv.URL)
	assertOutput(t, src, "200")
}

func TestE2EFetchUntypedInference(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const p = fetch("%s/flat")
    const r = await p
    console.log(r.status)
}
main2()
`, srv.URL)
	assertOutput(t, src, "200")
}

func TestE2EFetchLargeBody(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/large")
    const body: string = r.text()
    console.log(body.length)
}
main2()
`, srv.URL)
	assertOutput(t, src, "40000")
}

func TestE2EFetchNetworkFailureThrows(t *testing.T) {
	src := `
async function main2(): Promise<void> {
    try {
        const r: Response = await fetch("http://127.0.0.1:1/unreachable")
        console.log(r.status)
    } catch (e) {
        console.log("caught")
    }
}
main2()
`
	assertOutput(t, src, "caught")
}

// Regression test for a real stack-overflow crash (SIGSEGV, exit 139): a
// top-level `await fetch(...)` — outside any http.listen connection fiber,
// so __kml_await_fetch takes its "busyspin" path (curl_multi_perform in a
// tight loop, no delay) rather than yielding via swapcontext — used to hit
// an `alloca` placed inside the loop body instead of the function's entry
// block. Each spin iteration allocated fresh, never-freed stack space, so
// any fetch slow enough to need more than a couple of iterations overflowed
// the stack. A near-instant local response never iterated the loop enough
// to show it, which is why this needed a deliberately slow handler (300ms)
// rather than the other fetch tests' near-instant ones.
func TestE2EFetchTopLevelBusySpinDoesNotOverflowStack(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
const r = await fetch("%s/slow")
console.log(r.status)
console.log(r.text())
`, srv.URL)
	assertOutput(t, src, "200\ndone")
}

func TestE2EFetchWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fetch("a", "b", "c")`)
	if err == nil {
		t.Fatal("expected a compile error for fetch() with the wrong argument count, got none")
	}
}

func TestE2EFetchNonObjectInitRejected(t *testing.T) {
	_, err := parseAndCompile(`fetch("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for fetch()'s second argument not being an object, got none")
	}
}

func TestE2EFetchInitWrongFieldTypesRejected(t *testing.T) {
	cases := []string{
		`fetch("a", { method: 5 })`,
		`fetch("a", { body: 5 })`,
		`fetch("a", { headers: 5 })`,
		`fetch("a", { headers: new Map<string, number>() })`,
	}
	for _, src := range cases {
		if _, err := parseAndCompile(src); err == nil {
			t.Fatalf("expected a compile error for %q, got none", src)
		}
	}
}

// --- Headers / Request (TDD-00040) ---
//
// Headers IS a Map<string,string> under the hood (see IsHeaders's doc
// comment in codegen/llvm/types.go), so these focus on the two genuinely
// new behaviors — case-insensitive keys and append() — plus Request's own
// construction/defaults and its wiring into fetch().

func TestE2EHeadersGetSetHasDeleteCaseInsensitive(t *testing.T) {
	src := `
const h: Headers = new Headers()
h.set("Content-Type", "application/json")
console.log(h.get("content-type"))
console.log(h.get("CONTENT-TYPE"))
console.log(h.has("Content-Type"))
console.log(h.has("x-missing"))
h.delete("CONTENT-TYPE")
console.log(h.has("content-type"))
`
	assertOutput(t, src, "application/json\napplication/json\ntrue\nfalse\nfalse")
}

func TestE2EHeadersAppendCombinesWithComma(t *testing.T) {
	src := `
const h: Headers = new Headers()
h.append("X-Multi", "a")
h.append("X-Multi", "b")
console.log(h.get("x-multi"))
`
	assertOutput(t, src, "a, b")
}

func TestE2EHeadersConstructFromMapLowercasesKeys(t *testing.T) {
	src := `
const m: Map<string, string> = new Map<string, string>()
m.set("X-Foo", "bar")
const h: Headers = new Headers(m)
console.log(h.get("x-foo"))
`
	assertOutput(t, src, "bar")
}

func TestE2ERequestDefaultsMethodAndEmptyHeaders(t *testing.T) {
	src := `
const req: Request = new Request("http://example.com/x")
console.log(req.url)
console.log(req.method)
console.log(req.headers.has("anything"))
`
	assertOutput(t, src, "http://example.com/x\nGET\nfalse")
}

func TestE2ERequestInitOverridesMethodHeadersBody(t *testing.T) {
	src := `
const h: Headers = new Headers()
h.set("X-Custom-Header", "hi")
const req: Request = new Request("http://example.com/x", { method: "PUT", headers: h, body: "payload" })
console.log(req.method)
console.log(req.headers.get("x-custom-header"))
console.log(req.body)
`
	assertOutput(t, src, "PUT\nhi\npayload")
}

func TestE2EFetchWithRequestObject(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
interface EchoResp { method: string; body: string; x_custom: string }

async function main2(): Promise<void> {
    const h: Headers = new Headers()
    h.set("X-Custom-Header", "from-request")
    const req: Request = new Request("%s/echo", { method: "POST", headers: h, body: "req-body" })
    const r: Response = await fetch(req)
    const data: EchoResp = r.json()
    console.log(data.method)
    console.log(data.body)
    console.log(data.x_custom)
}
main2()
`, srv.URL)
	assertOutput(t, src, "POST\nreq-body\nfrom-request")
}

func TestE2EFetchInitAcceptsHeadersInstance(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
interface EchoResp { x_custom: string }

async function main2(): Promise<void> {
    const h: Headers = new Headers()
    h.set("X-Custom-Header", "via-headers-object")
    const r: Response = await fetch("%s/echo", { headers: h })
    const data: EchoResp = r.json()
    console.log(data.x_custom)
}
main2()
`, srv.URL)
	assertOutput(t, src, "via-headers-object")
}

func TestE2EFetchFieldAccessOnNonResponseRejected(t *testing.T) {
	_, err := parseAndCompile(`
const x: number = 5
console.log(x.status)
`)
	if err == nil {
		t.Fatal("expected a compile error for accessing .status on a non-Response value, got none")
	}
}

// --- Promise.all / .race / .allSettled over Array<Promise<Response>> ---
//
// This is the real-concurrency branch (ADR-00073, TDD-00016): unlike an
// array of ordinary async functions' promises (already resolved by
// construction — covered in tests/async_test.go), fetch()'s Promise<Response>
// is genuinely pending, so these three combinators wait on N in-flight
// fetches together via __kml_await_group_wait/the event loop's rcheckgroup
// scan, rather than one at a time.

func TestE2EPromiseAllFetchesCollectsInOrder(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const ps: Array<Promise<Response>> = []
    ps.push(fetch("%s/flat"))
    ps.push(fetch("%s/notfound"))
    const responses = await Promise.all(ps)
    console.log(responses.length)
    for (const r of responses) {
        console.log(r.status)
    }
}
main2()
`, srv.URL, srv.URL)
	assertOutput(t, src, "2\n200\n404")
}

// TestE2EPromiseRaceFetchesReturnsFirstDone puts the slow member first in
// array order and the fast member second, so a passing result can only mean
// a genuine race (whichever settles first wins) — not "always the first
// array element," which a naive implementation could fake.
func TestE2EPromiseRaceFetchesReturnsFirstDone(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const ps: Array<Promise<Response>> = []
    ps.push(fetch("%s/slow"))
    ps.push(fetch("%s/flat"))
    const winner = await Promise.race(ps)
    console.log(winner.status)
}
main2()
`, srv.URL, srv.URL)
	assertOutput(t, src, "200")
}

func TestE2EPromiseAllSettledFetchesReportsPerMemberFailure(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const ps: Array<Promise<Response>> = []
    ps.push(fetch("%s/flat"))
    ps.push(fetch("http://127.0.0.1:1/unreachable"))
    const settled = await Promise.allSettled(ps)
    console.log(settled[0].status)
    console.log(settled[0].value.status)
    console.log(settled[1].status)
}
main2()
`, srv.URL)
	assertOutput(t, src, "fulfilled\n200\nrejected")
}

// TestE2EPromiseAllFetchesRunConcurrently is the decisive test for
// ADR-00073, mirroring TestE2EHTTPListenConcurrentAwaitFetch's reasoning:
// two fetches against a 300ms-delayed upstream, waited on together via
// Promise.all, must finish in well under 2x300ms — proving
// __kml_await_group_wait genuinely waits on both in-flight transfers at
// once (via libcurl's multi-interface) rather than looping
// __kml_pending_finish serially, which would take the full 600ms+. Timed
// with Date.now() *inside* the compiled program rather than wall-clock
// around exec.Command: an external measurement was tried first and came
// back consistently ~200ms higher than the in-program figure for the exact
// same run — process start/exit overhead in this environment, unrelated to
// the feature under test — which would have made the threshold below flaky
// (or falsely failing) for the wrong reason.
func TestE2EPromiseAllFetchesRunConcurrently(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
const t0 = Date.now()
async function main2(): Promise<void> {
    const ps: Array<Promise<Response>> = []
    ps.push(fetch("%s/slow"))
    ps.push(fetch("%s/slow"))
    const responses = await Promise.all(ps)
    console.log(responses.length)
}
main2()
console.log(Date.now() - t0)
`, srv.URL, srv.URL)
	out := compileAndRun(t, src)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 || lines[0] != "2" {
		t.Fatalf("unexpected output: %q", out)
	}
	elapsedMs, err := strconv.Atoi(lines[1])
	if err != nil {
		t.Fatalf("parsing elapsed ms from %q: %v", lines[1], err)
	}
	if elapsedMs >= 500 {
		t.Errorf("two concurrent /slow (300ms) fetches via Promise.all took %dms — expected well under 600ms if they ran concurrently rather than serially (concurrency is broken)", elapsedMs)
	}
}

// --- Response.arrayBuffer() (ADR-00094): embedded-null-byte-safe bodies ---

func TestE2EFetchArrayBufferPreservesEmbeddedNullByte(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/binary")
    const buf = r.arrayBuffer()
    const arr = new Uint8Array(buf)
    console.log(arr.length)
    for (let i = 0; i < arr.length; i++) {
        console.log(arr[i])
    }
}
main2()
`, srv.URL)
	assertOutput(t, src, "6\n104\n105\n0\n98\n121\n101")
}

func TestE2EFetchTextStillStrlenBasedAfterArrayBuffer(t *testing.T) {
	// .text()/.json() are deliberately unchanged (ADR-00094) — still
	// strlen-based, so a body with an embedded null still reads back short
	// via .text() even though .arrayBuffer() (tested above) now sees the
	// whole thing. This pins that intentional split down as a regression
	// guard, not an oversight.
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/binary")
    console.log(r.text().length)
}
main2()
`, srv.URL)
	assertOutput(t, src, "2")
}

func TestE2EPromiseAllArrayBufferPreservesEmbeddedNullByte(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const ps: Array<Promise<Response>> = []
    ps.push(fetch("%s/binary"))
    ps.push(fetch("%s/binary"))
    const responses = await Promise.all(ps)
    for (const r of responses) {
        const arr = new Uint8Array(r.arrayBuffer())
        console.log(arr.length)
    }
}
main2()
`, srv.URL, srv.URL)
	assertOutput(t, src, "6\n6")
}

func TestE2EPromiseRaceArrayBufferPreservesEmbeddedNullByte(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const ps: Array<Promise<Response>> = []
    ps.push(fetch("%s/binary"))
    const r = await Promise.race(ps)
    const arr = new Uint8Array(r.arrayBuffer())
    console.log(arr.length)
}
main2()
`, srv.URL)
	assertOutput(t, src, "6")
}

func TestE2EPromiseAllSettledArrayBufferPreservesEmbeddedNullByte(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const ps: Array<Promise<Response>> = []
    ps.push(fetch("%s/binary"))
    const results = await Promise.allSettled(ps)
    for (const res of results) {
        console.log(res.status)
        const arr = new Uint8Array(res.value.arrayBuffer())
        console.log(arr.length)
    }
}
main2()
`, srv.URL)
	assertOutput(t, src, "fulfilled\n6")
}

// TDD-00085: an async generator that awaits fetch inside its body, consumed by
// for await...of — the primary use case (paginating over network responses).
func TestE2EAsyncGeneratorOverFetch(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function* statuses(): number {
  const urls: string[] = ["%s/flat", "%s/notfound", "%s/flat"]
  for (const u of urls) {
    const r = await fetch(u)
    yield r.status
  }
}
async function main2(): Promise<void> {
  for await (const s of statuses()) { console.log(s) }
}
main2()
`, srv.URL, srv.URL, srv.URL)
	assertOutput(t, src, "200\n404\n200")
}

// ADR-00258: .then/.catch/.finally directly on a raw fetch()'s Promise<Response>
// (no async fn, no await). The fetch is driven to a settled task promise and the
// chain runs as a microtask; the unannotated `r` parameter is hinted to Response.
func TestE2EFetchThenChain(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
fetch("%s/flat")
  .then((r) => r.status)
  .then((s: number) => { console.log("status " + s) })
console.log("sync")
`, srv.URL)
	assertOutput(t, src, "sync\nstatus 200")
}

// An HTTP 4xx is a fulfilled Response (per WHATWG) — .then sees it, .catch does not.
func TestE2EFetchThenNotFoundIsFulfilled(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
fetch("%s/notfound")
  .then((r) => { console.log(r.status + " ok=" + r.ok) })
console.log("sync")
`, srv.URL)
	assertOutput(t, src, "sync\n404 ok=false")
}

// A transport-level failure (connection refused) rejects the chain — .catch recovers.
func TestE2EFetchThenTransportFailureRejects(t *testing.T) {
	src := `
fetch("http://127.0.0.1:1/x")
  .then((r) => { console.log("no throw " + r.status) })
  .catch((e) => { console.log("caught") })
console.log("sync")
`
	assertOutput(t, src, "sync\ncaught")
}

// .finally passes the source Response through unchanged after running for effect.
func TestE2EFetchThenFinally(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
fetch("%s/flat")
  .finally(() => { console.log("cleanup") })
  .then((r) => { console.log(r.status) })
console.log("sync")
`, srv.URL)
	assertOutput(t, src, "sync\ncleanup\n200")
}

// The .then-on-fetch bridge defers the transport wait to a queued microtask:
// the synchronous script after the .then call runs immediately (while the slow
// fetch is still in flight), and only the event-loop drain blocks on it. The
// elapsed time measured across the sync tail must be well under the server's
// response delay — with the old synchronous drive it was ≥ the delay.
func TestE2EFetchThenDoesNotBlockSyncScript(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		fmt.Fprint(w, "late")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	src := fmt.Sprintf(`
const t0 = Date.now()
fetch("%s/slow").then((r) => { console.log("status " + r.status) })
const elapsed = Date.now() - t0
console.log(elapsed < 300 ? "sync ran immediately" : "sync was blocked")
`, srv.URL)
	assertOutput(t, src, "sync ran immediately\nstatus 200")
}

// A fetch promise stays a reusable value across the .then bridge: attaching a
// .then does not consume the handle — a later await of the same promise (or the
// reverse order, .then after an await already drove it) reads the same
// completed fetch, in both cases exactly once over the wire.
func TestE2EFetchThenAndAwaitShareOneHandle(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
  const p = fetch("%s/flat")
  p.then((r) => { console.log("then " + r.status) })
  const a: Response = await p
  console.log("await " + a.status)
  const q = fetch("%s/notfound")
  const b: Response = await q
  console.log("await2 " + b.status)
  q.then((r) => { console.log("then2 " + r.status) })
}
main2()
`, srv.URL, srv.URL)
	assertOutput(t, src, "then "+"200\nawait 200\nawait2 404\nthen2 404")
}

// for await...of over an array of raw fetch Promise<Response>: each element
// drives its fetch (started concurrently at the fetch() calls) and binds the
// built Response, in order. A 4xx element is a fulfilled Response; a
// transport-level failure rejects and stops the loop (catchable).
func TestE2EForAwaitOverArrayOfFetches(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
  const ps = [fetch("%s/flat"), fetch("%s/notfound"), fetch("%s/flat")]
  for await (const r of ps) { console.log(r.status + " ok=" + r.ok) }
  try {
    for await (const r of [fetch("http://127.0.0.1:1/x")]) { console.log("no throw " + r.status) }
  } catch (e) {
    console.log("caught transport failure")
  }
}
main2()
`, srv.URL, srv.URL, srv.URL)
	assertOutput(t, src, "200 ok=true\n404 ok=false\n200 ok=true\ncaught transport failure")
}

// TDD-00090: a fetch Promise<Response> is a reusable value too — its pending
// struct is never freed and __kml_await_fetch short-circuits an already-done
// handle, so double-awaiting the fetch promise (or reusing a member after a
// combinator) re-reads the finished Response instead of a freed slot.
func TestE2EFetchPromiseDoubleAwait(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const rp = fetch("%s/flat")
    const a: Response = await rp
    console.log(a.status)
    const b: Response = await rp
    console.log(b.status)
    console.log(b.text())
}
main2()
`, srv.URL)
	assertOutput(t, src, "200\n200\n"+`{"title":"hello","count":42,"active":true}`)
}

func TestE2EFetchMemberReuseAfterCombinator(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const rp = fetch("%s/flat")
    const rs: Response[] = await Promise.all([rp])
    console.log(rs[0].status)
    const again: Response = await rp
    console.log(again.status)
}
main2()
`, srv.URL)
	assertOutput(t, src, "200\n200")
}

// TDD-00138 Stage 1: the Node http client — http.get(url, cb) hands the callback
// an IncomingMessage (statusCode + 'data'/'end' events), built on the same
// libcurl primitive as fetch.
func TestE2EHTTPSClientGet(t *testing.T) {
	// The https module's get/request ride the same libcurl client as http's —
	// TLS comes from the URL scheme at curl's layer (verified manually against
	// a live https host; this test pins the module wiring itself against a
	// local upstream).
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
import https from 'https'
https.get("%s/flat", (res) => {
  console.log("status", res.statusCode)
})
`, srv.URL)
	assertOutputImports(t, src, `status 200`)
}

func TestE2EHTTPSCreateServerRejected(t *testing.T) {
	// https.createServer must be a clean rejection, never a silently non-TLS
	// server.
	_, err := parseAndCompileImports(t, `
import https from 'https'
https.createServer((req, res) => { res.end("x") })
`)
	if err == nil || !strings.Contains(err.Error(), "https.createServer is not implemented") {
		t.Fatalf("want clean https.createServer rejection, got %v", err)
	}
}

func TestE2EStreamWebReExports(t *testing.T) {
	// stream/web re-exports the ambient WHATWG stream constructors.
	assertOutputImports(t, `
import { ReadableStream } from 'stream/web'
const rs = new ReadableStream<number>({ start: (c) => { c.enqueue(41); c.close(); } })
const rd = rs.getReader()
const first = await rd.read()
console.log("v", first.value)
`, "v 41")
}

func TestE2EHTTPClientGet(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
import http from 'http'
http.get("%s/flat", (res) => {
  console.log("status", res.statusCode)
  let data = ""
  res.on('data', (chunk: string) => { data = data + chunk })
  res.on('end', () => { console.log("body", data) })
})
`, srv.URL)
	assertOutputImports(t, src, `status 200`+"\n"+`body {"title":"hello","count":42,"active":true}`)
}

func TestE2EHTTPClientGetStatusCode(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
import http from 'http'
http.get("%s/notfound", (res) => {
  console.log(res.statusCode)
  res.on('data', (chunk: string) => {})
  res.on('end', () => { console.log("done") })
})
`, srv.URL)
	assertOutputImports(t, src, "404\ndone")
}

// TDD-00138 Stage 2: async client delivery — http.get can call this program's
// OWN in-process http.listen server without deadlocking the single event loop
// (registers a completion reaction the loop fires; previously busy-blocked).
func TestE2EHTTPClientInProcessRoundTrip(t *testing.T) {
	assertOutputImports(t, `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200)
  res.end("pong")
}).listen(18499, () => {
  http.get("http://127.0.0.1:18499/", (res) => {
    let data = ""
    res.on('data', (chunk: string) => { data = data + chunk })
    res.on('end', () => {
      console.log("got", data, res.statusCode)
      process.exit(0)
    })
  })
})
`, "got pong 200")
}

// Response.headers (ADR-00490): raw header capture parsed lazily into a
// Map<string,string> with lowercased keys per fetch Headers semantics.
func TestE2EFetchResponseHeaders(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
async function main2(): Promise<void> {
    const r: Response = await fetch("%s/flat")
    const h: Map<string, string> = r.headers
    console.log(h.get("content-type"))
    console.log(h.has("x-nonexistent"))
}
main2()
`, srv.URL)
	assertOutput(t, src, "application/json\nfalse")
}
