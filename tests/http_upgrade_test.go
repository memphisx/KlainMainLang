package tests

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TDD-00158 Stage 1: the Node-faithful HTTP `'upgrade'` event.
// `server.on('upgrade', (req, socket, head) => …)` hands the raw connection to
// the handler as a net.Socket; the handler writes its own 101 and speaks a raw
// protocol (here a trivial line echo, no WebSocket framing — proving the event
// is protocol-agnostic, exactly like Node core).

const upgradeEchoServer = `
import http from 'http'
interface Res { status: number; body: string }
const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end('plain http')
})
server.on('upgrade', (req, socket, head) => {
  socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n')
  socket.on('data', (chunk) => { socket.write('echo:' + chunk.toString()) })
})
server.listen(%d)
`

// readUpgradeHandshake consumes the 101 response headers up to the blank line.
func readUpgradeHandshake(t *testing.T, r *bufio.Reader) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read handshake: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			return
		}
		if strings.HasPrefix(line, "HTTP/") && !strings.Contains(line, "101") {
			t.Fatalf("expected 101, got: %q", line)
		}
	}
}

func TestE2EHTTPUpgradeEventEcho(t *testing.T) {
	src := fmt.Sprintf(upgradeEchoServer, 8961)
	startHTTPServer(t, src, 8961)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:8961", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := "GET /chat HTTP/1.1\r\nHost: x\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write upgrade req: %v", err)
	}
	r := bufio.NewReader(conn)
	readUpgradeHandshake(t, r)

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("write data: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if got, want := string(buf[:n]), "echo:hello"; got != want {
		t.Errorf("echo: got %q, want %q", got, want)
	}
}

// A non-upgrade request on the same server still reaches the normal handler.
func TestE2EHTTPUpgradeCoexistsWithNormalHTTP(t *testing.T) {
	src := fmt.Sprintf(upgradeEchoServer, 8962)
	startHTTPServer(t, src, 8962)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:8962", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /plain HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The request sends `Connection: close`, so the server closes the socket
	// after the full response — read to EOF rather than assuming a single
	// Read() returns headers *and* body in one TCP segment (on Linux the body
	// often arrives in a later segment, so a lone Read sees only the headers).
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(string(resp), "plain http") {
		t.Errorf("normal request: got %q, want body 'plain http'", string(resp))
	}
}

// wss:// server: the same faithful 'upgrade' event on an https.createServer.
// The socket is TLS-backed, so socket.write goes through SSL with no extra
// code — the whole point of TDD-00158.
func TestE2EHTTPSUpgradeEventEchoTLS(t *testing.T) {
	certLit, keyLit := genSelfSignedPEM(t)
	src := fmt.Sprintf(`
import https from 'https'
const cert = "%s"
const key = "%s"
const server = https.createServer({ cert: cert, key: key }, (req, res) => {
  res.writeHead(200); res.end('secure http')
})
server.on('upgrade', (req, socket, head) => {
  socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n')
  socket.on('data', (chunk) => { socket.write('secho:' + chunk.toString()) })
})
server.listen(8963)
`, certLit, keyLit)
	startHTTPServer(t, src, 8963)

	conn, err := tls.Dial("tcp", "127.0.0.1:8963", &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer conn.Close()
	req := "GET /wschat HTTP/1.1\r\nHost: localhost\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write upgrade req: %v", err)
	}
	r := bufio.NewReader(conn)
	readUpgradeHandshake(t, r)

	if _, err := conn.Write([]byte("hi-tls")); err != nil {
		t.Fatalf("write data: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if got, want := string(buf[:n]), "secho:hi-tls"; got != want {
		t.Errorf("tls echo: got %q, want %q", got, want)
	}
}

// klain:ws WebSocketServer over https.createServer — the ergonomic WSConnection
// convenience speaking wss:// (TDD-00158 Stage 2). A real WebSocket handshake +
// masked text frame over TLS, echoed back through the frame codec.
func TestE2EWSSServerKlainWS(t *testing.T) {
	certLit, keyLit := genSelfSignedPEM(t)
	src := fmt.Sprintf(`
import https from 'https'
import { WebSocketServer } from 'klain:ws'
const cert = "%s"
const key = "%s"
const server = https.createServer({ cert: cert, key: key }, (req, res) => {
  res.writeHead(200); res.end('secure')
})
const wss = new WebSocketServer({ server })
wss.on('connection', (socket: WSConnection) => {
  socket.onmessage = (ev) => { socket.send('wsecho: ' + ev.data) }
})
server.listen(8964)
`, certLit, keyLit)
	startHTTPServer(t, src, 8964)

	conn, err := tls.Dial("tcp", "127.0.0.1:8964", &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer conn.Close()
	wsHandshake(t, conn, "/")
	wsSendText(t, conn, "over-tls")
	opcode, payload := wsRecvFrame(t, conn)
	if opcode != 1 {
		t.Errorf("opcode: got %d, want 1 (text)", opcode)
	}
	if string(payload) != "wsecho: over-tls" {
		t.Errorf("payload: got %q, want %q", payload, "wsecho: over-tls")
	}
}

// The full faithfulness proof (TDD-00159 closing TDD-00158): a WebSocket
// handshake completed by hand with pure Node APIs — http.createServer +
// server.on('upgrade') + crypto.createHash — no klain:ws. wsHandshake verifies
// the server's Sec-WebSocket-Accept is the spec-correct value for the key the
// client sent, so this passing means the hand-rolled Node path is genuinely
// faithful end to end.
func TestE2EHandRolledUpgradeAcceptKey(t *testing.T) {
	src := `
import http from 'http'
import crypto from 'crypto'
const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end('http')
})
server.on('upgrade', (req, socket, head) => {
  const key: string = req.headers.get('sec-websocket-key')
  const accept = crypto.createHash('sha1')
    .update(key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11')
    .digest('base64')
  socket.write('HTTP/1.1 101 Switching Protocols\r\n' +
    'Upgrade: websocket\r\nConnection: Upgrade\r\n' +
    'Sec-WebSocket-Accept: ' + accept + '\r\n\r\n')
})
server.listen(8965)
`
	startHTTPServer(t, src, 8965)
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8965", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	wsHandshake(t, conn, "/") // verifies the accept key is spec-correct
}
