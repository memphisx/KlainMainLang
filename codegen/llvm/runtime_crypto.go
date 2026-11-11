package llvm

import _ "embed"

// crypto.* randomness (TDD-00240) lives in cryptorandsrc/cryptorand.c: a real
// CSPRNG, deliberately not the source Math.random()'s portable fallback uses
// (plain C89 rand(), not cryptographically secure) — arc4random_buf on the
// BSDs and macOS, getrandom() on Linux.

//go:embed cryptorandsrc/cryptorand.c
var cryptoRandSource string

// CryptoRandSource is the randomness runtime's C source.
func CryptoRandSource() string { return cryptoRandSource }

// CryptoRandCFlags: cryptorand.c is compiled the same way for every
// program; the link drops what a program does not use (DeadStripFlags).
func (e *Emitter) CryptoRandCFlags() []string { return DeadStripFlags(e.exportsRuntime()) }
