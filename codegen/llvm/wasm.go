package llvm

import (
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// The WebAssembly natives (lib/node/kml_wasm.ts) and their engine: the
// kml_wasm_* interface and its wasmtime backend, compiled into the pool's
// unit only with -DKLAINPOOL_WASM (ThreadPoolCFlags).
//
//go:embed threadpoolsrc/klainwasm_wasmtime.c
var wasmBackendSource string

//go:embed threadpoolsrc/klainwasm.c
var wasmNativeSource string

// LocateWasm finds the wasmtime C API: its include and library directories
// (pkg-config's, a WASMTIME_DIR install, Homebrew's or /usr/local's). Off
// Windows the backend loads the library on first use (dlopen), so only its
// headers are needed to compile, and the library directory found is passed
// as KW_LIBDIR for the loader to look in first.
func LocateWasm() (cflags, libs []string) {
	if out, err := exec.Command("pkg-config", "--cflags", "wasmtime").Output(); err == nil {
		if lout, err := exec.Command("pkg-config", "--libs", "wasmtime").Output(); err == nil {
			cflags = strings.Fields(strings.TrimSpace(string(out)))
			lflags := strings.Fields(strings.TrimSpace(string(lout)))
			if runtime.GOOS == "windows" {
				return cflags, lflags
			}
			for _, f := range lflags {
				if strings.HasPrefix(f, "-L") {
					cflags = append(cflags, wasmLibDirFlag(strings.TrimPrefix(f, "-L")))
					break
				}
			}
			return cflags, wasmLibs()
		}
	}
	var dirs []string
	if d := os.Getenv("WASMTIME_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	if out, err := exec.Command("brew", "--prefix", "wasmtime").Output(); err == nil {
		dirs = append(dirs, strings.TrimSpace(string(out)))
	}
	dirs = append(dirs, "/usr/local", "/usr")
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, "include", "wasmtime.h")); err == nil {
			lib := filepath.Join(d, "lib")
			cflags = []string{"-I" + filepath.Join(d, "include")}
			if runtime.GOOS == "windows" {
				return cflags, append([]string{"-L" + lib}, wasmLibs()...)
			}
			return append(cflags, wasmLibDirFlag(lib)), wasmLibs()
		}
	}
	return nil, wasmLibs()
}

// wasmLibDirFlag is the backend's KW_LIBDIR define for dir.
func wasmLibDirFlag(dir string) string {
	return `-DKW_LIBDIR="` + strings.ReplaceAll(dir, `"`, `\"`) + `"`
}

// wasmLibs is what the backend links: on Windows wasmtime and the system
// libraries its MinGW build imports; elsewhere only the loader's library.
func wasmLibs() []string {
	if runtime.GOOS == "windows" {
		return []string{"-lwasmtime", "-lws2_32", "-lbcrypt", "-ladvapi32", "-luserenv", "-lntdll", "-lshell32", "-lole32"}
	}
	if runtime.GOOS == "linux" {
		return []string{"-ldl"}
	}
	return nil
}
