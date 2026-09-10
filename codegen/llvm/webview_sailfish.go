package llvm

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// webview_sailfish.go — the `-webview=sailfish` backend plumbing (TDD-00146
// Stage 3): the embedded C++ shim source and the target link recipe.
//
// Unlike the system backend (a single amalgamated .cc compiled straight on the
// clang line), the Sailfish shim is a Qt program: it uses Qt signals/slots to
// receive the page→native async message, so the build must run Qt's **moc** over
// the source and compile the generated meta-object alongside it. moc is the
// Sailfish target's own aarch64 `moc` (frozen Qt 5.6.3) — it emits
// architecture-independent C++, but its meta-object revision must match the 5.6
// QObject runtime, so a host moc of a different Qt version cannot be substituted.
// The shim `#include`s the generated `webview_sailfish.moc` at its end (the
// standard single-file idiom), so the build writes that file next to the source
// before compiling.
//
// Verified (2026-09-09, arm64 Linux container against a real Sailfish 5.1.0.11
// -devel sysroot built by docker/Dockerfile.sfos-sysroot): the shim compiles
// (moc 5.6.3 → meta-object; g++ -c → object) and links into an executable with
// the recipe below. On-device windowed run (Wayland/lipstick env, frame-script
// URL scheme) is the remaining runtime-verification tier.

//go:embed webviewsrc/webview_sailfish.cpp
var webviewSailfishSource string

// SailfishWebviewShimSource returns the C++ shim implementing the webview_* ABI
// over Sailfish's Gecko (embedlite/qtmozembed) RawWebView. The build must run
// moc over it first (see the file comment / RunSailfishMoc).
func SailfishWebviewShimSource() string { return webviewSailfishSource }

// sailfishWebviewPkgs are the pkg-config modules the shim links. sailfishwebengine
// transitively drags in qt5embedwidget, xpcomglue, xul, and the nspr trio plus
// the xulrunner rpath, so naming these five resolves the whole chain.
var sailfishWebviewPkgs = []string{"Qt5Core", "Qt5Gui", "Qt5Qml", "Qt5Quick", "sailfishwebengine"}

