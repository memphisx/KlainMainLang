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

// OSInfoLibs returns the extra link libraries the sidecar needs on this host:
// iphlpapi (GetAdaptersAddresses) on Windows — inet_ntop comes from the win32
// shim, which already owns that symbol — nothing elsewhere (uname/getifaddrs/
// getpwuid live in libc).
func OSInfoLibs() []string {
	if targetGOOS() == "windows" {
		return []string{"-liphlpapi"}
	}
	return nil
}

// UsesOSInfo reports whether the program reached one of the sidecar-backed
// os/process.env entry points, so the build compiles+links osinfo.c.
func (e *Emitter) UsesOSInfo() bool { return e.usedOSInfo }

// ensureOSInfo declares the sidecar's entry points exactly once and marks the
// program as needing the C file compiled in.
func (e *Emitter) ensureOSInfo() {
	if e.usedOSInfo {
		return
	}
	e.usedOSInfo = true
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`declare ptr @__kml_env_entries()`)
	e.emitGlobal(`declare ptr @__kml_os_type()`)
	e.emitGlobal(`declare ptr @__kml_os_release()`)
	e.emitGlobal(`declare ptr @__kml_os_version()`)
	e.emitGlobal(`declare ptr @__kml_os_machine()`)
	e.emitGlobal(`declare double @__kml_os_uptime()`)
	e.emitGlobal(`declare void @__kml_os_loadavg(ptr)`)
	e.emitGlobal(`declare i64 @__kml_os_avail_parallelism()`)
	e.emitGlobal(`declare i32 @__kml_os_userinfo(ptr, ptr, ptr, ptr, ptr)`)
	e.emitGlobal(`declare ptr @__kml_os_netifs(ptr)`)
}
