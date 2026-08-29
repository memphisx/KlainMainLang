package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// packaging.go — TDD-00142 Stage 4: wrap a compiled binary into a
// double-clickable desktop app for the host platform. macOS gets a `.app`
// bundle (Info.plist + optional .icns icon); Linux gets a `.desktop` launcher.
// Packaging targets the host GOOS — klainmain compiles a native binary for the
// machine it runs on, so there is no cross-packaging (the same host-gating as
// -static / -crypto=commoncrypto).
//
// Primary use is klain:webview GUI programs; the bundle is what grants a macOS
// window proper activation (a bare binary launched from a terminal can open a
// background window, while the double-clicked bundle foregrounds correctly).

// packageOpts is the resolved app metadata for -package. Every string field is
// non-empty after resolvePackageOpts except IconSrc, which is "" when no icon
// was requested.
type packageOpts struct {
	AppName string
	AppID   string
	Version string
	IconSrc string
}

// resolvePackageOpts fills in defaults and validates the icon path so failures
// surface before any bundle is written. outBin is the compiled binary path.
func resolvePackageOpts(outBin, name, id, version, icon string) (packageOpts, error) {
	opts := packageOpts{
		AppName: name,
		AppID:   id,
		Version: version,
		IconSrc: icon,
	}
	if opts.AppName == "" {
		opts.AppName = filepath.Base(outBin)
	}
	if opts.Version == "" {
		opts.Version = "1.0.0"
	}
	if opts.AppID == "" {
		opts.AppID = "com.klain." + appSlug(opts.AppName)
	}
	if opts.IconSrc != "" {
		if _, err := os.Stat(opts.IconSrc); err != nil {
			return opts, fmt.Errorf("-app-icon: file not found: %s", opts.IconSrc)
		}
		ext := strings.ToLower(filepath.Ext(opts.IconSrc))
		var accepted []string
		switch runtime.GOOS {
		case "darwin":
			accepted = []string{".icns", ".png"}
		case "linux":
			accepted = []string{".png", ".svg"}
		}
		if !contains(accepted, ext) {
			return opts, fmt.Errorf("-app-icon: %s is not a supported icon type on %s (accepted: %s)",
				ext, runtime.GOOS, strings.Join(accepted, ", "))
		}
	}
	return opts, nil
}

// appSlug lowercases AppName to alphanumerics for a reverse-DNS-safe id segment,
// stripping any leading digits; falls back to "app" if nothing usable remains.
func appSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	s := strings.TrimLeft(b.String(), "0123456789")
	if s == "" {
		return "app"
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// packageApp builds the platform bundle around an already-compiled binary and
// returns the path to the produced artifact. Dispatches on host GOOS.
func packageApp(outBin string, opts packageOpts) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return writeMacAppBundle(outBin, opts)
	case "linux":
		return writeLinuxDesktop(outBin, opts)
	default:
		return "", fmt.Errorf("-package is only supported on macOS and Linux (this run is on %s) — there is no bundle format to emit on this platform", runtime.GOOS)
	}
}

