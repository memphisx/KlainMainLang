package tests

import (
	"fmt"
	"testing"
)

// --- XMLHttpRequest (TDD-00040) ---
//
// send() looks synchronous from TS code but is built on the exact same
// non-blocking __kml_fetch_async primitive fetch() itself uses (see
// runtime_fetch.go's ensureFetchAwaitSettled) — these tests exercise it
// against a local httptest.Server, reusing newFetchTestServer from
// fetch_test.go (same package, same fixtures).

func TestE2EXHRBasicGetSuccess(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
const xhr = new XMLHttpRequest()
console.log(xhr.readyState)
let callCount: number = 0
let firstState: number = -1
xhr.onreadystatechange = () => {
    callCount = callCount + 1
    if (callCount === 1) {
        firstState = xhr.readyState
    }
}
xhr.open("GET", "%s/flat")
xhr.send()
console.log(callCount)
console.log(firstState)
console.log(xhr.readyState)
console.log(xhr.status)
console.log(xhr.responseText.indexOf('"title":"hello"') > -1)
console.log(xhr.response === xhr.responseText)
`, srv.URL)
	assertOutput(t, src, "0\n2\n1\n4\n200\ntrue\ntrue")
}

func TestE2EXHRPostWithHeadersAndBody(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
interface EchoResp { method: string; body: string; x_custom: string }

const xhr = new XMLHttpRequest()
xhr.open("POST", "%s/echo")
xhr.setRequestHeader("X-Custom-Header", "from-xhr")
xhr.send("payload-data")
const data: EchoResp = JSON.parse(xhr.responseText)
console.log(data.method)
console.log(data.body)
console.log(data.x_custom)
`, srv.URL)
	assertOutput(t, src, "POST\npayload-data\nfrom-xhr")
}

func TestE2EXHROnLoadFiresOnSuccessNotOnError(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
const xhr = new XMLHttpRequest()
let loaded = false
let errored = false
xhr.onload = () => { loaded = true }
xhr.onerror = () => { errored = true }
xhr.open("GET", "%s/flat")
xhr.send()
console.log(loaded)
console.log(errored)
`, srv.URL)
	assertOutput(t, src, "true\nfalse")
}

func TestE2EXHROnErrorFiresOnNetworkFailureNoThrow(t *testing.T) {
	src := `
const xhr = new XMLHttpRequest()
let loaded = false
let errored = false
xhr.onload = () => { loaded = true }
xhr.onerror = () => { errored = true }
xhr.open("GET", "http://127.0.0.1:1/unreachable")
xhr.send()
console.log(loaded)
console.log(errored)
console.log(xhr.status)
console.log(xhr.readyState)
`
	assertOutput(t, src, "false\ntrue\n0\n4")
}

func TestE2EXHRAbortResetsReadyState(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
const xhr = new XMLHttpRequest()
xhr.open("GET", "%s/flat")
xhr.send()
xhr.abort()
console.log(xhr.readyState)
`, srv.URL)
	assertOutput(t, src, "0")
}

func TestE2EXHRWrongArgCountsRejected(t *testing.T) {
	cases := []string{
		`const xhr = new XMLHttpRequest(); xhr.open("GET")`,
		`const xhr = new XMLHttpRequest(); xhr.setRequestHeader("X")`,
		`const xhr = new XMLHttpRequest(); xhr.send("a", "b")`,
		`const xhr = new XMLHttpRequest(); xhr.abort(1)`,
		`new XMLHttpRequest(1)`,
	}
	for _, src := range cases {
		if _, err := parseAndCompile(src); err == nil {
			t.Fatalf("expected a compile error for %q, got none", src)
		}
	}
}

// getResponseHeader/getAllResponseHeaders (ADR-00490): case-insensitive
// single-header lookup + the "name: value\r\n" concatenation, both read
// from the response-header capture the shared fetch runtime records.
func TestE2EXHRResponseHeaders(t *testing.T) {
	srv := newFetchTestServer(t)
	src := fmt.Sprintf(`
const xhr = new XMLHttpRequest()
xhr.open("GET", "%s/flat")
console.log(xhr.getAllResponseHeaders() === "")
xhr.send()
console.log(xhr.getResponseHeader("Content-Type"))
console.log(xhr.getResponseHeader("content-type"))
const all: string = xhr.getAllResponseHeaders()
console.log(all.indexOf("content-type: application/json") > -1)
`, srv.URL)
	assertOutput(t, src, "true\napplication/json\napplication/json\ntrue")
}
