// emit_classes.go — TDD-00009 Stage 1: class registration (fields, ctor and
// method signatures), constructor/method emission, `this`, `new
// ClassName(args)`, method-call dispatch, and Stage 1a's class-based
// for...of iterator protocol.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// ClassInfo is the registered shape + behavior of one user-defined class,
// populated by registerClasses before any function or method body is
// emitted (mirroring how registerInterfaces/registerFunctions front-load
// their own registries). Ty is also stored into e.interfaces[name] so
// resolveType's existing named-type lookup resolves a class name in any
// type annotation with no changes of its own.
type ClassInfo struct {
	Ty          Type // ClassType(name, fields) — the instance's storage type
	Constructor *ast.FunctionDeclaration
	CtorSig     FuncSig // RetType always TypeVoid; zero value if Constructor == nil
	Methods     map[string]*ast.FunctionDeclaration
	MethodSigs  map[string]FuncSig
	// TagID is this class's compile-time-assigned runtime identity (TDD-00009
	// Stage 2): a small monotonic integer, one per class in declaration order,
	// stored into every instance's hidden ClassTagField at construction time
	// and compared against by instanceof. Assigned in registerClasses's Pass B
	// — no cross-pass persistence needed, since Pass B already iterates
	// classes in the same order Pass A does.
	TagID int64
}

// canonicalizeClassTy swaps a class-typed Type for the live, fully-resolved
// registry entry (e.classes[ClassName].Ty) whenever one is available.
//
// Why this exists: Field.Ty is stored by value, not by reference, so a
// self- or mutually-referential class field (`class Node { nextNode: Node |
// null }`) necessarily captures a *snapshot* of the referenced class's Type
// at the moment it was resolved — which, for a genuine self-reference, is
// always the placeholder registerClasses seeds before that class's own
// fields exist yet (see registerClasses's Pass A). That snapshot's Fields is
// permanently stale (empty, for direct self-reference) no matter what
// e.classes is later updated to, because Go copies the value in rather than
// aliasing it. Left alone, this makes a *second* field access chained off
// the first (`node.nextNode.value`) fail with "no field 'value'" even
// though node.nextNode is a perfectly valid Node pointer at runtime.
//
// The fix is narrow rather than architectural: every place that returns a
// field's type as the type of the expression *for a caller to potentially
// drill into further* (emitMember, emitOptionalMember, inferExprType's
// field-read case) re-resolves a class-typed field's type through this
// helper before handing it back, so the caller always sees the final,
// fully-resolved field list — without changing Field/Type's storage shape
// or touching any of the many other call sites that read a field's type
// for a non-chaining purpose (coercion, alignment, GEP storage width),
// where the snapshot's IR-level shape (always "ptr" for any class) was
// already correct and sufficient.
func (e *Emitter) canonicalizeClassTy(ty Type) Type {
	if ty.IsClass {
		if info, ok := e.classes[ty.ClassName]; ok {
			// Nullable is a property of the field's own annotation (`Node |
			// null`), not of the class itself — info.Ty is the bare class
			// registry entry and is never Nullable, so swapping it in
			// wholesale would silently discard a caller's `| null`
			// annotation (found via instanceof's null-check reduction for a
			// nullable class-typed field, TDD-00009 Stage 2).
			canon := info.Ty
			canon.Nullable = ty.Nullable
			return canon
		}
	}
	return ty
}

// buildParamSig resolves a parameter list into a FuncSig's parameter half
// (types/names/defaults/rest), the same per-parameter rules
// registerFunctions uses for top-level functions: an explicit annotation is
// resolved via resolveType; a rest parameter with no annotation defaults to
// number[]; anything else unannotated defaults to (inferred) TypeI64.
func (e *Emitter) buildParamSig(params []ast.Param) FuncSig {
	var sig FuncSig
	for _, p := range params {
		var pty Type
		if p.Type != nil {
			pty = e.resolveType(p.Type)
		} else if p.Rest {
			pty = ArrayOf(TypeI64)
		} else {
			pty = TypeI64
			pty.Inferred = true
		}
		sig.ParamTypes = append(sig.ParamTypes, pty)
		sig.ParamNames = append(sig.ParamNames, p.Name)
		sig.Defaults = append(sig.Defaults, p.Default)
	}
	if len(params) > 0 && params[len(params)-1].Rest {
		sig.HasRest = true
	}
	return sig
}

