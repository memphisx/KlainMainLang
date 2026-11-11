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

// ensureWasmNatives compiles the WebAssembly natives into the pool's unit
// and links the engine: the `@link wasm` of lib/native.d.ts.
func (e *Emitter) ensureWasmNatives() {
	e.ensureMicrotasks()
	e.ensureStrHeaderRuntime()
	e.ensureThreadPool()
	e.usedWasmNatives = true
}

// UsesWasm reports whether the program runs WebAssembly.
func (e *Emitter) UsesWasm() bool { return e.usedWasmNatives }

// LocateWasm finds the wasmtime C API: its include and library directories
// (pkg-config's, a WASMTIME_DIR install, Homebrew's or /usr/local's).
func LocateWasm() (cflags, libs []string) {
	if out, err := exec.Command("pkg-config", "--cflags", "wasmtime").Output(); err == nil {
		if lout, err := exec.Command("pkg-config", "--libs", "wasmtime").Output(); err == nil {
			return strings.Fields(strings.TrimSpace(string(out))), strings.Fields(strings.TrimSpace(string(lout)))
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
			libs = []string{"-L" + lib}
			if runtime.GOOS != "windows" {
				libs = append(libs, "-Wl,-rpath,"+lib) // the shared library where it was found
			}
			return []string{"-I" + filepath.Join(d, "include")}, append(libs, wasmLibs()...)
		}
	}
	return nil, wasmLibs()
}

// wasmLibs links wasmtime and, on Windows, the system libraries its
// MinGW build imports.
func wasmLibs() []string {
	if runtime.GOOS == "windows" {
		return []string{"-lwasmtime", "-lws2_32", "-lbcrypt", "-ladvapi32", "-luserenv", "-lntdll", "-lshell32", "-lole32"}
	}
	return []string{"-lwasmtime"}
}
