package llvm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// ErrCObjectUncacheable is returned by CachedObject for a member that cannot
// be compiled ahead of its program: a caller falls back to handing clang the
// source, exactly as before.
var ErrCObjectUncacheable = errors.New("embedded C source is not cacheable")

var cObjLocks sync.Map // object path → *sync.Mutex

// CachedObject compiles this embedded C runtime file once into cacheDir and
// returns the object's path; every later call with the same source and flags
// — from this process or any other sharing cacheDir — reuses it. The embedded
// sources are identical for every program a test or conformance run builds,
// so recompiling them per program is pure repeated work: clang time, and a
// fresh set of source + object writes each time.
//
// The object name carries a hash of everything that shapes it (source, the
// member's own flags, the caller's compile flags, the host clang arguments,
// the Windows compat header), so a rebuilt compiler or a different flag set
// (-fsanitize, a cross target) never links a stale object. compileFlags are
// the caller's code-generation flags (-O2, -fsanitize=…, -g); link-only
// flags must not be passed.
//
// The C++/moc members are refused (ErrCObjectUncacheable): they involve a
// generated sibling file or the C++ driver, and are rare enough not to matter.
func (c CSource) CachedObject(cacheDir string, compileFlags []string) (string, error) {
	if c.Ext != "" || c.NeedsMoc() {
		return "", ErrCObjectUncacheable
	}
	h := sha256.New()
	for _, part := range [][]string{{c.Name, c.Content, runtime.GOOS, win32CompatHeader}, c.CFlags, compileFlags, HostClangArgs()} {
		for _, s := range part {
			fmt.Fprintf(h, "%d:%s", len(s), s)
		}
		h.Write([]byte{0})
	}
	tag := hex.EncodeToString(h.Sum(nil)[:10])
	obj := filepath.Join(cacheDir, c.Name+"-"+tag+".o")

	mu, _ := cObjLocks.LoadOrStore(obj, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	if st, err := os.Stat(obj); err == nil && st.Size() > 0 {
		return obj, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	// Per-process scratch names: another process (a parallel test shard) may be
	// building the same object; each writes its own and the rename publishes
	// whichever finishes — the results are interchangeable.
	stem := filepath.Join(cacheDir, fmt.Sprintf("%s-%s.%d", c.Name, tag, os.Getpid()))
	src, tmp := stem+".c", stem+".o.tmp"
	if err := os.WriteFile(src, []byte(c.Content), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(src)
	args := append([]string{}, compileFlags...)
	args = append(args, "-c", src, "-o", tmp)
	for _, f := range c.CFlags {
		// -l/-L/-Wl belong to the link, which still receives CFlags from the
		// caller; on a -c line they only produce "unused argument" noise.
		if strings.HasPrefix(f, "-l") || strings.HasPrefix(f, "-L") || strings.HasPrefix(f, "-Wl,") {
			continue
		}
		args = append(args, f)
	}
	if out, err := ClangCommand(args...).CombinedOutput(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("compiling embedded %s: %v\n%s", c.Name, err, out)
	}
	if err := os.Rename(tmp, obj); err != nil {
		os.Remove(tmp)
		if st, serr := os.Stat(obj); serr == nil && st.Size() > 0 {
			return obj, nil // another process published it first
		}
		return "", err
	}
	return obj, nil
}
