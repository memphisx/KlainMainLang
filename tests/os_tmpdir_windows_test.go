package tests

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// ADR-00739: os.tmpdir() on Windows follows Node's exact algorithm — TEMP,
// then TMP, then <SystemRoot|windir>\temp, with one trailing backslash
// stripped unless it names a drive root (lib/os.js).
func TestE2EOsTmpdirStripsTrailingBackslashOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows tmpdir semantics")
	}
	bin := buildBinaryImports(t, `
import os from 'os'
console.log(os.tmpdir())
`)
	run := func(temp string) string {
		env := []string{}
		for _, kv := range os.Environ() {
			u := strings.ToUpper(kv)
			if strings.HasPrefix(u, "TEMP=") || strings.HasPrefix(u, "TMP=") {
				continue
			}
			env = append(env, kv)
		}
		cmd := exec.Command(bin)
		cmd.Env = append(env, "TEMP="+temp)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("run with TEMP=%q: %v", temp, err)
		}
		return strings.TrimRight(string(out), "\r\n")
	}
	if got := run(`C:\KmlTmpTest\`); got != `C:\KmlTmpTest` {
		t.Fatalf("trailing backslash not stripped: got %q", got)
	}
	if got := run(`C:\`); got != `C:\` {
		t.Fatalf("drive root must keep its backslash: got %q", got)
	}
	if got := run(`C:\KmlTmpTest`); got != `C:\KmlTmpTest` {
		t.Fatalf("plain path changed: got %q", got)
	}
}
