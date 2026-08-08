package tests

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// wsHandshake performs the RFC 6455 client-side upgrade handshake over an
// already-dialed connection and verifies the server's Sec-WebSocket-Accept
// is the spec-correct value for the Sec-WebSocket-Key this function itself
// generated — not just that *some* 101 response came back.
func wsHandshake(t *testing.T, conn net.Conn, path string) {
	t.Helper()
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req := fmt.Sprintf(
		"GET %s HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, key)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	buf := make([]byte, 4096)
	total := 0
	for !bytes.Contains(buf[:total], []byte("\r\n\r\n")) {
		n, err := conn.Read(buf[total:])
		if err != nil {
			t.Fatalf("read handshake response: %v", err)
		}
		total += n
	}
	resp := string(buf[:total])
	if !strings.Contains(resp, "101") {
		t.Fatalf("handshake: expected 101 status, got: %q", resp)
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	wantAccept := base64.StdEncoding.EncodeToString(sum[:])
	if !strings.Contains(resp, "Sec-WebSocket-Accept: "+wantAccept) {
		t.Fatalf("handshake: Sec-WebSocket-Accept mismatch, want %q in %q", wantAccept, resp)
	}
}

// wsSendFrame sends a single masked frame of the given opcode —
// client-to-server frames MUST be masked per RFC 6455 §5.1. wsSendText is
// the common (opcode 1) case; wsSendFrame itself is used directly by tests
// exercising control frames (ping, close).
func wsSendFrame(t *testing.T, conn net.Conn, opcode byte, payload []byte) {
	t.Helper()
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		t.Fatalf("rand: %v", err)
	}
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}

	var hdr []byte
	switch {
	case len(payload) <= 125:
		hdr = []byte{0x80 | opcode, 0x80 | byte(len(payload))}
	case len(payload) <= 0xFFFF:
		hdr = make([]byte, 4)
		hdr[0], hdr[1] = 0x80|opcode, 0x80|126
		binary.BigEndian.PutUint16(hdr[2:], uint16(len(payload)))
	default:
		hdr = make([]byte, 10)
		hdr[0], hdr[1] = 0x80|opcode, 0x80|127
		binary.BigEndian.PutUint64(hdr[2:], uint64(len(payload)))
	}
	buf := append(hdr, mask...)
	buf = append(buf, masked...)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// wsSendText sends a single masked text frame (opcode 1).
func wsSendText(t *testing.T, conn net.Conn, msg string) {
	t.Helper()
	wsSendFrame(t, conn, 0x1, []byte(msg))
}

// wsRecvFrame reads exactly one unmasked frame (server-to-client frames
// MUST NOT be masked per RFC 6455 §5.1) from conn, returning its opcode and
// payload.
func wsRecvFrame(t *testing.T, conn net.Conn) (opcode int, payload []byte) {
	t.Helper()
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	opcode = int(hdr[0] & 0x0F)
	plen := int(hdr[1] & 0x7F)
	switch plen {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(conn, ext); err != nil {
			t.Fatalf("read ext len: %v", err)
		}
		plen = int(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(conn, ext); err != nil {
			t.Fatalf("read ext len: %v", err)
		}
		plen = int(binary.BigEndian.Uint64(ext))
	}
	payload = make([]byte, plen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return opcode, payload
}

func TestE2EWSHandshakeAndEcho(t *testing.T) {
	src := `
interface Res { status: number; body: string }
http.listen(8975, (req: Request): Res => {
  return { status: 200, body: "not a websocket request" }
}, {
  ws: (socket: WSConnection) => {
    socket.onmessage = (ev) => {
      socket.send("echo: " + ev.data)
    }
  }
})
`
	startHTTPServer(t, src, 8975)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:8975", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	wsHandshake(t, conn, "/")
	wsSendText(t, conn, "hello world")
	opcode, payload := wsRecvFrame(t, conn)
	if opcode != 1 {
		t.Errorf("opcode: got %d, want 1 (text)", opcode)
	}
	if string(payload) != "echo: hello world" {
		t.Errorf("payload: got %q, want %q", payload, "echo: hello world")
	}
}

func TestE2EWSExtendedLength(t *testing.T) {
	src := `
interface Res { status: number; body: string }
http.listen(8976, (req: Request): Res => {
  return { status: 200, body: "not a websocket request" }
}, {
  ws: (socket: WSConnection) => {
    socket.onmessage = (ev) => {
      socket.send("echo: " + ev.data)
    }
  }
})
`
	startHTTPServer(t, src, 8976)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:8976", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	wsHandshake(t, conn, "/")
	big := strings.Repeat("B", 300)
	wsSendText(t, conn, big)
	opcode, payload := wsRecvFrame(t, conn)
	if opcode != 1 {
		t.Errorf("opcode: got %d, want 1 (text)", opcode)
	}
	want := "echo: " + big
	if string(payload) != want {
		t.Errorf("payload length: got %d, want %d", len(payload), len(want))
	}
}

func TestE2EWSCoexistsWithNormalHTTP(t *testing.T) {
	src := `
interface Res { status: number; body: string }
http.listen(8977, (req: Request): Res => {
  return { status: 200, body: "plain http: " + req.path }
}, {
  ws: (socket: WSConnection) => {
    socket.onmessage = (ev) => {
      socket.send("ws: " + ev.data)
    }
  }
})
`
	startHTTPServer(t, src, 8977)

	// Plain HTTP request on the same listener must still behave normally.
	resp, err := http.Get("http://127.0.0.1:8977/hello")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "plain http: /hello" {
		t.Errorf("plain HTTP body: got %q, want %q", body, "plain http: /hello")
	}

	// A WebSocket upgrade on the same listener still works too.
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8977", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	wsHandshake(t, conn, "/socket")
	wsSendText(t, conn, "ping")
	opcode, payload := wsRecvFrame(t, conn)
	if opcode != 1 || string(payload) != "ws: ping" {
		t.Errorf("ws echo: got opcode=%d payload=%q, want opcode=1 payload=%q", opcode, payload, "ws: ping")
	}
}

func TestE2EWSCloseFrameEndsConnectionCleanly(t *testing.T) {
	src := `
interface Res { status: number; body: string }
http.listen(8978, (req: Request): Res => {
  return { status: 200, body: "not a websocket request" }
}, {
  ws: (socket: WSConnection) => {
    socket.onmessage = (ev) => {
      socket.send("echo: " + ev.data)
    }
  }
})
`
	startHTTPServer(t, src, 8978)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:8978", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	wsHandshake(t, conn, "/")

	// RFC 6455 §5.5.1: an endpoint that receives a Close frame must echo one
	// back before actually closing (TDD-00039 Stage 2) — send a Close frame
	// carrying status code 1000 (0x03E8) and confirm the server echoes a
	// Close frame back with the same payload, rather than just silently
	// dropping the connection the way Stage 1 did.
	closeCode := []byte{0x03, 0xE8}
	wsSendFrame(t, conn, 0x8, closeCode)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload := wsRecvFrame(t, conn)
	if opcode != 8 {
		t.Errorf("close echo: got opcode %d, want 8 (close)", opcode)
	}
	if !bytes.Equal(payload, closeCode) {
		t.Errorf("close echo payload: got %v, want %v (echoed verbatim)", payload, closeCode)
	}
	conn.Close()

	// The server process itself must still be alive and accepting new
	// connections after handling the close — a real, confirmed regression
	// risk given the persistent loop's own cleanup path.
	conn2, err := net.DialTimeout("tcp", "127.0.0.1:8978", 2*time.Second)
	if err != nil {
		t.Fatalf("server did not survive a client close frame: %v", err)
	}
	defer conn2.Close()
	wsHandshake(t, conn2, "/")
	wsSendText(t, conn2, "still alive")
	opcode, payload = wsRecvFrame(t, conn2)
	if opcode != 1 || string(payload) != "echo: still alive" {
		t.Errorf("post-close echo: got opcode=%d payload=%q", opcode, payload)
	}
}

// TestE2EWSPingPong verifies TDD-00039 Stage 2's automatic Pong reply: RFC
// 6455 §5.5.2 requires a Pong to carry identical application data to the
// Ping it answers, and the reply must never reach `.onmessage` (it's a
// control frame, not a text/binary one).
func TestE2EWSPingPong(t *testing.T) {
	src := `
interface Res { status: number; body: string }
http.listen(8979, (req: Request): Res => {
  return { status: 200, body: "not a websocket request" }
}, {
  ws: (socket: WSConnection) => {
    socket.onmessage = (ev) => {
      socket.send("got onmessage: " + ev.data)
    }
  }
})
`
	startHTTPServer(t, src, 8979)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:8979", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	wsHandshake(t, conn, "/")

	pingPayload := []byte("ping-data")
	wsSendFrame(t, conn, 0x9, pingPayload)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload := wsRecvFrame(t, conn)
	if opcode != 10 {
		t.Fatalf("ping reply: got opcode %d, want 10 (pong)", opcode)
	}
	if !bytes.Equal(payload, pingPayload) {
		t.Errorf("pong payload: got %q, want %q (must match the ping exactly)", payload, pingPayload)
	}

	// The ping must never have reached onmessage — a subsequent real text
	// message should still be the very next thing the connection produces.
	wsSendText(t, conn, "hello")
	opcode, payload = wsRecvFrame(t, conn)
	if opcode != 1 || string(payload) != "got onmessage: hello" {
		t.Errorf("post-ping message: got opcode=%d payload=%q, want opcode=1 payload=%q", opcode, payload, "got onmessage: hello")
	}
}

// TestE2EWSServerInitiatedClose verifies `socket.close()` (called from
// server-side code, not in response to a client Close frame) sends its own
// Close frame with status code 1000 (Normal Closure) before closing the
// connection (TDD-00039 Stage 2).
func TestE2EWSServerInitiatedClose(t *testing.T) {
	src := `
interface Res { status: number; body: string }
http.listen(8980, (req: Request): Res => {
  return { status: 200, body: "not a websocket request" }
}, {
  ws: (socket: WSConnection) => {
    socket.onmessage = (ev) => {
      if (ev.data === "bye") {
        socket.close()
      } else {
        socket.send("echo: " + ev.data)
      }
    }
  }
})
`
	startHTTPServer(t, src, 8980)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:8980", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	wsHandshake(t, conn, "/")

	wsSendText(t, conn, "bye")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload := wsRecvFrame(t, conn)
	if opcode != 8 {
		t.Fatalf("server-initiated close: got opcode %d, want 8 (close)", opcode)
	}
	wantCode := []byte{0x03, 0xE8} // 1000, Normal Closure
	if !bytes.Equal(payload, wantCode) {
		t.Errorf("close status code: got %v, want %v (1000, Normal Closure)", payload, wantCode)
	}
}

// runClientWithTimeout compiles and runs src, killing it and failing the
// test if it hasn't exited within timeout — a real, not just theoretical,
// concern for `new WebSocket(url)`: a bug in the client's synchronous
// connect/handshake or its event-loop scan step can hang the whole process
// indefinitely (found directly during this feature's own development, as a
// genuine infinite loop that briefly wedged a stale test binary), and
// exec.Command's plain .Output() has no timeout of its own to guard against
// that repeating.
func runClientWithTimeout(t *testing.T, src string, timeout time.Duration) string {
	t.Helper()
	binFile := buildBinary(t, src)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, binFile).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("client did not exit within %s (likely hung) — output so far:\n%s", timeout, out)
	}
	if err != nil {
		t.Fatalf("run: %v\noutput:\n%s", err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// TestE2EWebSocketClientAgainstServer is TDD-00039 Stage 3's own end-to-end
// check: a real `new WebSocket(url)` client (compiled and run as its own
// process, not a hand-rolled Go test client the way every earlier test in
// this file drives the server) against a real compiled server, covering
// the full round trip: synchronous connect + handshake, the deferred
// onopen notification (WebSocketClientType's own documented ordering
// requirement — onopen must fire only after the constructor call has
// already returned and user code has had a chance to assign it),
// .send()/.onmessage, and .close()/.onclose ending the process cleanly.
func TestE2EWebSocketClientAgainstServer(t *testing.T) {
	serverSrc := `
interface Res { status: number; body: string }
http.listen(8981, (req: Request): Res => {
  return { status: 200, body: "not a websocket request" }
}, {
  ws: (socket: WSConnection) => {
    socket.onmessage = (ev) => {
      socket.send("echo: " + ev.data)
    }
  }
})
`
	startHTTPServer(t, serverSrc, 8981)

	clientSrc := `
const ws = new WebSocket("ws://127.0.0.1:8981/")
console.log("readyState right after construction: " + ws.readyState)
ws.onopen = () => {
  console.log("onopen")
  ws.send("hello from client")
}
ws.onmessage = (ev) => {
  console.log("onmessage: " + ev.data)
  ws.close()
}
ws.onclose = () => {
  console.log("onclose")
  process.exit(0)
}
setTimeout(() => {
  console.log("SAFETY TIMEOUT — should not be reached")
  process.exit(1)
}, 4000)
`
	out := runClientWithTimeout(t, clientSrc, 8*time.Second)
	want := "readyState right after construction: 0\nonopen\nonmessage: echo: hello from client\nonclose"
	if out != want {
		t.Errorf("client output:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

// TestE2EWebSocketClientConnectionRefused verifies a failed connect (no
// server listening) never throws synchronously from `new WebSocket(url)`
// — matching real WebSocket, which always reports a network-level failure
// via onerror/onclose, never a thrown exception from the constructor
// itself — and that both fire, deferred to the first event-loop pass, the
// same as a successful onopen would.
func TestE2EWebSocketClientConnectionRefused(t *testing.T) {
	clientSrc := `
const ws = new WebSocket("ws://127.0.0.1:1/")
ws.onopen = () => {
  console.log("onopen (should not fire)")
}
ws.onerror = () => {
  console.log("onerror")
}
ws.onclose = () => {
  console.log("onclose")
  console.log("readyState: " + ws.readyState)
  process.exit(0)
}
setTimeout(() => {
  console.log("SAFETY TIMEOUT — should not be reached")
  process.exit(1)
}, 4000)
`
	out := runClientWithTimeout(t, clientSrc, 8*time.Second)
	want := "onerror\nonclose\nreadyState: 2"
	if out != want {
		t.Errorf("client output:\ngot:\n%s\nwant:\n%s", out, want)
	}
}
