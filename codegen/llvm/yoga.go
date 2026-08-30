package llvm

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// yoga.go — vendoring of Facebook's Yoga flexbox engine (TDD-00150 Stage 1),
// the layout substrate behind `klain:tui`.
//
// Yoga is ~12k lines of C++20 across 19 .cpp + 59 .h with nested
// `#include <yoga/...>` angle-bracket includes, so — unlike the webview binding
// (a single amalgamated header compiled as one .cc) — it cannot be a single
// self-contained translation unit: the headers must be physically present on an
// include path, and a naive unity build (one .cc that #includes every .cpp)
// fails on an anonymous-namespace `Node` collision between event.cpp and
// node/Node.h that only surfaces when the two TUs are merged.
//
// So Yoga is vendored as its real source tree (yogasrc/, //go:embed'd) and
// compiled the faithful way: each .cpp as its OWN translation unit, exactly as
// upstream's build does. To do that without touching any of the CLI/conformance
// /test build loops (which each write one CSource.Content to a file and hand it
// to clang), EmbeddedCSources materializes the embedded tree to a temp dir once
// and returns one CSource per .cpp whose Content is a one-line stub —
// `#include <yoga/algorithm/CalculateLayout.cpp>` — resolved against that dir
// via `-I`. Every member shares `-std=c++20 -I<dir>`; the C++ runtime
// (`-lc++`/`-lstdc++`) is linked exactly like the webview binding.
//
// Vendored from yogalayout/yoga @ bd8fe0d (see yogasrc/LICENSE, MIT).

//go:embed all:yogasrc
var yogaFS embed.FS

// yogaVersion keys the extraction directory so a change to the vendored source
// (a version bump) lands in a fresh dir rather than reusing stale files.
const yogaVersion = "bd8fe0d"

// yogaExtractDir is the on-disk root the embedded Yoga tree is materialized to,
// deterministic per version so concurrent conformance workers and repeated CLI
// runs share one extraction instead of each spilling its own copy.
func yogaExtractDir() string {
	sum := sha256.Sum256([]byte("kml-yoga-" + yogaVersion))
	return filepath.Join(os.TempDir(), "kml-yoga-"+hex.EncodeToString(sum[:6]))
}

// extractYoga writes the embedded Yoga source tree to dir (idempotently) and
// returns the include root (the parent that makes `<yoga/...>` resolve) plus
// the list of .cpp paths, relative to that root, in sorted order for a stable
// build. The go:embed FS stores files under "yogasrc/yoga/...", so the include
// root is "<dir>/yogasrc" and a stub includes "<yoga/...>".
func extractYoga(dir string) (includeRoot string, cpps []string, err error) {
	includeRoot = filepath.Join(dir, "yogasrc")
	walkErr := fs.WalkDir(yogaFS, "yogasrc", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := yogaFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out := filepath.Join(dir, filepath.FromSlash(p))
		if merr := os.MkdirAll(filepath.Dir(out), 0755); merr != nil {
			return merr
		}
		// Skip rewriting an identical file so a shared dir isn't churned.
		if existing, rerr := os.ReadFile(out); rerr == nil && string(existing) == string(data) {
			// still record .cpp members below
		} else if werr := os.WriteFile(out, data, 0644); werr != nil {
			return werr
		}
		if strings.HasSuffix(p, ".cpp") {
			// path relative to includeRoot, e.g. "yoga/algorithm/Cache.cpp"
			rel := strings.TrimPrefix(p, "yogasrc/")
			cpps = append(cpps, rel)
		}
		return nil
	})
	if walkErr != nil {
		return "", nil, walkErr
	}
	sort.Strings(cpps)
	return includeRoot, cpps, nil
}

// TuiCSources returns the klain:tui painter + Yoga link inputs when the program
// used klain:tui, and nil otherwise — the exported entry point the test build
// path uses to mirror EmbeddedCSources without re-deriving the extraction.
func (e *Emitter) TuiCSources() ([]CSource, error) {
	if !e.usedTui {
		return nil, nil
	}
	return yogaCSources()
}

// yogaCSources materializes and pre-compiles the vendored Yoga engine, returning
// a single CSource whose Libs are the resulting object-file paths plus the C++
// runtime — i.e. Yoga joins the final link as prebuilt objects, not as source on
// the shared clang line.
//
// Why prebuilt objects instead of stub .cc members: Yoga needs C++20, but the
// driver compiles the emitted IR, this program's C runtimes (dtoa/tty/crypto/…),
// and any embedded C++ in ONE clang invocation, so a `-std=c++20` on that line
// is global — and `-std=c++20` is a hard error on every C translation unit. So
// each .cpp is compiled to a .o here, in its own clang call with -std=c++20,
// cached per-version under the extraction dir; only the objects (language-free)
// reach the shared link. The one member's Content is an empty C++ TU so the
// existing "write Content, append CFlags/Libs" build loops carry the objects
// through unchanged.
func yogaCSources() ([]CSource, error) {
	dir := yogaExtractDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("yoga: temp dir: %w", err)
	}
	includeRoot, cpps, err := extractYoga(dir)
	if err != nil {
		return nil, fmt.Errorf("yoga: extract: %w", err)
	}
	objDir := filepath.Join(dir, "obj")
	if err := os.MkdirAll(objDir, 0755); err != nil {
		return nil, fmt.Errorf("yoga: obj dir: %w", err)
	}
	objs := make([]string, 0, len(cpps))
	for _, rel := range cpps {
		name := strings.NewReplacer("/", "_", ".", "_").Replace(strings.ToLower(strings.TrimSuffix(rel, ".cpp")))
		obj := filepath.Join(objDir, name+".o")
		src := filepath.Join(includeRoot, filepath.FromSlash(rel))
		if fi, serr := os.Stat(obj); serr != nil || fi.Size() == 0 {
			cmd := exec.Command("clang", "-std=c++20", "-O2", "-I"+includeRoot, "-c", src, "-o", obj)
			if out, cerr := cmd.CombinedOutput(); cerr != nil {
				return nil, fmt.Errorf("yoga: compiling %s: %v\n%s", rel, cerr, out)
			}
		}
		objs = append(objs, obj)
	}
	cxxRuntime := "-lstdc++"
	if runtime.GOOS == "darwin" {
		cxxRuntime = "-lc++"
	}
	libs := append(objs, cxxRuntime)
	// The painter runtime (tui.c) is a C TU that #includes <yoga/Yoga.h>, so it
	// needs the same include root (a language-neutral -I, safe on the shared
	// clang line). The empty Yoga member carries the prebuilt objects + C++ rt.
	tui := CSource{Name: "tui", Content: TuiSource(), CFlags: []string{"-I" + includeRoot}}
	yoga := CSource{Name: "yoga", Content: "// Yoga is linked as prebuilt objects (see yoga.go).\n", Ext: "cc", Libs: libs}
	return []CSource{tui, yoga}, nil
}
