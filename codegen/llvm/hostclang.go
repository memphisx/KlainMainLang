package llvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// HostClangArgv is the single place that builds a `clang` argv for the
// host, so every caller (the compiler driver, the E2E test helpers, the
// embedded C++ bindings, the conformance runner) gets the same host-specific
// prefix. On Linux and
// macOS that prefix is empty: the system clang already targets the host and
// finds its own libc. On Windows, LLVM's stock clang defaults to the MSVC
// target and ships no C runtime at all, so the emitted program is instead
// built for the mingw-w64 UCRT target against an MSYS2 sysroot — see
// TDD-00177 for why the toolchain is mingw while the platform semantics are
// written against Win32 directly. ClangCommand wraps it in an exec.Cmd; a
// caller that needs its own Cmd (the conformance runner's killable,
// timeout-bound one) takes the argv directly.
func HostClangArgv(args ...string) []string {
	full := HostClangArgs()
	if runtime.GOOS == "windows" {
		// The shim objects go *before* the caller's arguments: they shadow a
		// few library exports (curl_multi_fdset), and lld resolves a symbol
		// from the first definition it meets — an object listed after -lcurl
		// would collide with the import-library member already pulled in.
		full = append(full, win32CompileArgs(args)...)
		full = append(full, win32LinkArgs(args)...)
	}
	full = append(full, args...)
	// Stable-partition every -l flag to the end, after all objects and
	// sources, preserving relative library order. GNU ld never re-searches a
	// library listed before an object that needs it, and on Windows the
	// .dll.a import libraries are archives — a sidecar .c listed after
	// -lpcre2-8/-lcurl linked with unresolved references there while the
	// ELF/Mach-O shared libraries forgave the same order (ADR-00738). Doing
	// it here fixes every caller (driver, test helpers, conformance runner,
	// webview builds) at the one point they all share.
	var head, libs []string
	for _, a := range full {
		if strings.HasPrefix(a, "-l") {
			libs = append(libs, a)
		} else {
			head = append(head, a)
		}
	}
	return append(head, libs...)
}

// ClangCommand is HostClangArgv as a ready-to-run command.
func ClangCommand(args ...string) *exec.Cmd {
	return exec.Command("clang", HostClangArgv(args...)...)
}

// staticLinkMode is set once from the --static flag (SetStaticLink) before any
// compilation. It gates whether the non-system feature libraries (C++ runtime,
// winpthread, pcre2, the curl chain) link statically for a self-contained binary,
// or dynamically (the default — the program then needs those DLLs bundled beside
// it or on PATH, the same way a Mac/Linux dynamic binary needs its libs present).
// A package var, not an Emitter field, because the link-flag helpers are package
// functions shared by main.go, the test helpers, and the conformance runner; it
// is written once at startup and read-only thereafter (a single process only ever
// compiles at one --static setting — main.go builds one program; tests/conformance
// are always dynamic — so the parallel conformance workers never race it).
var staticLinkMode bool

// SetStaticLink records whether --static was requested (main.go, once at startup).
func SetStaticLink(v bool) { staticLinkMode = v }

// StaticLink reports the current --static mode.
func StaticLink() bool { return staticLinkMode }

// winStaticFeatureLibs are the feature libraries linked as a *static* archive on
// Windows (the `-l:lib<name>.a` colon form) instead of the DLL import lib, so a
// program that uses them is still a single self-contained .exe — the point of a
// terminal/CLI binary. pcre2 (the RegExp engine) is a lone self-contained archive;
// curl expands to a whole dependency chain instead (winCurlStaticChain). ADR-00771/00772.
var winStaticFeatureLibs = map[string]bool{"pcre2-8": true}

