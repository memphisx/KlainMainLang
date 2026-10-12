package llvm

import (
	_ "embed"
	"os/exec"
	"runtime"
	"strings"
)

// The dns module's resolver: Node's cares_wrap.cc over c-ares, a unit of
// its own (c-ares' headers bring the platform's socket headers, which the
// pool's unit keeps out on Windows).
//
//go:embed caressrc/klaincares.c
var caresSource string

// CaresSource returns the resolver's C runtime.
func CaresSource() string { return caresSource }

// ensureCaresNatives compiles the resolver's c-ares natives and links
// libcares: the `@link cares` of lib/native.d.ts. Its completions go
// through the pool's.
func (e *Emitter) ensureCaresNatives() {
	e.ensureNativePool()
	if e.usedCaresNatives {
		return
	}
	e.usedCaresNatives = true
	if e.opts.Target.OS() == "windows" {
		e.requireLink("cares")
		e.requireLink("iphlpapi")
		e.requireLink("ws2_32")
	}
}

// CaresFlags returns the resolver unit's cflags and link flags: c-ares'
// (pkg-config's libcares where it has one) and, on POSIX, libuv's, whose
// loop drives the channels.
func (e *Emitter) CaresFlags() (cflags, libs []string) {
	goos := e.opts.Target.OS()
	cross := e.opts.Target.Triple != ""
	cflags = []string{"-pthread"}
	if goos == "windows" {
		if runtime.GOOS == "windows" && staticLinkMode {
			cflags = append(cflags, "-DCARES_STATICLIB")
		}
		return cflags, nil
	}
	uvflags, _ := LocateUV(goos, cross)
	cflags = append(cflags, uvflags...)
	if cross {
		return cflags, []string{"-lcares"}
	}
	if out, err := exec.Command("pkg-config", "--cflags", "libcares").Output(); err == nil {
		cflags = append(cflags, strings.Fields(strings.TrimSpace(string(out)))...)
	}
	if out, err := exec.Command("pkg-config", "--libs", "libcares").Output(); err == nil {
		libs = strings.Fields(strings.TrimSpace(string(out)))
	}
	if len(libs) == 0 {
		libs = []string{"-lcares"}
	}
	return cflags, libs
}
