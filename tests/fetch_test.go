package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestE2EFetchWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fetch("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for fetch() with the wrong argument count, got none")
	}
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
