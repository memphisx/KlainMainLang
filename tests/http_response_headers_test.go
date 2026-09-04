package tests

import (
	"io"
	"net/http"
	"net/http/httptrace"
	"testing"
	"time"
)

// TestE2EHTTPResponseDateAndKeepAlive verifies two Node-fidelity properties of
// an http.createServer response (ADR-00691): every response carries an
// auto-stamped RFC 7231 `Date` header, and HTTP/1.1 connections are persistent
// by default (`Connection: keep-alive` + `Keep-Alive: timeout=5`), with the
// socket reused for a second request on the same connection.
func TestE2EHTTPResponseDateAndKeepAlive(t *testing.T) {
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.end("ok")
}).listen(8971)
`
	startHTTPServer(t, src, 8971)

	// A single Transport so the connection pool can reuse the socket.
	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}

	// First request: assert Date + keep-alive headers.
	resp1, err := client.Get("http://127.0.0.1:8971/first")
	if err != nil {
		t.Fatalf("GET #1: %v", err)
	}
	if d := resp1.Header.Get("Date"); d == "" {
		t.Error("first response missing Date header")
	} else if _, perr := time.Parse(http.TimeFormat, d); perr != nil {
		t.Errorf("Date header %q is not RFC 7231 format: %v", d, perr)
	}
	if ka := resp1.Header.Get("Keep-Alive"); ka != "timeout=5" {
		t.Errorf("Keep-Alive: got %q, want %q", ka, "timeout=5")
	}
	// Go's client strips the Connection header from resp.Header, so verify
	// persistence via actual socket reuse below instead.
	io.Copy(io.Discard, resp1.Body)
	resp1.Body.Close()

	// Second request on the same client: httptrace must report a reused conn.
	var reused bool
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
	}
	req2, _ := http.NewRequest("GET", "http://127.0.0.1:8971/second", nil)
	req2 = req2.WithContext(httptrace.WithClientTrace(req2.Context(), trace))
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("GET #2: %v", err)
	}
	if d := resp2.Header.Get("Date"); d == "" {
		t.Error("second response missing Date header")
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if string(body2) != "ok" {
		t.Errorf("second body: got %q, want %q", string(body2), "ok")
	}
	if !reused {
		t.Error("second request did not reuse the keep-alive connection")
	}
}

// TestE2EHTTPResponseConnectionClose verifies that a client-sent
// `Connection: close` is honored: the server closes after one response and
// labels it `Connection: close` (ADR-00691).
func TestE2EHTTPResponseConnectionClose(t *testing.T) {
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.end("bye")
}).listen(8973)
`
	startHTTPServer(t, src, 8973)

	req, _ := http.NewRequest("GET", "http://127.0.0.1:8973/", nil)
	req.Close = true // send Connection: close
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Date") == "" {
		t.Error("response missing Date header")
	}
	// Go surfaces the server's Connection: close via resp.Close.
	if !resp.Close {
		t.Error("expected Connection: close to be honored (resp.Close == true)")
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "bye" {
		t.Errorf("body: got %q, want %q", string(body), "bye")
	}
}
