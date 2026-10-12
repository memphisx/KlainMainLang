package tests

import (
	"fmt"
	"net"
	"testing"
)

func TestE2EFetchFailedCause(t *testing.T) {
	// A failed fetch rejects with Node's `TypeError: fetch failed`, the
	// reason as its cause: the connect error, the lookup error, or `bad
	// port` for a port the fetch standard blocks (ADR-01418).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listens there now: the connection is refused
	assertOutput(t, fmt.Sprintf(`
async function main() {
  for (const u of ["http://127.0.0.1:%d/", "http://nonexistent.invalid/", "http://127.0.0.1:6000/"]) {
    try { await fetch(u) } catch (e: any) {
      const c = e.cause
      console.log(e.name, e.message, c.message, c.code, c.syscall, c.address, c.port, c.hostname, c.errno < 0)
    }
  }
}
main()
`, port), fmt.Sprintf("TypeError fetch failed connect ECONNREFUSED 127.0.0.1:%d ECONNREFUSED connect 127.0.0.1 %d undefined true\n"+
		"TypeError fetch failed getaddrinfo ENOTFOUND nonexistent.invalid ENOTFOUND getaddrinfo undefined undefined nonexistent.invalid true\n"+
		"TypeError fetch failed bad port undefined undefined undefined undefined undefined false", port, port))
}
