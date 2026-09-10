package llvm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// webview_win32.go — the WebView2 backend's build inputs and the one
// behavioural shim the Win32 engine needs (ADR-00725).

// locateWebviewWindows resolves the WebView2 build inputs on Windows. The
// header lives in the mingw sysroot's include directory once MSYS2's
// webview2-loader package is installed, so no -I is needed; the probe exists
// only to turn a bare "'WebView2.h' file not found" into an error naming the
// package. The runtime itself (Edge WebView2) is a per-machine install that
// the binding's built-in loader resolves at run time, not a link input.
func locateWebviewWindows() (cflags, libs []string, err error) {
	hdr := filepath.Join(windowsSysroot(), "include", "WebView2.h")
	if _, serr := os.Stat(hdr); serr != nil {
		return nil, nil, fmt.Errorf("webview: WebView2.h not found in the mingw sysroot (%s) — install the WebView2 SDK header: `pacman -S mingw-w64-ucrt-x86_64-webview2-loader`", hdr)
	}
	libs = []string{"-lole32", "-lshell32", "-lshlwapi", "-luser32", "-ladvapi32", "-lversion"}
	if staticLinkMode {
		// --static: the C++ runtime links statically (libstdc++ + winpthread group);
		// the amalgamated binding is precompiled with g++ (windowsWebviewObject) so
		// its objects are COMDAT-compatible with the gcc archive. ADR-00772.
		libs = append(libs, winCxxStaticRuntime()...)
	} else {
		// Default: dynamic C++ runtime (libstdc++-6.dll, bundled/on-PATH).
		libs = append(libs, "-lstdc++")
	}
	return nil, libs, nil
}

// windowsWebviewObject precompiles the amalgamated webview C++ source with g++
// (not clang) into a cached object on Windows. The desktop app then links the
// static gcc libstdc++.a; a clang-built object's RTTI COMDATs differ in size from
// gcc's and ld.bfd rejects the mix, exactly as for Yoga (ADR-00771). g++ ships
// with the required mingw toolchain and is native to the ucrt64 sysroot (so
// WebView2.h resolves without a -I). Returns the object path; the caller carries
// it as a link input instead of compiling WebviewSource() on the shared clang line.
func windowsWebviewObject() (string, error) {
	src := WebviewSource("system") // Windows uses the system engine (WebView2); non-system backends are rejected upstream.
	sum := sha256.Sum256([]byte("kml-webview-" + src))
	dir := filepath.Join(os.TempDir(), "kml-webview-"+hex.EncodeToString(sum[:6]))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("webview: temp dir: %w", err)
	}
	obj := filepath.Join(dir, "webview.o")
	if fi, serr := os.Stat(obj); serr == nil && fi.Size() > 0 {
		return obj, nil
	}
	srcPath := filepath.Join(dir, "webview.cc")
	if err := os.WriteFile(srcPath, []byte(src), 0644); err != nil {
		return "", fmt.Errorf("webview: write source: %w", err)
	}
	cmd := exec.Command("g++", "-std=c++17", "-O2", "-c", srcPath, "-o", obj)
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		return "", fmt.Errorf("webview: g++ compiling amalgamation: %v\n%s", cerr, out)
	}
	return obj, nil
}

// webviewWin32Deferral is appended to the vendored header on Windows. On
// WebKit (WKWebView, WebKitGTK) nothing loads until the run loop starts, so a
// program may register its `init` script after `html()`/`navigate()` — or
// after the constructor's own `serve` navigation — and still have it run at
// document creation. WebView2 starts the navigation the moment `Navigate` is
// issued, so an `init` registered afterwards misses the first page. These two
// entry points post the navigation through webview_dispatch instead: it then
// happens when the loop runs, after every `init` the program issued before
// `run()`, which is exactly the order WebKit gives. A navigation requested
// from inside the loop (a bound callback, a timer) is likewise deferred to the
// next iteration, which is unobservable.
const webviewWin32Deferral = `

// --- deferred navigation for the WebView2 backend (this project's shim) ---
extern "C" {
static void __kml_wv_nav_tramp(webview_t w, void *arg) {
  auto *u = static_cast<std::string *>(arg);
  webview_navigate(w, u->c_str());
  delete u;
}
static void __kml_wv_html_tramp(webview_t w, void *arg) {
  auto *h = static_cast<std::string *>(arg);
  webview_set_html(w, h->c_str());
  delete h;
}
WEBVIEW_API void __kml_wv_navigate(webview_t w, const char *url) {
  webview_dispatch(w, __kml_wv_nav_tramp, new std::string(url));
}
WEBVIEW_API void __kml_wv_set_html(webview_t w, const char *html) {
  webview_dispatch(w, __kml_wv_html_tramp, new std::string(html));
}
}
`

// webviewNavigateSym / webviewSetHTMLSym name the navigation entry points the
// emitted IR calls: the upstream functions on WebKit hosts, the deferring
// shims above on Windows.
func webviewNavigateSym() string {
	if runtime.GOOS == "windows" {
		return "__kml_wv_navigate"
	}
	return "webview_navigate"
}

func webviewSetHTMLSym() string {
	if runtime.GOOS == "windows" {
		return "__kml_wv_set_html"
	}
	return "webview_set_html"
}
