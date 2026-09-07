package tests

import (
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
