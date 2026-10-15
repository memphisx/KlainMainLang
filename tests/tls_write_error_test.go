package tests

import (
	"strings"
	"testing"
)

// TestE2ETLSServerWriteAfterClientDestroy is TDD-00180's TLS write-error
// probe: a TLS server keeps writing while its client destroys the
// connection. The server socket's 'error' code (or a plain close) must be
// Node's on the same machine, Windows included.
func TestE2ETLSServerWriteAfterClientDestroy(t *testing.T) {
	certLit, keyLit := genSelfSignedPEM(t)
	src := strings.NewReplacer(`"KEY"`, `"`+keyLit+`"`, `"CERT"`, `"`+certLit+`"`).Replace(`
import tls from 'tls'
import type { AddressInfo } from 'net'
const key = "KEY", cert = "CERT"
let reported = false
const report = (what: string) => { if (!reported) { reported = true; console.log(what) } }
const server = tls.createServer({ key, cert }, (s) => {
  const chunk = Buffer.alloc(64 * 1024, 120)
  s.on('error', (e: any) => { report('server error ' + e.code) })
  s.on('close', () => { report('server closed'); server.close() })
  const pump = () => { if (!s.destroyed) { s.write(chunk); setTimeout(pump, 1) } }
  pump()
})
server.listen(0, '127.0.0.1', () => {
  const port = (server.address() as AddressInfo).port
  const c = tls.connect({ host: '127.0.0.1', port, ca: cert, servername: 'kml.test' }, () => {
    c.once('data', () => { c.destroy() })
  })
  c.on('error', () => {})
})
`)
	theirs := runNodeTS(t, src)
	ours := compileAndRunImports(t, src)
	if ours != theirs || !strings.HasPrefix(ours, "server ") {
		t.Fatalf("output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", ours, theirs)
	}
	t.Logf("both: %s", ours)
}
