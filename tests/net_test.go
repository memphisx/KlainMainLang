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

// TestE2ENetStringChunkEcho: a 'data' listener that declares (chunk: string)
// and concatenates it must see exactly the received bytes. KML strings are
// NUL-terminated (concat goes through strlen), so the chunk buffer the dispatch
// hands the listener must be NUL-terminated — otherwise strlen runs past the
// read length into trailing heap garbage (ADR-00358: the intermittent
// "echo:helloV" TLS-server failure). The Uint8Array/TextDecoder path above
// carries an explicit length and never exercised this.
func TestE2ENetStringChunkEcho(t *testing.T) {
	src := `
import net from 'net'
const server = net.createServer((socket) => {
  socket.on('data', (chunk: string) => {
    socket.write("echo:" + chunk)
  })
})
server.listen(8956, () => {})
`
	startHTTPServer(t, src, 8956)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:8956", 2*time.Second)
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

// TDD-00131 (net.Server): server.address() reports the actual bound port,
// making the `listen(0)` ephemeral-port idiom usable — bind picks the port,
// address() reads it back via getsockname, and a self-connect round-trips.
func TestE2ENetServerAddressEphemeralPort(t *testing.T) {
	assertOutputImports(t, `
import net from 'net'
const dec = new TextDecoder()
const server = net.createServer((socket) => {
  socket.on('data', (chunk: Uint8Array) => { socket.write("pong") })
})
server.listen(0, () => {
  const addr = server.address()
  console.log(addr.family, addr.address, addr.port > 0)
  const client = net.connect(addr.port, "127.0.0.1", () => { client.write("ping") })
  client.on('data', (chunk: Uint8Array) => {
    console.log("roundtrip", dec.decode(chunk))
    client.end()
    server.close()
  })
})
`, "IPv4 0.0.0.0 true\nroundtrip pong")
}

// TDD-00131 (net.Server): socket.address() reports the socket's real local
// address+port, and server.on('listening') fires when the server binds.
func TestE2ENetSocketAddressAndListening(t *testing.T) {
	assertOutputImports(t, `
import net from 'net'
const dec = new TextDecoder()
const server = net.createServer((sock) => {
  sock.on('data', (c: Uint8Array) => { sock.write("pong") })
})
server.on('listening', () => { console.log("listening") })
server.listen(0, () => {
  const client = net.connect(server.address().port, "127.0.0.1", () => {
    const ca = client.address()
    console.log("local", ca.address, ca.family, ca.port > 0)
    client.write("ping")
  })
  client.on('data', (c: Uint8Array) => {
    console.log("got", dec.decode(c))
    client.end()
    server.close()
  })
})
`, "listening\nlocal 127.0.0.1 IPv4 true\ngot pong")
}

// TDD-00131 (net completion): net.isIP / isIPv4 / isIPv6 address-family checks.
func TestE2ENetIsIP(t *testing.T) {
	assertOutputImports(t, `
import net from 'net'
console.log(net.isIP("1.2.3.4"), net.isIP("::1"), net.isIP("nope"));
console.log(net.isIPv4("10.0.0.1"), net.isIPv4("::1"));
console.log(net.isIPv6("::1"), net.isIPv6("1.2.3.4"));
`, "4 6 0\ntrue false\ntrue false")
}

// net.connect options-object form { port, host } + socket setNoDelay/destroy.
func TestE2ENetConnectOptionsObject(t *testing.T) {
	assertOutputImports(t, `
import net from 'net'
const dec = new TextDecoder()
const server = net.createServer((sock) => {
  sock.setNoDelay(true)
  sock.on('data', (c: Uint8Array) => { sock.write("ok") })
})
server.listen(0, () => {
  const client = net.connect({ port: server.address().port, host: "127.0.0.1" }, () => {
    client.setKeepAlive()
    client.write("go")
  })
  client.on('data', (c: Uint8Array) => {
    console.log(dec.decode(c))
    client.destroy()
    server.close()
  })
})
`, "ok")
}

func TestE2ENetConnectPortOnly(t *testing.T) {
	// `net.connect(port)` — host defaults to localhost; the shape Node code
	// uses with server.address().port. Full echo round trip against an
	// in-process net server, closing cleanly from the data handler.
	assertOutputImports(t, `
import net from 'net'
const server = net.createServer((socket) => {
  socket.on('data', (c: string) => { socket.write("e:" + c); socket.end() })
})
server.listen(0, () => {
  const sock = net.connect(server.address().port)
  sock.on('data', (c: string) => { console.log("got", c); server.close() })
  sock.write("hi")
})
`, "got e:hi")
}

func TestE2EDgramSocketAddress(t *testing.T) {
	// dgram socket.address() — getsockname on the bound fd, the bind(0)
	// ephemeral-port idiom.
	assertOutputImports(t, `
import dgram from 'dgram'
const sock = dgram.createSocket('udp4')
sock.bind(0, () => {
  const port: number = sock.address().port
  if (port > 0) { console.log("dgram port ok") }
  sock.close()
})
`, "dgram port ok")
}
