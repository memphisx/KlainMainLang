package tests

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- wss:// WebSocket client over TLS (TDD-00039 Stage 4) ---
//
// A local httptest.NewTLSServer whose handler hijacks the connection and speaks
// the WebSocket handshake + one masked-frame echo — a real TLS + WebSocket peer
// at 127.0.0.1 (the WS client resolves numeric IPs only on macOS). The client
// verifies the self-signed cert via SSL_CERT_FILE pointing at the server's cert.

const wsMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + wsMagic))
	return base64.StdEncoding.EncodeToString(h[:])
}

// wsReadText reads one masked client text frame (payload < 126 bytes).
func wsReadText(r *bufio.Reader) (string, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(r, h); err != nil {
		return "", err
	}
	l := int(h[1] & 0x7f)
	mask := make([]byte, 4)
	if _, err := io.ReadFull(r, mask); err != nil {
		return "", err
	}
	p := make([]byte, l)
	if _, err := io.ReadFull(r, p); err != nil {
		return "", err
	}
	for i := range p {
		p[i] ^= mask[i%4]
	}
	return string(p), nil
}

// wsWriteText writes one unmasked server text frame (payload < 126 bytes).
func wsWriteText(w *bufio.Writer, s string) {
	b := []byte(s)
	w.Write([]byte{0x81, byte(len(b))})
	w.Write(b)
	w.Flush()
}

func newWSSEchoServer(t *testing.T) (port, certFile string) {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept := wsAccept(r.Header.Get("Sec-WebSocket-Key"))
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("no hijack")
			return
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
		brw.Flush()
		msg, err := wsReadText(brw.Reader)
		if err != nil {
			return
		}
		wsWriteText(brw.Writer, "echo: "+msg)
	})
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)

	// Write the server's self-signed cert to a temp file for the client to trust.
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	cf := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(cf, certPEM, 0644); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	return u.Port(), cf
}

func TestE2EWSSClientEcho(t *testing.T) {
	port, certFile := newWSSEchoServer(t)
	src := fmt.Sprintf(`
const ws = new WebSocket("wss://127.0.0.1:%s/")
ws.onopen = () => { ws.send("hi-wss") }
ws.onmessage = (ev) => { console.log(ev.data); ws.close() }
ws.onclose = () => { process.exit(0) }
ws.onerror = () => { console.log("error"); process.exit(1) }
setTimeout(() => { console.log("timeout"); process.exit(2) }, 6000)
`, port)
	bin := buildBinaryImports(t, src)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+certFile)
	out, _ := cmd.CombinedOutput()
	got := strings.TrimSpace(string(out))
	if got != "echo: hi-wss" {
		t.Errorf("wss echo: got %q, want %q", got, "echo: hi-wss")
	}
}
