// emit_memory.go — Memory.free(x): Stage 1 of the staged manual-memory-
// management plan in STATUS.md. A raw, unsafe, opt-in escape hatch — not a
// general change to this compiler's "malloc everything, free nothing"
// default. Recognized as a pseudo-namespace, like Math/JSON/process/fs —
// not a real importable module.
//
// Shallow free only, deliberately: frees the value's own top-level heap
// allocation(s) (and, for Map/Set/closures, their own internal backing
// buffers — see ensureMapFree/ensureClosureFree in runtime.go), never
// anything reachable *through* it (a string field inside a freed object, an
// element of a freed array, a captured variable's shared cell). No
// analysis, no double-free detection, no use-after-free protection beyond
// nulling out a named variable's own storage after freeing it — exactly as
// unsafe as C's own free(), by design.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitMemoryFree implements Memory.free(x).
func (e *Emitter) emitMemoryFree(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Memory.free takes exactly 1 argument", pos.Line, pos.Col)
	}

	// Named-variable case: free the underlying pointer(s) directly from the
	// symbol table, then null out the symbol's own storage — a subsequent
	// read at least sees null (a clean crash on next dereference) rather
	// than freed heap memory, without pretending to prevent misuse from any
	// other alias of the same value.
	if id, ok := args[0].(*ast.Identifier); ok {
		sym, found := e.lookup(id.Name)
		if !found {
			return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
		}
		if sym.Ty.IsArray {
			e.ensureFree()
			dataPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtr, sym.Ptr))
			e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", dataPtr))
			e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", sym.Ptr))
			e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", sym.LenPtr))
			return Value{Ty: TypeVoid}, nil
		}
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, sym.Ptr))
		if err := e.freeResolvedPointer(ptrReg, sym.Ty, pos); err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", sym.Ptr))
		return Value{Ty: TypeVoid}, nil
	}

	// General expression case: evaluate and free; nothing to null out since
	// there's no variable storage to write back into.
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if val.Ty.IsArray {
		e.ensureFree()
		dataPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtr, val.Ref))
		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", dataPtr))
		return Value{Ty: TypeVoid}, nil
	}
	if err := e.freeResolvedPointer(val.Ref, val.Ty, pos); err != nil {
		return Value{}, err
	}
	return Value{Ty: TypeVoid}, nil
}

// freeResolvedPointer frees ptrReg — a plain `ptr` register already holding
// the value's own top-level heap pointer (loaded from a Symbol, or a
// directly-evaluated expression's Value.Ref) — dispatching on ty for
// anything that needs more than a single free() call.
func (e *Emitter) freeResolvedPointer(ptrReg string, ty Type, pos ast.Pos) error {
	switch {
	case ty.IsMap || ty.IsSet:
		e.ensureMapFree()
		e.emitInstr(fmt.Sprintf("call void @__kml_map_free(ptr %s)", ptrReg))
	case ty.IsFunc:
		e.ensureClosureFree()
		e.emitInstr(fmt.Sprintf("call void @__kml_closure_free(ptr %s)", ptrReg))
	case ty.IsObject || ty.IsPromise || (ty.IR == "ptr" && !ty.IsDynamic && !ty.IsArray):
		// A plain single heap pointer: string, object literal/interface
		// value, or an un-awaited Promise's malloc'd slot.
		e.ensureFree()
		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", ptrReg))
	default:
		return fmt.Errorf("%d:%d: Memory.free is not supported for this type — nothing heap-allocated to free (or, for any/unknown, no safe way to tell what's inside without runtime tag inspection this builtin doesn't do)", pos.Line, pos.Col)
	}
	return nil
}
