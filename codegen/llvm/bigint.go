package llvm

import (
	_ "embed"
	"os/exec"
	"strings"
)

// bigint.go — the selectable bigint backend plumbing (TDD-00074). Each backend
// is a small C file implementing the identical __kml_bigint_* ABI (see
// emit_bigint.go); the emitter is backend-agnostic and only the linked library
// differs. main.go writes the selected source next to the .ll and compiles it
// alongside — the same "one embedded C file, compiled with the program" shape
// gcsrc/gcshim.c uses for -mm=gc.

//go:embed bigintsrc/bigint_tommath.c
var bigIntTommathSource string

//go:embed bigintsrc/bigint_gmp.c
var bigIntGmpSource string

// BigIntBackends are the accepted -bigint values, default (libtommath) first.
var BigIntBackends = []string{"libtommath", "gmp"}

// BigIntBackendSource returns the C source implementing the __kml_bigint_* ABI
// for a backend, and whether the name is known.
func BigIntBackendSource(backend string) (string, bool) {
	switch backend {
	case "", "libtommath":
		return bigIntTommathSource, true
	case "gmp":
		return bigIntGmpSource, true
	}
	return "", false
}

func bigIntLinkLib(backend string) string {
	if backend == "gmp" {
		return "-lgmp"
	}
	return "-ltommath"
}

// LocateBigInt returns the clang cflags/libs to compile and link the selected
// bigint backend. Homebrew installs gmp/libtommath under its prefix
// (/opt/homebrew on Apple Silicon, /usr/local on Intel), off clang's default
// search path — `brew --prefix` resolves it portably without hardcoding either.
// On Linux the distro packages land in default paths, so only the -l flag is
// needed. Mirrors LocateGC's posture for -mm=gc.
func LocateBigInt(backend string) (cflags, libs []string) {
	lib := bigIntLinkLib(backend)
	// The backend wrappers deliberately ignore some mp_*/mpz_* return codes
	// (allocation failure is fatal and unrecoverable here anyway); silence the
	// resulting -Wunused-result noise so the program's own build stays clean.
	base := []string{"-Wno-unused-result"}
	if out, err := exec.Command("brew", "--prefix").Output(); err == nil {
		if prefix := strings.TrimSpace(string(out)); prefix != "" {
			return append(base, "-I"+prefix+"/include"), []string{"-L" + prefix + "/lib", lib}
		}
	}
	return base, []string{lib}
}
