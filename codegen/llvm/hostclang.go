package llvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	return full
}

// ClangCommand is HostClangArgv as a ready-to-run command.
func ClangCommand(args ...string) *exec.Cmd {
	return exec.Command("clang", HostClangArgv(args...)...)
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