// registerClasses pre-scans all top-level class declarations, resolving
// field/constructor/method shapes before any function or method body is
// emitted — mirroring registerInterfaces (field resolution) and
// registerFunctions (signature building, including best-effort
// unannotated-return-type inference).
func (e *Emitter) registerClasses(prog *ast.Program) error {
	// Pass A: register every class under a placeholder type (correct IR/
	// IsObject/IsClass/ClassName, no fields yet) before resolving any
	// field/param/return type. Without this, a self-referential field
	// (`class Node { nextNode: Node | null }`) or a forward reference to a
	// class declared later in the file would try to resolve a name that
	// isn't in e.interfaces yet — ResolveTypeName's unknown-name fallback
	// silently defaults to TypeI64 rather than erroring, which is a silent
	// miscompile, not just a missed feature (found while building this
	// stage's linked-list iterator example).
	for _, stmt := range prog.Body {
		cd, ok := stmt.(*ast.ClassDeclaration)
		if !ok {
			continue
		}
		e.interfaces[cd.Name] = ClassType(cd.Name, nil)
	}

	var nextTagID int64
	for _, stmt := range prog.Body {
		cd, ok := stmt.(*ast.ClassDeclaration)
		if !ok {
			continue
		}

		fields := make([]Field, len(cd.Fields))
		for i, f := range cd.Fields {
			if f.Name == ClassTagField {
				return fmt.Errorf("%d:%d: class '%s' cannot declare a field named '%s' — reserved for the compiler's internal runtime type tag", cd.GetPos().Line, cd.GetPos().Col, cd.Name, ClassTagField)
			}
			fields[i] = Field{Name: f.Name, Ty: e.resolveType(f.Type)}
		}
		// A class with fields but no constructor would leave every instance's
		// fields as uninitialized (garbage) malloc'd memory — this compiler
		// has no field-initializer syntax (Stage 0 scope) to fall back on, and
		// emitObjectLiteral already refuses to leave an object field unset;
		// requiring a constructor here is the same "no silently uninitialized
		// state" philosophy, just enforced at class-declaration time instead
		// of at each construction site.
		if len(fields) > 0 && cd.Constructor == nil {
			return fmt.Errorf("%d:%d: class '%s' has fields but no constructor to initialize them", cd.GetPos().Line, cd.GetPos().Col, cd.Name)
		}

		classTy := ClassType(cd.Name, fields)
		e.interfaces[cd.Name] = classTy

		info := ClassInfo{
			Ty:         classTy,
			Methods:    make(map[string]*ast.FunctionDeclaration),
			MethodSigs: make(map[string]FuncSig),
			TagID:      nextTagID,
		}
		nextTagID++

		if cd.Constructor != nil {
			info.Constructor = cd.Constructor
			sig := e.buildParamSig(cd.Constructor.Params)
			sig.RetType = TypeVoid
			info.CtorSig = sig
		}

		for _, m := range cd.Methods {
			if _, dup := info.MethodSigs[m.Name]; dup {
				return fmt.Errorf("%d:%d: class '%s' declares more than one method named '%s'", m.GetPos().Line, m.GetPos().Col, cd.Name, m.Name)
			}
			sig := e.buildParamSig(m.Params)
			if m.ReturnType != nil {
				sig.RetType = e.resolveType(m.ReturnType)
			} else {
				sig.RetType = TypeVoid
				// Best-effort inference, same as registerFunctions — but the
				// method's own "this" also needs to be visible in the temp
				// scope inferUnannotatedReturnType pushes for its parameters,
				// since the inferred return expression may read this.field.
				e.pushScope()
				e.define("this", Symbol{Ty: classTy})
				if inferred, ok := e.inferUnannotatedReturnType(m.Body, sig.ParamNames, sig.ParamTypes); ok {
					sig.RetType = inferred
				}
				e.popScope()
			}
			info.Methods[m.Name] = m
			info.MethodSigs[m.Name] = sig
		}

		e.classes[cd.Name] = info
	}
	return nil
}

