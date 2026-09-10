package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// rpmbuildErr handles a writeRPM result whose rpmbuild run targets aarch64. On a
// native-aarch64 host (arm64) a failure is a real bug and fails the test; on
// other hosts a present-but-arch-incapable rpmbuild (stock rpm lacks the aarch64
// platform files → "No compatible architectures found for build") is tolerated —
// the spec + build tree are written before rpmbuild runs, so the structural
// assertions still validate. The real cross-build is covered by the arm64 lane.
func rpmbuildErr(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if runtime.GOARCH == "arm64" {
		t.Fatalf("writeRPM: %v", err)
	}
	t.Logf("tolerating rpmbuild failure on non-aarch64 host (%s): %v", runtime.GOARCH, err)
}

// packaging_rpm_test.go — TDD-00146 Stage 2. Pure-builder unit tests (run
// everywhere, no rpmbuild) plus a structural test that fabricates a stub binary
// and checks the emitted spec + build tree. The actual `rpmbuild` run is
// exercised on the arm64/Sailfish lanes, not required here.

func TestBuildRPMSpecPlain(t *testing.T) {
	spec := buildRPMSpec("greet", "1.0.0", "Greeter", "MIT", "aarch64", "greet", false, "", "", nil)
	for _, want := range []string{
		"Name:           greet",
		"Version:        1.0.0",
		"License:        MIT",
		"BuildArch:      aarch64",
		"Source0:        greet",
		"install -Dm0755 %{SOURCE0} %{buildroot}%{_bindir}/greet",
		"%{_bindir}/greet",
		"%global debug_package %{nil}",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("plain spec missing %q\n%s", want, spec)
		}
	}
	if strings.Contains(spec, "icons/hicolor") || strings.Contains(spec, ".desktop") {
		t.Errorf("plain (non-harbour, no icon) spec should have no desktop/icon lines\n%s", spec)
	}
}

func TestBuildRPMSpecHarbourWithIcon(t *testing.T) {
	spec := buildRPMSpec("harbour-greet", "2.0.0", "Greeter", "GPLv3+", "aarch64",
		"harbour-greet", true, "harbour-greet.png", "harbour-greet.desktop", nil)
	for _, want := range []string{
		"Name:           harbour-greet",
		"Source1:        harbour-greet.png",
		"Source2:        harbour-greet.desktop",
		"install -Dm0644 %{SOURCE1} %{buildroot}%{_datadir}/icons/hicolor/108x108/apps/harbour-greet.png",
		"install -Dm0644 %{SOURCE2} %{buildroot}%{_datadir}/applications/harbour-greet.desktop",
		"%{_datadir}/applications/harbour-greet.desktop",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("harbour spec missing %q\n%s", want, spec)
		}
	}
}

func TestRpmArch(t *testing.T) {
	if got := rpmArch("aarch64-meego-linux-gnu"); got != "aarch64" {
		t.Errorf("aarch64 triple → %q, want aarch64", got)
	}
	if got := rpmArch("x86_64-unknown-linux-gnu"); got != "x86_64" {
		t.Errorf("x86_64 triple → %q, want x86_64", got)
	}
	// Empty triple falls back to the host arch (aarch64/x86_64 on the dev boxes).
	if got := rpmArch(""); got == "" {
		t.Errorf("empty triple should map to a host arch, got empty")
	}
}

func TestRpmPackageName(t *testing.T) {
	opts := packageOpts{AppName: "Weather 2026"}
	if got := rpmPackageName(opts, false); got != "weather2026" {
		t.Errorf("plain name → %q, want weather2026", got)
	}
	if got := rpmPackageName(opts, true); got != "harbour-weather2026" {
		t.Errorf("harbour name → %q, want harbour-weather2026", got)
	}
}

func TestResolvePackageOptsRPMBadIcon(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "app")
	if err := os.WriteFile(bin, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	svg := filepath.Join(dir, "icon.svg")
	if err := os.WriteFile(svg, []byte("<svg/>"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePackageOptsRPM(bin, "", "", svg); err == nil {
		t.Fatalf("a .svg icon should be rejected for rpm:harbour (needs .png)")
	}
}

// TestWriteRPMStructure fabricates a stub binary and checks writeRPM lays down
// the spec + SOURCES + build tree. Robust to rpmbuild's presence: the spec and
// staged binary are written before rpmbuild is ever invoked, so they exist
// regardless of the returned artifact kind.
func TestWriteRPMStructure(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "greeter")
	if err := os.WriteFile(bin, []byte("#!/bin/true\n"), 0755); err != nil {
		t.Fatal(err)
	}
	opts := packageOpts{AppName: "Greeter", Version: "3.1.4"}
	_, err := writeRPM(bin, opts, true, "MIT", "aarch64-meego-linux-gnu", "", nil)
	rpmbuildErr(t, err)
	top := filepath.Join(dir, "rpmbuild")
	spec := filepath.Join(top, "SPECS", "harbour-greeter.spec")
	if _, err := os.Stat(spec); err != nil {
		t.Fatalf("spec not written: %v", err)
	}
	staged := filepath.Join(top, "SOURCES", "harbour-greeter")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("binary not staged into SOURCES: %v", err)
	}
	b, _ := os.ReadFile(spec)
	if !strings.Contains(string(b), "BuildArch:      aarch64") {
		t.Errorf("spec arch not derived from the meego triple:\n%s", b)
	}
}

func TestHarbourLibsToBundle(t *testing.T) {
	got := harbourLibsToBundle([]string{"curl", "pcre2-8", "m", "crypto", "pcre2-8"}, false)
	if len(got) != 1 || got[0] != "pcre2-8" {
		t.Fatalf("expected only [pcre2-8] bundled (curl/openssl/m are Harbour-allowed), got %v", got)
	}
}

