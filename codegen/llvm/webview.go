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

// WebviewSource returns the C/C++ translation unit implementing the webview_*
// ABI for the selected backend (TDD-00144). "system"/"" is the amalgamated
// webview/webview binding (the header, compiled directly as the translation
// unit — see the file comment); cef/qt/sailfish are the opt-in shims, not yet
// built. LocateWebview is the gate that rejects an unbuilt backend, so this is
// only reached for system.
func WebviewSource(backend string) string {
	if backend == "sailfish" {
		// The Sailfish Gecko/embedlite shim (TDD-00146 Stage 3). Compiles+links
		// against a Sailfish -devel sysroot (verified); needs a moc pre-step,
		// so LocateWebview still gates end-to-end builds until that lands.
		return SailfishWebviewShimSource()
	}
	if backend != "" && backend != "system" {
		// Unreachable in practice — main.go and LocateWebview reject a
		// non-system backend before any source is requested. Return a stub so
		// the plumbing stays total.
		return fmt.Sprintf("// webview backend %q not yet implemented (TDD-00144).\n", backend)
	}
	if runtime.GOOS == "windows" {
		return webviewSource + webviewWin32Deferral
	}
	return webviewSource
}

// LocateWebview returns the clang cflags/libs needed to compile and link the
// webview binding on the host platform, following crypto.go's LocateCrypto
// framework/pkg-config split:
//
//   - darwin: WKWebView lives in WebKit.framework, which ships with the OS —
//     zero install step. CoreGraphics is linked explicitly for CGRect/CGSize.
//     -lc++ pulls in the C++ runtime for the amalgamated C++ source.
//
//   - linux: pkg-config probe for gtk4 + webkitgtk-6.0, falling back to
//     gtk+-3.0 + webkit2gtk-4.1 (both API generations are supported upstream).
//     -lstdc++ for the C++ runtime. A clean error naming the dev packages if
//     neither generation resolves.
//
//   - windows: the upstream WebView2 backend (ADR-00725). The Edge WebView2
//     runtime ships with Windows 10/11; the build needs only the SDK header
//     (`WebView2.h`, MSYS2's `mingw-w64-ucrt-x86_64-webview2-loader` package)
//     since the binding's built-in loader locates the runtime itself — no
//     WebView2Loader.dll beside the binary. The link line is the system
//     libraries the header names for MSVC via #pragma comment, which the
//     mingw driver does not honour, plus -lstdc++ for the C++ runtime.
//   - the backend argument selects which engine's shim is compiled+linked
//     (TDD-00144): "system"/"" is the per-platform system engine below; cef
//     (Chromium Embedded Framework), qt (QtWebEngine), and sailfish
//     (Gecko/embedlite, TDD-00146 Stage 3) are recognized opt-in backends
//     whose shims are not yet built, so they return a clean "not yet
//     implemented" error naming the flag rather than a broken link line.
func LocateWebview(backend string) (cflags, libs []string, err error) {
	switch backend {
	case "", "system":
		// fall through to the per-platform system engine below.
	case "sailfish":
		// The Sailfish Gecko/embedlite shim (TDD-00146 Stage 3): compile+link
		// flags come from the target sysroot's pkg-config, and the build runs the
		// target moc over the shim (NeedsMoc, see EmbeddedCSources). Requires a
		// --sysroot; LocateWebviewSailfish returns a clean error without one.
		return LocateWebviewSailfish(CrossTargetSysroot())
	case "cef", "qt":
		return nil, nil, fmt.Errorf("-webview=%s is not yet implemented — only -webview=system (the default) is currently built (TDD-00144)", backend)
	default:
		return nil, nil, fmt.Errorf("unrecognized webview backend %q — must be one of: system (default), cef, qt, sailfish", backend)
	}
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
	case "windows":
		return locateWebviewWindows()
	default:
		return nil, nil, fmt.Errorf("webview: unsupported platform %q (macOS, Linux, Windows)", runtime.GOOS)
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