// emitClassDecl emits one class's constructor (if declared) and every
// method as ordinary LLVM functions named "@ClassName_constructor" /
// "@ClassName_methodName".
func (e *Emitter) emitClassDecl(cd *ast.ClassDeclaration) error {
	info := e.classes[cd.Name]
	if cd.Constructor != nil {
		llvmName := cd.Name + "_constructor"
		if err := e.emitClassMember(llvmName, info.Ty, cd.Constructor.Params, info.CtorSig, cd.Constructor.Body, TypeVoid, cd.Constructor.GetPos()); err != nil {
			return err
		}
	}
	for _, m := range cd.Methods {
		sig := info.MethodSigs[m.Name]
		llvmName := cd.Name + "_" + m.Name
		if err := e.emitClassMember(llvmName, info.Ty, m.Params, sig, m.Body, sig.RetType, m.GetPos()); err != nil {
			return err
		}
	}
	return nil
}

// emitClassMember emits one method or constructor as a plain LLVM function,
// with an implicit receiver spliced in as LLVM parameter slot 0 (bound to
// scope symbol "this") ahead of the member's own declared parameters. This
// is a deliberately separate function from emitFunctionDecl rather than a
// refactor of it: methods are structurally never async (the parser hardcodes
// isAsync=false for every class member), so there's no async/coroutine
// branch to thread a receiver through, and duplicating the smaller
// non-async subset carries far lower regression risk than adding a
// conditional receiver parameter to a function every top-level function
// already depends on (same reasoning docs/adr/ADR-00061.md gives for
// resolveObjectPtr's ObjectLiteral case duplicating emitObjectLiteral).
func (e *Emitter) emitClassMember(llvmName string, classTy Type, params []ast.Param, sig FuncSig, body *ast.BlockStatement, retType Type, pos ast.Pos) error {
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = retType
	e.pushScope()

	// Implicit receiver: LLVM parameter slot 0, never part of the AST's own
	// parameter list.
	llvmParams := []string{"ptr %p_this"}
	thisPtr := "%v_this"
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", thisPtr))
	e.emitInstr(fmt.Sprintf("store ptr %%p_this, ptr %s, align 8", thisPtr))
	e.define("this", Symbol{Ptr: thisPtr, Ty: classTy})

	for i, p := range params {
		pty := sig.ParamTypes[i]
		if pty.IsDynamic || containsDynamicElement(pty) {
			return fmt.Errorf("%d:%d: any/unknown is not yet supported as a method parameter type", pos.Line, pos.Col)
		}
		if pty.IsArray {
			llvmParams = append(llvmParams,
				fmt.Sprintf("ptr %%p_%s_ptr", p.Name),
				fmt.Sprintf("i64 %%p_%s_len", p.Name),
			)
			ptrAlloca := "%v_" + p.Name + "_ptr"
			lenAlloca := "%v_" + p.Name + "_len"
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrAlloca))
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))
			e.emitInstr(fmt.Sprintf("store ptr %%p_%s_ptr, ptr %s, align 8", p.Name, ptrAlloca))
			e.emitInstr(fmt.Sprintf("store i64 %%p_%s_len, ptr %s, align 8", p.Name, lenAlloca))
			e.define(p.Name, Symbol{Ptr: ptrAlloca, LenPtr: lenAlloca, Ty: pty})
		} else {
			llvmParams = append(llvmParams, fmt.Sprintf("%s %%p_%s", pty.IR, p.Name))
			ptrName := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, pty.IR, pty.Align()))
			e.emitInstr(fmt.Sprintf("store %s %%p_%s, ptr %s, align %d", pty.IR, p.Name, ptrName, pty.Align()))
			e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
		}
	}

	for _, stmt := range body.Body {
		if err := e.emitStmt(stmt); err != nil {
			return err
		}
	}

	if retType.IR == "void" {
		e.emitTerminator("ret void")
	} else {
		e.emitTerminator("unreachable")
	}
	e.functions.WriteString(fmt.Sprintf("\ndefine %s @%s(%s) {\nentry:\n",
		retType.LLVMRetType(), llvmName, strings.Join(llvmParams, ", ")))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
	e.blockDone = false

	return nil
}

