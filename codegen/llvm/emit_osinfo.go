// emit_osinfo.go — the osinfo.c-backed half of the os module (type/release/
// version/machine/uptime/loadavg/userInfo/availableParallelism/
// networkInterfaces, plus os.arch/endianness/devNull constants) and the bare
// `process.env` value (enumeration through Object.keys/for…in/spread).
package llvm

import (
	"fmt"
)

// emitProcessEnvValue implements the bare `process.env` value (not a keyed
// read): a dynamic object holding every variable of the live environment
// block, so Object.keys / for…in / spread / JSON.stringify all work through
// the D1 paths. The bag is a snapshot taken at this expression's evaluation
// (ADR-01077): keyed reads and writes (`process.env.X`) stay live.
func (e *Emitter) emitProcessEnvValue() (Value, error) {
	e.ensureNanBox()
	e.ensureShapeRuntime()
	e.ensureAnyToPrimitive()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_process_env_bag()", bag))
	return Value{Ref: e.emitNbTagPtr(bag, kmlTagDynObject), Ty: TypeAny}, nil
}
