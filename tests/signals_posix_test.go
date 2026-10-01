//go:build !windows

package tests

import (
	"os/exec"
	"syscall"
	"testing"
)

// TestE2ESignalAnyNameOnceAndRemoval: as in Node, any signal is an event
// name (a dynamic one too), a once listener fires one time, and removing a
// signal's last listener restores its default disposition — SIGUSR2 then
// terminates the process. The program signals itself (process.kill).
func TestE2ESignalAnyNameOnceAndRemoval(t *testing.T) {
	skipSignalDeliveryOnWindows(t)
	bin := buildBinary(t, `
const name: string = "SIGHUP";
process.once(name, (sig) => { console.log("once " + sig + " " + process.listenerCount(name)); });
function onUsr2(sig: string): void {
  console.log("usr2 " + sig);
  process.off('SIGUSR2', onUsr2);
}
process.on('SIGUSR2', onUsr2);
setTimeout(() => process.kill(process.pid, 'SIGHUP'), 10);
setTimeout(() => process.kill(process.pid, 'SIGUSR2'), 60);
setTimeout(() => { console.log("default next"); process.kill(process.pid, 'SIGUSR2'); }, 120);
setTimeout(() => console.log("unreachable"), 2000);
`)
	cmd := exec.Command(bin)
	out := &syncBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	err := cmd.Wait()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("want termination by SIGUSR2, got %v; output:\n%s", err, out.String())
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGUSR2 {
		t.Fatalf("want termination by SIGUSR2, got %v; output:\n%s", err, out.String())
	}
	if got, want := out.String(), "once SIGHUP 0\nusr2 SIGUSR2\ndefault next\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
