package llvm

import (
	_ "embed"
	"os/exec"
	"strings"
)

// http2.go — lib/node/http2.ts's nghttp2 natives.

//go:embed http2src/h2node.c
var h2NodeSource string

// H2NodeSource returns the nghttp2 session natives lib/node/http2.ts calls
// (`@link http2`).
func H2NodeSource() string { return h2NodeSource }

// UsesH2Node reports whether the program links the http2 module's natives.
func (e *Emitter) UsesH2Node() bool { return e.usedH2Node }

// ensureH2Natives compiles h2node.c in; it reports strings through the
// pool's lastString.
func (e *Emitter) ensureH2Natives() {
	e.ensureNativePool()
	e.usedH2Node = true
}

// LocateHTTP2 returns the clang cflags/libs to compile and link http2src/h2node.c:
// libnghttp2's include path via pkg-config, plus -lnghttp2 (added via
// requireLink so it also flows through LinkLibs). Falls back to a bare -lnghttp2
// when pkg-config isn't present (Homebrew/Linux both ship the .pc).
func LocateHTTP2() (cflags, libs []string) {
	if out, err := exec.Command("pkg-config", "--cflags", "libnghttp2").Output(); err == nil {
		cflags = strings.Fields(strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("pkg-config", "--libs", "libnghttp2").Output(); err == nil {
		libs = strings.Fields(strings.TrimSpace(string(out)))
	}
	if len(libs) == 0 {
		libs = []string{"-lnghttp2"}
	}
	return cflags, libs
}