// TestHarbourLibsToBundleGC checks that -mm=gc folds bdw-gc (linked via LocateGC,
// so absent from LinkLibs) into the private-bundle list, and only once even if a
// requireLink somehow also named it.
func TestHarbourLibsToBundleGC(t *testing.T) {
	got := harbourLibsToBundle([]string{"curl", "pcre2-8"}, true)
	if len(got) != 2 || got[0] != "pcre2-8" || got[1] != "gc" {
		t.Fatalf("expected [pcre2-8 gc] bundled under -mm=gc, got %v", got)
	}
	// Without gc mode, no gc even when nothing else bundles.
	if got := harbourLibsToBundle([]string{"curl"}, false); len(got) != 0 {
		t.Fatalf("expected nothing bundled off gc mode, got %v", got)
	}
	// gc mode with no bundled feature libs still bundles just gc.
	if got := harbourLibsToBundle([]string{"curl"}, true); len(got) != 1 || got[0] != "gc" {
		t.Fatalf("expected [gc] bundled under -mm=gc with no feature libs, got %v", got)
	}
}

func TestBuildRPMSpecBundlesLib(t *testing.T) {
	// No icon → the bundled lib is the first extra source (Source1).
	spec := buildRPMSpec("harbour-rx", "1.0.0", "Rx", "MIT", "aarch64",
		"harbour-rx", true, "", "", []string{"libpcre2-8.so.0"})
	for _, want := range []string{
		"Source1:        libpcre2-8.so.0",
		"install -Dm0755 %{SOURCE1} %{buildroot}%{_datadir}/harbour-rx/lib/libpcre2-8.so.0",
		"%{_datadir}/harbour-rx/lib/libpcre2-8.so.0",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("bundled-lib spec missing %q\n%s", want, spec)
		}
	}
}

func TestFindSonameInSysroot(t *testing.T) {
	root := t.TempDir()
	lib64 := filepath.Join(root, "usr", "lib64")
	if err := os.MkdirAll(lib64, 0755); err != nil {
		t.Fatal(err)
	}
	// The realpath plus the SONAME symlink-style file and the dev symlink.
	for _, n := range []string{"libpcre2-8.so.0.14.0", "libpcre2-8.so.0", "libpcre2-8.so"} {
		if err := os.WriteFile(filepath.Join(lib64, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	soname, path, err := findSonameInSysroot(root, "pcre2-8")
	if err != nil {
		t.Fatalf("findSonameInSysroot: %v", err)
	}
	if soname != "libpcre2-8.so.0" {
		t.Errorf("SONAME → %q, want libpcre2-8.so.0 (not the full version, not the bare .so)", soname)
	}
	if filepath.Base(path) != "libpcre2-8.so.0" {
		t.Errorf("path basename → %q", filepath.Base(path))
	}
}

func TestWriteRPMBundlesPcre2(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "rx")
	if err := os.WriteFile(bin, []byte("#!/bin/true\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// Fake sysroot carrying the pcre2 SONAME.
	sysroot := t.TempDir()
	lib64 := filepath.Join(sysroot, "usr", "lib64")
	if err := os.MkdirAll(lib64, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib64, "libpcre2-8.so.0"), []byte("SONAME"), 0644); err != nil {
		t.Fatal(err)
	}
	opts := packageOpts{AppName: "Rx", Version: "1.0.0"}
	_, err := writeRPM(bin, opts, true, "MIT", "aarch64-meego-linux-gnu", sysroot, []string{"pcre2-8"})
	rpmbuildErr(t, err)
	top := filepath.Join(dir, "rpmbuild")
	staged := filepath.Join(top, "SOURCES", "libpcre2-8.so.0")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("pcre2 SONAME not staged into SOURCES: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(top, "SPECS", "harbour-rx.spec"))
	if !strings.Contains(string(b), "%{_datadir}/harbour-rx/lib/libpcre2-8.so.0") {
		t.Errorf("spec does not install the bundled pcre2 under the harbour lib path:\n%s", b)
	}
}

// TestWriteRPMBundlesGC exercises the -mm=gc path: harbourLibsToBundle(..., true)
// yields "gc", and writeRPM must resolve libgc.so.N out of the sysroot, stage it,
// and install it under the private harbour lib dir just like a feature lib.
func TestWriteRPMBundlesGC(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "rx")
	if err := os.WriteFile(bin, []byte("#!/bin/true\n"), 0755); err != nil {
		t.Fatal(err)
	}
	sysroot := t.TempDir()
	lib64 := filepath.Join(sysroot, "usr", "lib64")
	if err := os.MkdirAll(lib64, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib64, "libgc.so.1"), []byte("SONAME"), 0644); err != nil {
		t.Fatal(err)
	}
	bundle := harbourLibsToBundle(nil, true) // -mm=gc, no feature libs
	opts := packageOpts{AppName: "Rx", Version: "1.0.0"}
	_, err := writeRPM(bin, opts, true, "MIT", "aarch64-meego-linux-gnu", sysroot, bundle)
	rpmbuildErr(t, err)
	top := filepath.Join(dir, "rpmbuild")
	if _, err := os.Stat(filepath.Join(top, "SOURCES", "libgc.so.1")); err != nil {
		t.Fatalf("libgc SONAME not staged into SOURCES: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(top, "SPECS", "harbour-rx.spec"))
	if !strings.Contains(string(b), "%{_datadir}/harbour-rx/lib/libgc.so.1") {
		t.Errorf("spec does not install the bundled bdw-gc under the harbour lib path:\n%s", b)
	}
}
