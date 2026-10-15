package llvm

import (
	"KlainMainLang/options"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// HostClangArgv is the single place that builds a `clang` argv for the
// host, so every caller (the compiler driver, the E2E test helpers, the
// embedded C++ bindings, the conformance runner) gets the same host-specific
// prefix. On Linux that
// prefix is empty, and on macOS it only silences a spurious warning: the
// system clang already targets the host and finds its own libc. On Windows, LLVM's stock clang defaults to the MSVC
// target and ships no C runtime at all, so the emitted program is instead
// built for the mingw-w64 UCRT target against an MSYS2 sysroot — see
// TDD-00177 for why the toolchain is mingw while the platform semantics are
// written against Win32 directly. ClangCommand wraps it in an exec.Cmd; a
// caller that needs its own Cmd (the conformance runner's killable,
// timeout-bound one) takes the argv directly.
func HostClangArgv(args ...string) []string { return Toolchain{}.Argv(args...) }

// Toolchain is the clang invocation for one compile target: the host's for
// the zero value, or a cross target's (--target/--sysroot), which every
// clang call of that build (the main compile, the embedded-C runtime, the GC
// shim) goes through, matching the `target triple` the IR carries.
type Toolchain struct{ Target options.Target }

// Argv is HostClangArgv for tc's target.
func (tc Toolchain) Argv(args ...string) []string {
	if runtime.GOOS != "windows" && tc.Target.Triple == "" {
		// The host's sidecar sources link as cached objects.
		args = cacheSidecars(args)
	}
	full := tc.Args()
	if runtime.GOOS == "windows" {
		// The shim objects go *before* the caller's arguments: they define
		// the POSIX socket calls under Winsock's names (inet_pton, getaddrinfo,
		// …) and their import slots, and lld resolves a symbol
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
func ClangCommand(args ...string) *exec.Cmd { return Toolchain{}.Command(args...) }

// Command is Argv as a ready-to-run command.
func (tc Toolchain) Command(args ...string) *exec.Cmd {
	return exec.Command("clang", tc.ExecArgv(args...)...)
}

// HostClangExecArgv is HostClangArgv as clang's command line: on Windows,
// one that would overflow CreateProcess's limit goes through a response file.
func HostClangExecArgv(args ...string) []string { return Toolchain{}.ExecArgv(args...) }

// ExecArgv is Argv as clang's command line (see HostClangExecArgv).
func (tc Toolchain) ExecArgv(args ...string) []string {
	argv := tc.Argv(args...)
	if runtime.GOOS == "windows" {
		if rsp, ok := responseFile(argv); ok {
			argv = []string{"--rsp-quoting=windows", "@" + rsp}
		}
	}
	return argv
}

// winCmdLineMax is CreateProcess's command-line limit (32767 UTF-16 units),
// with room for the program name.
const winCmdLineMax = 32000

// responseFile moves an argv that would overflow Windows' command line into a
// clang response file. Its name is the content's hash, so repeating a link
// rewrites the same file instead of adding one; the file is not removed
// because the command it serves has not run yet.
func responseFile(argv []string) (string, bool) {
	var b strings.Builder
	for _, a := range argv {
		b.WriteString(winQuoteArg(a))
		b.WriteByte('\n')
	}
	if b.Len() < winCmdLineMax {
		return "", false
	}
	dir := filepath.Join(os.TempDir(), "klainmain-rsp")
	if os.MkdirAll(dir, 0o755) != nil {
		return "", false
	}
	path := filepath.Join(dir, fmt.Sprintf("%x.rsp", sha256.Sum256([]byte(b.String()))))
	if os.WriteFile(path, []byte(b.String()), 0o644) != nil {
		return "", false
	}
	return path, true
}

// winQuoteArg quotes one argument the way clang's Windows response-file
// tokenizer (the CommandLineToArgvW rules) reads it back: backslashes are
// literal unless they precede a double quote.
func winQuoteArg(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
		default:
			slashes = 0
		}
		b.WriteByte(c)
	}
	b.WriteString(strings.Repeat(`\`, slashes))
	b.WriteByte('"')
	return b.String()
}

// RunLink is RunClangLink for tc's target.
func (tc Toolchain) RunLink(args ...string) error {
	final, idx := "", -1
	if runtime.GOOS == "windows" {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-o" && !isASCII(args[i+1]) {
				final, idx = args[i+1], i+1
			}
		}
	}
	var tmpDir string
	if idx >= 0 {
		d, err := os.MkdirTemp("", "kml-link-")
		if err != nil || !isASCII(d) {
			// No ASCII temp location either: fall through and let the linker
			// report the failure against the real path.
			if err == nil {
				os.RemoveAll(d)
			}
			idx = -1
		} else {
			tmpDir = d
			defer os.RemoveAll(tmpDir)
			args = append([]string{}, args...)
			args[idx] = filepath.Join(tmpDir, "out"+filepath.Ext(final))
		}
	}
	cmd := tc.Command(args...)
	cmd.Stdout = os.Stdout
	var diag bytes.Buffer
	cmd.Stderr = &diag
	err := cmd.Run()
	out := dropIntendedDuplicates(diag.Bytes())
	if irSites {
		out = []byte(AnnotateClangOutput(out)) // annotated with the emitting Go sites
	}
	os.Stderr.Write(out)
	if err != nil {
		return err
	}
	if idx < 0 {
		return nil
	}
	return moveFile(args[idx], final)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// moveFile renames src over dst, falling back to copy+remove when they sit on
// different volumes (the temp directory and the project often do).
func moveFile(src, dst string) error {
	os.Remove(dst)
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0755); err != nil {
		return err
	}
	return os.Remove(src)
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

// ParseTarget is the compile target a --target triple and --sysroot name
// (TDD-00146): the triple's OS and arch parsed to Go's spelling ("" when
// unrecognized). An empty triple is the host.
func ParseTarget(triple, sysroot string) options.Target {
	if triple == "" {
		return options.Target{}
	}
	return options.Target{Triple: triple, Sysroot: sysroot, GOOS: tripleGOOS(triple), GOARCH: tripleGOARCH(triple)}
}

// tripleGOOS extracts the OS a clang triple names, normalized to the Go GOOS
// spelling, or "" if unrecognized. Triples are arch[-vendor]-os[-abi]; a
// substring match on the OS token tolerates the vendor field being present or
// absent (e.g. aarch64-meego-linux-gnu → "linux"). Mirrors main.go's
// targetOSFromTriple, kept here so the llvm package is self-contained.
func tripleGOOS(triple string) string {
	t := strings.ToLower(triple)
	switch {
	case strings.Contains(t, "linux"):
		return "linux"
	case strings.Contains(t, "darwin"), strings.Contains(t, "macos"), strings.Contains(t, "apple"):
		return "darwin"
	case strings.Contains(t, "windows"), strings.Contains(t, "mingw"), strings.Contains(t, "w64"):
		return "windows"
	default:
		return ""
	}
}

// tripleGOARCH extracts the CPU architecture from a clang triple's first field,
// normalized to the Go GOARCH spelling, or "" if unrecognized. The arch is
// always the leading token before the first '-'.
func tripleGOARCH(triple string) string {
	arch := triple
	if i := strings.Index(arch, "-"); i >= 0 {
		arch = arch[:i]
	}
	switch strings.ToLower(arch) {
	case "aarch64", "arm64":
		return "arm64"
	case "x86_64", "amd64":
		return "amd64"
	case "i386", "i486", "i586", "i686":
		return "386"
	case "riscv64":
		return "riscv64"
	case "armv7", "armv7l", "armv7hl", "arm":
		return "arm"
	default:
		return ""
	}
}

// lldAvailable reports whether an LLVM `lld` is on PATH. For a genuine
// cross-arch link the host's system `ld` cannot emit the target's object
// format, so lld is preferred (`-fuse-ld=lld`, the TDD-00146 Stage 1 linking
// note); when the target arch matches the host (e.g. aarch64-linux from an
// arm64 Linux container) the default linker already works, so lld is optional
// and only used if present.
func lldAvailable() bool {
	for _, name := range []string{"ld.lld", "lld"} {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

// winStaticFeatureLibs are the feature libraries linked as a *static* archive on
// Windows (the `-l:lib<name>.a` colon form) instead of the DLL import lib, so a
// program that uses them is still a single self-contained .exe — the point of a
// terminal/CLI binary. pcre2 (the RegExp engine) is a lone self-contained archive;
// curl expands to a whole dependency chain instead (winCurlStaticChain). ADR-00771/00772.
var winStaticFeatureLibs = map[string]bool{"pcre2-8": true, "cares": true, "sqlite3": true, "z": true,
	"brotlienc": true, "brotlidec": true, "brotlicommon": true, "zstd": true}

// winSelfContained: a --static build for Windows, which imports only what
// Windows ships (TDD-00253).
func winSelfContained(goos string) bool {
	return goos == "windows" && runtime.GOOS == "windows" && staticLinkMode
}

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

// FFILinkFlags returns the link-phase flags for a program that uses node:ffi
// (TDD-00164). node:ffi's `dlopen(null)` resolves symbols against the running
// process's global scope, so the executable must (1) export its own dynamic
// symbol table (-rdynamic → --export-dynamic) and (2) actually load the common
// system libraries a program FFIs into. libm is the important one: `pow` and the
// other math symbols live there, and on Linux the linker drops an unreferenced
// `-lm` under the distro-default --as-needed — so it is force-kept with a
// --no-as-needed island. (macOS bundles libm/libdl in libSystem, always linked,
// which is why this only bites Linux — see runtime_ffi.go.) Skipped for a fully
// static build, where dlopen(null) can't grow the symbol scope at runtime anyway.
func FFILinkFlags() []string {
	if runtime.GOOS != "linux" || staticLinkMode {
		return nil
	}
	return []string{"-rdynamic", "-Wl,--no-as-needed", "-lm", "-Wl,--as-needed"}
}

// HostClangArgs returns the host-specific arguments ClangCommand prepends.
// Exposed so a caller that must build its own argv (e.g. for logging) can
// still stay in sync.
func HostClangArgs() []string { return Toolchain{}.Args() }

// Args is HostClangArgs for tc's target.
func (tc Toolchain) Args() []string {
	// An explicit --target/--sysroot (TDD-00146 Stage 1) takes precedence over
	// host defaults, including the Windows-host mingw preset below: the whole
	// point is to retarget away from the host. The embedded-C runtime is
	// compiled through this same argv, so it follows the sysroot automatically.
	if tc.Target.Triple != "" {
		// -Wno-override-module: the IR stamps the requested triple, and clang
		// normalizes --target to its own canonical 4-field form (aarch64-linux-gnu
		// → aarch64-unknown-linux-gnu), so the two differ textually and clang
		// would warn that it is overriding the module triple with the --target
		// one. That override is exactly the intended behavior — the --target value
		// wins — so the warning is pure noise here.
		args := []string{"--target=" + tc.Target.Triple, "-Wno-override-module"}
		if tc.Target.Sysroot != "" {
			args = append(args, "--sysroot="+tc.Target.Sysroot)
		}
		if lldAvailable() {
			args = append(args, "-fuse-ld=lld")
		}
		return args
	}
	if runtime.GOOS != "windows" {
		// clang warns that it overrides the module triple whenever the IR's
		// spelling differs from its own (Apple clang 21 for every textual-IR
		// input; Linux clang 18 normalizes x86_64-unknown-linux-gnu to
		// x86_64-pc-linux-gnu); the host triple is the intended one.
		return []string{"-Wno-override-module"}
	}
	args := []string{"--target=x86_64-w64-mingw32", "--sysroot=" + windowsSysroot()}
	// lld links on Windows when installed: GNU ld's COFF reader takes a
	// section number as a signed 16-bit value and loses every symbol in a
	// section numbered past 32767, so a program with that many functions and
	// literals (each in its own section or comdat) does not link with it.
	if windowsUsesLLD() {
		args = append(args, "-fuse-ld=lld")
	}
	if emulatedTLS {
		args = append(args, "-femulated-tls")
	}
	return args
}

// emulatedTLS compiles every thread-local through a control variable and
// __emutls_get_address (-femulated-tls): a lazy dynamic-import library on
// Windows can import a variable from the executable, never a TLS slot
// (TDD-00254). Part of every compile's arguments, so of the object cache's
// keys too.
var emulatedTLS bool

// SetEmulatedTLS turns emulated thread-locals on for this compile.
func SetEmulatedTLS(v bool) { emulatedTLS = v }

// windowsUsesLLD reports whether Windows links with lld (Args).
func windowsUsesLLD() bool {
	lldOnce.Do(func() { lldFound = lldAvailable() })
	return lldFound
}

var (
	lldOnce  sync.Once
	lldFound bool
)

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

// SanitizerClang is the clang an ASan/UBSan build runs with the same argv as
// ClangCommand, and the files a sanitized program needs beside it at run
// time. Elsewhere it is the host's clang and nothing. On Windows MSYS2's
// UCRT64 clang ships no sanitizer runtime for the mingw target; the CLANG64
// environment's clang (the same LLVM release, the same UCRT C runtime) does,
// and with ClangCommand's --target/--sysroot it builds against the UCRT64
// libraries the program links, taking only the sanitizer runtime from its
// own tree; it links libgcc's unwinder, as UCRT64's code does (its own
// default, libunwind, is not in that tree). The runtime DLL and the libc++
// it imports are copied beside the program. ok is false when no sanitizer
// clang is installed.
func SanitizerClang() (clang string, extraArgs, runtimeFiles []string, ok bool) {
	if runtime.GOOS != "windows" {
		path, err := exec.LookPath("clang")
		return path, nil, nil, err == nil
	}
	extraArgs = []string{"--unwindlib=libgcc"}
	bin := filepath.Join(filepath.Dir(windowsSysroot()), "clang64", "bin")
	clang = filepath.Join(bin, "clang.exe")
	runtimeFiles = []string{
		filepath.Join(bin, "libclang_rt.asan_dynamic-x86_64.dll"),
		filepath.Join(bin, "libc++.dll"),
	}
	for _, f := range append([]string{clang}, runtimeFiles...) {
		if _, err := os.Stat(f); err != nil {
			return "", nil, nil, false
		}
	}
	return clang, extraArgs, runtimeFiles, true
}

// HostExeSuffix is ".exe" on Windows and "" elsewhere — the suffix a caller
// appends to a default output path that has no extension.
func HostExeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// dropIntendedDuplicates removes lld's "duplicate symbol" warnings from a
// Windows --static link, where the shim's POSIX-named socket and time
// functions deliberately shadow the same names in the static archives and
// Winsock's import library (--allow-multiple-definition, ADR-00772). GNU ld
// said nothing about them; lld lists each one. Any other output is kept.
func dropIntendedDuplicates(b []byte) []byte {
	if runtime.GOOS != "windows" || !staticLinkMode || !bytes.Contains(b, []byte("duplicate symbol")) {
		return b
	}
	var out bytes.Buffer
	skipping := false
	for _, line := range strings.SplitAfter(string(b), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.Contains(t, "warning: duplicate symbol:"):
			skipping = true
			continue
		case skipping && (strings.HasPrefix(t, ">>>") || t == ""):
			continue
		}
		skipping = false
		out.WriteString(line)
	}
	return out.Bytes()
}
