package tests

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
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
	port := startUDPServer(t, src, 8961)

	conn, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
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

// socket.setBroadcast(flag) is a real setsockopt(SO_BROADCAST) on the bound
// socket; before the bind there is no socket yet (EBADF), as in Node.
func TestE2EDgramSetBroadcast(t *testing.T) {
	assertOutputImports(t, `
import dgram from 'dgram'
const s = dgram.createSocket('udp4')
try { s.setBroadcast(true) } catch (e: any) { console.log(e.code, e.syscall) }
s.bind(0, () => {
  s.setBroadcast(true)
  const a = s.address()
  console.log(a.family, a.port > 0)
  s.setBroadcast(false)
  s.close()
})
`, "EBADF setBroadcast\nIPv4 true")
}

// Node's lib/dgram.js in TypeScript: 'listening', a send before the bind
// completes (queued), the offset/length and list forms, connect and
// disconnect, remoteAddress, the error codes, and 'close'.
func TestE2EDgramSendFormsAndConnect(t *testing.T) {
	assertOutputImports(t, `
import dgram from 'dgram'
const server = dgram.createSocket('udp4')
server.on('listening', () => {
  const a = server.address()
  console.log('listening', a.address, a.family)
  const client = dgram.createSocket({ type: 'udp4' })
  client.send('hello', a.port, '127.0.0.1', (err, bytes) => {
    console.log('sent', err, bytes)
    client.send(Buffer.from('xxabcxx'), 2, 3, a.port, '127.0.0.1', () => {
      client.connect(a.port, '127.0.0.1', () => {
        console.log('connected', client.remoteAddress().port === a.port)
        client.send(['multi', '-part'])
        try { client.send('x', a.port) } catch (e: any) { console.log('err', e.code) }
        client.disconnect()
        try { client.remoteAddress() } catch (e: any) { console.log('err2', e.code, e.message) }
        setTimeout(() => { client.close(() => console.log('client closed')) }, 20)
      })
    })
  })
})
let n = 0
server.on('message', (msg, rinfo) => {
  console.log('message', msg.toString(), rinfo.address, rinfo.family, rinfo.size)
  if (++n === 3) server.close(() => console.log('server closed'))
})
server.bind(0, '127.0.0.1')
try { dgram.createSocket('udp5' as any) } catch (e: any) { console.log(e.code, e.message) }
const s2 = dgram.createSocket('udp4')
s2.bind(0)
try { s2.bind(0) } catch (e: any) { console.log(e.code) }
s2.close()
try { s2.close() } catch (e: any) { console.log(e.code, e.message) }
`, "ERR_SOCKET_BAD_TYPE Bad socket type specified. Valid types are: udp4, udp6\nERR_SOCKET_ALREADY_BOUND\nERR_SOCKET_DGRAM_NOT_RUNNING Not running\nlistening 127.0.0.1 IPv4\nsent null 5\nmessage hello 127.0.0.1 IPv4 5\nconnected true\nerr2 ERR_SOCKET_DGRAM_NOT_CONNECTED Not connected\nmessage abc 127.0.0.1 IPv4 3\nmessage multi-part 127.0.0.1 IPv4 10\nserver closed\nclient closed")
}

// udp6, the TTL/multicast options, membership, and a bind conflict.
func TestE2EDgramUdp6AndOptions(t *testing.T) {
	assertOutputImports(t, `
import dgram from 'dgram'
const s6 = dgram.createSocket('udp6')
s6.on('message', (msg, rinfo) => { console.log('v6', msg.toString(), rinfo.family, rinfo.address); s6.close() })
s6.bind(0, '::1', () => {
  const a = s6.address()
  console.log('v6 bound', a.family, a.address)
  const c = dgram.createSocket('udp6')
  c.send('six', a.port, '::1', () => c.close())
})
const s = dgram.createSocket('udp4')
s.bind(0, () => {
  console.log('ttl', s.setTTL(64), s.setMulticastTTL(2), s.setMulticastLoopback(true))
  s.addMembership('239.1.2.3')
  s.dropMembership('239.1.2.3')
  try { s.addMembership('') } catch (e: any) { console.log(e.code) }
  const port = s.address().port
  const t2 = dgram.createSocket('udp4')
  t2.on('error', (e: any) => { console.log('bind error', e.code, e.syscall); t2.close(); s.close() })
  t2.bind(port)
})
`, "v6 bound IPv6 ::1\nttl 64 2 true\nERR_MISSING_ARGS\nbind error EADDRINUSE bind\nv6 six IPv6 ::1")
}

// startUDPServer compiles a dgram server and waits until it responds to a
// probe datagram before returning; killed via t.Cleanup since it never exits.
func startUDPServer(t *testing.T, src string, port int) int {
	t.Helper()
	np := freeUDPPort(t)
	binFile := buildBinaryImports(t, subPort(src, port, np))
	cmd := exec.Command(binFile)
	// Kept for the failure message: a server that never answers is otherwise
	// opaque (run with KML_IO_TRACE=1 in the environment to get the Windows
	// reactor's trace here too).
	serverOut := &syncBuffer{}
	cmd.Stdout = serverOut
	cmd.Stderr = serverOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	addr := fmt.Sprintf("127.0.0.1:%d", np)
	deadline := time.Now().Add(15 * time.Second)
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
			return np
		}
		conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("dgram server never responded on %s; server output:\n%s", addr, tailLines(serverOut.String(), 40))
	return -1
}

// tailLines returns the last n lines of s (for failure messages).
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// freeUDPPort asks the OS for a free *UDP* port. freePort probes TCP, and a port
// free for TCP can still be taken for UDP (a resolver, a browser's QUIC socket) —
// the server's bind then fails and it never answers a probe.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a UDP port: %v", err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}
