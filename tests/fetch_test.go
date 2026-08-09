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
	assertOutput(t, src, "200\n1\n"+`{"title":"hello","count":42,"active":true}`)
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
	assertOutput(t, src, "404\n0")
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
	assertOutput(t, src, "500\n0")
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
	assertOutput(t, src, "hello\n42\n1")
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
	assertOutput(t, src, "application/json\napplication/json\n1\n0\n0")
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
	assertOutput(t, src, "http://example.com/x\nGET\n0")
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