// emitThisExpression evaluates `this` — valid only inside a method or
// constructor body, where emitClassMember has already bound scope symbol
// "this" to the receiver. Same load-from-alloca shape any other
// object-typed identifier read uses.
func (e *Emitter) emitThisExpression(pos ast.Pos) (Value, error) {
	sym, ok := e.lookup("this")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: 'this' is only valid inside a method or constructor body", pos.Line, pos.Col)
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", reg, sym.Ptr))
	return Value{Ref: reg, Ty: sym.Ty}, nil
}

// emitNewExpression evaluates `new ClassName(args)`: malloc an instance
// sized to the class's field layout, call its constructor (if any) with the
// fresh pointer as the implicit receiver, and return the pointer.
func (e *Emitter) emitNewExpression(ex *ast.NewExpression) (Value, error) {
	info, ok := e.classes[ex.ClassName]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: unknown class '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.ClassName)
	}

	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, info.Ty.StructSize()))

	tagGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", tagGep, info.Ty.StructIR(), dataReg))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", info.TagID, tagGep))

	if info.Constructor != nil {
		if len(ex.Args) != len(info.CtorSig.ParamTypes) {
			return Value{}, fmt.Errorf("%d:%d: %s constructor expects %d argument(s), got %d",
				ex.GetPos().Line, ex.GetPos().Col, ex.ClassName, len(info.CtorSig.ParamTypes), len(ex.Args))
		}
		argParts := []string{"ptr " + dataReg}
		for i, a := range ex.Args {
			val, err := e.emitExpr(a)
			if err != nil {
				return Value{}, err
			}
			val = e.coerce(val, info.CtorSig.ParamTypes[i])
			argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
		}
		e.emitInstr(fmt.Sprintf("call void @%s_constructor(%s)", ex.ClassName, strings.Join(argParts, ", ")))
	} else if len(ex.Args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: class '%s' has no constructor but was called with %d argument(s)",
			ex.GetPos().Line, ex.GetPos().Col, ex.ClassName, len(ex.Args))
	}

	return Value{Ref: dataReg, Ty: info.Ty}, nil
}

// emitClassMethodCall emits `objExpr.methodName(args)` as a plain static
// call `call RETTY @ClassName_methodName(ptr this, ARGS...)` — objExpr is
// evaluated generically via emitExpr, so this works on any expression
// (a named variable, a call result, a chained new ClassName(...), ...), not
// just a bare identifier.
func (e *Emitter) emitClassMethodCall(objTy Type, objExpr ast.Expression, methodName string, args []ast.Expression, pos ast.Pos) (Value, error) {
	info := e.classes[objTy.ClassName]
	sig := info.MethodSigs[methodName]

	thisVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	if len(args) != len(sig.ParamTypes) {
		return Value{}, fmt.Errorf("%d:%d: %s.%s expects %d argument(s), got %d",
			pos.Line, pos.Col, objTy.ClassName, methodName, len(sig.ParamTypes), len(args))
	}

	argParts := []string{"ptr " + thisVal.Ref}
	for i, a := range args {
		val, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		val = e.coerce(val, sig.ParamTypes[i])
		argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
	}

	llvmName := objTy.ClassName + "_" + methodName
	if sig.RetType.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", llvmName, strings.Join(argParts, ", ")))
		return Value{Ty: TypeVoid}, nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", reg, sig.RetType.LLVMRetType(), llvmName, strings.Join(argParts, ", ")))
	return Value{Ref: reg, Ty: sig.RetType}, nil
}

