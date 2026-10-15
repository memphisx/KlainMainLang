package tests

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
)

// staticBuild flips the compiler into --static mode for a single test and resets
// it after. Self-containment (no bundled runtime DLLs) is a property of a
// --static build; the default build is dynamic. E2E tests do not run in parallel,
// so toggling this package setting per test is safe.
func staticBuild(t *testing.T) {
	t.Helper()
	llvm.SetStaticLink(true)
	t.Cleanup(func() { llvm.SetStaticLink(false) })
}

// ADR-00771: a klain:tui program (Yoga C++ + the pcre2 RegExp engine) must
// compile to a single self-contained .exe on Windows — its PE import table
// carries only stock Windows DLLs, never the mingw C++ runtime
// (libstdc++-6.dll / libgcc_s_seh-1.dll / libwinpthread-1.dll) or
// libpcre2-8-0.dll, none of which exist on a stock box. Before the g++ +
// static-archive link path this failed to launch with "libstdc++-6.dll was not
// found" off ucrt64/bin. This guards against regressing to dynamic DLLs.
//
// Windows-only: the _windows_test.go suffix is a GOOS build constraint, which is
// exactly right here — the mingw-DLL hazard is Windows-specific.
func TestE2ETuiBinaryIsSelfContained(t *testing.T) {
	staticBuild(t)
	bin := buildBinaryImports(t, `
import { Box, Text, render } from 'klain:tui'
const label: string = 'x'.replace(/x/, 'y')  // pull in the RegExp engine (pcre2)
render(Box({ width: 10 }, [Text(label)]))
`)
	assertNoBundledRuntimeDLLs(t, bin, "TUI")
	assertImportsOnlyWindows(t, bin)
}

// ADR-00771: a klain:webview desktop program must likewise be a single
// self-contained .exe — its amalgamated C++ binding (precompiled with g++) and
// the C++ runtime (incl. winpthread, which webview's dispatch queue pulls in via
// std::thread) are linked statically. The per-machine Edge WebView2 runtime is a
// separate system component, not a bundled DLL. Compile-tier: builds but never
// opens a window.
func TestE2EWebviewBinaryIsSelfContained(t *testing.T) {
	staticBuild(t)
	bin := buildBinaryImports(t, `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "T" })
`)
	assertNoBundledRuntimeDLLs(t, bin, "webview")
	assertImportsOnlyWindows(t, bin)
}

// TDD-00253: a --static build depends on nothing Windows does not ship. One
// program pulls in every feature library: the HTTP server and fetch (curl,
// OpenSSL, nghttp2, libuv), zlib with Brotli and Zstd (loaded at run time
// without --static), sqlite, BigInt (libtommath), the RegExp engine and
// crypto. It runs with only Windows' own directories on PATH.
func TestE2EStaticBinaryImportsOnlyWindows(t *testing.T) {
	staticBuild(t)
	bin := buildBinaryImports(t, `
import http from 'node:http'
import zlib from 'node:zlib'
import crypto from 'node:crypto'
import { DatabaseSync } from 'node:sqlite'
const db = new DatabaseSync(':memory:')
db.exec('create table t(x)')
console.log(zlib.brotliDecompressSync(zlib.brotliCompressSync('br')).toString(),
  zlib.zstdDecompressSync(zlib.zstdCompressSync('zs')).toString(),
  zlib.gunzipSync(zlib.gzipSync('gz')).toString(), 10n ** 30n > 0n,
  crypto.createHash('sha256').update('x').digest('hex').slice(0, 8), /a(b)/.exec('ab')![1])
const s = http.createServer((q, r) => r.end('ok'))
s.listen(0, '127.0.0.1', async () => {
  const res = await fetch('http://127.0.0.1:' + (s.address() as any).port + '/')
  console.log(await res.text())
  s.close()
})
`)
	assertImportsOnlyWindows(t, bin)
	cmd := exec.Command(bin)
	sys := os.Getenv("SystemRoot")
	cmd.Env = []string{"SystemRoot=" + sys, "PATH=" + sys + `\System32;` + sys}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "br zs gz true 2d711642 b\nok\n" {
		t.Fatalf("run with a Windows-only PATH: %v\n%s", err, out)
	}
}

// windowsShippedDLLs: what a stock Windows 10/11 provides. The UCRT's
// api-ms-win-crt-* forwarders are matched by prefix.
var windowsShippedDLLs = map[string]bool{
	"kernel32.dll": true, "user32.dll": true, "advapi32.dll": true, "shell32.dll": true,
	"ws2_32.dll": true, "iphlpapi.dll": true, "userenv.dll": true, "bcrypt.dll": true,
	"crypt32.dll": true, "secur32.dll": true, "wldap32.dll": true, "dbghelp.dll": true,
	"ole32.dll": true, "oleaut32.dll": true, "psapi.dll": true, "shlwapi.dll": true,
	"version.dll": true, "gdi32.dll": true, "ntdll.dll": true, "ncrypt.dll": true,
	"comctl32.dll": true, "comdlg32.dll": true, "dwmapi.dll": true, "uxtheme.dll": true,
	"winmm.dll": true, "normaliz.dll": true, "rpcrt4.dll": true, "mswsock.dll": true,
}

// assertImportsOnlyWindows fails when the binary imports a DLL Windows does
// not ship (TDD-00253).
func assertImportsOnlyWindows(t *testing.T, bin string) {
	t.Helper()
	out, err := exec.Command("objdump", "-p", bin).CombinedOutput()
	if err != nil {
		t.Skipf("objdump unavailable (need binutils on PATH): %v", err)
	}
	for _, ln := range strings.Split(string(out), "\n") {
		i := strings.Index(ln, "DLL Name:")
		if i < 0 {
			continue
		}
		dll := strings.ToLower(strings.TrimSpace(ln[i+len("DLL Name:"):]))
		if strings.HasPrefix(dll, "api-ms-win-") || windowsShippedDLLs[dll] {
			continue
		}
		t.Errorf("imports %s, which Windows does not ship (TDD-00253)", dll)
	}
}

// assertNoBundledRuntimeDLLs fails if the produced Windows binary dynamically
// imports a mingw runtime / feature DLL that should be statically linked into a
// self-contained binary (ADR-00771).
func assertNoBundledRuntimeDLLs(t *testing.T, bin, kind string) {
	t.Helper()
	out, err := exec.Command("objdump", "-p", bin).CombinedOutput()
	if err != nil {
		t.Skipf("objdump unavailable (need binutils on PATH): %v", err)
	}
	low := strings.ToLower(string(out))
	for _, dll := range []string{"libstdc++", "libgcc_s", "libwinpthread", "libpcre2"} {
		if strings.Contains(low, dll) {
			var imports []string
			for _, ln := range strings.Split(string(out), "\n") {
				if strings.Contains(ln, "DLL Name") {
					imports = append(imports, strings.TrimSpace(ln))
				}
			}
			t.Errorf("%s binary dynamically imports %s — not self-contained (ADR-00771).\nImports:\n%s",
				kind, dll, strings.Join(imports, "\n"))
		}
	}
}
