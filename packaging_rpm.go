package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// packaging_rpm.go — TDD-00146 Stage 2: emit an RPM package around a compiled
// binary, the distribution shape for Sailfish OS (and RPM Linux generally).
// Two naming modes:
//   - `-package=rpm`          plain package `<slug>`, binary at /usr/bin/<slug>
//   - `-package=rpm:harbour`  Sailfish Harbour rules: package + binary named
//                             `harbour-<slug>`, GUI desktop/icon under the
//                             sanctioned paths when an icon is supplied, and
//                             non-allowlisted libraries (pcre2, and bdw-gc
//                             under -mm=gc) bundled privately under
//                             /usr/share/harbour-<name>/lib with an rpath —
//                             the sanctioned Harbour pattern.
// Unlike the .app/.desktop host bundles, RPM packaging is not host-gated: it is
// a *target* package, produced alongside a `--target` cross build. The `.spec`
// is always written; `rpmbuild` is driven only when it is on PATH.

// harbourBundledLibs are the link libraries NOT on the Sailfish Harbour
// allowed-libraries list, so a Harbour store app must ship them app-privately
// (under /usr/share/harbour-<name>/lib, reached via an rpath) rather than rely
// on the system. libcurl, OpenSSL, libstdc++, pthread, and libm are all
// Harbour-allowed and stay system libraries. bdw-gc under `-mm=gc` is also not
// allowed, but it is linked via LocateGC (a bare `-lgc`/pkg-config flag) rather
// than the requireLink list, so it never appears in LinkLibs and is folded in
// by gcMode in harbourLibsToBundle instead — its SONAME is libgc.so.N, so the
// map key that findSonameInSysroot resolves is "gc".
var harbourBundledLibs = map[string]bool{
	"pcre2-8": true,
}

// harbourLibsToBundle filters a program's link libraries to the ones a Harbour
// app must bundle privately, preserving order and de-duplicating. gcMode is the
// `-mm=gc` flag: when set, bdw-gc (libgc) is appended, since it is linked via
// LocateGC rather than requireLink and so is absent from linkLibs even though it
// is off the Harbour allowed-libraries list.
func harbourLibsToBundle(linkLibs []string, gcMode bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range linkLibs {
		if harbourBundledLibs[l] && !seen[l] {
			out = append(out, l)
			seen[l] = true
		}
	}
	if gcMode && !seen["gc"] {
		out = append(out, "gc")
	}
	return out
}

// harbourRpathDir is the on-target directory a Harbour app's private libraries
// live in, and the rpath the binary is linked with. %{_datadir} is /usr/share.
func harbourRpathDir(pkgName string) string {
	return "/usr/share/" + pkgName + "/lib"
}

// resolvePackageOptsRPM fills defaults for the RPM path and validates the icon.
// Unlike the host-bundle resolver, the icon rule is host-independent (a Harbour
// app icon is a .png) because this is a target package, not a host artifact.
// AppID is unused by RPM, so it is left empty.
func resolvePackageOptsRPM(outBin, name, version, icon string) (packageOpts, error) {
	opts := packageOpts{AppName: name, Version: version, IconSrc: icon}
	if opts.AppName == "" {
		opts.AppName = filepath.Base(outBin)
	}
	if opts.Version == "" {
		opts.Version = "1.0.0"
	}
	if opts.IconSrc != "" {
		if _, err := os.Stat(opts.IconSrc); err != nil {
			return opts, fmt.Errorf("-app-icon: file not found: %s", opts.IconSrc)
		}
		if strings.ToLower(filepath.Ext(opts.IconSrc)) != ".png" {
			return opts, fmt.Errorf("-app-icon for -package=rpm:harbour must be a .png (got %s)", filepath.Ext(opts.IconSrc))
		}
	}
	return opts, nil
}

// rpmArch maps the build target to the RPM architecture tag. For a cross build
// it is the arch field of the triple (aarch64-meego-linux-gnu → aarch64); for a
// host build it is runtime.GOARCH mapped to the RPM spelling.
func rpmArch(crossTriple string) string {
	if crossTriple != "" {
		arch := crossTriple
		if i := strings.Index(arch, "-"); i >= 0 {
			arch = arch[:i]
		}
		return arch
	}
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64"
	case "amd64":
		return "x86_64"
	default:
		return runtime.GOARCH
	}
}

// rpmPackageName is the package (and installed-binary) name: harbour-<slug> in
// Harbour mode, else the bare slug. Harbour requires this prefix for both the
// package and the binary.
func rpmPackageName(opts packageOpts, harbour bool) string {
	slug := appSlug(opts.AppName)
	if harbour {
		return "harbour-" + slug
	}
	return slug
}

// sonameRe matches a versioned SONAME symlink, e.g. libpcre2-8.so.0 — one
// numeric component after .so (not the full libpcre2-8.so.0.14.0 realpath, and
// not the bare libpcre2-8.so dev symlink).
var sonameRe = regexp.MustCompile(`\.so\.[0-9]+$`)

