package llvm

import (
	"os/exec"
	"strings"
)

// LocateGC returns the clang cflags/libs needed to compile against and link
// the Boehm-Demers-Weiser garbage collector (libgc/bdw-gc), for -mm=gc
// builds. Prefers pkg-config, since Homebrew's bdw-gc formula installs its
// headers/lib under a versioned Cellar path that isn't on clang's default
// search path (and hardcoding /opt/homebrew would break Intel Macs, which
// use /usr/local instead) — pkg-config resolves that portably on any
// machine that has the package installed, on any prefix. Falls back to a
// bare -lgc when pkg-config (or its .pc file) isn't available, since Linux
// distro packages like libgc-dev typically install to clang's default
// system search paths already.
func LocateGC() (cflags []string, libs []string, err error) {
	if _, err := exec.LookPath("pkg-config"); err != nil {
		return nil, []string{"-lgc"}, nil
	}

	// "bdw-gc" is Homebrew's formula/pkg-config name; "gc" shows up on some
	// Linux distro packagings of the same library.
	for _, name := range []string{"bdw-gc", "gc"} {
		if exec.Command("pkg-config", "--exists", name).Run() != nil {
			continue
		}
		cflagsOut, err := exec.Command("pkg-config", "--cflags", name).Output()
		if err != nil {
			continue
		}
		libsOut, err := exec.Command("pkg-config", "--libs", name).Output()
		if err != nil {
			continue
		}
		return strings.Fields(string(cflagsOut)), strings.Fields(string(libsOut)), nil
	}

	return nil, []string{"-lgc"}, nil
}
