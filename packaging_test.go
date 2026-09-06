package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// packaging_test.go — TDD-00142 Stage 4. Two tiers: pure-builder unit tests that
// run on every platform (no clang, no display), and a guarded bundle-structure
// test that fabricates a stub executable (so it needs neither the compiler nor
// WebKit).

func TestBuildInfoPlist(t *testing.T) {
	opts := packageOpts{AppName: "Tom & Jerry", AppID: "com.klain.tomjerry", Version: "2.1.0"}
	p := buildInfoPlist(opts, "prog", "icon.icns")

	for _, want := range []string{
		"<key>CFBundleExecutable</key>", "<string>prog</string>",
		"<key>CFBundleIdentifier</key>", "<string>com.klain.tomjerry</string>",
		"<key>CFBundlePackageType</key>", "<string>APPL</string>",
		"<key>CFBundleShortVersionString</key>", "<string>2.1.0</string>",
		"<key>NSHighResolutionCapable</key>", "<true/>",
		"<key>CFBundleIconFile</key>", "<string>icon.icns</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("Info.plist missing %q\n%s", want, p)
		}
	}
	// The app name's & must be XML-escaped.
	if !strings.Contains(p, "Tom &amp; Jerry") {
		t.Errorf("app name not XML-escaped:\n%s", p)
	}
	if strings.Contains(p, "Tom & Jerry") {
		t.Errorf("raw unescaped & leaked into plist:\n%s", p)
	}
	// LSUIElement must NOT be present (a regular, activating GUI app).
	if strings.Contains(p, "LSUIElement") {
		t.Errorf("plist should not set LSUIElement:\n%s", p)
	}
}

func TestBuildInfoPlistNoIcon(t *testing.T) {
	p := buildInfoPlist(packageOpts{AppName: "X", AppID: "com.klain.x", Version: "1.0.0"}, "x", "")
	if strings.Contains(p, "CFBundleIconFile") {
		t.Errorf("CFBundleIconFile must be omitted when there is no icon:\n%s", p)
	}
}

func TestBuildDesktopEntry(t *testing.T) {
	opts := packageOpts{AppName: "My App", AppID: "com.klain.myapp", Version: "1.0.0"}
	d := buildDesktopEntry(opts, "/abs/path/My App", "/abs/icon.png")
	for _, want := range []string{
		"[Desktop Entry]", "Type=Application", "Name=My App",
		`Exec="/abs/path/My App"`, "Icon=/abs/icon.png", "Terminal=false",
	} {
		if !strings.Contains(d, want) {
			t.Errorf(".desktop missing %q\n%s", want, d)
		}
	}
	// Exec must be an absolute, quoted path (handles the space).
	if !strings.Contains(d, `Exec="/`) {
		t.Errorf("Exec not quoted-absolute:\n%s", d)
	}
}

func TestBuildDesktopEntryNoIcon(t *testing.T) {
	d := buildDesktopEntry(packageOpts{AppName: "X", Version: "1.0.0"}, "/abs/x", "")
	if strings.Contains(d, "Icon=") {
		t.Errorf("Icon line must be omitted when there is no icon:\n%s", d)
	}
}

