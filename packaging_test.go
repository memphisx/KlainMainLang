package main

import (
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
	artifact, err := packageApp(bin, opts)
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