// findSonameInSysroot locates the runtime SONAME file for a link library inside
// a sysroot (searching the RPM lib64 dirs first, then lib), returning the file's
// basename — which is exactly the binary's DT_NEEDED, so shipping a file of that
// name in the rpath dir satisfies the loader. libSearchRoot is the --sysroot, or
// "/" for a host build.
func findSonameInSysroot(libSearchRoot, lib string) (string, string, error) {
	if libSearchRoot == "" {
		libSearchRoot = "/"
	}
	for _, d := range []string{"usr/lib64", "lib64", "usr/lib", "lib"} {
		dir := filepath.Join(libSearchRoot, d)
		matches, _ := filepath.Glob(filepath.Join(dir, "lib"+lib+".so.*"))
		for _, m := range matches {
			if sonameRe.MatchString(m) {
				return filepath.Base(m), m, nil
			}
		}
	}
	return "", "", fmt.Errorf("could not find a SONAME for lib%s (lib%s.so.N) under %s — the sysroot needs the library present to bundle it", lib, lib, libSearchRoot)
}

// buildRPMSpec renders a `.spec` for a *prebuilt* binary — there is no compile
// step, so %build is empty and %install just drops the binary (and, for Harbour,
// an optional .desktop + icon and any privately-bundled libraries) into the
// buildroot. Pure: no fs/exec. Source indices are assigned in order
// (0 = binary, then icon/desktop, then bundled libs) so the %{SOURCEn}
// references stay in sync. `debug_package %{nil}` disables the debuginfo
// subpackage rpmbuild would otherwise try (and fail) to synthesize from a
// stripped, prebuilt binary.
func buildRPMSpec(pkgName, version, summary, license, arch, exeSrc string, harbour bool, iconSrc, desktopSrc string, bundledSonames []string) string {
	var b strings.Builder
	b.WriteString("%global debug_package %{nil}\n\n")
	b.WriteString("Name:           " + pkgName + "\n")
	b.WriteString("Version:        " + version + "\n")
	b.WriteString("Release:        1\n")
	b.WriteString("Summary:        " + rpmSanitizeLine(summary) + "\n")
	b.WriteString("License:        " + rpmSanitizeLine(license) + "\n")
	b.WriteString("BuildArch:      " + arch + "\n")
	b.WriteString("Source0:        " + exeSrc + "\n")

	idx := 1
	iconIdx, desktopIdx := 0, 0
	if harbour && iconSrc != "" {
		iconIdx = idx
		b.WriteString(fmt.Sprintf("Source%d:        %s\n", idx, iconSrc))
		idx++
		desktopIdx = idx
		b.WriteString(fmt.Sprintf("Source%d:        %s\n", idx, desktopSrc))
		idx++
	}
	libIdx := make([]int, len(bundledSonames))
	for i, so := range bundledSonames {
		libIdx[i] = idx
		b.WriteString(fmt.Sprintf("Source%d:        %s\n", idx, so))
		idx++
	}

	b.WriteString("\n%description\n" + rpmSanitizeLine(summary) + "\n")
	b.WriteString("\n%prep\n\n%build\n")

	b.WriteString("\n%install\n")
	b.WriteString("install -Dm0755 %{SOURCE0} %{buildroot}%{_bindir}/" + pkgName + "\n")
	if harbour && iconSrc != "" {
		// Sailfish Harbour icon + desktop paths. 108x108 is a valid Harbour icon
		// size; the single supplied icon is installed there.
		b.WriteString(fmt.Sprintf("install -Dm0644 %%{SOURCE%d} %%{buildroot}%%{_datadir}/icons/hicolor/108x108/apps/%s.png\n", iconIdx, pkgName))
		b.WriteString(fmt.Sprintf("install -Dm0644 %%{SOURCE%d} %%{buildroot}%%{_datadir}/applications/%s.desktop\n", desktopIdx, pkgName))
	}
	for i, so := range bundledSonames {
		b.WriteString(fmt.Sprintf("install -Dm0755 %%{SOURCE%d} %%{buildroot}%%{_datadir}/%s/lib/%s\n", libIdx[i], pkgName, so))
	}

	b.WriteString("\n%files\n")
	b.WriteString("%{_bindir}/" + pkgName + "\n")
	if harbour && iconSrc != "" {
		b.WriteString("%{_datadir}/icons/hicolor/108x108/apps/" + pkgName + ".png\n")
		b.WriteString("%{_datadir}/applications/" + pkgName + ".desktop\n")
	}
	for _, so := range bundledSonames {
		b.WriteString("%{_datadir}/" + pkgName + "/lib/" + so + "\n")
	}

	b.WriteString("\n%changelog\n")
	return b.String()
}

