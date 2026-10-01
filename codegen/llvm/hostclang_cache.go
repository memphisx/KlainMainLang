package llvm

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// hostclang_cache.go — the embedded C runtime sources a link names (the
// sidecar .c files: dynjson, inspect, casemap, …) are the same for most
// programs; each is compiled once per content, flags and clang version into
// an object under the user cache directory, and the link names the object.
// Anything uncertain (no cache directory, a failed compile) links the
// source as before.

var (
	clangVersionOnce sync.Once
	clangVersion     string
)

// clangIdentity is `clang --version`, part of every cache key.
func clangIdentity() string {
	clangVersionOnce.Do(func() {
		out, err := exec.Command("clang", "--version").Output()
		if err == nil {
			clangVersion = string(out)
		}
	})
	return clangVersion
}

// linkOnlyFlag reports a flag compiling a single C file has no use for.
func linkOnlyFlag(a string) bool {
	switch {
	case strings.HasPrefix(a, "-l"), strings.HasPrefix(a, "-L"), strings.HasPrefix(a, "-Wl,"):
		return true
	case a == "-static", a == "-shared", a == "-rdynamic":
		return true
	}
	return false
}

// cacheSidecars replaces each .c source of a link command with its cached
// object, compiling it on a miss.
func cacheSidecars(args []string) []string {
	var sources []int
	outIdx := -1
	for i, a := range args {
		switch {
		case a == "-c" || a == "-S" || a == "-E":
			return args // not a link
		case a == "-o" && i+1 < len(args):
			outIdx = i + 1
		case strings.HasSuffix(a, ".c") && i != outIdx:
			sources = append(sources, i)
		}
	}
	if len(sources) == 0 || os.Getenv("KLAIN_NO_SIDECAR_CACHE") != "" {
		return args
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return args
	}
	dir := filepath.Join(cacheRoot, "klainmain", "sidecars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return args
	}
	// The compile flags: every flag but the link-only ones and -o's value;
	// an input file (the .ll, a .s, another .c) is not a flag.
	var flags []string
	for i, a := range args {
		if i == outIdx || a == "-o" || !strings.HasPrefix(a, "-") || linkOnlyFlag(a) {
			continue
		}
		flags = append(flags, a)
	}
	out := append([]string{}, args...)
	replaced := false
	for _, i := range sources {
		src, err := os.ReadFile(args[i])
		if err != nil {
			continue
		}
		h := sha256.New()
		h.Write([]byte(clangIdentity()))
		h.Write([]byte{0})
		h.Write([]byte(strings.Join(flags, "\x00")))
		h.Write([]byte{0})
		h.Write(src)
		obj := filepath.Join(dir, hex.EncodeToString(h.Sum(nil))[:32]+".o")
		if _, err := os.Stat(obj); err != nil {
			tmp, err := os.CreateTemp(dir, "tmp-*.o")
			if err != nil {
				continue
			}
			tmp.Close()
			cargs := append([]string{"-c", args[i], "-o", tmp.Name(), "-Wno-unused-command-line-argument"}, flags...)
			if cerr := exec.Command("clang", cargs...).Run(); cerr != nil {
				os.Remove(tmp.Name())
				continue
			}
			if err := os.Rename(tmp.Name(), obj); err != nil {
				os.Remove(tmp.Name())
				continue
			}
		}
		out[i] = obj
		replaced = true
	}
	if replaced {
		// The sources' compile flags (-I …) now reach only the link.
		out = append(out, "-Wno-unused-command-line-argument")
	}
	return out
}
