package llvm

import (
	_ "embed"
	"os/exec"
	"strings"
)

// loopsrc/loop.c is the event loop (TDD-00244); ensureHTTPRuntime links it.

//go:embed loopsrc/loop.c
var loopSource string

// LoopSource is the event loop's C source.
func LoopSource() string { return layoutHeader() + loopSource }

// httpsrvsrc/httpsrv.c is the HTTP server's fixed runtime (TDD-00244 Stage
// 5), linked with the loop.

//go:embed httpsrvsrc/httpsrv.c
var httpSrvSource string

// HTTPSrvSource is the HTTP server runtime's C source.
func HTTPSrvSource() string { return httpSrvSource }

// LocateUV returns the cflags and libs that compile and link against libuv:
// pkg-config's, or a bare -luv. On Windows (TDD-00252) the ucrt64 sysroot's
// libuv, static under --static like the other feature libraries, with the
// system libraries its libuv.pc names. A cross build takes a bare -luv, which
// --sysroot resolves: the host's pkg-config names the host's copy.
func LocateUV(goos string, cross bool) (cflags, libs []string) {
	if goos == "windows" {
		uv := "-luv"
		if staticLinkMode {
			uv = "-l:libuv.a"
		}
		return nil, []string{uv, "-lpsapi", "-luser32", "-ladvapi32", "-liphlpapi",
			"-luserenv", "-lws2_32", "-ldbghelp", "-lole32", "-lshell32"}
	}
	if cross {
		return nil, []string{"-luv"}
	}
	if out, err := exec.Command("pkg-config", "--cflags", "libuv").Output(); err == nil {
		cflags = strings.Fields(strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("pkg-config", "--libs", "libuv").Output(); err == nil {
		// -lpthread -lm come along already; repeating them warns
		for _, f := range strings.Fields(strings.TrimSpace(string(out))) {
			if f != "-lpthread" && f != "-lm" {
				libs = append(libs, f)
			}
		}
	}
	if len(libs) == 0 {
		libs = []string{"-luv"}
	}
	return cflags, libs
}