func TestAppSlug(t *testing.T) {
	cases := map[string]string{
		"My App": "myapp", "123 App": "app", "!!!": "app", "Klain Demo": "klaindemo",
	}
	for in, want := range cases {
		if got := appSlug(in); got != want {
			t.Errorf("appSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolvePackageOptsDefaults(t *testing.T) {
	opts, err := resolvePackageOpts("/tmp/build/myapp", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if opts.AppName != "myapp" {
		t.Errorf("AppName default = %q, want myapp", opts.AppName)
	}
	if opts.AppID != "com.klain.myapp" {
		t.Errorf("AppID default = %q", opts.AppID)
	}
	if opts.Version != "1.0.0" {
		t.Errorf("Version default = %q", opts.Version)
	}
}

func TestResolvePackageOptsBadIcon(t *testing.T) {
	if _, err := resolvePackageOpts("/tmp/x", "", "", "", "/no/such/icon.png"); err == nil {
		t.Fatal("expected error for a missing icon file")
	}
}

// TestPackageBundleStructure fabricates a stub executable and packages it,
// asserting the platform bundle's structure. Needs no compiler or WebKit.
func TestPackageBundleStructure(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("packaging is macOS/Linux only (this is %s)", runtime.GOOS)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "stub")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ntrue\n"), 0755); err != nil {
		t.Fatal(err)
	}
	opts, err := resolvePackageOpts(bin, "Stub App", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := packageApp(bin, opts, nil)
	if err != nil {
		t.Fatal(err)
	}

	switch runtime.GOOS {
	case "darwin":
		exe := filepath.Join(artifact, "Contents", "MacOS", "stub")
		fi, err := os.Stat(exe)
		if err != nil {
			t.Fatalf("bundled executable missing: %v", err)
		}
		if fi.Mode()&0111 == 0 {
			t.Errorf("bundled executable is not executable: %v", fi.Mode())
		}
		plist, err := os.ReadFile(filepath.Join(artifact, "Contents", "Info.plist"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(plist), "<string>APPL</string>") {
			t.Errorf("Info.plist missing CFBundlePackageType APPL")
		}
		// The standalone binary must still exist (copy, not move).
		if _, err := os.Stat(bin); err != nil {
			t.Errorf("standalone binary was removed: %v", err)
		}
	case "linux":
		entry, err := os.ReadFile(artifact)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(entry), "Type=Application") || !strings.Contains(string(entry), `Exec="/`) {
			t.Errorf("bad .desktop:\n%s", entry)
		}
	}
}

// --- Windows (ADR-00726): pure builders, run on every host ---

func TestVersionQuad(t *testing.T) {
	cases := map[string]string{
		"1.0.0": "1,0,0,0", "2.3": "2,3,0,0", "1.2.3.4": "1,2,3,4", "1.2.3.4.5": "1,2,3,4",
		"1.2.3-beta": "1,2,3,0", "v1": "0,0,0,0", "": "0,0,0,0", "70000.1": "0,0,0,0",
	}
	for in, want := range cases {
		if got := versionQuad(in); got != want {
			t.Errorf("versionQuad(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildWindowsRC(t *testing.T) {
	opts := packageOpts{AppName: `Tom "the" App`, AppID: "com.klain.tom", Version: "2.1.0"}
	rc := buildWindowsRC(opts, `C:\x\app.ico`)
	for _, want := range []string{
		`1 ICON "C:/x/app.ico"`, "1 VERSIONINFO", "FILEVERSION 2,1,0,0", "PRODUCTVERSION 2,1,0,0",
		`VALUE "FileDescription", "Tom ""the"" App"`, `VALUE "OriginalFilename", "Tom ""the"" App.exe"`,
		`VALUE "FileVersion", "2.1.0"`, `VALUE "Translation", 0x409, 1200`,
	} {
		if !strings.Contains(rc, want) {
			t.Errorf(".rc missing %q\n%s", want, rc)
		}
	}
	if strings.Contains(buildWindowsRC(opts, ""), "ICON") {
		t.Errorf("ICON statement must be omitted when there is no icon")
	}
	if got := rcQuote(`a\b`); got != `"a\\b"` {
		t.Errorf("rcQuote backslash: %s", got)
	}
}

func TestPngToICO(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	ico, err := pngToICO(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	// ICONDIR (6) + one ICONDIRENTRY (16) + the PNG verbatim.
	if len(ico) != 22+buf.Len() || !bytes.Equal(ico[22:], buf.Bytes()) {
		t.Fatalf("ico layout wrong: %d bytes for a %d-byte PNG", len(ico), buf.Len())
	}
	if ico[2] != 1 || ico[4] != 1 || ico[6] != 1 || ico[7] != 1 {
		t.Errorf("ICONDIR/ENTRY header wrong: % x", ico[:22])
	}
	buf.Reset()
	_ = png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 300, 300)))
	if _, err := pngToICO(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "256x256") {
		t.Errorf("expected the 256x256 ceiling error, got %v", err)
	}
	if _, err := pngToICO([]byte("not a png")); err == nil {
		t.Errorf("expected a decode error for non-PNG data")
	}
}

// TestPackageWindowsAppStructure packages a stub through a fake relink and
// asserts the artifact layout and link arguments — no clang, runs only where
// packageApp dispatches to the Windows writer.
func TestPackageWindowsAppStructure(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skipf("Windows packaging dispatch (this is %s)", runtime.GOOS)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "stub.exe")
	if err := os.WriteFile(bin, []byte("MZ"), 0755); err != nil {
		t.Fatal(err)
	}
	opts, err := resolvePackageOpts(bin, "", "", "3.4.5", "")
	if err != nil {
		t.Fatal(err)
	}
	if opts.AppName != "stub" {
		t.Fatalf("AppName default = %q, want stub (extension stripped)", opts.AppName)
	}
	var gotExtra []string
	var gotOut string
	relink := func(extra []string, out string) error {
		gotExtra, gotOut = extra, out
		return os.WriteFile(out, []byte("MZ"), 0755)
	}
	artifact, err := packageApp(bin, opts, relink)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "stub", "stub.exe"); artifact != want || gotOut != want {
		t.Fatalf("artifact = %q (relink out %q), want %q", artifact, gotOut, want)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("artifact missing: %v", err)
	}
	if !contains(gotExtra, "-Wl,--subsystem,windows") {
		t.Errorf("relink args lack the GUI subsystem switch: %v", gotExtra)
	}
	// The sidecar resources are cleaned up after the link.
	for _, side := range []string{"app.rc", "app.res", "app.ico"} {
		if _, err := os.Stat(filepath.Join(dir, "stub", side)); err == nil {
			t.Errorf("sidecar %s left behind", side)
		}
	}
}
