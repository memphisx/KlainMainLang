package tests

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildPipeEchoServer compiles a one-shot named-pipe server: it creates the
// pipe, prints "ready", accepts one client, answers "echo:<what it read>" and
// then a second message "bye", and closes — so the client sees data, more data,
// then end-of-stream.
func buildPipeEchoServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "pipesrv.c")
	writeFile(t, src, `#include <windows.h>
#include <stdio.h>
#include <string.h>
int main(int argc, char **argv) {
	HANDLE p = CreateNamedPipeA(argv[1], PIPE_ACCESS_DUPLEX, PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT,
	                            1, 65536, 65536, 0, NULL);
	if (p == INVALID_HANDLE_VALUE) { printf("create failed %lu\n", GetLastError()); return 1; }
	printf("ready\n"); fflush(stdout);
	if (!ConnectNamedPipe(p, NULL) && GetLastError() != ERROR_PIPE_CONNECTED) return 2;
	char buf[256]; DWORD n = 0, w = 0;
	if (!ReadFile(p, buf, sizeof buf - 1, &n, NULL)) return 3;
	buf[n] = 0;
	char out[300];
	int on = snprintf(out, sizeof out, "echo:%s", buf);
	WriteFile(p, out, (DWORD)on, &w, NULL);
	Sleep(150);
	WriteFile(p, "bye", 3, &w, NULL);
	/* wait for the client's end of stream, then report what else it sent */
	char tail[64]; DWORD tn = 0;
	if (ReadFile(p, tail, sizeof tail - 1, &tn, NULL) && tn) { tail[tn] = 0; printf("tail:%s\n", tail); }
	FlushFileBuffers(p);
	CloseHandle(p);
	printf("closed\n");
	return 0;
}
`)
	exe := filepath.Join(dir, "pipesrv.exe")
	if out, err := exec.Command("clang", "-O1", "-o", exe, src).CombinedOutput(); err != nil {
		t.Fatalf("clang: %v\n%s", err, out)
	}
	return exe
}

// net.connect({ path }) with a path in the pipe namespace is a Windows named
// pipe, as in Node — the transport every local Windows service listens on —
// not a Unix-domain socket. The stream is duplex, 'data' arrives per message,
// and the server closing its end is the client's 'end'/'close'.
func TestE2EWinNetConnectNamedPipe(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	srv := buildPipeEchoServer(t)
	name := fmt.Sprintf(`\\.\pipe\kml-test-%d`, time.Now().UnixNano())
	cmd := exec.Command(srv, name)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	rd := bufio.NewReader(stdout)
	if line, _ := rd.ReadString('\n'); strings.TrimSpace(line) != "ready" {
		t.Fatalf("pipe server did not come up: %q", line)
	}

	src := fmt.Sprintf(`
import net from 'net'
const dec = new TextDecoder()
const sock = net.connect({ path: %q }, () => {
  console.log("connected")
  sock.write("hello")
})
let n = 0
sock.on('data', (chunk: Uint8Array) => {
  n = n + 1
  console.log("data" + n + ":", dec.decode(chunk))
  if (n === 2) sock.write("thanks")
})
sock.on('end', () => { console.log("end") })
sock.on('close', () => { console.log("close") })
sock.on('error', (e) => { console.log("error", (e as any).code) })
`, name)
	bin := buildBinaryImports(t, src)
	run := exec.Command(bin)
	var buf bytes.Buffer
	run.Stdout, run.Stderr = &buf, &buf
	if os.Getenv("KML_TEST_IO_TRACE") != "" {
		run.Env = append(os.Environ(), "KML_IO_TRACE=1")
	}
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run.Wait() }()
	select {
	case rerr := <-done:
		if rerr != nil {
			t.Fatalf("run client: %v\noutput: %s", rerr, buf.String())
		}
	case <-time.After(30 * time.Second):
		run.Process.Kill()
		<-done
		t.Fatalf("client hung; output so far:\n%s", buf.String())
	}
	out := buf.Bytes()
	want := "connected\ndata1: echo:hello\ndata2: bye\nend\nclose"
	if got := strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")); got != want {
		t.Errorf("client output:\ngot  %q\nwant %q", got, want)
	}
	rest, _ := rd.ReadString(0)
	if !strings.Contains(rest, "tail:thanks") {
		t.Errorf("server never received the client's second write; server said %q", rest)
	}
}

// A pipe nobody serves is ENOENT through the socket's 'error' event, the same
// shape a refused TCP connect has.
func TestE2EWinNetConnectNamedPipeMissing(t *testing.T) {
	assertOutputImports(t, `
import net from 'net'
const sock = net.connect({ path: "\\\\.\\pipe\\kml-definitely-not-there-xyz" }, () => { console.log("connected?!") })
sock.on('error', (e) => { console.log("error", (e as any).code, (e as any).syscall) })
sock.on('close', () => { console.log("close") })
`, "error ENOENT connect\nclose")
}
