package tests

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// reactor_windows_test.go — the Windows completion-port reactor's Stage 2
// acceptance tests (TDD-00183): every socket and every pipe() read end waits on
// the port, so an idle program makes no periodic wake-ups at all. The
// behavioural suites (net/http/tls/dgram/cluster/worker/child_process) prove
// the I/O still works; these prove *how* it waits, which no behavioural test
// can see — a reactor that polls every 10 ms passes all of those too.

// selectCalls runs bin under KML_IO_TRACE and returns how many blocking waits
// the reactor took (one "[io] wait" trace line each), plus the program's stdout.
// A reactor that waits on completions blocks a handful of times over an idle
// second; one that polls blocks once per slice — a hundred times.
func selectCalls(t *testing.T, bin string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "KML_IO_TRACE=1")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstderr tail:\n%s", err, tailLines(stderr.String(), 20))
	}
	return watchedSelects(stderr.String()), stdout.String()
}

// watchedSelects counts the reactor's blocking waits in a trace.
func watchedSelects(trace string) int {
	return strings.Count(trace, "[io] wait slice=")
}

// A parent idling on a child's stdout/stderr pipes: the read ends are armed
// with zero-byte overlapped reads, so the loop sleeps until the child writes or
// exits. The polled anonymous pipe this replaces re-entered select() every
// 10 ms — about 150 times over this child's lifetime.
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
	n, out := selectCalls(t, bin)
	if !strings.Contains(out, "closed 0") {
		t.Fatalf("child close not observed; stdout:\n%s", out)
	}
	if n > 12 {
		t.Errorf("reactor blocked %d separate times while idling on a child's pipes; a completion-driven wait needs a handful", n)
	}
}

// A listening server with a SIGINT handler installed, idle until a timer closes
// it. The listener waits on a posted AcceptEx and a signal wakes the port with
// a packet, so neither the 10 ms socket slice nor the 50 ms
// handler-installed slice exists any more.
func TestE2EReactorIdleServerDoesNotPoll(t *testing.T) {
	bin := buildBinaryImports(t, `
import net from 'net'
process.on('SIGINT', () => { console.log('sigint') })
const server = net.createServer((sock) => { sock.end() })
server.listen(0)
setTimeout(() => { server.close(); console.log('done'); process.exit(0) }, 1500)
`)
	n, out := selectCalls(t, bin)
	if !strings.Contains(out, "done") {
		t.Fatalf("server did not run to its timer; stdout:\n%s", out)
	}
	if n > 12 {
		t.Errorf("reactor blocked %d separate times while an idle listener waited 1.5 s; a completion-driven wait needs a handful", n)
	}
}

// Datagram read-interest is a zero-byte receive with MSG_PEEK: it must report
// each queued datagram without consuming any. (Without MSG_PEEK a zero-length
// receive swallows the datagram it completes on.) A burst arriving faster than
// the loop reads must be delivered whole and in order.
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
server.bind(%d)
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

// One loop, three kinds of socket at once: a listener (AcceptEx), its accepted
// connection (zero-read), and libcurl's upstream socket — a socket this layer
// does not own, watched by an AFD poll issued on the reactor's helper device. A
// handler that awaits a slow upstream parks on exactly that poll; the trace
// must show its completions arriving through the port (op kind 3 on a
// description the trace marks foreign) rather than the loop spinning or stalling.
func TestE2EReactorForeignSocketOnPort(t *testing.T) {
	upstream := newDelayedUpstreamServer(t, 700*time.Millisecond)
	np := freePort(t)
	src := subPort(fmt.Sprintf(`
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8974, async (req: HttpRequest): Promise<Res> => {
  const r: Response = await fetch("%s" + req.path)
  return { status: 200, body: await r.text() }
})
`, upstream.URL), 8974, np)
	bin := buildBinaryImports(t, src)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "KML_IO_TRACE=1")
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr := fmt.Sprintf("127.0.0.1:%d", np)
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never listened on %s", addr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	before := watchedSelects(stderr.String())
	body := httpGetBody(t, fmt.Sprintf("http://%s/slow", addr))
	if body != "upstream /slow" {
		t.Fatalf("body: got %q, want %q", body, "upstream /slow")
	}
	trace := stderr.String()
	foreign := false
	for _, line := range strings.Split(trace, "\n") {
		// Foreign-ness is the description's kind, reported by the trace — a libcurl
		// socket takes an ordinary pool fd, so its number says nothing.
		var fd, kind, live, isForeign int
		var st string
		if n, _ := fmt.Sscanf(line, "[io] port op fd=%d kind=%d live=%d st=%s foreign=%d", &fd, &kind, &live, &st, &isForeign); n == 5 && kind == 3 && isForeign == 1 {
			foreign = true
			break
		}
	}
	if !foreign {
		t.Errorf("no AFD poll completion for a foreign (libcurl) socket reached the port; trace tail:\n%s", tailLines(trace, 30))
	}
	if n := watchedSelects(trace) - before; n > 150 {
		t.Errorf("reactor blocked %d separate times across one 700 ms upstream wait; the foreign socket is being polled, not awaited", n)
	}
}

func httpGetBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
