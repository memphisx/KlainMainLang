package llvm

import (
	_ "embed"
	"os/exec"
	"strings"
)

// crypto.go — the selectable crypto backend plumbing (TDD-00104). Each backend
// is a C file implementing the identical __kml_crypto_* subtle-crypto ABI
// (see runtime_crypto_subtle.go); the emitter is backend-agnostic and only
// what gets compiled+linked differs. main.go writes the selected source next
// to the .ll and compiles it alongside — the same shape as -bigint
// (bigint.go) and -mm=gc (gcsrc/gcshim.c).

//go:embed cryptosrc/crypto_openssl.c
var cryptoOpenSSLSource string

//go:embed cryptosrc/crypto_commoncrypto.c
var cryptoCommonCryptoSource string

// CryptoBackends are the accepted -crypto values, default (openssl) first.
var CryptoBackends = []string{"openssl", "commoncrypto"}

// CryptoBackendSource returns the C source implementing the __kml_crypto_*
// subtle-crypto ABI for a backend, and whether the name is known.
func CryptoBackendSource(backend string) (string, bool) {
	switch backend {
	case "", "openssl":
		return cryptoOpenSSLSource, true
	case "commoncrypto":
		return cryptoCommonCryptoSource, true
	}
	return "", false
}

// LocateCrypto returns the clang cflags/libs to compile and link the selected
// crypto backend. openssl: pkg-config first (Linux distros and any properly
// configured install), then `brew --prefix openssl@3` (Homebrew's OpenSSL is
// keg-only — deliberately off clang's default search path, so the generic
// `brew --prefix` root LocateBigInt uses is not enough here), then a bare
// -lcrypto. commoncrypto: the symmetric primitives live in libSystem (no -l
// at all); SecKey-based RSA/EC needs Security + CoreFoundation frameworks.
func LocateCrypto(backend string) (cflags, libs []string) {
	if backend == "commoncrypto" {
		return nil, []string{"-framework", "Security", "-framework", "CoreFoundation"}
	}
	if out, err := exec.Command("pkg-config", "--cflags", "libcrypto").Output(); err == nil {
		cflags = strings.Fields(strings.TrimSpace(string(out)))
		if out, err := exec.Command("pkg-config", "--libs", "libcrypto").Output(); err == nil {
			return cflags, strings.Fields(strings.TrimSpace(string(out)))
		}
	}
	if out, err := exec.Command("brew", "--prefix", "openssl@3").Output(); err == nil {
		if prefix := strings.TrimSpace(string(out)); prefix != "" {
			return []string{"-I" + prefix + "/include"}, []string{"-L" + prefix + "/lib", "-lcrypto"}
		}
	}
	return nil, []string{"-lcrypto"}
}
