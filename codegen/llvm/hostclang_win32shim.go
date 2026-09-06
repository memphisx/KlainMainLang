package llvm

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed win32src/win32shim.c
var win32ShimSource string

//go:embed win32src/win32io.c
var win32IOSource string

//go:embed win32src/win32fs.c
var win32FSSource string

//go:embed win32src/win32proc.c
var win32ProcSource string

//go:embed win32src/kml_posix_compat.h
var win32CompatHeader string

// win32ShimObject compiles win32shim.c once per distinct source (keyed by a
// content hash, so a compiler rebuild with a changed shim never links a
// stale object) into a per-user cache directory and returns the object
// path. Errors are returned rather than fatal so a caller can fall back to
// plain clang and let the link report the missing symbols itself.
func win32ShimObjects() ([]string, error) {
	var objs []string
	for _, s := range []struct{ name, src string }{{"win32shim", win32ShimSource}, {"win32io", win32IOSource}, {"win32fs", win32FSSource}, {"win32proc", win32ProcSource}} {
		o, err := win32ShimObject(s.name, s.src)
		if err != nil {
			return nil, err
		}
		objs = append(objs, o)
	}
	return objs, nil
}

func win32ShimObject(name, source string) (string, error) {
	sum := sha256.Sum256([]byte(source))
	tag := hex.EncodeToString(sum[:8])
	dir := win32ShimDir()
	obj := filepath.Join(dir, name+"-"+tag+".o")
	if st, err := os.Stat(obj); err == nil && st.Size() > 0 {
		return obj, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	src := filepath.Join(dir, name+"-"+tag+".c")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		return "", err
	}
	tmp := obj + ".tmp"
	args := append(HostClangArgs(), "-O2", "-c", src, "-o", tmp)
	out, err := exec.Command("clang", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("compiling win32 shim: %v\n%s", err, out)
	}
	if err := os.Rename(tmp, obj); err != nil {
		return "", err
	}
	return obj, nil
}

// win32LinkArgs returns what a Windows link step needs beyond the target
// and sysroot: the shim object plus the system libraries it and the emitted
// IR depend on (bcrypt for the CSPRNG). Returns nil for a compile-only
// (-c) invocation, where an extra object would be an error.
func win32LinkArgs(args []string) []string {
	for _, a := range args {
		if a == "-c" || a == "-fsyntax-only" || a == "-E" {
			return nil
		}
	}
	// winpthread: the IR uses pthread mutexes/condvars (Atomics, workers) and
	// glibc no longer needs -lpthread for them, so the emitters never add it.
	extra := []string{"-lbcrypt", "-lpsapi", "-lwinpthread"}
	if objs, err := win32ShimObjects(); err == nil {
		extra = append(objs, extra...)
	} else {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	return extra
}

// hasArg reports whether args already carries flag (exact match).
func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if strings.EqualFold(a, flag) {
			return true
		}
	}
	return false
}

// win32ShimDir is the per-user cache directory holding the compiled shim
// objects and kml_posix_compat.h; HostClangArgs passes it with -I so the
// embedded C helpers' `#ifdef _WIN32` include resolves.
func win32ShimDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	// One directory per distinct set of embedded shim sources: two compiler
	// builds running side by side (a test binary and a rebuilt CLI, say)
	// must never share a header or object, or one overwrites the other's.
	sum := sha256.Sum256([]byte(win32ShimSource + win32IOSource + win32FSSource + win32ProcSource + win32CompatHeader))
	return filepath.Join(base, "klainmain", "win32shim", hex.EncodeToString(sum[:6]))
}

// win32EnsureCompatHeader writes kml_posix_compat.h into the shim dir when
// missing or stale (content compare, so a compiler rebuild refreshes it).
func win32EnsureCompatHeader() {
	dir := win32ShimDir()
	p := filepath.Join(dir, "kml_posix_compat.h")
	if cur, err := os.ReadFile(p); err == nil && string(cur) == win32CompatHeader {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(p, []byte(win32CompatHeader), 0o644)
}

// win32CompileArgs adds -I<shim dir> when the invocation compiles C/C++
// (the embedded helpers include kml_posix_compat.h); a pure link step gets
// nothing, which keeps clang's unused-argument warning out of test output.
func win32CompileArgs(args []string) []string {
	for _, a := range args {
		if strings.HasSuffix(a, ".c") || strings.HasSuffix(a, ".cc") || strings.HasSuffix(a, ".cpp") {
			win32EnsureCompatHeader()
			return []string{"-I" + win32ShimDir()}
		}
	}
	return nil
}
