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
	// Key the object cache by compiler so a dynamic (clang) build and a --static
	// (g++, Windows) build don't reuse each other's incompatible objects.
	objSub := "obj-clang"
	if runtime.GOOS == "windows" && staticLinkMode {
		objSub = "obj-gpp-static"
	}
	objDir := filepath.Join(dir, objSub)
	if err := os.MkdirAll(objDir, 0755); err != nil {
		return nil, fmt.Errorf("yoga: obj dir: %w", err)
	}
	objs := make([]string, 0, len(cpps))
	for _, rel := range cpps {
		name := strings.NewReplacer("/", "_", ".", "_").Replace(strings.ToLower(strings.TrimSuffix(rel, ".cpp")))
		obj := filepath.Join(objDir, name+".o")
		src := filepath.Join(includeRoot, filepath.FromSlash(rel))
		if fi, serr := os.Stat(obj); serr != nil || fi.Size() == 0 {
			var cmd *exec.Cmd
			if runtime.GOOS == "windows" && staticLinkMode {
				// --static on Windows links the *static* gcc-built libstdc++.a; compile
				// Yoga's C++ with g++ (not clang) so its RTTI COMDATs match the archive —
				// ld.bfd rejects mixing clang and gcc COMDATs ("duplicate section has
				// different size"). g++ ships with the required mingw toolchain and is
				// native to the ucrt64 sysroot (no --target/--sysroot). Dynamic builds
				// (default) and POSIX keep clang: the C++ runtime is a dynamic DLL/.so, so
				// there is no static-archive ABI-match constraint (ADR-00772).
				cmd = exec.Command("g++", "-std=c++20", "-O2", "-I"+includeRoot, "-c", src, "-o", obj)
			} else {
				cmd = ClangCommand("-std=c++20", "-O2", "-I"+includeRoot, "-c", src, "-o", obj)
			}
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
	// Yoga's pixel-grid rounding pulls in libm (`round`); glibc keeps libm as a
	// separate DSO the default link step won't add, so name it explicitly. No-op
	// on macOS where libSystem already folds in libm (ADR-00034).
	libs := append([]string{}, objs...)
	if runtime.GOOS == "windows" && staticLinkMode {
		// --static: the C++ runtime is the static libstdc++/winpthread group (no
		// separate -lstdc++). ADR-00772.
		libs = append(libs, "-lm")
		libs = append(libs, winCxxStaticRuntime()...)
	} else {
		// Default / POSIX: dynamic C++ runtime (libstdc++-6.dll / libc++ / libstdc++.so).
		libs = append(libs, cxxRuntime, "-lm")
	}
	// The painter runtime (tui.c) is a C TU that #includes <yoga/Yoga.h>, so it
	// needs the same include root (a language-neutral -I, safe on the shared
	// clang line). The empty Yoga member carries the prebuilt objects + C++ rt.
	tui := CSource{Name: "tui", Content: TuiSource(), CFlags: []string{"-I" + includeRoot}}
	yoga := CSource{Name: "yoga", Content: "// Yoga is linked as prebuilt objects (see yoga.go).\n", Ext: "cc", Libs: libs}
	return []CSource{tui, yoga}, nil
}

// winCxxStaticRuntime returns the link flags that complete a self-contained C++
// runtime on Windows, paired with the caller's `-l:libstdc++.a` (the static C++
// archive). Empty on POSIX (the C++ runtime is a system library there). Shared by
// klain:tui (Yoga) and klain:webview so a terminal or desktop binary is a single
// .exe with no libstdc++-6.dll / libgcc_s_seh-1.dll / libwinpthread-1.dll beside
// it (ADR-00771):
//   - -static-libgcc drops the libgcc_s_seh-1.dll dependency (the driver knows
//     libgcc.a's path).
//   - -l:libwinpthread.a forces the static winpthread that libstdc++ pulls in;
//     the colon form survives HostClangArgv's `-l` partition, unlike a
//     `-Wl,-Bstatic … -Wl,-Bdynamic` toggle which the partition would split.
//   - -Wl,--allow-multiple-definition lets the win32 shim's `strerror`/etc. win
//     over the static CRT copies (first-definition-wins, as the shim ordering
//     already relies on).
//
// The C++ objects that reference this archive must be g++-built (Yoga here, the
// webview amalgamation in webview_win32.go): clang RTTI COMDATs differ in size
// from the gcc-built libstdc++.a and ld.bfd rejects the mix.
func winCxxStaticRuntime() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	return []string{
		// libstdc++ + winpthread as a static --start-group: -Bstatic forces the
		// archives (not the DLL import libs), --start-group resolves their mutual
		// references regardless of order, and -Bdynamic restores normal linking for
		// everything after (system libs, curl/ssl on a networking program). An
		// unreferenced winpthread is simply not pulled — a TUI app that uses no
		// std::thread stays free of it — while klain:webview's pthread_create (its
		// dispatch queue) links static. One comma-joined -Wl arg so it survives
		// HostClangArgv's `-l` partition intact.
		"-Wl,-Bstatic,--start-group,-lstdc++,-lwinpthread,--end-group,-Bdynamic",
		// -static-libgcc drops libgcc_s_seh-1.dll (the driver knows libgcc.a's path).
		"-static-libgcc",
		// A trailing static winpthread (colon form → moved to the very end of the
		// link by HostClangArgv's -l partition, after every object) catches
		// pthread references the head group missed — e.g. the klain:sync goroutine
		// runtime in a program that also uses klain:tui (loadtest), whose
		// klainsync.o is linked after the group. Redundant with the group's own
		// -lwinpthread; --allow-multiple-definition below reconciles them.
		"-l:libwinpthread.a",
		// The win32 shim's `strerror`/etc. win over the static CRT copies
		// (first-definition-wins, as the shim ordering already relies on).
		"-Wl,--allow-multiple-definition",
	}
}
