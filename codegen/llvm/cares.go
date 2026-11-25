package llvm

import (
	_ "embed"
	"os/exec"
	"path/filepath"
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
	if staticLinkMode {
		libs = caresStaticLibs(libs)
	}
	return cflags, libs
}

// caresStaticLibs names the static archive where its file is not
// libcares.a: Debian and Ubuntu install c-ares' as libcares_static.a,
// which -lcares does not find under --static.
func caresStaticLibs(libs []string) []string {
	dirs := []string{}
	if out, err := exec.Command("pkg-config", "--variable=libdir", "libcares").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			dirs = append(dirs, d)
		}
	}
	for _, l := range libs {
		if strings.HasPrefix(l, "-L") {
			dirs = append(dirs, l[2:])
		}
	}
	multi, _ := filepath.Glob("/usr/lib/*-linux-gnu")
	dirs = append(dirs, multi...)
	dirs = append(dirs, "/usr/local/lib", "/usr/lib")
	for _, d := range dirs {
		if fileExists(filepath.Join(d, "libcares.a")) {
			return libs
		}
		if fileExists(filepath.Join(d, "libcares_static.a")) {
			out := make([]string, 0, len(libs))
			for _, l := range libs {
				if l == "-lcares" {
					l = "-l:libcares_static.a"
				}
				out = append(out, l)
			}
			return out
		}
	}
	return libs
}
