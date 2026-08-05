// httpbin-lite is a tiny, local, dependency-free stand-in for httpbin.org,
// used only by `make examples` (see ../../Makefile and ADR-00096) so that
// examples/fetch/*.ts and examples/async/promise_all.ts don't depend on a
// third-party website's uptime/rate-limiting for CI to pass. It implements
// only the handful of endpoints those examples actually exercise — it is
// not a general httpbin clone.
//
// This is Go, not KlainMainLang, only because of one route: every endpoint
// below except /bytes/{n} is already expressible with this compiler's own
// http.listen (arbitrary status/headers, any method, query/body reads) —
// /bytes/{n} needs a response body with a guaranteed embedded null byte,
// which http.listen can't carry through today (see docs/tdd/TDD-00026.md).
// Once that lands, this fixture should be rewritten in KlainMainLang.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	port := os.Getenv("HTTPBIN_LITE_PORT")
	if port == "" {
		port = "8765"
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /get", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"origin":"127.0.0.1"}`)
	})

	mux.HandleFunc("GET /ip", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"origin":"127.0.0.1"}`)
	})

	mux.HandleFunc("GET /status/{code}", func(w http.ResponseWriter, r *http.Request) {
		code, err := strconv.Atoi(r.PathValue("code"))
		if err != nil {
			code = http.StatusInternalServerError
		}
		w.WriteHeader(code)
		fmt.Fprintf(w, "status %d", code)
	})

	mux.HandleFunc("GET /redirect-to", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Query().Get("url"), http.StatusFound)
	})

	mux.HandleFunc("GET /bytes/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n <= 0 {
			n = 16
		}
		// Deliberately includes an embedded null byte (unlike httpbin's
		// genuinely random bytes, which only sometimes did) so this
		// consistently exercises the .arrayBuffer() byte-count-survives-a-
		// null-byte path (see examples/fetch/fetch.ts and ADR-00094).
		body := make([]byte, n)
		for i := range body {
			body[i] = byte(i + 1)
		}
		if n > 5 {
			body[5] = 0
		}
		w.Write(body)
	})

	mux.HandleFunc("POST /post", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	})

	mux.HandleFunc("DELETE /delete", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "deleted")
	})

	mux.HandleFunc("GET /headers", func(w http.ResponseWriter, r *http.Request) {
		for name, values := range r.Header {
			for _, v := range values {
				fmt.Fprintf(w, "%s: %s\n", name, v)
			}
		}
	})

	mux.HandleFunc("GET /delay/{n}", func(w http.ResponseWriter, r *http.Request) {
		secs, err := strconv.ParseFloat(r.PathValue("n"), 64)
		if err != nil || secs < 0 {
			secs = 1
		}
		time.Sleep(time.Duration(secs * float64(time.Second)))
		fmt.Fprint(w, "delayed")
	})

	fmt.Fprintf(os.Stderr, "httpbin-lite listening on 127.0.0.1:%s\n", port)
	if err := http.ListenAndServe("127.0.0.1:"+port, mux); err != nil {
		fmt.Fprintln(os.Stderr, "httpbin-lite:", err)
		os.Exit(1)
	}
}
