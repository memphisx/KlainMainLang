package llvm

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// The OpenSSL libraries are named by path on macOS, so an earlier -L on the
// program's link line (Homebrew's shared lib directory, which may hold a
// different OpenSSL) cannot redirect -lcrypto/-lssl.
func TestPinDarwinLibs(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"libcrypto.dylib", "libssl.dylib"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in := []string{"-L" + dir, "-lcrypto", "-lssl", "-lz"}
	got := pinDarwinLibs(in)
	want := in
	if runtime.GOOS == "darwin" {
		want = []string{"-L" + dir, filepath.Join(dir, "libcrypto.dylib"), filepath.Join(dir, "libssl.dylib"), "-lz"}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("pinDarwinLibs(%q) = %q, want %q", in, got, want)
	}
}
