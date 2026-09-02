package llvm

import (
	"fmt"
	"runtime"
)

// CSource is one embedded C runtime file that must be compiled and linked
// alongside the emitted LLVM IR when the program uses the corresponding
// feature. Name is a short suffix (e.g. "dtoa") used to build a per-program
// temp filename; Content is the C source; CFlags/Libs are the extra clang
// flags and `-l` libraries that file needs.
type CSource struct {
	Name    string
	Content string
	CFlags  []string
	Libs    []string
	// Ext is the source-file extension clang compiles this member as, without
	// the leading dot. Empty means "c" (the overwhelming default); the one
	// C++14 member — the webview binding — sets "cc" so clang's driver picks
	// the C++ frontend. SrcExt() resolves the default.
	Ext string
}

// SrcExt returns the source-file extension for this member (without the dot),
// defaulting to "c" when unset — the single point every writer (main.go, the
// conformance runner, the test build path) consults so a .cc member can never
// be written as .c by accident.
func (c CSource) SrcExt() string {
	if c.Ext == "" {
		return "c"
	}
	return c.Ext
}

// EmbeddedCSources returns every embedded C runtime file this program's emitted
// IR depends on, based on which features it actually used — the single source
// of truth shared by the CLI driver (main.go) and the conformance runner, so
// the two can never drift on which .c files get linked (a drift that silently
// under-counted conformance: any test needing dtoa/bigint/JSON/etc. failed to
// link in the runner even though the CLI compiled it fine). Order matches the
// CLI's historical order. Returns an error only for an unknown backend name.
func (e *Emitter) EmbeddedCSources() ([]CSource, error) {
	var out []CSource
	if e.UsesBigInt() {
		backend := e.BigIntBackend()
		src, ok := BigIntBackendSource(backend)
		if !ok {
			return nil, fmt.Errorf("bigint: unknown backend %q", backend)
		}
		cflags, libs := LocateBigInt(backend)
		out = append(out, CSource{"bigint", src, cflags, libs, ""})
	}
	if e.UsesCrypto() {
		backend := e.CryptoBackend()
		src, ok := CryptoBackendSource(backend)
		if !ok {
			return nil, fmt.Errorf("crypto: unknown backend %q", backend)
		}
		cflags, libs := LocateCrypto(backend)
		out = append(out, CSource{"crypto", src, cflags, libs, ""})
	}
	if e.UsesTLS() {
		if e.CryptoBackend() == "commoncrypto" {
			return nil, fmt.Errorf("tls: the `tls` module requires the OpenSSL crypto backend")
		}
		cflags, libs := LocateTLS()
		out = append(out, CSource{"tls", TLSClientSource(), cflags, libs, ""})
	}
	if e.UsesHTTP2() {
		cflags, libs := LocateHTTP2()
		out = append(out, CSource{"http2", HTTP2ServerSource(), cflags, libs, ""})
	}
	if e.UsesSpawnSync() {
		out = append(out, CSource{"spawnsync", SpawnSyncSource(), nil, nil, ""})
	}
	if e.UsesIPC() {
		out = append(out, CSource{"ipc", IPCSource(), nil, nil, ""})
	}
	if e.UsesBufferCodecs() {
		out = append(out, CSource{"bufcodecs", BufferCodecsSource(), nil, nil, ""})
	}
	if e.usesDynamicImport && e.dynamicImportMode == "lazy" {
		// The dlopen island loader (TDD-00056). Linux resolves dlopen from
		// libdl (-ldl); macOS ships it in libSystem, so no extra lib there.
		var libs []string
		if runtime.GOOS != "darwin" {
			libs = []string{"-ldl"}
		}
		out = append(out, CSource{"dynimport", DynImportShimSource(), nil, libs, ""})
	}
	if e.UsesJSONParse() {
		out = append(out, CSource{"jsontree", JSONParseTreeSource(), nil, nil, ""})
	}
	if e.UsesDynJSON() {
		out = append(out, CSource{"dynjson", DynJSONSource(), nil, nil, ""})
	}
	if e.UsesURLPattern() {
		out = append(out, CSource{"urlpattern", URLPatternSource(), nil, nil, ""})
	}
	if e.UsesFloatFmt() {
		out = append(out, CSource{"dtoa", DtoaSource(), nil, nil, ""})
	}
	if e.UsesWebview() {
		cflags, libs, err := LocateWebview()
		if err != nil {
			return nil, err
		}
		out = append(out, CSource{"webview", WebviewSource(), cflags, libs, "cc"})
	}
	if e.UsesTtyShim() {
		// TDD-00031: termios/ioctl/raw-read shim. No extra libs — termios and
		// ioctl live in libc on both platforms.
		out = append(out, CSource{"tty", TTYShimSource(), nil, nil, ""})
	}
	if e.UsesTui() {
		// TDD-00150 Stage 1: the klain:tui painter runtime (tui.c) plus the
		// vendored Yoga flexbox engine, which is pre-compiled to objects and
		// linked in — see yogaCSources for why (C++20 can't share the C line).
		tui, err := yogaCSources()
		if err != nil {
			return nil, err
		}
		out = append(out, tui...)
	}
	if e.UsesSync() {
		// TDD-00143: the klain:sync GMP goroutine runtime. Needs pthread; the
		// macOS ucontext calls are deprecated-but-functional (silence the
		// warning). Under -mm=gc the runtime registers each M thread and each
		// goroutine stack with Boehm — gated by KLAINSYNC_GC; the -lgc link is
		// already added globally in gc mode (LocateGC).
		cflags := []string{"-pthread", "-Wno-deprecated-declarations"}
		if e.isGCMode() {
			cflags = append(cflags, "-DKLAINSYNC_GC=1")
		}
		out = append(out, CSource{"klainsync", SyncSource(), cflags, nil, ""})
	}
	if e.UsesEmbeddedAssets() {
		// The embedded static server needs pthread; -pthread is already added
		// by the CLI when workers are used, but a serve-only program needs it too.
		out = append(out, CSource{"embedassets", EmbedAssetsSource(), []string{"-pthread"}, nil, ""})
	}
	return out, nil
}
