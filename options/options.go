// Package options is the one set of compile modes (TDD-00230 P2.6). The CLI
// builds it once from its flags; the binder, the checker and codegen all read
// the same value, so a program and each of its dynamic-import islands are
// compiled under identical modes.
package options

import "runtime"

// Options are the compile modes. The zero value is every default.
type Options struct {
	// Compat is the compatibility axis (TDD-00075): "strict" (default, also
	// "") or "js", best-effort JS-faithful.
	Compat string
	// NoAny bans the any/unknown escape hatch (TDD-00209). It has no meaning
	// under -compat=js, and the CLI never sets it there.
	NoAny bool
	// Decorators is the decorator dialect (TDD-00161): "experimental"
	// (default, also "") or "standard" (TC39).
	Decorators string
	// EmitDecoratorMetadata emits design:type/paramtypes/returntype metadata
	// for decorated members under the experimental dialect.
	EmitDecoratorMetadata bool
	// MemMode is the memory-management mode: "manual" (default, also ""),
	// "gc" or "auto" (TDD-00173).
	MemMode string
	// DynamicImport is the import() backend: "eager" (default, also "") or
	// "lazy", shared-library islands (TDD-00056).
	DynamicImport string
	// Regex is the RegExp dialect (TDD-00067); "" resolves to the highest
	// implemented ES stage.
	Regex string
	// BigInt, Crypto and Webview name the backend libraries linked for
	// bigint (TDD-00074), crypto.subtle (TDD-00104) and klain:webview
	// (TDD-00144); "" is each one's default.
	BigInt, Crypto, Webview string
	// OptimizeMemory turns on the allocation optimizations (TDD-00134).
	OptimizeMemory bool
	// Finalizers selects the FinalizationRegistry exit diagnostics
	// (TDD-00163): "off" (default, also "") or "report".
	Finalizers string
	// Target is the platform the program is compiled for (--target,
	// --sysroot, TDD-00146); the zero value is the host.
	Target Target
}

// Target is a compile target: a clang triple and the sysroot holding its
// headers and libraries, with the triple's OS and architecture in Go's
// spelling ("" when the triple's field is not one this compiler knows).
type Target struct {
	Triple, Sysroot string
	GOOS, GOARCH    string
}

// OS is the target's operating system in Go's spelling: the triple's, or
// the host's when building for the host.
func (t Target) OS() string {
	if t.GOOS != "" {
		return t.GOOS
	}
	return runtime.GOOS
}

// Arch is the target's architecture in Go's spelling, as OS.
func (t Target) Arch() string {
	if t.GOARCH != "" {
		return t.GOARCH
	}
	return runtime.GOARCH
}

// CompatJS reports the -compat=js lane.
func (o Options) CompatJS() bool { return o.Compat == "js" }
