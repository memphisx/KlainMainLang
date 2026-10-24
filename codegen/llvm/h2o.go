package llvm

import (
	_ "embed"
	"os/exec"
	"strings"
)

// klain:http's h2o backend (TDD-00255 Stage 2): libh2o's libuv build on the
// program's loop, a unit of its own (h2o's headers bring OpenSSL's and
// libuv's).
//
//go:embed h2osrc/klainh2o.c
var h2oSource string

// H2OSource returns the h2o backend's C runtime.
func H2OSource() string { return h2oSource }

// ensureH2ONatives compiles the h2o backend's natives and links libh2o: the
// `@link h2o` of lib/native.d.ts.
func (e *Emitter) ensureH2ONatives() {
	e.ensureNativePool()
	e.usedH2ONatives = true
}

// LocateH2O returns libh2o's cflags and link flags (pkg-config's libh2o,
// else -lh2o), and whether the library is there: the libuv build, which
// Ubuntu ships as libh2o-dev. Homebrew's h2o has only libh2o-evloop, h2o's
// own event loop, so macOS has none.
func LocateH2O() (cflags, libs []string, ok bool) {
	if out, err := exec.Command("pkg-config", "--cflags", "libh2o").Output(); err == nil {
		cflags = strings.Fields(strings.TrimSpace(string(out)))
		if out, err := exec.Command("pkg-config", "--libs", "libh2o").Output(); err == nil {
			libs = strings.Fields(strings.TrimSpace(string(out)))
		}
		return cflags, libs, true
	}
	if out, err := exec.Command("cc", "-print-file-name=libh2o.so").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "libh2o.so" && p != "" {
			return nil, []string{"-lh2o"}, true
		}
	}
	return nil, nil, false
}

// H2OFlags returns the h2o unit's cflags and link flags: libh2o's, libuv's
// and OpenSSL's (h2o's sockets are TLS-capable).
func (e *Emitter) H2OFlags() (cflags, libs []string) {
	goos := e.opts.Target.OS()
	cross := e.opts.Target.Triple != ""
	uvflags, uvlibs := LocateUV(goos, cross)
	hc, hl, ok := LocateH2O()
	if !ok || cross {
		hl = []string{"-lh2o"}
	}
	cflags = append(append([]string{"-pthread"}, uvflags...), hc...)
	libs = append(append(hl, uvlibs...), "-lssl", "-lcrypto")
	return cflags, libs
}
