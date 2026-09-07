package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// packaging_win32.go — `-package` on Windows (ADR-00726). Not build-tagged:
// the builders are pure and unit-tested on every host; only packageApp's
// dispatch is host-gated.
//
// There is no bundle format on Windows. What a double-clickable desktop app
// needs there is (1) the GUI subsystem, so Explorer does not open a console
// window beside the webview, and (2) an icon and VERSIONINFO resource, which
// is what Explorer's tile and Properties dialog read. Both are link-time
// properties of the executable, so the packager re-links the already-compiled
// program once more with `--subsystem,windows` and a compiled `.res`, into
// `<AppName>\<AppName>.exe` beside the standalone (console) binary — the
// per-app folder every Windows install uses, and the reason the artifact never
// collides with the console build, which stays the one to run from a terminal
// (a GUI-subsystem process has no stdout).

// relinkFunc re-runs the program's link step with extra arguments and a
// different output path. main.go builds it from the clang argv it just used.
type relinkFunc func(extra []string, out string) error

// writeWindowsApp assembles <dir>\<AppName>\<AppName>.exe next to outBin.
func writeWindowsApp(outBin string, opts packageOpts, relink relinkFunc) (string, error) {
	if relink == nil {
		return "", fmt.Errorf("packaging on Windows needs the link step (internal: no relink)")
	}
	appDir := filepath.Join(filepath.Dir(outBin), opts.AppName)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", err
	}
	exe := filepath.Join(appDir, opts.AppName+".exe")

	// Resources: an .ico (a .png is wrapped into a single-image .ico) and the
	// VERSIONINFO block. Sidecars live in the app folder and are removed after
	// the link; a resource-compiler failure warns and drops the resources —
	// the subsystem change alone is still worth producing, matching the
	// best-effort icon on macOS.
	var extra []string
	iconPath := ""
	if opts.IconSrc != "" {
		iconPath = filepath.Join(appDir, "app.ico")
		if err := makeICO(opts.IconSrc, iconPath); err != nil {
			fmt.Fprintf(os.Stderr, "klainmain: warning: could not build app icon (%v); packaging without one\n", err)
			iconPath = ""
		}
	}
	rcPath := filepath.Join(appDir, "app.rc")
	resPath := filepath.Join(appDir, "app.o")
	if err := os.WriteFile(rcPath, []byte(buildWindowsRC(opts, iconPath)), 0644); err != nil {
		return "", err
	}
	if err := compileRC(rcPath, resPath); err != nil {
		fmt.Fprintf(os.Stderr, "klainmain: warning: could not compile the app resources (%v); packaging without icon/version info\n", err)
	} else {
		extra = append(extra, resPath)
	}
	extra = append(extra, "-Wl,--subsystem,windows")
	rerr := relink(extra, exe)
	_ = os.Remove(rcPath)
	_ = os.Remove(resPath)
	if iconPath != "" {
		_ = os.Remove(iconPath)
	}
	if rerr != nil {
		return "", fmt.Errorf("re-linking as a GUI application: %w", rerr)
	}
	return exe, nil
}

// compileRC compiles an .rc into a COFF object (.o) the mingw linker takes
// directly. A Microsoft `.res` file (what `llvm-rc` and `windres -O res`
// emit) is NOT linkable by the mingw `ld`/`lld` driver — it reports "file
// format not recognized" (ADR-00746). GNU windres can emit a COFF object
// with `-O coff`; llvm-rc cannot, so its `.res` is converted with
// llvm-cvtres. windres (MSYS2 binutils, always in the toolchain) is tried
// first; the LLVM pair is the fallback.
func compileRC(rc, obj string) error {
	if p, err := exec.LookPath("windres"); err == nil {
		out, err := exec.Command(p, "--codepage=65001", "-i", rc, "-O", "coff", "-o", obj).CombinedOutput()
		if err == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "klainmain: windres failed (%v):\n%s", err, out)
	}
	rcTool, rcErr := exec.LookPath("llvm-rc")
	cvtTool, cvtErr := exec.LookPath("llvm-cvtres")
	if rcErr == nil && cvtErr == nil {
		// /C 65001: the .rc is written as UTF-8 (the app name may be non-ASCII).
		res := strings.TrimSuffix(obj, filepath.Ext(obj)) + ".res"
		out, err := exec.Command(rcTool, "/C", "65001", "/FO", res, rc).CombinedOutput()
		if err != nil {
			return fmt.Errorf("llvm-rc: %v\n%s", err, out)
		}
		defer os.Remove(res)
		// llvm-cvtres turns the MS .res into a COFF object the linker links.
		out, err = exec.Command(cvtTool, "/MACHINE:X64", "/OUT:"+obj, res).CombinedOutput()
		if err != nil {
			return fmt.Errorf("llvm-cvtres: %v\n%s", err, out)
		}
		return nil
	}
	return fmt.Errorf("no resource compiler found (windres from MSYS2's binutils, or llvm-rc + llvm-cvtres from LLVM)")
}