// LocateWebviewSailfish returns the clang cflags/libs to compile+link the
// Sailfish webview shim, resolved from the target sysroot's pkg-config metadata
// (PKG_CONFIG_SYSROOT_DIR prefixes every -I/-L into the sysroot). It requires a
// cross build with an explicit --sysroot (the recipe is target-specific, never
// the host); an empty sysroot is a clean error.
func LocateWebviewSailfish(sysroot string) (cflags, libs []string, err error) {
	if sysroot == "" {
		return nil, nil, fmt.Errorf("-webview=sailfish requires --sysroot pointing at a Sailfish -devel sysroot (build one with docker/Dockerfile.sfos-sysroot)")
	}
	pcPath := filepath.Join(sysroot, "usr", "lib64", "pkgconfig") + string(os.PathListSeparator) +
		filepath.Join(sysroot, "usr", "share", "pkgconfig")
	env := append(os.Environ(),
		"PKG_CONFIG_SYSROOT_DIR="+sysroot,
		"PKG_CONFIG_LIBDIR="+pcPath,
	)
	run := func(flag string) ([]string, error) {
		args := append([]string{flag}, sailfishWebViewPkgArgs()...)
		cmd := exec.Command("pkg-config", args...)
		cmd.Env = env
		out, e := cmd.Output()
		if e != nil {
			return nil, fmt.Errorf("pkg-config %s failed against sysroot %s: %w (install the Qt5/webview -devel packages — docker/Dockerfile.sfos-sysroot)", flag, sysroot, e)
		}
		return strings.Fields(strings.TrimSpace(string(out))), nil
	}
	if cflags, err = run("--cflags"); err != nil {
		return nil, nil, err
	}
	if libs, err = run("--libs"); err != nil {
		return nil, nil, err
	}
	// The frozen Qt 5.6 headers use `T(0) < T(-1)` enum constexpr comparisons in
	// qtypetraits.h, which modern clang flags as -Wenum-constexpr-conversion (an
	// error by default; gcc doesn't diagnose it). Silence it so a current clang
	// can compile the shim against the 5.6 sysroot.
	cflags = append(cflags, "-Wno-enum-constexpr-conversion")

	// xulrunner's libraries live behind *absolute* symlinks that escape the
	// sysroot under --sysroot, so ld can't follow them: the devel `-L…/lib` is a
	// symlink to `…/sdk/lib` (which holds the static libxpcomglue.a), and
	// sdk/lib/libxul.so is in turn a symlink to the runtime dir that holds the
	// real libxul.so. Two rewrites cover both:
	//   1. resolve a sysroot -L that is an absolute symlink back into the sysroot
	//      (reaches sdk/lib → libxpcomglue.a);
	//   2. derive a -L from each -rpath (the runtime dir naming the real .so files
	//      → libxul.so), version-agnostically.
	var extraL []string
	for i, f := range libs {
		if strings.HasPrefix(f, "-L") {
			dir := f[2:]
			if fi, e := os.Lstat(dir); e == nil && fi.Mode()&os.ModeSymlink != 0 {
				if tgt, e2 := os.Readlink(dir); e2 == nil && filepath.IsAbs(tgt) {
					libs[i] = "-L" + filepath.Join(sysroot, tgt)
				}
			}
			continue
		}
		if strings.HasPrefix(f, "-Wl,-rpath,") {
			rp := strings.TrimPrefix(f, "-Wl,-rpath,")
			if filepath.IsAbs(rp) {
				extraL = append(extraL, "-L"+filepath.Join(sysroot, rp))
			}
		}
	}
	libs = append(libs, extraL...)
	// The shim is C++ (std::map/std::string/std::function); link the C++ runtime,
	// exactly as the system Linux backend does. Sailfish uses gcc's libstdc++.
	libs = append(libs, "-lstdc++")
	return cflags, libs, nil
}

func sailfishWebViewPkgArgs() []string { return sailfishWebviewPkgs }

// SailfishMocPath returns the moc binary to run over the shim: the target
// sysroot's own aarch64 moc (usr/lib64/qt5/bin/moc) when present — its meta-object
// revision matches the frozen 5.6 QObject runtime — else a bare "moc" from PATH.
// The sysroot moc is native-executable only on a same-arch Linux build host,
// which is the only place a Sailfish webview build runs (cross-OS webview is
// rejected upstream), so no emulator is involved.
func SailfishMocPath(sysroot string) string {
	if sysroot != "" {
		cand := filepath.Join(sysroot, "usr", "lib64", "qt5", "bin", "moc")
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return "moc"
}

// RunSailfishMoc runs the target's moc over the shim source, writing the
// generated meta-object to webview_sailfish.moc beside it (which the source
// #includes). mocPath is the aarch64 moc from the sysroot (usr/lib64/qt5/bin/moc);
// it emits portable C++, so running it under an emulator or in the target
// container is equivalent. Returned so the build step can invoke it before clang.
//
// The sysroot's moc links the sysroot's Qt libraries, so its LD_LIBRARY_PATH must
// include <sysroot>/usr/lib64 — but *only* for the moc subprocess: exporting it
// process-wide would make the host clang load the target's libstdc++/libtinfo and
// fail. So the path is set on this command's env alone.
func RunSailfishMoc(mocPath, srcPath, sysroot string) error {
	mocOut := filepath.Join(filepath.Dir(srcPath), "webview_sailfish.moc")
	cmd := exec.Command(mocPath, srcPath, "-o", mocOut)
	if sysroot != "" {
		libDir := filepath.Join(sysroot, "usr", "lib64")
		ld := "LD_LIBRARY_PATH=" + libDir
		if prev := os.Getenv("LD_LIBRARY_PATH"); prev != "" {
			ld += string(os.PathListSeparator) + prev
		}
		cmd.Env = append(os.Environ(), ld)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("moc over %s failed: %w\n%s", srcPath, err, out)
	}
	return nil
}