// buildRPMDesktopEntry renders the .desktop for a Harbour app: unlike the host
// bundle's absolute-path Exec, the installed binary is on PATH, so Exec/Icon are
// the package name.
func buildRPMDesktopEntry(opts packageOpts, pkgName string) string {
	var b strings.Builder
	b.WriteString("[Desktop Entry]\n")
	b.WriteString("Type=Application\n")
	b.WriteString("Name=" + desktopSanitize(opts.AppName) + "\n")
	b.WriteString("Exec=" + pkgName + "\n")
	b.WriteString("Icon=" + pkgName + "\n")
	b.WriteString("Terminal=false\n")
	return b.String()
}

// rpmSanitizeLine strips newlines from a single-line spec tag value.
func rpmSanitizeLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, s)
}

// writeRPM assembles an rpmbuild tree next to outBin, writes the .spec and the
// prebuilt binary (plus desktop/icon and privately-bundled libraries for a
// Harbour app), and runs `rpmbuild -bb` when available. Returns the path to the
// produced .rpm, or — when rpmbuild is not on PATH — the path to the .spec, with
// a hint. bundleLibs are the link-lib names to ship privately (Harbour only);
// libSearchRoot is the --sysroot they are copied from.
func writeRPM(outBin string, opts packageOpts, harbour bool, license, crossTriple, libSearchRoot string, bundleLibs []string) (string, error) {
	pkgName := rpmPackageName(opts, harbour)
	arch := rpmArch(crossTriple)

	base := filepath.Dir(outBin)
	top := filepath.Join(base, "rpmbuild")
	if err := os.RemoveAll(top); err != nil {
		return "", fmt.Errorf("clearing old rpmbuild tree: %w", err)
	}
	for _, d := range []string{"SPECS", "SOURCES", "BUILD", "BUILDROOT", "RPMS", "SRPMS"} {
		if err := os.MkdirAll(filepath.Join(top, d), 0755); err != nil {
			return "", err
		}
	}
	sources := filepath.Join(top, "SOURCES")

	// The prebuilt binary is Source0; copy it under the installed name so the
	// spec's %{SOURCE0} and the on-disk name agree.
	exeSrc := pkgName
	if err := copyFileMode(outBin, filepath.Join(sources, exeSrc), 0755); err != nil {
		return "", fmt.Errorf("staging binary: %w", err)
	}

	iconSrc, desktopSrc := "", ""
	if harbour && opts.IconSrc != "" {
		iconSrc = pkgName + ".png"
		if err := copyFileMode(opts.IconSrc, filepath.Join(sources, iconSrc), 0644); err != nil {
			return "", fmt.Errorf("staging icon: %w", err)
		}
		desktopSrc = pkgName + ".desktop"
		if err := os.WriteFile(filepath.Join(sources, desktopSrc), []byte(buildRPMDesktopEntry(opts, pkgName)), 0644); err != nil {
			return "", fmt.Errorf("staging desktop entry: %w", err)
		}
	}

	// Bundle non-allowlisted libraries privately (Harbour). Copy each one's
	// SONAME file out of the sysroot into SOURCES under its exact SONAME, so the
	// installed file matches the binary's DT_NEEDED and the rpath resolves it.
	var bundledSonames []string
	if harbour {
		for _, lib := range bundleLibs {
			soname, srcPath, err := findSonameInSysroot(libSearchRoot, lib)
			if err != nil {
				return "", err
			}
			if err := copyFileMode(srcPath, filepath.Join(sources, soname), 0755); err != nil {
				return "", fmt.Errorf("staging bundled %s: %w", lib, err)
			}
			bundledSonames = append(bundledSonames, soname)
		}
	}

	spec := buildRPMSpec(pkgName, opts.Version, opts.AppName, license, arch, exeSrc, harbour, iconSrc, desktopSrc, bundledSonames)
	specPath := filepath.Join(top, "SPECS", pkgName+".spec")
	if err := os.WriteFile(specPath, []byte(spec), 0644); err != nil {
		return "", err
	}

	if _, err := exec.LookPath("rpmbuild"); err != nil {
		fmt.Fprintf(os.Stderr, "klainmain: rpmbuild not on PATH — wrote the spec + build tree only. Build the RPM where rpmbuild exists (a Sailfish SDK/target, or the docker-sailfishos-builder image) with:\n  rpmbuild -bb --define \"_topdir %s\" --target %s %s\n", top, arch, specPath)
		return specPath, nil
	}

	topAbs, _ := filepath.Abs(top)
	cmd := exec.Command("rpmbuild", "-bb",
		"--define", "_topdir "+topAbs,
		"--target", arch,
		specPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("rpmbuild: %w", err)
	}
	rpm := filepath.Join(top, "RPMS", arch, fmt.Sprintf("%s-%s-1.%s.rpm", pkgName, opts.Version, arch))
	return rpm, nil
}
