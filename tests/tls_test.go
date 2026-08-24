package tests

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// genSelfSignedPEM produces a self-signed cert + key as PEM strings, escaped for
// embedding in a TS string literal (newlines → \n) — the { cert, key } a
// tls.createServer program needs.
func genSelfSignedPEM(t *testing.T) (certLit, keyLit string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	esc := func(b []byte) string { return strings.ReplaceAll(string(b), "\n", "\\n") }
	return esc(certPEM), esc(keyPEM)
}

// TestE2ETLSCreateServer: a compiled tls.createServer echoes each chunk; a Go
// crypto/tls client connects (InsecureSkipVerify for the self-signed cert),
// writes, and reads the echo back.
func TestE2ETLSCreateServer(t *testing.T) {
	certLit, keyLit := genSelfSignedPEM(t)
	src := fmt.Sprintf(`
import tls from 'tls'
const cert = "%s"
const key = "%s"
const server = tls.createServer({ cert: cert, key: key }, (socket) => {
  socket.on('data', (chunk: string) => { socket.write("echo:" + chunk) })
})
server.listen(8971, () => {})
`, certLit, keyLit)
	startHTTPServer(t, src, 8971)

	conn, err := tls.Dial("tcp", "127.0.0.1:8971", &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got, want := string(buf[:n]), "echo:hello"; got != want {
		t.Errorf("echo: got %q, want %q", got, want)
	}
}

// --- tls.connect (TDD-00109) ---
//
// A local httptest.NewTLSServer (a self-signed HTTPS server on a real port) is
// the offline fixture: a real TLS handshake over a real TCP connection, not a
// mock. tls.connect reuses the net client path — a blocking TCP connect + a
// blocking libssl handshake — so these exercise the real OpenSSL integration.

func newTLSTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "tls-ok")
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	return srv, u.Port()
}

// A full TLS round-trip against the self-signed fixture (rejectUnauthorized:
// false to accept the self-signed cert): connect, send a raw HTTP GET, read the
// response, confirm the body arrived.
func TestE2ETLSConnectRoundTrip(t *testing.T) {
	_, port := newTLSTestServer(t)
	src := fmt.Sprintf(`
import tls from 'tls'
const sock = tls.connect(%s, "127.0.0.1", { rejectUnauthorized: false }, () => {
  sock.write("GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
})
let got = ""
sock.on("data", (chunk: string) => { got = got + chunk })
sock.on("end", () => {
  console.log(got.indexOf("tls-ok") >= 0 ? "got-body" : "no-body")
})
`, port)
	assertOutputImports(t, src, "got-body")
}

// The default (rejectUnauthorized: true) rejects the self-signed fixture cert —
// the handshake fails and tls.connect throws a catchable Error.
func TestE2ETLSConnectVerifyFails(t *testing.T) {
	_, port := newTLSTestServer(t)
	src := fmt.Sprintf(`
import tls from 'tls'
try {
  const sock = tls.connect(%s, "127.0.0.1")
  console.log("unexpected connect")
} catch (e) {
  console.log("verify rejected")
}
`, port)
	assertOutputImports(t, src, "verify rejected")
}

// Connecting to a closed port throws a catchable Error (connect refused).
func TestE2ETLSConnectRefused(t *testing.T) {
	src := `
import tls from 'tls'
try {
  const sock = tls.connect(1, "127.0.0.1", { rejectUnauthorized: false })
  console.log("unexpected connect")
} catch (e) {
  console.log("refused")
}
`
	assertOutputImports(t, src, "refused")
}
