package llvm

// runtime_process_native.go — lib/native.d.ts's process primitives, the
// counterparts of Node's process binding (src/node_process_methods.cc) that
// lib/node/internal_process_methods.ts builds process.cwd, chdir, uptime,
// hrtime, kill, memoryUsage, umask and the credential reads on. A failing
// call returns -errno; the TypeScript side builds Node's error from it. The
// bodies are in processsrc/process.c; each ensure* here declares its natives.

import (
	"fmt"
	"strings"
)

// ensureNativeProcessVersion defines the version strings loop.c's
// __kml_native_process_version answers (0 version, 1 node, 2 v8, 3 klain):
// the compiler's own version is the compiler build's, so it is the
// program's data, not the cached runtime object's.
func (e *Emitter) ensureNativeProcessVersion() {
	if e.fnDecls["__kml_process_versions"] {
		return
	}
	e.fnDecls["__kml_process_versions"] = true
	var elems []string
	for i, v := range []string{"v" + nodeCompatVersion, nodeCompatVersion, nodeCompatV8, KlainVersion} {
		name := fmt.Sprintf("@__kml_process_version_%d", i)
		e.emitGlobal(fmt.Sprintf("%s = private"+irGlobalAS+" constant [%d x i8] c\"%s\\00\"", name, len(v)+1, v))
		elems = append(elems, irPtr+" "+name)
	}
	e.emitGlobal(fmt.Sprintf("@__kml_process_versions ="+irGlobalAS+" constant [4 x "+irPtr+"] [%s]", strings.Join(elems, ", ")))
}