// writeMacAppBundle assembles <AppName>.app/Contents/{MacOS/<exe>, Info.plist,
// Resources/icon.icns} next to outBin. The binary is COPIED (the standalone
// binary remains). The bundle is rebuilt from scratch each run so no stale
// files linger.
func writeMacAppBundle(outBin string, opts packageOpts) (string, error) {
	exe := filepath.Base(outBin)
	appDir := filepath.Join(filepath.Dir(outBin), opts.AppName+".app")

	// Guard: only ever RemoveAll a path we computed that ends in .app.
	if !strings.HasSuffix(appDir, ".app") {
		return "", fmt.Errorf("refusing to package: computed bundle path %q is not a .app", appDir)
	}
	if err := os.RemoveAll(appDir); err != nil {
		return "", fmt.Errorf("clearing old bundle: %w", err)
	}

	macOSDir := filepath.Join(appDir, "Contents", "MacOS")
	if err := os.MkdirAll(macOSDir, 0755); err != nil {
		return "", err
	}
	if err := copyFileMode(outBin, filepath.Join(macOSDir, exe), 0755); err != nil {
		return "", fmt.Errorf("copying executable: %w", err)
	}

	// Icon (best-effort — a failure warns and drops the icon, never aborts).
	iconFile := ""
	if opts.IconSrc != "" {
		resDir := filepath.Join(appDir, "Contents", "Resources")
		if err := os.MkdirAll(resDir, 0755); err != nil {
			return "", err
		}
		if err := makeICNS(opts.IconSrc, filepath.Join(resDir, "icon.icns")); err != nil {
			fmt.Fprintf(os.Stderr, "klainmain: warning: could not build app icon (%v); bundling without one\n", err)
			_ = os.Remove(filepath.Join(resDir, "icon.icns"))
		} else {
			iconFile = "icon.icns"
		}
	}

	plist := buildInfoPlist(opts, exe, iconFile)
	if err := os.WriteFile(filepath.Join(appDir, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
		return "", err
	}
	return appDir, nil
}

// makeICNS produces dst (an .icns) from src. A .icns source is copied verbatim.
// A .png source is turned into a multi-resolution .icns via the macOS
// iconset + iconutil pipeline, falling back to a single-resolution `sips`
// conversion. All temporary files live under an os.MkdirTemp dir.
func makeICNS(src, dst string) error {
	if strings.ToLower(filepath.Ext(src)) == ".icns" {
		return copyFileMode(src, dst, 0644)
	}
	// PNG → iconset → iconutil.
	tmp, err := os.MkdirTemp("", "klain-iconset-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	iconset := filepath.Join(tmp, "icon.iconset")
	if err := os.MkdirAll(iconset, 0755); err != nil {
		return err
	}
	// (size, @2x?) variants Apple's iconutil expects.
	variants := []struct {
		px   int
		name string
	}{
		{16, "icon_16x16.png"}, {32, "icon_16x16@2x.png"},
		{32, "icon_32x32.png"}, {64, "icon_32x32@2x.png"},
		{128, "icon_128x128.png"}, {256, "icon_128x128@2x.png"},
		{256, "icon_256x256.png"}, {512, "icon_256x256@2x.png"},
		{512, "icon_512x512.png"}, {1024, "icon_512x512@2x.png"},
	}
	ok := true
	for _, v := range variants {
		out := filepath.Join(iconset, v.name)
		if err := exec.Command("sips", "-z", fmt.Sprint(v.px), fmt.Sprint(v.px), src, "--out", out).Run(); err != nil {
			ok = false
			break
		}
	}
	if ok {
		if err := exec.Command("iconutil", "-c", "icns", iconset, "-o", dst).Run(); err == nil {
			return nil
		}
	}
	// Fallback: single-resolution direct conversion.
	if err := exec.Command("sips", "-s", "format", "icns", src, "--out", dst).Run(); err != nil {
		return fmt.Errorf("sips/iconutil conversion failed")
	}
	return nil
}

// writeLinuxDesktop writes <AppName>.desktop next to outBin with an absolute
// Exec path (and absolute Icon path when given), and prints an install hint.
func writeLinuxDesktop(outBin string, opts packageOpts) (string, error) {
	execAbs, err := filepath.Abs(outBin)
	if err != nil {
		return "", err
	}
	iconAbs := ""
	if opts.IconSrc != "" {
		if a, err := filepath.Abs(opts.IconSrc); err == nil {
			iconAbs = a
		}
	}
	entry := buildDesktopEntry(opts, execAbs, iconAbs)
	dst := filepath.Join(filepath.Dir(outBin), opts.AppName+".desktop")
	if err := os.WriteFile(dst, []byte(entry), 0644); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "klainmain: install with: cp %q ~/.local/share/applications/\n", dst)
	return dst, nil
}

// buildInfoPlist renders the macOS Info.plist. Pure: no fs/exec, XML-escapes
// every interpolated value, and emits CFBundleIconFile only when iconFile != "".
func buildInfoPlist(opts packageOpts, execName, iconFile string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")
	kv := func(k, v string) {
		b.WriteString("\t<key>" + k + "</key>\n\t<string>" + xmlEscape(v) + "</string>\n")
	}
	kv("CFBundleDevelopmentRegion", "en")
	kv("CFBundleExecutable", execName)
	kv("CFBundleIdentifier", opts.AppID)
	kv("CFBundleInfoDictionaryVersion", "6.0")
	kv("CFBundleName", opts.AppName)
	kv("CFBundlePackageType", "APPL")
	kv("CFBundleShortVersionString", opts.Version)
	kv("CFBundleVersion", opts.Version)
	kv("LSMinimumSystemVersion", "10.13")
	b.WriteString("\t<key>NSHighResolutionCapable</key>\n\t<true/>\n")
	if iconFile != "" {
		kv("CFBundleIconFile", iconFile)
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// buildDesktopEntry renders a freedesktop .desktop file. Pure: no fs/exec. The
// Exec value is always double-quoted (spec-legal, handles spaces); Icon is
// omitted when iconAbs == "".
func buildDesktopEntry(opts packageOpts, execAbs, iconAbs string) string {
	var b strings.Builder
	b.WriteString("[Desktop Entry]\n")
	b.WriteString("Type=Application\n")
	b.WriteString("Version=1.0\n")
	b.WriteString("Name=" + desktopSanitize(opts.AppName) + "\n")
	b.WriteString(`Exec="` + execAbs + "\"\n")
	if iconAbs != "" {
		b.WriteString("Icon=" + iconAbs + "\n")
	}
	b.WriteString("Terminal=false\n")
	b.WriteString("Categories=Utility;\n")
	return b.String()
}

// desktopSanitize strips control characters (incl. newlines) from a .desktop
// value, which runs to end-of-line and must not contain a literal newline.
func desktopSanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// copyFileMode copies src to dst and sets dst's mode explicitly (io.Copy does
// not preserve the executable bit).
func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
