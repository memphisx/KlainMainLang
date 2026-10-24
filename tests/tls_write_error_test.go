package tests

import (
	"strings"
	"testing"
)

// TestE2ETLSServerWriteAfterClientDestroy is TDD-00180's TLS write-error
// probe: a TLS server keeps writing while its client destroys the
// connection with bytes unread, so the client's kernel resets it. Which of
// the server's operations meets the reset first is a race in Node too (on
// macOS a write's EPIPE or a read's ECONNRESET, run to run), so both sides
// must report one of those kernel errors, never a plain close.
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
    // Paused, so the server's next records stay unread in the client's
    // socket: its close is then a reset on every platform, not a FIN.
    c.once('data', () => { c.pause(); setTimeout(() => c.destroy(), 50) })
  })
  c.on('error', () => {})
})
`)
	reset := func(out string) bool {
		out = strings.TrimSpace(out)
		return out == "server error EPIPE" || out == "server error ECONNRESET"
	}
	theirs := runNodeTS(t, src)
	ours := compileAndRunImports(t, src)
	if !reset(ours) || !reset(theirs) {
		t.Fatalf("not the reset's error:\n--- ours ---\n%s\n--- node ---\n%s", ours, theirs)
	}
	t.Logf("ours: %s; node: %s", ours, theirs)
}

// Node's writeAfterFIN: once the peer's end has ended a socket without
// allowHalfOpen, a write is EPIPE ("ended by the other party"), handed to
// its callback and the 'error' listener.
func TestE2ENetWriteAfterPeerEndIsEPIPE(t *testing.T) {
	src := `
import net from 'net'
import type { AddressInfo } from 'net'
const server = net.createServer((s) => {
  s.on('end', () => {
    setImmediate(() => {
      console.log('ended', s.writableEnded)
      s.write('late', (e) => console.log('cb', e && (e as any).code))
    })
  })
  s.on('error', (e: any) => console.log('error', e.code, e.message))
  s.on('close', () => { console.log('closed'); server.close() })
  s.resume()
})
server.listen(0, '127.0.0.1', () => {
  const c = net.connect((server.address() as AddressInfo).port, '127.0.0.1', () => c.end())
  c.resume()
})
`
	compareLines(t, compileAndRunImports(t, src), runNodeTS(t, src))
}

// A shutdown the peer's end asks for (no allowHalfOpen) completes in the
// same pass of the loop as the read, as libuv's stream drains after reading:
// 'finish' comes before a setImmediate queued from 'end'.
func TestE2ENetEndShutdownSamePass(t *testing.T) {
	src := `
import net from 'net'
import type { AddressInfo } from 'net'
const server = net.createServer((s) => {
  s.on('end', () => {
    console.log('end')
    setImmediate(() => console.log('immediate', s.writableFinished, s.destroyed))
  })
  s.on('finish', () => console.log('finish'))
  s.on('close', () => { console.log('closed'); server.close() })
  s.resume()
})
server.listen(0, '127.0.0.1', () => {
  const c = net.connect((server.address() as AddressInfo).port, '127.0.0.1', () => c.end())
  c.resume()
})
`
	compareLines(t, compileAndRunImports(t, src), runNodeTS(t, src))
}
