package tests

import "testing"

// --- Node `dns`: dns.lookup (ADR-00326) ---
//
// getaddrinfo-backed IPv4 resolution; the callback fires synchronously with
// (err, address, family), like zlib's callback forms.

func TestE2EDnsLookupLocalhost(t *testing.T) {
	assertOutputImports(t, `
import dns from 'dns'
dns.lookup("localhost", (err, address, family) => {
  console.log("err null:", err === null)
  console.log("addr:", address)
  console.log("family:", family)
})
`, "err null: true\naddr: 127.0.0.1\nfamily: 4")
}

func TestE2EDnsLookupNumericIP(t *testing.T) {
	assertOutputImports(t, `
import dns from 'dns'
dns.lookup("127.0.0.1", (err, address, family) => {
  console.log(address)
})
`, "127.0.0.1")
}

func TestE2EDnsLookupFailure(t *testing.T) {
	assertOutputImports(t, `
import dns from 'dns'
dns.lookup("no.such.host.invalid.example", (err, address, family) => {
  console.log("err set:", err !== null)
})
`, "err set: true")
}

// --- dns extras: resolve4 + promises.lookup (ADR-00329) ---

func TestE2EDnsResolve4(t *testing.T) {
	assertOutputImports(t, `
import dns from 'dns'
dns.resolve4("localhost", (err, addresses) => {
  console.log("err null:", err === null)
  console.log("has 127.0.0.1:", addresses.indexOf("127.0.0.1") >= 0)
})
`, "err null: true\nhas 127.0.0.1: true")
}

func TestE2EDnsPromisesLookup(t *testing.T) {
	assertOutputImports(t, `
import dns from 'dns'
async function main() {
  const r = await dns.promises.lookup("localhost")
  console.log("addr:", r.address)
  console.log("family:", r.family)
}
main()
`, "addr: 127.0.0.1\nfamily: 4")
}

func TestE2EDnsPromisesLookupRejects(t *testing.T) {
	assertOutputImports(t, `
import dns from 'dns'
async function main() {
  try {
    await dns.promises.lookup("no.such.host.invalid.example")
    console.log("no throw")
  } catch (e) {
    console.log("rejected")
  }
}
main()
`, "rejected")
}