// winCurlStaticChain is the full static link chain for libcurl on the ucrt64
// mingw sysroot, so a `fetch`/`http`/`tls`/WebSocket program is a single
// self-contained .exe on Windows with no libcurl-4.dll (or its ssl/nghttp2/…
// DLLs) beside it (ADR-00772). Derived from `pkg-config --static --libs libcurl`:
// the mingw archives are forced static inside one comma-joined -Wl group
// (-Bstatic + --start-group so their circular refs resolve, -Bdynamic restores
// normal linking; the single -Wl arg survives HostClangArgv's -l partition), then
// the Windows *system* import libs (ws2_32/secur32/crypt32/…) stay dynamic — curl
// here uses Windows SSPI, not MIT krb5/gssapi, so there is no external krb5 chain
// to resolve (the Debian static-curl blocker does not apply on ucrt64). The IR
// declares curl symbols directly (no dllimport), so no CURL_STATICLIB define is
// needed for the emitted program.
func winCurlStaticChain() []string {
	group := "-Wl,-Bstatic,--start-group," +
		"-lcurl,-lssl,-lcrypto,-lz,-lssh2,-lbrotlidec,-lbrotlicommon,-lzstd," +
		"-lnghttp2,-lngtcp2_crypto_ossl,-lngtcp2,-lnghttp3,-lpsl,-lunistring,-liconv,-lidn2," +
		"--end-group,-Bdynamic"
	system := []string{"-lwldap32", "-lbcrypt", "-ladvapi32", "-lcrypt32",
		"-lsecur32", "-lws2_32", "-liphlpapi", "-lgdi32"}
	// Static libcurl.a pulls libws2_32.a's socket symbols (connect/bind/getaddrinfo/…)
	// and the static CRT's `strerror`, which the win32io/win32fs shims also define;
	// --allow-multiple-definition lets the shim's definitions win (first-def), the
	// same rule the C++ static path already relies on. curl's own socket calls then
	// route through the shim's Winsock wrappers (verified functional). ADR-00772.
	out := append([]string{group}, system...)
	return append(out, "-Wl,--allow-multiple-definition")
}

// LinkLibFlags maps a required library name (as recorded by requireLink) to its
// clang link flag(s) for the host: the static-archive colon form on Windows for a
// self-contained-safe feature lib, the full static chain for curl on Windows, else
// the plain `-l<name>` import/shared form. Returns a slice because curl expands.
func LinkLibFlags(lib string) []string {
	if runtime.GOOS == "windows" && staticLinkMode {
		if lib == "curl" {
			return winCurlStaticChain()
		}
		if winStaticFeatureLibs[lib] {
			return []string{"-l:lib" + lib + ".a"}
		}
	}
	return []string{"-l" + lib}
}

// WorkerPthreadLinkFlags returns the link-phase flags for a program that uses
// worker threads / the klain:sync goroutine runtime. On POSIX that is -pthread
// (links the system pthread). On Windows -pthread would link libwinpthread-1.dll
// *dynamically*, defeating a single self-contained .exe, so instead the static
// archive is linked (colon form → after all objects via the -l partition) with
// --allow-multiple-definition to reconcile it with any other winpthread the C++
// static group already pulled (ADR-00772).
func WorkerPthreadLinkFlags() []string {
	if runtime.GOOS == "windows" && staticLinkMode {
		return []string{"-l:libwinpthread.a", "-Wl,--allow-multiple-definition"}
	}
	// POSIX, and dynamic-mode Windows: -pthread (links libwinpthread dynamically
	// on Windows — the DLL is bundled/on-PATH for a dynamic build).
	return []string{"-pthread"}
}

// HostClangArgs returns the host-specific arguments ClangCommand prepends.
// Exposed so a caller that must build its own argv (e.g. for logging) can
// still stay in sync.
func HostClangArgs() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	return []string{"--target=x86_64-w64-mingw32", "--sysroot=" + windowsSysroot()}
}

// windowsSysroot locates the mingw-w64 UCRT sysroot. KLAIN_SYSROOT overrides;
// otherwise the default MSYS2 layout (C:\msys64\ucrt64) is probed, then the
// same tree under an MSYS2 install found via the MSYS2_ROOT variable some
// installers set. Falling back to the bare default lets clang print its own
// "file not found" for a missing sysroot rather than this code guessing.
func windowsSysroot() string {
	if v := os.Getenv("KLAIN_SYSROOT"); v != "" {
		return v
	}
	candidates := []string{`C:\msys64\ucrt64`}
	if r := os.Getenv("MSYS2_ROOT"); r != "" {
		candidates = append([]string{filepath.Join(r, "ucrt64")}, candidates...)
	}
	for _, c := range candidates {
		if st, err := os.Stat(filepath.Join(c, "include", "stdio.h")); err == nil && !st.IsDir() {
			return c
		}
	}
	return candidates[len(candidates)-1]
}

// HostExeSuffix is ".exe" on Windows and "" elsewhere — the suffix a caller
// appends to a default output path that has no extension.
func HostExeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
