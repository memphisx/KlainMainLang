package llvm

// emit_signal.go — the native side's view of an AbortSignal. AbortSignal is
// a class of the global module lib/node/kml_event_target.ts (TDD-00232); the
// fetch await loop and the event loop's resume scan, which run where no
// TypeScript can, read three of its private fields through fetch.c's
// accessors, at the offsets signalCFlags hands it from the class's layout:
//
//	__kml_signal_aborted(sig)  #aborted, or #deadline elapsed
//	__kml_signal_reason(sig)   #reason (a NaN-boxed any)
//	__kml_signal_timeout(sig)  #deadline set (AbortSignal.timeout)
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
	if !ok || !t.IsClass() {
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
}

// signalCFlags hand fetch.c the AbortSignal fields it reads: their byte
// offsets, #aborted's width and whether #deadline is a double, or
// KML_SIG_AB=-1 when the program has no AbortSignal class.
func (e *Emitter) signalCFlags() []string {
	sig, ok := e.abortSignalClass()
	if !ok || !e.usedSignalAborted {
		return []string{"-DKML_SIG_AB=-1", "-DKML_SIG_AB_SIZE=1", "-DKML_SIG_REASON=0", "-DKML_SIG_DL=0", "-DKML_SIG_DL_DOUBLE=1"}
	}
	abIdx, abTy, sig, ok1 := e.classField(sig, "#aborted")
	rIdx, _, _, ok2 := e.classField(sig, "#reason")
	dlIdx, dlTy, _, ok3 := e.classField(sig, "#deadline")
	if !ok1 || !ok2 || !ok3 {
		panic("internal: AbortSignal lacks a field fetch.c reads")
	}
	dbl := 0
	if dlTy.IR == "double" {
		dbl = 1
	}
	return []string{
		fmt.Sprintf("-DKML_SIG_AB=%d", sig.FieldOffset(abIdx)),
		fmt.Sprintf("-DKML_SIG_AB_SIZE=%d", abTy.Align()),
		fmt.Sprintf("-DKML_SIG_REASON=%d", sig.FieldOffset(rIdx)),
		fmt.Sprintf("-DKML_SIG_DL=%d", sig.FieldOffset(dlIdx)),
		fmt.Sprintf("-DKML_SIG_DL_DOUBLE=%d", dbl),
	}
}

// isGlobalClassInstance reports t an instance of the class a global module
// implements global name with, or of a class derived from it.
func (e *Emitter) isGlobalClassInstance(t Type, name string) bool {
	cls, ok := e.globalClass(name)
	if !ok || !t.IsClass() || t.IsDynamic() {
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