// emitForOfClassIterator implements Stage 1a's for...of extension: a class
// declaring a zero-arg `next(): T | null` method iterates by calling next()
// repeatedly until it returns the sentinel (null for a ptr-shaped T, 0
// otherwise — the same "zero/null doubles as absent" convention .find()
// already uses, including its same pre-existing ambiguity for numeric T,
// not a new limitation here). This is a genuinely different loop shape from
// the array/Map/Set path in emitForOf (no known length, one call per
// iteration instead of an index into a pre-materialized array), so it's a
// fully independent loop body — it just reuses the cond/body/inc/end labels
// and the break/continue/pendingLabel bookkeeping the caller already set up.
// recvVal is the already-evaluated receiver (evaluated once by the caller,
// not per iteration, since s.Iterable may be an arbitrary expression).
func (e *Emitter) emitForOfClassIterator(s *ast.ForOfStatement, objTy Type, nextSig FuncSig, recvVal Value, condL, bodyL, incL, endL string) error {
	elemTy := nextSig.RetType
	elemTy.Nullable = false

	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resultAlloca, elemTy.IR, elemTy.Align()))

	varPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", varPtr, elemTy.IR, elemTy.Align()))
	e.define(s.VarName, Symbol{Ptr: varPtr, Ty: elemTy})

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	nextReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @%s_next(ptr %s)", nextReg, elemTy.IR, objTy.ClassName, recvVal.Ref))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, nextReg, resultAlloca, elemTy.Align()))
	doneReg := e.freshReg()
	zero := "null"
	if elemTy.IR != "ptr" {
		zero = "0"
	}
	e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, %s", doneReg, elemTy.IR, nextReg, zero))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", doneReg, endL, bodyL))

	e.emitLabel(bodyL)
	loaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loaded, elemTy.IR, resultAlloca, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, loaded, varPtr, elemTy.Align()))
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

// emitInstanceOf implements `x instanceof ClassName` (TDD-00009 Stage 2). The
// right-hand side is never evaluated as a general expression — it must be a
// bare identifier naming a registered user-defined class, since this
// compiler has no runtime constructor values to compare against (Error,
// Date, Response, and unregistered names are all rejected here with the same
// error, since none of them carry the class tag this checks).
//
// Four cases, ordered cheapest first — before Stage 3 (inheritance) exists,
// every class-typed variable's concrete class is already known at compile
// time, so only the any/unknown case ever needs a real runtime check:
//  1. Left is any/unknown: unbox, confirm the runtime tag is "object", then
//     compare the pointed-to instance's own hidden class tag against the
//     target class's TagID. The only genuinely non-trivial case.
//  2. Left statically IsClass with a matching ClassName: always true, unless
//     the type is nullable, in which case it reduces to a ptr-vs-null check.
//  3. Left statically IsClass with a different ClassName: constant false —
//     no inheritance means two distinct classes can never be related.
//  4. Any other static type (number, string, bool, array, plain object,
//     Error, Date, Response, ...): constant false, matching real JS (a
//     non-object or non-matching-constructor value is never `instanceof`
//     anything).
func (e *Emitter) emitInstanceOf(ex *ast.BinaryExpression) (Value, error) {
	rightIdent, ok := ex.Right.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: right-hand side of instanceof must be a class name", ex.GetPos().Line, ex.GetPos().Col)
	}
	info, ok := e.classes[rightIdent.Name]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: instanceof is only supported against user-defined classes; '%s' is not a registered class", ex.GetPos().Line, ex.GetPos().Col, rightIdent.Name)
	}

	leftVal, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}

	if leftVal.Ty.IsDynamic {
		tag, payload := e.emitUnboxTagPayload(leftVal)
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))

		resultAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resultAlloca))
		objL := e.freshLabel("instanceof.obj")
		notObjL := e.freshLabel("instanceof.notobj")
		mergeL := e.freshLabel("instanceof.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, notObjL))

		e.emitLabel(objL)
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", ptrReg, payload))
		tagGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", tagGep, info.Ty.StructIR(), ptrReg))
		loadedTag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loadedTag, tagGep))
		classMatch := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", classMatch, loadedTag, info.TagID))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", classMatch, resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(notObjL)
		e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resultAlloca))
		return Value{Ref: result, Ty: TypeBool}, nil
	}

	if leftVal.Ty.IsClass && leftVal.Ty.ClassName == rightIdent.Name {
		if leftVal.Ty.Nullable {
			result := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", result, leftVal.Ref))
			return Value{Ref: result, Ty: TypeBool}, nil
		}
		return Value{Ref: "1", Ty: TypeBool}, nil
	}

	return Value{Ref: "0", Ty: TypeBool}, nil
}
