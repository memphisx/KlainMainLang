package llvm

import (
	_ "embed"
)

// osinfo_c.go — the embedded host-information runtime (osinfosrc/osinfo.c)
// behind os.type/release/version/machine/uptime/loadavg/userInfo/
// availableParallelism/networkInterfaces and `process.env` enumeration.
// Compiled alongside the program only when one of those is used.

//go:embed osinfosrc/osinfo.c
var osinfoSource string

// OSInfoSource returns the C source of the host-information sidecar.
func OSInfoSource() string { return osinfoSource }

//go:embed osinfosrc/osinfo_bags.c
var osinfoBagsSource string

// OSInfoBagsSource is the C source of process.env's and
// os.networkInterfaces()'s dynamic objects.
func OSInfoBagsSource() string { return osinfoBagsSource }

// OSInfoLibs returns the extra link libraries the sidecar needs on this host:
// iphlpapi (GetAdaptersAddresses) on Windows — inet_ntop comes from the win32
// shim, which already owns that symbol — nothing elsewhere (uname/getifaddrs/
// getpwuid live in libc).
func (e *Emitter) OSInfoLibs() []string {
	if e.opts.Target.OS() == "windows" {
		return []string{"-liphlpapi"}
	}
	return nil
}
