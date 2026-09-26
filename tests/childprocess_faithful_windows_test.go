//go:build windows

package tests

import (
	"KlainMainLang/codegen/llvm"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The child_process / process rows of the Windows faithfulness audit
// (TDD-00180 §3/§6) that only a real Windows host can observe.

// buildEnvDumper compiles a tiny native program that prints its environment
// block in the order the OS hands it over, so a test can see what the parent
// actually built (cmd's `set` re-sorts, which would hide the point).
func buildEnvDumper(t *testing.T, dir, name string) string {
	t.Helper()
	src := filepath.Join(dir, name+".c")
	writeFile(t, src, `#include <windows.h>
#include <stdio.h>
int main(int argc, char **argv) {
	if (argc > 1) { printf("%s\n", argv[1]); return 0; }
	wchar_t *b = GetEnvironmentStringsW();
	for (wchar_t *q = b; *q; q += wcslen(q) + 1) {
		if (q[0] == L'=') continue; /* hidden per-drive variables */
		char u[4096];
		WideCharToMultiByte(CP_UTF8, 0, q, -1, u, sizeof u, NULL, NULL);
		char *eq = strchr(u, '=');
		if (eq) *eq = 0;
		printf("%s\n", u);
	}
	return 0;
}
`)
	exe := filepath.Join(dir, name+".exe")
	if out, err := exec.Command("clang", "-O1", "-o", exe, src).CombinedOutput(); err != nil {
		t.Fatalf("clang: %v\n%s", err, llvm.AnnotateClangOutput(out))
	}
	return exe
}

// A custom `env` replaces the child's environment, but the block the child
// receives is sorted case-insensitively, de-duplicated (first spelling wins),
// and still carries the variables Windows programs cannot start without.
func TestE2EWinSpawnEnvBlockSortedDedupedWithRequired(t *testing.T) {
	dir := tempDir(t)
	exe := filepath.ToSlash(buildEnvDumper(t, dir, "envdump"))
	out := compileAndRunImports(t, `
import { spawn } from 'child_process'
const c = spawn("`+exe+`", [], { env: { zeta: "1", Alpha: "2", ALPHA: "3", mid: "4" } })
const dec = new TextDecoder()
let buf = ""
c.stdout.on('data', (d: Uint8Array) => { buf = buf + dec.decode(d) })
c.on('close', () => { console.log(buf.split(/\r?\n/).filter((l) => l.length > 0).join(",")) })
`)
	names := strings.Split(strings.TrimSpace(out), ",")
	idx := map[string]int{}
	for i, n := range names {
		idx[n] = i
	}
	if _, ok := idx["ALPHA"]; ok {
		t.Errorf("duplicate key ALPHA survived (first spelling should win): %v", names)
	}
	for _, want := range []string{"Alpha", "mid", "zeta", "SystemRoot", "PATH", "TEMP", "USERPROFILE"} {
		found := false
		for _, n := range names {
			if strings.EqualFold(n, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("child environment lacks %s: %v", want, names)
		}
	}
	for i := 1; i < len(names); i++ {
		if strings.ToUpper(names[i-1]) > strings.ToUpper(names[i]) {
			t.Errorf("block not sorted at %q > %q: %v", names[i-1], names[i], names)
			break
		}
	}
}

// The program is looked up along the CHILD's PATH: a custom env whose PATH
// names a private directory finds a program that the parent's PATH does not
// contain, by bare name and without the .exe extension.
func TestE2EWinSpawnSearchesChildPath(t *testing.T) {
	dir := tempDir(t)
	bin := filepath.Join(dir, "privatebin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	buildEnvDumper(t, bin, "kmlonlyhere")
	assertOutputImports(t, `
import { spawn } from 'child_process'
const c = spawn("kmlonlyhere", ["found-it"], { env: { PATH: "`+filepath.ToSlash(bin)+`" } })
const dec = new TextDecoder()
let buf = ""
c.stdout.on('data', (d: Uint8Array) => { buf = buf + dec.decode(d) })
c.on('error', (e) => { console.log("error", (e as any).code) })
c.on('close', () => { console.log(buf.trim()) })
const miss = spawn("kmlonlyhere", ["x"])
miss.on('error', (e) => { console.log("parent-path", (e as any).code) })
`, "parent-path ENOENT\nfound-it")
}

// process.kill with a signal that has no Windows meaning is ENOSYS (libuv's
// uv_kill), not a silent TerminateProcess of the target.
func TestE2EWinKillUnsupportedSignalIsENOSYS(t *testing.T) {
	assertOutputImports(t, `
import { spawn } from 'child_process'
const c = spawn("cmd.exe", ["/d", "/c", "ping -n 30 127.0.0.1 >NUL"])
try {
  process.kill(c.pid, 'SIGHUP')
  console.log("no throw")
} catch (e: any) {
  console.log(e.code)
}
c.on('exit', (code: number | null, sig: string | null) => { console.log("exit " + code + " " + sig) })
c.kill('SIGTERM')
`, "ENOSYS\nexit null SIGTERM")
}

// os.homedir() without USERPROFILE falls back to the token's profile directory
// instead of throwing.
func TestE2EWinHomedirWithoutUserProfile(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory known to the test process")
	}
	t.Setenv("USERPROFILE", "")
	os.Unsetenv("USERPROFILE")
	assertOutputImports(t, `
import os from 'os'
console.log(os.homedir().toLowerCase())
`, strings.ToLower(home))
}
