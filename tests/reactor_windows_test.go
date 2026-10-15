package tests

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// reactor_windows_test.go — how the Windows event loop waits (TDD-00252): it
// is libuv's, which blocks in its completion port until a socket, pipe,
// timer or signal has something, so an idle program takes no periodic
// wake-ups. The behavioural suites (net/http/tls/dgram/cluster/worker/
// child_process) prove the I/O works; these prove the loop does not spin or
// poll while it waits, which no behavioural test can see — a loop that polls
// every millisecond passes all of those too.

// idleCPU runs bin and returns the processor time it used (user + kernel)
// and its stdout.
func idleCPU(t *testing.T, bin string) (time.Duration, string) {
	t.Helper()
	cmd := exec.Command(bin)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstderr tail:\n%s", err, tailLines(stderr.String(), 20))
	}
	return cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime(), stdout.String()
}

// idleCPUBudget is the processor time a program that waits 1.5 s may use:
// its start-up and exit, with room for a loaded machine. A loop that spins
// uses the whole 1.5 s; one that polls every millisecond, several hundred
// milliseconds.
const idleCPUBudget = 250 * time.Millisecond

// A parent idling on a child's stdout/stderr pipes until the child exits.
func TestE2EReactorIdleChildPipesDoNotPoll(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep on PATH")
	}
	bin := buildBinaryImports(t, `
import { spawn } from 'node:child_process'
const c = spawn('sleep', ['1.5'])
c.stdout.on('data', (d: string) => { console.log('out ' + d) })
c.on('close', (code: number) => { console.log('closed ' + code) })
`)
	cpu, out := idleCPU(t, bin)
	if !strings.Contains(out, "closed 0") {
		t.Fatalf("child close not observed; stdout:\n%s", out)
	}
	if cpu > idleCPUBudget {
		t.Errorf("the loop used %v of processor time while idling 1.5 s on a child's pipes; a blocking wait needs a fraction of that", cpu)
	}
}

// A listening server with a SIGINT handler installed, idle until a timer
// closes it.
func TestE2EReactorIdleServerDoesNotPoll(t *testing.T) {
	bin := buildBinaryImports(t, `
import net from 'net'
process.on('SIGINT', () => { console.log('sigint') })
const server = net.createServer((sock) => { sock.end() })
server.listen(0, '127.0.0.1')
setTimeout(() => { server.close(); console.log('done'); process.exit(0) }, 1500)
`)
	cpu, out := idleCPU(t, bin)
	if !strings.Contains(out, "done") {
		t.Fatalf("server did not run to its timer; stdout:\n%s", out)
	}
	if cpu > idleCPUBudget {
		t.Errorf("the loop used %v of processor time while an idle listener waited 1.5 s; a blocking wait needs a fraction of that", cpu)
	}
}

// A burst of datagrams arriving faster than the loop reads must be delivered
// whole and in order.
func TestE2EReactorUDPBurstLosesNothing(t *testing.T) {
	const port = 8973
	const count = 40
	src := fmt.Sprintf(`
import dgram from 'dgram'
const dec = new TextDecoder()
const server = dgram.createSocket('udp4')
let got = ""
let n = 0
server.on('message', (msg: Uint8Array, rinfo) => {
  const text = dec.decode(msg)
  if (text === "probe") { server.send("ready", rinfo.port, rinfo.address); return }
  got = got + text + ","
  n = n + 1
  if (n === %d) { server.send(got, rinfo.port, rinfo.address) }
})
server.bind(%d, '127.0.0.1')
`, count, port)
	p := startUDPServer(t, src, port)
	conn, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", p), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	want := ""
	for i := 0; i < count; i++ {
		if _, err := conn.Write([]byte(fmt.Sprintf("d%d", i))); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		want += fmt.Sprintf("d%d,", i)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	k, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no summary datagram came back (a datagram was lost, so the count never reached %d): %v", count, err)
	}
	if got := string(buf[:k]); got != want {
		t.Errorf("burst delivered out of order or incomplete:\n got %q\nwant %q", got, want)
	}
}
