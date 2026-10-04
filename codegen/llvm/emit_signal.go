package llvm

// emit_signal.go — the native side's view of an AbortSignal. AbortSignal is
// a class of the global module lib/node/kml_event_target.ts (TDD-00232); the
// fetch await loop and the event loop's resume scan, which run where no
// TypeScript can, read three of its private fields through these accessors,
// generated from the class's layout:
//
//	@__kml_signal_aborted(ptr sig) -> i1   #aborted, or #deadline elapsed
//	@__kml_signal_reason(ptr sig)  -> i64  #reason (a NaN-boxed any)
//	@__kml_signal_timeout(ptr sig) -> i1   #deadline set (AbortSignal.timeout)
//
// A null signal is never aborted. A program that never names AbortSignal
// has no such class; its accessors are those of a signal never given.

import "fmt"

// abortSignalClass is the program's AbortSignal class type, or false when
// the program does not include the global module.
func (e *Emitter) abortSignalClass() (Type, bool) {
	return e.globalClass("AbortSignal")
}

// globalClass is the class type a global module implements global name with
// (TDD-00232), or false when the program does not include the module.
func (e *Emitter) globalClass(name string) (Type, bool) {
	linked, ok := e.globalLinks[name]
	if !ok {
		return Type{}, false
	}
	info, ok := e.classes[linked]
	if !ok {
		return Type{}, false
	}
	return info.Ty, true
}

// isAbortSignalType reports whether t is the AbortSignal class (or one
// derived from it).
func (e *Emitter) isAbortSignalType(t Type) bool {
	sig, ok := e.abortSignalClass()
	if !ok || !t.IsClass {
		return false
	}
	for name := t.ClassName; name != ""; {
		if name == sig.ClassName {
			return true
		}
		info, found := e.classes[name]
		if !found {
			return false
		}
		name = info.BaseClass
	}
	return false
}

func (e *Emitter) ensureSignalAborted() {
	if e.usedSignalAborted {
		return
	}
	e.usedSignalAborted = true
	sig, ok := e.abortSignalClass()
	if !ok {
		e.emitGlobal(`define i1 @__kml_signal_aborted(ptr %sig) {
entry:
  ret i1 0
}
define i64 @__kml_signal_reason(ptr %sig) {
entry:
  ret i64 ` + fmt.Sprint(nbUndefined) + `
}
define i1 @__kml_signal_timeout(ptr %sig) {
entry:
  ret i1 0
}`)
		return
	}
	e.ensurePerformanceNow()
	abIdx, abTy, sig, _ := e.classField(sig, "#aborted")
	rIdx, _, _, _ := e.classField(sig, "#reason")
	dlIdx, dlTy, _, _ := e.classField(sig, "#deadline")
	structIR := sig.StructIR()
	// The deadline as a double (a `number` field is either width).
	dlLoad := "  %dl = load double, ptr %dl_p, align 8"
	if dlTy.IR != "double" {
		dlLoad = fmt.Sprintf("  %%dl_i = load %s, ptr %%dl_p, align 8\n  %%dl = sitofp %s %%dl_i to double", dlTy.IR, dlTy.IR)
	}
	e.emitGlobal(fmt.Sprintf(`define i1 @__kml_signal_aborted(ptr %%sig) {
entry:
  %%isnull = icmp eq ptr %%sig, null
  br i1 %%isnull, label %%no, label %%chk
chk:
  %%ab_p = getelementptr %[1]s, ptr %%sig, i32 0, i32 %[2]d
  %%ab = load %[3]s, ptr %%ab_p, align 1
  %%isab = icmp ne %[3]s %%ab, 0
  br i1 %%isab, label %%yes, label %%chkdl
chkdl:
  %%dl_p = getelementptr %[1]s, ptr %%sig, i32 0, i32 %[4]d
%[5]s
  %%hasdl = fcmp ogt double %%dl, 0.0
  br i1 %%hasdl, label %%cmpdl, label %%no
cmpdl:
  %%now = call double @__kml_performance_now()
  %%elapsed = fcmp oge double %%now, %%dl
  br i1 %%elapsed, label %%yes, label %%no
yes:
  ret i1 1
no:
  ret i1 0
}
define i64 @__kml_signal_reason(ptr %%sig) {
entry:
  %%r_p = getelementptr %[1]s, ptr %%sig, i32 0, i32 %[6]d
  %%r = load i64, ptr %%r_p, align 8
  ret i64 %%r
}
define i1 @__kml_signal_timeout(ptr %%sig) {
entry:
  %%dl_p = getelementptr %[1]s, ptr %%sig, i32 0, i32 %[4]d
%[5]s
  %%t = fcmp ogt double %%dl, 0.0
  ret i1 %%t
}`, structIR, abIdx, abTy.IR, dlIdx, dlLoad, rIdx))
}

// isGlobalClassInstance reports t an instance of the class a global module
// implements global name with, or of a class derived from it.
func (e *Emitter) isGlobalClassInstance(t Type, name string) bool {
	cls, ok := e.globalClass(name)
	if !ok || !t.IsClass || t.IsDynamic {
		return false
	}
	for c, i := t.ClassName, 0; c != "" && i < 64; i++ {
		if c == cls.ClassName {
			return true
		}
		c = e.classes[c].BaseClass
	}
	return false
}
