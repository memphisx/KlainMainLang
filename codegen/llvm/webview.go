package llvm

import (
	_ "embed"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// webview.go — the C++ system-webview binding plumbing (TDD-00142, Stage 0).
//
// webview/webview (MIT, pinned at 0.10.0) is a ~13-function extern "C" API over
// the platform browser engine — WKWebView on macOS, WebKitGTK on Linux. The
// whole library is a single amalgamated header whose implementation is compiled
// inline (there is no WEBVIEW_HEADER-only mode in 0.10.0), so the vendored
// webview.h *is* the translation unit: EmbeddedCSources emits it as a `.cc`
// member (CSource.Ext == "cc") and clang's driver compiles it as C++14. No
// separate stub .cc is needed. The MIT license text is retained at the top of
// webviewsrc/webview.h.
//
// This mirrors the crypto/http2 embedded-source pattern exactly (go:embed +
// EmbeddedCSources + requireLink), with the one genuinely new piece the C++
// extension field enables — see embedded_c.go's CSource.Ext.

//go:embed webviewsrc/webview.h
var webviewSource string

// WebviewSource returns the amalgamated webview/webview C++ source (the header,
// compiled directly as the translation unit — see the file comment).
func WebviewSource() string { return webviewSource }

// LocateWebview returns the clang cflags/libs needed to compile and link the
// webview binding on the host platform, following crypto.go's LocateCrypto
// framework/pkg-config split:
//
//   - darwin: WKWebView lives in WebKit.framework, which ships with the OS —
//     zero install step. CoreGraphics is linked explicitly for CGRect/CGSize.
//     -lc++ pulls in the C++ runtime for the amalgamated C++ source.
//   - linux: pkg-config probe for gtk4 + webkitgtk-6.0, falling back to
//     gtk+-3.0 + webkit2gtk-4.1 (both API generations are supported upstream).
//     -lstdc++ for the C++ runtime. A clean error naming the dev packages if
//     neither generation resolves.
//
// Windows is out of scope (TDD-00020), rejected loudly here.
func LocateWebview() (cflags, libs []string, err error) {
	switch runtime.GOOS {
	case "darwin":
		return nil, []string{"-framework", "WebKit", "-framework", "CoreGraphics", "-lc++"}, nil
	case "linux":
		// gtk4 + webkitgtk-6.0 is the current generation; gtk+-3.0 +
		// webkit2gtk-4.1 the widely-shipped fallback (Debian/Ubuntu LTS).
		for _, gen := range [][2]string{{"gtk4", "webkitgtk-6.0"}, {"gtk+-3.0", "webkit2gtk-4.1"}} {
			cf, lf, ok := pkgConfigPair(gen[0], gen[1])
			if ok {
				cf = append(cf, defineForGen(gen[1])...)
				return cf, append(lf, "-lstdc++"), nil
			}
		}
		return nil, nil, fmt.Errorf("webview: no supported WebKitGTK found — install the dev packages (Debian/Ubuntu: `libgtk-4-dev libwebkitgtk-6.0-dev`, or the older `libgtk-3-dev libwebkit2gtk-4.1-dev`; Fedora: `gtk4-devel webkitgtk6.0-devel`)")
	default:
		return nil, nil, fmt.Errorf("webview: unsupported platform %q (POSIX only — macOS/Linux)", runtime.GOOS)
	}
}

// defineForGen selects the webview backend macro matching the resolved GTK
// generation. webview.h auto-detects GTK by default (the WEBVIEW_GTK path), so
// no define is strictly required on Linux; the empty slice keeps the call site
// uniform and leaves room for a future WEBVIEW_GTK4 pin if upstream splits it.
func defineForGen(webkit string) []string { return nil }

// pkgConfigPair runs `pkg-config --cflags/--libs` for two modules together,
// returning their flags and whether both resolved.
func pkgConfigPair(a, b string) (cflags, libs []string, ok bool) {
	cout, err := exec.Command("pkg-config", "--cflags", a, b).Output()
	if err != nil {
		return nil, nil, false
	}
	lout, err := exec.Command("pkg-config", "--libs", a, b).Output()
	if err != nil {
		return nil, nil, false
	}
	return strings.Fields(strings.TrimSpace(string(cout))),
		strings.Fields(strings.TrimSpace(string(lout))), true
}
