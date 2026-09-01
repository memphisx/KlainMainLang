package tests

import (
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"
)

// --- Node `dgram`: UDP sockets (ADR-00327) ---
//
// dgram.createSocket('udp4') + bind/on('message')/send/close. Each socket's fd
// folds into the same select() event loop; recvfrom fires 'message' with a
// Buffer and an rinfo { address, port }. The Go test drives the compiled server
// as a UDP client, the http_test.go posture.

// TestE2EDgramEchoServer: a UDP server that echoes each datagram back to its
// sender (via rinfo.port/address), proving receive, rinfo, and send.
func TestE2EDgramEchoServer(t *testing.T) {
	src := `
import dgram from 'dgram'
const dec = new TextDecoder()
const server = dgram.createSocket('udp4')
server.on('message', (msg: Uint8Array, rinfo) => {
  server.send("echo:" + dec.decode(msg), rinfo.port, rinfo.address)
})
server.bind(8961)
`
	startUDPServer(t, src, 8961)

	conn, err := net.DialTimeout("udp", "127.0.0.1:8961", 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got, want := string(buf[:n]), "echo:ping"; got != want {
		t.Errorf("echo: got %q, want %q", got, want)
	}
}

// ADR-00581: socket.setBroadcast(flag) is a real setsockopt(SO_BROADCAST); the
// socket stays usable and .address() still reports the bound ephemeral port.
func TestE2EDgramSetBroadcast(t *testing.T) {
	assertOutputImports(t, `
import dgram from 'dgram'
const s = dgram.createSocket('udp4')
s.setBroadcast(true)
s.bind(0)
const a = s.address()
console.log(a.family, a.port > 0)
s.setBroadcast(false)
s.close()
`, "IPv4 true")
}

// startUDPServer compiles a dgram server and waits until it responds to a
// probe datagram before returning; killed via t.Cleanup since it never exits.
func startUDPServer(t *testing.T, src string, port int) {
	t.Helper()
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("udp", addr, 100*time.Millisecond)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		conn.Write([]byte("probe"))
		conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		buf := make([]byte, 64)
		if _, err := conn.Read(buf); err == nil {
			conn.Close()
			return
		}
		conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("dgram server never responded on %s", addr)
}
