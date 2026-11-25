package llvm

import (
	_ "embed"
	"fmt"
	"strconv"
)

// The process runtime (hooks, uptime, cwd, ids, exec path, signals; TDD-00240)
// lives in processsrc/process.c; the natives in runtime_process_native.go are
// declared against it.
//
//go:embed processsrc/process.c
var processSource string

// ProcessSource is the process runtime's C source.
func ProcessSource() string { return processSource }

// ProcessCFlags pass process.c the NaN-box constants; it is compiled the
// same way for every program (DeadStripFlags).
func (e *Emitter) ProcessCFlags() []string {
	return append([]string{"-DKML_NB_UNDEFINED=" + strconv.Itoa(nbUndefined) + "LL", "-DKML_NB_DOUBLE_OFFSET=" + strconv.FormatInt(nbDoubleOffset, 10) + "LL"}, DeadStripFlags(e.exportsRuntime())...)
}

// emitCaughtRecord is the { i8 tag, i64 payload } caught-value aggregate
// built from its two words.
func (e *Emitter) emitCaughtRecord(tag, pay string) string {
	r0, r := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } undef, i8 %s, 0", r0, tag))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } %s, i64 %s, 1", r, r0, pay))
	return r
}
