package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

// TDD-00251: every runtime symbol a runtime object leaves undefined is
// defined by a runtime object (on Windows, the shim's included). A runtime
// file that names a symbol only the generated program defines links on
// Linux and macOS (their linkers resolve it against the program, or ignore
// it when unreached) and fails on Windows, whose COFF link resolves every
// object's references: what the C reads from or calls into the program goes
// through hooks.c (hookssrc/, runtime_hooks.go).
func TestRuntimeSymbolsResolveWithinRuntime(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	nm, err := exec.LookPath("llvm-nm")
	if err != nil {
		if nm, err = exec.LookPath("nm"); err != nil {
			t.Skip("neither llvm-nm nor nm on PATH")
		}
	}
	// One program that reaches every runtime file it can: the server and
	// client, timers, child processes, fs and fs.watch, zlib, sqlite, crypto,
	// BigInt, RegExp, Workers, dns, dgram, signals, streams.
	src := `
import http from 'node:http'
import net from 'node:net'
import dgram from 'node:dgram'
import dns from 'node:dns'
import fs from 'node:fs'
import zlib from 'node:zlib'
import crypto from 'node:crypto'
import { spawn, execSync } from 'node:child_process'
import { Worker, isMainThread } from 'node:worker_threads'
import { DatabaseSync } from 'node:sqlite'
import { Readable } from 'node:stream'
if (isMainThread) {
  const w = new Worker(__filename)
  w.on('exit', () => {})
}
process.on('SIGINT', () => {})
fs.watch('.', () => {}).close()
const db = new DatabaseSync(':memory:')
console.log(zlib.gzipSync('x').length, crypto.randomUUID().length, 2n ** 70n, /a/.test('a'), execSync('echo').length)
dns.lookup('localhost', () => {})
dgram.createSocket('udp4').close()
Readable.from(['a']).on('data', () => {})
spawn('echo').on('close', () => {})
const s = http.createServer((q, r) => r.end('ok'))
s.listen(0, '127.0.0.1', async () => {
  await fetch('http://127.0.0.1:' + (s.address() as any).port)
  net.connect((s.address() as any).port, '127.0.0.1').destroy()
  setTimeout(() => s.close(), 1)
})
`
	dir := tempDir(t)
	srcFile := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	prog, err := resolver.ResolveProgram(srcFile)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	em := llvm.NewEmitter()
	if _, err := em.EmitProgram(prog); err != nil {
		t.Fatalf("codegen: %v", err)
	}
	srcs, err := em.EmbeddedCSources()
	if err != nil {
		t.Fatalf("runtime sources: %v", err)
	}
	defined := map[string]bool{}
	undefinedBy := map[string][]string{}
	for _, cs := range srcs {
		obj, err := cs.CachedObject(filepath.Join(dir, "obj"), []string{"-O2"})
		if err != nil {
			continue // the C++/moc members: not cached, not checked
		}
		for _, sym := range nmSymbols(t, nm, obj, "--defined-only") {
			defined[sym] = true
		}
		for _, sym := range nmSymbols(t, nm, obj, "--undefined-only") {
			undefinedBy[sym] = append(undefinedBy[sym], cs.Name)
		}
	}
	// The Windows shim is linked into every Windows program.
	if runtime.GOOS == "windows" {
		shims, _ := filepath.Glob(filepath.Join("..", "codegen", "llvm", "win32src", "*.c"))
		for _, src := range shims {
			obj := filepath.Join(dir, strings.TrimSuffix(filepath.Base(src), ".c")+".o")
			if out, err := exec.Command("clang", "-c", "-O2", "-w", "-o", obj, src).CombinedOutput(); err != nil {
				t.Fatalf("compiling %s: %v\n%s", src, err, out)
			}
			for _, sym := range nmSymbols(t, nm, obj, "--defined-only") {
				defined[sym] = true
			}
		}
	}
	var missing []string
	for sym, by := range undefinedBy {
		if !strings.HasPrefix(sym, "__kml_") || defined[sym] {
			continue
		}
		sort.Strings(by)
		missing = append(missing, sym+"  (named by "+strings.Join(by, ", ")+")")
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("runtime objects name symbols no runtime object defines (TDD-00251): define them in the runtime, or register them in hooks.c:\n%s",
			strings.Join(missing, "\n"))
	}
}

var nmSymbolRE = regexp.MustCompile(`(?m)^\s*(?:[0-9a-fA-F]+\s+)?[A-Za-z]\s+(\S+)\s*$`)

// nmSymbols lists an object's symbols of one kind (llvm-nm's flag).
func nmSymbols(t *testing.T, nm, obj, which string) []string {
	t.Helper()
	out, err := exec.Command(nm, which, obj).Output()
	if err != nil {
		t.Fatalf("llvm-nm %s %s: %v", which, obj, err)
	}
	var syms []string
	for _, m := range nmSymbolRE.FindAllStringSubmatch(string(out), -1) {
		sym := m[1]
		// Mach-O prefixes every C symbol with an underscore: `___kml_x` is
		// C's `__kml_x`.
		if runtime.GOOS == "darwin" {
			sym = strings.TrimPrefix(sym, "_")
		}
		syms = append(syms, sym)
	}
	return syms
}
