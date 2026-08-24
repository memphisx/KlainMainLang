package tests

import (
	"bufio"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// --- Node `net`: TCP server (ADR-00324) ---
//
// net.createServer(socket => ...) + server.listen(port, cb), with connection
// sockets exposing .on('data'|'end'), .write, .end. The listen fd and every
// accepted connection fd fold into the same select() event loop as the
// child_process read pipes; the Go test drives the compiled server as a TCP
// client, the same posture as http_test.go's startHTTPServer.

// TestE2ENetEchoServer: a server that echoes every chunk back. Verifies the
// connection listener fires, socket.on('data') delivers a Buffer, and
// socket.write sends it back over the accepted connection.
func TestE2ENetEchoServer(t *testing.T) {
	src := `
import net from 'net'
const dec = new TextDecoder()
const server = net.createServer((socket) => {
  socket.on('data', (chunk: Uint8Array) => {
    socket.write("echo:" + dec.decode(chunk))
  })
})
server.listen(8951, () => {})
`
	startHTTPServer(t, src, 8951)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:8951", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
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

// TestE2ENetMultipleConnections: each connection gets its own socket with its
// own 'data' listener, proving the per-connection registry keeps sockets
// independent rather than sharing one handler/fd.
func TestE2ENetMultipleConnections(t *testing.T) {
	src := `
import net from 'net'
const dec = new TextDecoder()
const server = net.createServer((socket) => {
  socket.on('data', (chunk: Uint8Array) => {
    socket.write(dec.decode(chunk).toUpperCase())
  })
})
server.listen(8952, () => {})
`
	startHTTPServer(t, src, 8952)

	for i, msg := range []string{"one", "two", "three"} {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:8952", 2*time.Second)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		if _, err := conn.Write([]byte(msg)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		r := bufio.NewReader(conn)
		buf := make([]byte, 64)
		n, _ := r.Read(buf)
		want := map[string]string{"one": "ONE", "two": "TWO", "three": "THREE"}[msg]
		if got := string(buf[:n]); got != want {
			t.Errorf("conn %d: got %q, want %q", i, got, want)
		}
		conn.Close()
	}
}

// TestE2ENetServerOnConnection: the connection listener registered via
// server.on('connection', ...) (rather than the createServer argument) is
// equivalent — same stored closure header, same dispatch.
func TestE2ENetServerOnConnection(t *testing.T) {
	src := `
import net from 'net'
const server = net.createServer()
server.on('connection', (socket) => {
  socket.on('data', (chunk: Uint8Array) => {
    socket.write("ok")
  })
})
server.listen(8953, () => {})
`
	startHTTPServer(t, src, 8953)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:8953", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "go")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 8)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(buf[:n]); got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
}

// --- net.connect (TCP client, ADR-00328) ---
//
// A blocking-connect client reusing the same socket machinery as the server.
// Go stands up a TCP echo server; the compiled client connects, writes via the
// connect callback (which receives the socket), and prints the echo.

func TestE2ENetConnectClient(t *testing.T) {
	// Go-side TCP echo server.
	ln, err := net.Listen("tcp", "127.0.0.1:8971")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 128)
		n, _ := conn.Read(buf)
		conn.Write([]byte("echo:" + string(buf[:n])))
	}()

	// The connect callback and 'data' listener both close over the `const sock`
	// binding — exercising the fixed capture-in-own-initializer path (ADR-00330).
	src := `
import net from 'net'
const dec = new TextDecoder()
const sock = net.connect(8971, "127.0.0.1", () => {
  sock.write("hello")
})
sock.on('data', (chunk: Uint8Array) => {
  console.log("got:", dec.decode(chunk))
  sock.end()
})
`
	bin := buildBinaryImports(t, src)
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run client: %v\noutput: %s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), "got: echo:hello"; got != want {
		t.Errorf("client output: got %q, want %q", got, want)
	}
}

// A connect to a closed port throws a catchable Error (V1: failure throws
// rather than emitting an async 'error' event).
func TestE2ENetConnectFailureThrows(t *testing.T) {
	assertOutputImports(t, `
import net from 'net'
try {
  net.connect(9, "127.0.0.1")   // port 9 (discard) refused on loopback here
  console.log("no throw")
} catch (e) {
  console.log("caught")
}
`, "caught")
}