// buildWindowsRC renders the resource script: the app icon as resource ID 1
// (the lowest ID is what Explorer shows) and a VERSIONINFO block carrying the
// app name and version. Strings are RC-escaped; the version is folded to the
// four-number form the binary block needs while the string form is kept
// verbatim.
func buildWindowsRC(opts packageOpts, iconPath string) string {
	var b strings.Builder
	if iconPath != "" {
		// Forward slashes: the path is a Windows one by construction, and
		// filepath.ToSlash is a no-op on a POSIX host (the unit tests run there).
		b.WriteString("1 ICON " + rcQuote(strings.ReplaceAll(iconPath, `\`, "/")) + "\n")
	}
	quad := versionQuad(opts.Version)
	b.WriteString("1 VERSIONINFO\n")
	b.WriteString("FILEVERSION " + quad + "\n")
	b.WriteString("PRODUCTVERSION " + quad + "\n")
	b.WriteString("BEGIN\n  BLOCK \"StringFileInfo\"\n  BEGIN\n    BLOCK \"040904B0\"\n    BEGIN\n")
	kv := func(k, v string) {
		b.WriteString("      VALUE " + rcQuote(k) + ", " + rcQuote(v) + "\n")
	}
	kv("FileDescription", opts.AppName)
	kv("ProductName", opts.AppName)
	kv("InternalName", opts.AppName)
	kv("OriginalFilename", opts.AppName+".exe")
	kv("FileVersion", opts.Version)
	kv("ProductVersion", opts.Version)
	b.WriteString("    END\n  END\n  BLOCK \"VarFileInfo\"\n  BEGIN\n    VALUE \"Translation\", 0x409, 1200\n  END\nEND\n")
	return b.String()
}

// versionQuad folds "1.2.3" / "2.0" / "1.2.3-beta" into the "1,2,3,0" form
// VERSIONINFO's binary fields take: up to four leading numeric components,
// anything else is 0.
func versionQuad(v string) string {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == '+' })
	nums := make([]string, 0, 4)
	for _, p := range parts {
		if len(nums) == 4 {
			break
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 65535 {
			break
		}
		nums = append(nums, strconv.Itoa(n))
	}
	for len(nums) < 4 {
		nums = append(nums, "0")
	}
	return strings.Join(nums, ",")
}

// rcQuote renders a string as an RC string literal (backslashes and quotes
// doubled, control characters dropped).
func rcQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`""`)
		case r < 0x20:
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// makeICO produces the .ico the resource references: an .ico is copied; a
// .png is wrapped as a single PNG-compressed image (the form Windows Vista+
// reads natively, so no re-encoding), which needs the image to fit the
// format's 256×256 ceiling.
func makeICO(src, dst string) error {
	if strings.ToLower(filepath.Ext(src)) == ".ico" {
		return copyFileMode(src, dst, 0644)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	ico, err := pngToICO(data)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, ico, 0644)
}

// pngToICO wraps one PNG into an ICO container (ICONDIR + one ICONDIRENTRY +
// the PNG bytes verbatim). A 256-pixel dimension is encoded as 0, per the
// format.
func pngToICO(data []byte) ([]byte, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a PNG: %v", err)
	}
	if cfg.Width > 256 || cfg.Height > 256 {
		return nil, fmt.Errorf("PNG is %dx%d; an .ico image is at most 256x256 (resize it, or pass an .ico)", cfg.Width, cfg.Height)
	}
	dim := func(n int) byte {
		if n >= 256 {
			return 0
		}
		return byte(n)
	}
	var out bytes.Buffer
	// ICONDIR: reserved, type 1 (icon), count 1.
	binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, 1})
	// ICONDIRENTRY: width, height, palette count, reserved, planes, bpp, size, offset.
	out.Write([]byte{dim(cfg.Width), dim(cfg.Height), 0, 0})
	binary.Write(&out, binary.LittleEndian, [2]uint16{1, 32})
	binary.Write(&out, binary.LittleEndian, [2]uint32{uint32(len(data)), 6 + 16})
	out.Write(data)
	return out.Bytes(), nil
}
