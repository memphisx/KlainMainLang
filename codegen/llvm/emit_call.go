package llvm

import (
	"fmt"
	"runtime"
	"strings"
	"KlainMainLang/ast"
)

// Call dispatch (emitCall router) and all built-in call implementations:
// console.log, JSON, Math, Number statics, parseInt/parseFloat.

func (e *Emitter) emitCall(ex *ast.CallExpression) (Value, error) {
	// Special-case: console.log(...) and array.push(...)
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "String" {
			return e.emitStringStaticCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Number" {
			return e.emitNumberStaticCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Math" {
			return e.emitMathCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "JSON" {
			switch mem.Property {
			case "stringify":
				return e.emitJSONStringify(ex.Args, ex.GetPos())
			case "parse":
				return e.emitJSONParse(ex.Args, TypePtr, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Date" && mem.Property == "now" {
			return e.emitDateNow()
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "performance" && mem.Property == "now" {
			e.ensurePerformanceNow()
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call double @__kml_performance_now()", r))
			return Value{Ref: r, Ty: TypeF64}, nil
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Date" && mem.Property == "parse" {
			return e.emitDateParse(ex.Args, ex.GetPos())
		}
		if isDateSetterName(mem.Property) && e.inferExprType(mem.Object).IsDate {
			return e.emitDateSetterCall(mem, mem.Property, ex.Args, ex.GetPos())
		}
		if isDateMethodName(mem.Property) && e.inferExprType(mem.Object).IsDate {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitDateCall(objVal, mem.Property, ex.GetPos())
		}
		if isResponseMethodName(mem.Property) && e.inferExprType(mem.Object).IsResponse {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitResponseCall(objVal, mem.Property, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Array" {
			if mem.Property == "isArray" {
				if len(ex.Args) != 1 {
					return Value{}, fmt.Errorf("%d:%d: Array.isArray takes exactly 1 argument", ex.GetPos().Line, ex.GetPos().Col)
				}
				isArr := e.inferExprType(ex.Args[0]).IsArray
				if isArr {
					return Value{Ref: "true", Ty: TypeBool}, nil
				}
				return Value{Ref: "false", Ty: TypeBool}, nil
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Object" {
			switch mem.Property {
			case "groupBy":
				return e.emitObjectGroupBy(ex.Args, ex.GetPos())
			case "keys":
				return e.emitObjectKeys(ex.Args, ex.GetPos())
			case "values":
				return e.emitObjectValues(ex.Args, ex.GetPos())
			case "entries":
				return e.emitObjectEntries(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "process" {
			switch mem.Property {
			case "exit":
				return e.emitProcessExit(ex.Args, ex.GetPos())
			case "readLineSync":
				if len(ex.Args) != 0 {
					return Value{}, fmt.Errorf("%d:%d: process.readLineSync takes no arguments", ex.GetPos().Line, ex.GetPos().Col)
				}
				e.ensureReadLineSync()
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_read_line_sync()", r))
				return Value{Ref: r, Ty: TypePtr}, nil
			case "execFileSync":
				return e.emitProcessExecFileSync(ex.Args, ex.GetPos())
			case "cwd":
				return e.emitProcessCwd(ex.Args, ex.GetPos())
			case "chdir":
				return e.emitProcessChdir(ex.Args, ex.GetPos())
			case "kill":
				return e.emitProcessKill(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "fs" {
			switch mem.Property {
			case "readFileSync":
				return e.emitFsReadFileSync(ex.Args, ex.GetPos())
			case "writeFileSync":
				return e.emitFsWriteFileSync(ex.Args, ex.GetPos())
			case "appendFileSync":
				return e.emitFsAppendFileSync(ex.Args, ex.GetPos())
			case "existsSync":
				return e.emitFsExistsSync(ex.Args, ex.GetPos())
			case "unlinkSync":
				return e.emitFsUnlinkSync(ex.Args, ex.GetPos())
			case "mkdirSync":
				return e.emitFsMkdirSync(ex.Args, ex.GetPos())
			case "rmdirSync":
				return e.emitFsRmdirSync(ex.Args, ex.GetPos())
			case "renameSync":
				return e.emitFsRenameSync(ex.Args, ex.GetPos())
			case "copyFileSync":
				return e.emitFsCopyFileSync(ex.Args, ex.GetPos())
			case "readdirSync":
				return e.emitFsReaddirSync(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "crypto" {
			switch mem.Property {
			case "getRandomValues":
				return e.emitCryptoGetRandomValues(ex.Args, ex.GetPos())
			case "randomUUID":
				return e.emitCryptoRandomUUID(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Memory" && mem.Property == "free" {
			return e.emitMemoryFree(ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "console" {
			switch mem.Property {
			case "log", "info", "debug":
				return e.emitConsolePrint(ex.Args, 1, "")
			case "error":
				return e.emitConsolePrint(ex.Args, 2, "")
			case "warn":
				return e.emitConsolePrint(ex.Args, 2, "Warning: ")
			case "trace":
				return e.emitConsolePrint(ex.Args, 2, "Trace: ")
			case "assert":
				return e.emitConsoleAssert(ex.Args, ex.GetPos())
			case "dir":
				return e.emitConsoleDir(ex.Args, ex.GetPos())
			case "time":
				return e.emitConsoleTime(ex.Args, ex.GetPos())
			case "timeEnd":
				return e.emitConsoleTimeEnd(ex.Args, ex.GetPos())
			case "count":
				return e.emitConsoleCount(ex.Args, ex.GetPos())
			case "countReset":
				return e.emitConsoleCountReset(ex.Args, ex.GetPos())
			case "group":
				return e.emitConsoleGroup(ex.Args, ex.GetPos())
			case "groupEnd":
				return e.emitConsoleGroupEnd(ex.Args, ex.GetPos())
			}
		}
		if mem.Property == "push" {
			return e.emitPush(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "pop" {
			return e.emitPop(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "shift" {
			return e.emitShift(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "unshift" {
			return e.emitUnshift(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "splice" {
			return e.emitSplice(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "slice" {
			if e.inferExprType(mem.Object).IsArray {
				return e.emitArraySlice(mem, ex.Args, ex.GetPos())
			}
			return e.emitStringSlice(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "substring" {
			return e.emitStringSubstring(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "indexOf" {
			if e.inferExprType(mem.Object).IsArray {
				return e.emitArrayIndexOf(mem, ex.Args, ex.GetPos())
			}
			return e.emitStringIndexOf(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "includes" {
			if e.inferExprType(mem.Object).IsArray {
				return e.emitArrayIncludes(mem, ex.Args, ex.GetPos())
			}
			return e.emitStringIncludes(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "at" {
			if e.inferExprType(mem.Object).IsArray {
				return e.emitArrayAt(mem, ex.Args, ex.GetPos())
			}
			return e.emitStringAt(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "concat" {
			if e.inferExprType(mem.Object).IsArray {
				return e.emitArrayConcat(mem, ex.Args, ex.GetPos())
			}
		}
		if mem.Property == "findIndex" {
			return e.emitArrayFindIndex(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "reverse" {
			return e.emitArrayReverse(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "fill" {
			return e.emitArrayFill(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "toFixed" {
			return e.emitNumberToFixed(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "repeat" {
			return e.emitStringRepeat(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "padStart" {
			return e.emitStringPadStart(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "padEnd" {
			return e.emitStringPadEnd(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "charCodeAt" {
			return e.emitStringCharCodeAt(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "charAt" {
			return e.emitStringCharAtMethod(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "codePointAt" {
			return e.emitStringCodePointAt(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "search" {
			return e.emitStringSearch(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "localeCompare" {
			return e.emitStringLocaleCompare(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "trim" {
			return e.emitStringTrim(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "trimStart" {
			return e.emitStringTrimStart(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "trimEnd" {
			return e.emitStringTrimEnd(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "toUpperCase" {
			return e.emitStringToUpper(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "toLowerCase" {
			return e.emitStringToLower(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "startsWith" {
			return e.emitStringStartsWith(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "endsWith" {
			return e.emitStringEndsWith(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "replace" {
			return e.emitStringReplace(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "replaceAll" {
			return e.emitStringReplaceAll(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "split" {
			return e.emitStringSplit(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "map" {
			return e.emitArrayMap(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "filter" {
			return e.emitArrayFilter(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "reduce" {
			return e.emitArrayReduce(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "find" {
			return e.emitArrayFind(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "some" {
			return e.emitArraySome(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "every" {
			return e.emitArrayEvery(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "join" {
			return e.emitArrayJoin(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "sort" {
			return e.emitArraySort(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "forEach" {
			return e.emitArrayForEach(mem, ex.Args, ex.GetPos())
		}
		// Map<K,V> and Set<T> method dispatch.
		if id, ok := mem.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsMap {
				return e.emitMapCall(sym, mem.Property, ex.Args, ex.GetPos())
			}
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsSet {
				return e.emitSetCall(sym, mem.Property, ex.Args, ex.GetPos())
			}
		}
		// Calling a function-typed object field: obj.callback(...), none of
		// the hardcoded built-in method names above matched, so treat mem as
		// a plain value expression and call it as a closure if its static
		// type says it is one.
		if e.inferExprType(mem).IsFunc {
			memVal, err := e.emitExpr(mem)
			if err != nil {
				return Value{}, err
			}
			return e.emitClosureCallByPtr(memVal.Ref, memVal.Ty, ex.Args, ex.GetPos())
		}
	}

	// Calling a function value stored in an array element: arr[i](...).
	if idxEx, ok := ex.Callee.(*ast.IndexExpression); ok {
		if e.inferExprType(idxEx).IsFunc {
			idxVal, err := e.emitExpr(idxEx)
			if err != nil {
				return Value{}, err
			}
			return e.emitClosureCallByPtr(idxVal.Ref, idxVal.Ty, ex.Args, ex.GetPos())
		}
	}

	// Global built-in functions.
	if id, ok := ex.Callee.(*ast.Identifier); ok {
		switch id.Name {
		case "parseInt":
			return e.emitParseInt(ex.Args, ex.GetPos())
		case "parseFloat":
			return e.emitParseFloat(ex.Args, ex.GetPos())
		case "isNaN":
			return e.emitNumberIsNaN(ex.Args, ex.GetPos())
		case "isFinite":
			return e.emitNumberIsFinite(ex.Args, ex.GetPos())
		case "fetch":
			return e.emitFetch(ex.Args, ex.GetPos())
		case "btoa":
			return e.emitStringToStringBuiltin(ex.Args, ex.GetPos(), "btoa", "@__kml_btoa", e.ensureBase64Encode)
		case "atob":
			return e.emitStringToStringBuiltin(ex.Args, ex.GetPos(), "atob", "@__kml_atob", e.ensureBase64Decode)
		case "encodeURIComponent":
			return e.emitStringToStringBuiltin(ex.Args, ex.GetPos(), "encodeURIComponent", "@__kml_encode_uri_component", e.ensureEncodeURIComponent)
		case "decodeURIComponent":
			return e.emitStringToStringBuiltin(ex.Args, ex.GetPos(), "decodeURIComponent", "@__kml_decode_uri_component", e.ensureDecodeURIComponent)
		case "encodeURI":
			return e.emitStringToStringBuiltin(ex.Args, ex.GetPos(), "encodeURI", "@__kml_encode_uri", e.ensureEncodeURI)
		case "decodeURI":
			return e.emitStringToStringBuiltin(ex.Args, ex.GetPos(), "decodeURI", "@__kml_decode_uri", e.ensureDecodeURI)
		case "setTimeout":
			return e.emitSetTimeout(ex.Args, ex.GetPos())
		case "setInterval":
			return e.emitSetInterval(ex.Args, ex.GetPos())
		case "clearTimeout":
			return e.emitClearTimer(ex.Args, "clearTimeout", ex.GetPos())
		case "clearInterval":
			return e.emitClearTimer(ex.Args, "clearInterval", ex.GetPos())
		}
	}

	// Immediately-invoked arrow function: ((x: number) => x+1)(5)
	if af, ok := ex.Callee.(*ast.ArrowFunction); ok {
		closureVal, err := e.emitArrowFunction(af)
		if err != nil {
			return Value{}, err
		}
		return e.emitClosureCallByPtr(closureVal.Ref, closureVal.Ty, ex.Args, ex.GetPos())
	}

	// Call via bare identifier: named function or closure variable.
	if id, ok := ex.Callee.(*ast.Identifier); ok {
		// Named (top-level) function.
		if sig, found := e.funcs[id.Name]; found {
			var argParts []string
			// How many args map to regular (non-rest) params.
			regularCount := len(sig.ParamTypes)
			if sig.HasRest {
				regularCount-- // last param slot is the rest array
			}
			for i := 0; i < regularCount; i++ {
				var paramTy Type
				if i < len(sig.ParamTypes) {
					paramTy = sig.ParamTypes[i]
				}
				// Use provided arg or fall back to the default expression.
				if i < len(ex.Args) && !(sig.HasRest && i >= regularCount) {
					arg := ex.Args[i]
					if paramTy.IsArray {
						if arrId, ok := arg.(*ast.Identifier); ok {
							sym, ok := e.lookup(arrId.Name)
							if !ok {
								return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", arg.GetPos().Line, arg.GetPos().Col, arrId.Name)
							}
							if !sym.Ty.IsArray {
								return Value{}, fmt.Errorf("%d:%d: '%s' is not an array", arg.GetPos().Line, arg.GetPos().Col, arrId.Name)
							}
							ptrReg := e.freshReg()
							lenReg := e.freshReg()
							e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, sym.Ptr))
							e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, sym.LenPtr))
							argParts = append(argParts, "ptr "+ptrReg, "i64 "+lenReg)
						} else {
							val, err := e.emitExpr(arg)
							if err != nil {
								return Value{}, err
							}
							if !val.Ty.IsArray {
								return Value{}, fmt.Errorf("%d:%d: expression does not yield an array", arg.GetPos().Line, arg.GetPos().Col)
							}
							ptrReg := e.freshReg()
							lenReg := e.freshReg()
							e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
							e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
							argParts = append(argParts, "ptr "+ptrReg, "i64 "+lenReg)
						}
					} else {
						val, err := e.emitExpr(arg)
						if err != nil {
							return Value{}, err
						}
						if paramTy.Inferred && !isSafeNumericArg(val.Ty) {
							name := id.Name
							paramName := fmt.Sprintf("%d", i+1)
							if i < len(sig.ParamNames) {
								paramName = "'" + sig.ParamNames[i] + "'"
							}
							return Value{}, fmt.Errorf("%d:%d: parameter %s of '%s' has no type annotation (defaults to number) but was called with a non-numeric argument here — add an explicit type annotation", arg.GetPos().Line, arg.GetPos().Col, paramName, name)
						}
						if paramTy.IR != "" {
							val = e.coerce(val, paramTy)
						}
						argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
					}
				} else if i < len(sig.Defaults) && sig.Defaults[i] != nil {
					// Evaluate default expression at call site.
					val, err := e.emitExpr(sig.Defaults[i])
					if err != nil {
						return Value{}, fmt.Errorf("default value for param %d: %w", i, err)
					}
					if paramTy.IR != "" {
						val = e.coerce(val, paramTy)
					}
					argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
				} else {
					return Value{}, fmt.Errorf("%d:%d: missing argument %d with no default", ex.GetPos().Line, ex.GetPos().Col, i+1)
				}
			}
			// Pack rest args into a temporary heap array.
			if sig.HasRest {
				restStart := regularCount
				if restStart > len(ex.Args) {
					restStart = len(ex.Args)
				}
				restArgs := ex.Args[restStart:]
				restTy := sig.ParamTypes[len(sig.ParamTypes)-1]
				elemTy := TypeI64
				if restTy.ElemType != nil {
					elemTy = *restTy.ElemType
				}
				if len(restArgs) == 0 {
					argParts = append(argParts, "ptr null", "i64 0")
				} else {
					n := int64(len(restArgs))
					e.ensureMalloc()
					dataReg := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*int64(elemTy.Align())))
					for i, arg := range restArgs {
						val, err := e.emitExpr(arg)
						if err != nil {
							return Value{}, err
						}
						val = e.coerce(val, elemTy)
						gepReg := e.freshReg()
						e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
						e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, gepReg, elemTy.Align()))
					}
					argParts = append(argParts, fmt.Sprintf("ptr %s", dataReg), fmt.Sprintf("i64 %d", n))
				}
			}
			argsStr := strings.Join(argParts, ", ")
			if sig.RetType.IR == "void" {
				e.emitInstr(fmt.Sprintf("call void @%s(%s)", id.Name, argsStr))
				return Value{Ty: TypeVoid}, nil
			}
			reg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", reg, sig.RetType.LLVMRetType(), id.Name, argsStr))
			return Value{Ref: reg, Ty: sig.RetType}, nil
		}
		// Closure variable.
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsFunc {
			return e.emitClosureCall(sym, ex.Args, ex.GetPos())
		}
		return Value{}, fmt.Errorf("%d:%d: undefined function or closure '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name)
	}

	return Value{}, fmt.Errorf("%d:%d: only simple function calls are supported", ex.GetPos().Line, ex.GetPos().Col)
}

// =============================================================================
// Closure / arrow-function support
// =============================================================================

// CapturedVar describes one variable captured from an enclosing scope.

// emitConsolePrint is the shared core for all console.* output methods.
// fd=1 writes to stdout via printf; fd=2 writes to stderr via dprintf.
// prefix, if non-empty, is printed before the first argument on the same line.
//
// Each argument is printed on its own line (this compiler's own long-
// standing convention, not real console.log's single-space-joined-line
// behavior) — so console.group()'s indent is applied once at the very start
// (before prefix, or before the first argument if there's no prefix) and
// again before every argument after the first, since each of those starts a
// fresh line of its own.
func (e *Emitter) emitConsolePrint(args []ast.Expression, fd int, prefix string) (Value, error) {
	if fd == 2 {
		e.ensureDprintf()
	} else {
		e.ensurePrintf()
	}
	e.emitConsoleGroupIndent(fd)
	if prefix != "" {
		pfxPtr := e.internString(prefix)
		fmtStr := e.internString("%s")
		if fd == 2 {
			e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, ptr %s)", fmtStr, pfxPtr))
		} else {
			e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", fmtStr, pfxPtr))
		}
	}
	for i, arg := range args {
		if i > 0 {
			e.emitConsoleGroupIndent(fd)
		}
		val, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		if val.Ty.IsArray {
			return Value{}, fmt.Errorf("%d:%d: console output does not support arrays; iterate and print each element", arg.GetPos().Line, arg.GetPos().Col)
		}
		if val.Ty.IsDynamic {
			strVal, err := e.emitDynamicToString(val)
			if err != nil {
				return Value{}, err
			}
			fmtPtr := e.internString("%s\n")
			e.emitConsolePrintVal(strVal, fmtPtr, fd)
			continue
		}
		fmtPtr := e.internString(val.Ty.PrintfFmt() + "\n")
		e.emitConsolePrintVal(val, fmtPtr, fd)
	}
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleGroupIndent prints "  " (two spaces) once per current
// console.group() nesting level, with no trailing newline — called right
// before the start of every line this compiler's console.* output ever
// prints. Uses fd's own already-established printf-vs-dprintf convention
// (never dprintf on fd 1: mixing a raw fd write with stdio's own buffered
// printf on the same descriptor risks interleaving output out of order).
//
// Bails out immediately if the current block is already dead (past a
// terminator, e.g. unreachable code after return/process.exit/throw).
// emitLabel below unconditionally starts a fresh (reachable-looking) block
// regardless of blockDone, so without this guard a dead console.log call
// would "come back to life" partway through this helper — the depth value
// loaded before the loop's labels gets silently dropped (correctly, since
// it's genuinely dead), but the loop body after emitLabel would still
// reference it, and LLVM's verifier rejects that as a use of an undefined
// value. Every other value this function touches lives in an alloca
// (unconditionally emitted regardless of blockDone) except this one, so
// bailing out early here is the correct, minimal fix rather than reworking
// the loop to avoid the cross-block dependency.
func (e *Emitter) emitConsoleGroupIndent(fd int) {
	if e.blockDone {
		return
	}
	e.ensureConsoleGroupDepth()
	if fd == 2 {
		e.ensureDprintf()
	} else {
		e.ensurePrintf()
	}
	depthReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_console_group_depth, align 8", depthReg))

	counterPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", counterPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", counterPtr))

	loopL := e.freshLabel("group.indent.loop")
	bodyL := e.freshLabel("group.indent.body")
	doneL := e.freshLabel("group.indent.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(loopL)
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, counterPtr))
	cond := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", cond, cur, depthReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond, bodyL, doneL))

	e.emitLabel(bodyL)
	indentStr := e.internString("  ")
	fmtStr := e.internString("%s")
	if fd == 2 {
		e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, ptr %s)", fmtStr, indentStr))
	} else {
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", fmtStr, indentStr))
	}
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, counterPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(doneL)
}

// emitConsoleGroup implements console.group(label?): prints label (if
// given) at the current indent depth, then increases the depth by one so
// every subsequent console.* call (until the matching groupEnd) is indented
// one level further.
func (e *Emitter) emitConsoleGroup(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: console.group takes 0 or 1 arguments (label?)", pos.Line, pos.Col)
	}
	if len(args) == 1 {
		if _, err := e.emitConsolePrint(args, 1, ""); err != nil {
			return Value{}, err
		}
	}
	e.ensureConsoleGroupDepth()
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_console_group_depth, align 8", cur))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_console_group_depth, align 8", next))
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleGroupEnd implements console.groupEnd(): decreases the indent
// depth by one, floored at 0 (an extra, unbalanced groupEnd() call is
// harmless rather than underflowing into a negative depth).
func (e *Emitter) emitConsoleGroupEnd(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: console.groupEnd takes no arguments", pos.Line, pos.Col)
	}
	e.ensureConsoleGroupDepth()
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_console_group_depth, align 8", cur))
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sle i64 %s, 0", isZero, cur))
	dec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", dec, cur))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", next, isZero, dec))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_console_group_depth, align 8", next))
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleDir implements console.dir(obj, options?): prints obj exactly
// like a single-argument console.log — options (real Node's depth/color
// controls) is accepted syntactically but ignored, a documented V1 scope
// narrowing.
func (e *Emitter) emitConsoleDir(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: console.dir takes 1 or 2 arguments (obj, options?)", pos.Line, pos.Col)
	}
	return e.emitConsolePrint(args[:1], 1, "")
}

// consoleLabelArg resolves an optional single string-label argument (0 or 1
// args), defaulting to "default" when omitted — matching real Node's own
// default label for time/timeEnd/count/countReset.
func (e *Emitter) consoleLabelArg(args []ast.Expression, name string, pos ast.Pos) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("%d:%d: console.%s takes 0 or 1 arguments (label?)", pos.Line, pos.Col, name)
	}
	if len(args) == 0 {
		return e.internString("default"), nil
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return "", err
	}
	val = e.coerce(val, TypePtr)
	return val.Ref, nil
}

// emitConsoleTime implements console.time(label?): stores the current
// monotonic time. V1 scope: a single global slot, not a per-label map — see
// ensureConsoleTimer.
func (e *Emitter) emitConsoleTime(args []ast.Expression, pos ast.Pos) (Value, error) {
	if _, err := e.consoleLabelArg(args, "time", pos); err != nil {
		return Value{}, err
	}
	e.ensureConsoleTimer()
	nowReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_performance_now()", nowReg))
	e.emitInstr(fmt.Sprintf("store double %s, ptr @__kml_console_time_start, align 8", nowReg))
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleTimeEnd implements console.timeEnd(label?): prints "<label>:
// <elapsed>ms" using the elapsed time since the matching console.time()
// call (no validation that time() was actually called first — the single-
// slot V1 scope means there's no separate label to check existence of).
func (e *Emitter) emitConsoleTimeEnd(args []ast.Expression, pos ast.Pos) (Value, error) {
	labelPtr, err := e.consoleLabelArg(args, "timeEnd", pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureConsoleTimer()
	e.ensureSprintf()
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensurePrintf()

	nowReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @__kml_performance_now()", nowReg))
	startReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load double, ptr @__kml_console_time_start, align 8", startReg))
	elapsed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", elapsed, nowReg, startReg))

	labelLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", labelLen, labelPtr))
	bufSize := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 48", bufSize, labelLen))
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, bufSize))
	msgFmt := e.internString("%s: %gms")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, double %s)", buf, msgFmt, labelPtr, elapsed))

	e.emitConsoleGroupIndent(1)
	nlFmt := e.internString("%s\n")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", nlFmt, buf))
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleCountMapEnsure returns a register holding the lazily-created
// console.count() backing map, creating it on first use. Uses the
// alloca+store-in-each-branch+load-after-merge shape emitOptionalMember
// already established for "branch, then merge a value back" — simpler and
// safer than hand-tracking phi predecessor labels.
func (e *Emitter) emitConsoleCountMapEnsure() string {
	e.ensureConsoleCountMap()
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_console_count_map, align 8", cur))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cur, resPtr))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, cur))
	createL := e.freshLabel("consolecount.create")
	doneL := e.freshLabel("consolecount.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, createL, doneL))

	e.emitLabel(createL)
	newMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", newMap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_console_count_map, align 8", newMap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newMap, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return result
}

// emitConsoleCount implements console.count(label?): increments and prints
// a per-label counter (default label "default"), backed by a real
// Map<string, number> — matches real Node's multi-label semantics exactly,
// unlike console.time's single-slot V1 narrowing above.
func (e *Emitter) emitConsoleCount(args []ast.Expression, pos ast.Pos) (Value, error) {
	labelPtr, err := e.consoleLabelArg(args, "count", pos)
	if err != nil {
		return Value{}, err
	}
	mapReg := e.emitConsoleCountMapEnsure()
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", cur, mapReg, labelPtr))
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", mapReg, labelPtr, next))

	e.ensureSprintf()
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensurePrintf()
	labelLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", labelLen, labelPtr))
	bufSize := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 32", bufSize, labelLen))
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, bufSize))
	msgFmt := e.internString("%s: %lld")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, i64 %s)", buf, msgFmt, labelPtr, next))

	e.emitConsoleGroupIndent(1)
	nlFmt := e.internString("%s\n")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", nlFmt, buf))
	return Value{Ty: TypeVoid}, nil
}

// emitConsoleCountReset implements console.countReset(label?): resets the
// given label's counter back to 0 (does not remove the label).
func (e *Emitter) emitConsoleCountReset(args []ast.Expression, pos ast.Pos) (Value, error) {
	labelPtr, err := e.consoleLabelArg(args, "countReset", pos)
	if err != nil {
		return Value{}, err
	}
	mapReg := e.emitConsoleCountMapEnsure()
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 0)", mapReg, labelPtr))
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) emitConsolePrintVal(val Value, fmtPtr string, fd int) {
	call := func(extra ...string) {
		parts := append([]string{fmtPtr}, extra...)
		joined := strings.Join(parts, ", ")
		if fd == 2 {
			e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s)", joined))
		} else {
			e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s)", joined))
		}
	}
	switch val.Ty.IR {
	case "i8", "i16", "i32", "i64":
		pv := val
		if val.Ty.IR != "i64" {
			r := e.freshReg()
			ext := "sext"
			if !val.Ty.Signed {
				ext = "zext"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s to i64", r, ext, val.Ty.IR, val.Ref))
			pv = Value{Ref: r, Ty: TypeI64}
		}
		call("i64 " + pv.Ref)
	case "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i32", r, val.Ref))
		call("i32 " + r)
	case "float":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", r, val.Ref))
		call("double " + r)
	case "double":
		call("double " + val.Ref)
	case "ptr":
		call("ptr " + val.Ref)
	}
}

// emitConsoleAssert emits console.assert(condition, ...message).
// If condition is falsy, it prints "Assertion failed: <message>" to stderr and continues.
func (e *Emitter) emitConsoleAssert(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) == 0 {
		return Value{}, fmt.Errorf("%d:%d: console.assert requires at least one argument", pos.Line, pos.Col)
	}
	e.ensureDprintf()

	cond, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	cond = e.toBool(cond)

	failL  := e.freshLabel("assert.fail")
	passL  := e.freshLabel("assert.pass")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, passL, failL))

	e.emitLabel(failL)
	if len(args) > 1 {
		pfxPtr := e.internString("Assertion failed: ")
		fmtStr := e.internString("%s")
		e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, ptr %s)", fmtStr, pfxPtr))
		msgArgs := args[1:]
		if _, err := e.emitConsolePrint(msgArgs, 2, ""); err != nil {
			return Value{}, err
		}
	} else {
		msgPtr := e.internString("Assertion failed\n")
		fmtStr := e.internString("%s")
		e.emitInstr(fmt.Sprintf("call i32 (i32, ptr, ...) @dprintf(i32 2, ptr %s, ptr %s)", fmtStr, msgPtr))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", passL))

	e.emitLabel(passL)
	return Value{Ty: TypeVoid}, nil
}


// emitJSONStringifyArray builds a JSON array "[e1,e2,...]" from any element
// type by looping at runtime and delegating each element to
// emitJSONStringifyValue (which already correctly handles numbers, strings,
// booleans, and nested objects) — the same runtime accumulator-loop shape
// emitArrayJoin uses, just bracketed and JSON-encoding each element instead of
// plain-string-joining. Replaces the old num/string-only special-cased
// C helpers, which silently mishandled boolean and object element types.
func (e *Emitter) emitJSONStringifyArray(arrExpr ast.Expression, pos ast.Pos) (Value, error) {
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(arrExpr, pos)
	if err != nil {
		return Value{}, err
	}

	accAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("["), accAlloca))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("jsonarr.cond")
	bodyL := e.freshLabel("jsonarr.body")
	firstL := e.freshLabel("jsonarr.first")
	restL := e.freshLabel("jsonarr.rest")
	incL := e.freshLabel("jsonarr.inc")
	doneL := e.freshLabel("jsonarr.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.freshReg()
	inElem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", inElem, elemTy.IR, inGep, elemTy.Align()))
	elemJSONVal, err := e.emitJSONStringifyValue(Value{Ref: inElem, Ty: elemTy})
	if err != nil {
		return Value{}, err
	}
	isFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isFirst, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFirst, firstL, restL))

	e.emitLabel(firstL)
	accAtFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accAtFirst, accAlloca))
	firstAcc, err := e.emitStringConcat(Value{Ref: accAtFirst, Ty: TypePtr}, elemJSONVal)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", firstAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(restL)
	accCur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accCur, accAlloca))
	withComma, err := e.emitStringConcat(Value{Ref: accCur, Ty: TypePtr}, Value{Ref: e.internString(","), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	newAcc, err := e.emitStringConcat(withComma, elemJSONVal)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	preClose := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", preClose, accAlloca))
	return e.emitStringConcat(Value{Ref: preClose, Ty: TypePtr}, Value{Ref: e.internString("]"), Ty: TypePtr})
}

func (e *Emitter) emitJSONStringify(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: JSON.stringify expects at least 1 argument", pos.Line, pos.Col)
	}
	argTy := e.inferExprType(args[0])

	if argTy.IsArray && argTy.ElemType != nil {
		return e.emitJSONStringifyArray(args[0], pos)
	}

	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}

	return e.emitJSONStringifyValue(val)
}

// emitJSONStringifyObject builds {"k1":v1,"k2":v2,...} inline by walking the
// known fields of a statically-typed object. Handles nested objects recursively.
func (e *Emitter) emitJSONStringifyObject(val Value) (Value, error) {
	acc := Value{Ref: e.internString("{"), Ty: TypePtr}
	for i, field := range val.Ty.Fields {
		// Load the field value via GEP.
		gepReg := e.freshReg()
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d",
			gepReg, val.Ty.StructIR(), val.Ref, i))
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
			loadReg, field.Ty.IR, gepReg, field.Ty.Align()))
		fieldVal := Value{Ref: loadReg, Ty: field.Ty}

		// Key segment: `"name":` with a leading comma after the first field.
		keyStr := `"` + field.Name + `":`
		if i > 0 {
			keyStr = "," + keyStr
		}
		keyPart := Value{Ref: e.internString(keyStr), Ty: TypePtr}
		var err error
		acc, err = e.emitStringConcat(acc, keyPart)
		if err != nil {
			return Value{}, err
		}

		// JSON-encode the field value.
		jsonVal, err := e.emitJSONStringifyValue(fieldVal)
		if err != nil {
			return Value{}, err
		}
		acc, err = e.emitStringConcat(acc, jsonVal)
		if err != nil {
			return Value{}, err
		}
	}
	return e.emitStringConcat(acc, Value{Ref: e.internString("}"), Ty: TypePtr})
}

// emitJSONStringifyValue returns a ptr string with the JSON encoding of val.
// Handles strings (quoted), numbers, booleans, and nested objects recursively.
func (e *Emitter) emitJSONStringifyValue(val Value) (Value, error) {
	if val.Ty.IsObject {
		return e.emitJSONStringifyObject(val)
	}
	switch val.Ty.IR {
	case "i1":
		trueStr := e.internString("true")
		falseStr := e.internString("false")
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, val.Ref, trueStr, falseStr))
		return Value{Ref: r, Ty: TypePtr}, nil
	case "ptr":
		e.ensureJSONStringifyStr()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_str_str(ptr %s)", r, val.Ref))
		return Value{Ref: r, Ty: TypePtr}, nil
	default:
		if val.Ty.IsDate {
			// Real JS calls Date.prototype.toJSON() (== toISOString()) during
			// stringification instead of serializing the raw ms timestamp;
			// reuse the existing formatter and JSON-quote its result like any
			// other string.
			iso, err := e.emitDateToISOString(val)
			if err != nil {
				return Value{}, err
			}
			e.ensureJSONStringifyStr()
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_str_str(ptr %s)", r, iso.Ref))
			return Value{Ref: r, Ty: TypePtr}, nil
		}
		if val.Ty.Float {
			// Coercing a float to i64 below would truncate (9.5 -> 9) instead
			// of formatting it; emitValueToString already does correct %g
			// formatting for floats, so reuse it instead of a separate helper.
			return e.emitValueToString(val)
		}
		e.ensureJSONStringifyNum()
		coerced := e.coerce(val, TypeI64)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_str_num(i64 %s)", r, coerced.Ref))
		return Value{Ref: r, Ty: TypePtr}, nil
	}
}

func (e *Emitter) emitJSONParse(args []ast.Expression, targetTy Type, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: JSON.parse expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	return e.emitJSONParseValue(val, targetTy, pos)
}

// emitJSONParseValue is emitJSONParse's core, factored out so any already-
// evaluated string Value can be parsed (not just a literal call argument) —
// used directly by Response.json() (emit_fetch.go), which already has the
// buffered response body as a Value with nothing left to re-evaluate.
func (e *Emitter) emitJSONParseValue(val Value, targetTy Type, pos ast.Pos) (Value, error) {
	if val.Ty.IR != "ptr" {
		return Value{}, fmt.Errorf("%d:%d: JSON.parse expects a string argument", pos.Line, pos.Col)
	}
	if targetTy.IsObject {
		return e.emitJSONParseObject(val, targetTy, pos)
	}
	if targetTy.IR == TypeI64.IR && !targetTy.IsArray && !targetTy.IsObject {
		e.ensureAtoll()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @atoll(ptr %s)", r, val.Ref))
		return Value{Ref: r, Ty: TypeI64}, nil
	}
	e.ensureJSONParseStr()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_parse_str(ptr %s)", r, val.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitJSONParseObject parses a flat JSON object text into a heap-allocated
// struct matching targetTy's field layout, known fully at compile time from
// the type annotation. Per field: find "name": in the text (or use a
// zero-value default if the key is missing), parse the value according to
// the field's compile-time type, and GEP+store it — the same "malloc struct,
// then per-field GEP+store" shape emitObjectLiteral/emitJSONStringifyObject
// already use, just sourcing each value from the runtime JSON text instead of
// a literal expression. Nested object fields are not supported (would need
// brace-matched substring isolation to avoid a field-finder incorrectly
// matching a same-named key belonging to a later sibling object) — a clean
// error here instead of silently producing wrong reads for that shape.
func (e *Emitter) emitJSONParseObject(jsonVal Value, targetTy Type, pos ast.Pos) (Value, error) {
	for _, f := range targetTy.Fields {
		if f.Ty.IsObject {
			return Value{}, fmt.Errorf("%d:%d: JSON.parse into a nested object field ('%s') is not yet supported", pos.Line, pos.Col, f.Name)
		}
	}

	e.ensureMalloc()
	structIR := targetTy.StructIR()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, targetTy.StructSize()))

	e.ensureJSONFindValue()
	for i, f := range targetTy.Fields {
		pattern := e.internString(`"` + f.Name + `":`)
		valStart := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_find_value(ptr %s, ptr %s)", valStart, jsonVal.Ref, pattern))
		isMissing := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isMissing, valStart))

		foundL := e.freshLabel("jsonobj.found")
		missingL := e.freshLabel("jsonobj.missing")
		mergeL := e.freshLabel("jsonobj.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isMissing, missingL, foundL))

		e.emitLabel(foundL)
		parsedVal, err := e.emitJSONParseFieldValue(valStart, f.Ty)
		if err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(missingL)
		// A missing plain-string field must default to an empty string, not
		// zeroRef's general ptr default of `null` — every other string
		// operation in this compiler (concatenation, .length, console.log,
		// etc.) assumes a `string`-typed value is never null, unlike an
		// object/array/closure field, where null genuinely is the only
		// sensible zero value. Storing `null` here and later printing or
		// concatenating it is undefined behavior (passing NULL to printf's
		// "%s") — confirmed directly: `JSON.parse` into an object whose
		// string field's key is absent from the source text crashed
		// (SIGTRAP/SIGSEGV, depending on how aggressively the optimizer
		// exploited the resulting UB) before this fix.
		defaultRef := zeroRef(f.Ty)
		if f.Ty.IR == "ptr" && !f.Ty.IsObject && !f.Ty.IsArray && !f.Ty.IsFunc {
			defaultRef = e.internString("")
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

		e.emitLabel(mergeL)
		fieldReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = phi %s [ %s, %%%s ], [ %s, %%%s ]", fieldReg, f.Ty.IR, parsedVal.Ref, foundL, defaultRef, missingL))

		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, i))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", f.Ty.IR, fieldReg, gepReg, f.Ty.Align()))
	}

	return Value{Ref: dataReg, Ty: targetTy}, nil
}

// emitJSONParseFieldValue parses the JSON value text starting at valStart
// (already past whitespace) according to fieldTy: boolean via strncmp
// against "true", float via strtod, integer via atoll (matching the existing
// JSON.parse(s) -> number behavior), string via __kml_json_parse_field_str.
func (e *Emitter) emitJSONParseFieldValue(valStart string, fieldTy Type) (Value, error) {
	switch {
	case fieldTy.IR == "i1":
		e.ensureStrncmp()
		trueStr := e.internString("true")
		cmp := e.freshReg()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strncmp(ptr %s, ptr %s, i64 4)", cmp, valStart, trueStr))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", result, cmp))
		return Value{Ref: result, Ty: TypeBool}, nil
	case fieldTy.Float:
		e.ensureStrtod()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @strtod(ptr %s, ptr null)", result, valStart))
		return Value{Ref: result, Ty: fieldTy}, nil
	case fieldTy.IR == "ptr" && !fieldTy.IsObject && !fieldTy.IsArray && !fieldTy.IsFunc:
		e.ensureJSONParseFieldStr()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_parse_field_str(ptr %s)", result, valStart))
		return Value{Ref: result, Ty: TypePtr}, nil
	default:
		e.ensureAtoll()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @atoll(ptr %s)", result, valStart))
		return e.coerce(Value{Ref: result, Ty: TypeI64}, fieldTy), nil
	}
}

func (e *Emitter) emitMathCall(property string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch property {
	case "floor", "ceil", "round", "trunc":
		return e.emitMathRound(property, args, pos)
	case "abs":
		return e.emitMathAbs(args, pos)
	case "sqrt", "log", "log2", "log10", "sin", "cos", "tan",
		"asin", "acos", "atan", "sinh", "cosh", "tanh", "cbrt", "expm1", "log1p":
		return e.emitMathUnaryFloat(property, args, pos)
	case "pow":
		return e.emitMathBinaryFloat("pow", args, pos)
	case "hypot":
		return e.emitMathBinaryFloat("hypot", args, pos)
	case "atan2":
		return e.emitMathBinaryFloat("atan2", args, pos)
	case "min":
		return e.emitMathMinMax("min", args, pos)
	case "max":
		return e.emitMathMinMax("max", args, pos)
	case "sign":
		return e.emitMathSign(args, pos)
	case "random":
		return e.emitMathRandom(pos)
	case "clamp":
		return e.emitMathClamp(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: Math.%s is not supported", pos.Line, pos.Col, property)
}

func (e *Emitter) emitMathRound(fn string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.%s expects 1 argument", pos.Line, pos.Col, fn)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if val.Ty.IR == "i64" || (val.Ty.IsInteger() && !val.Ty.Float) {
		return e.coerce(val, TypeI64), nil
	}
	fval := e.coerce(val, TypeF64)
	e.ensureMathFuncs()
	rounded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @%s(double %s)", rounded, fn, fval.Ref))
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", result, rounded))
	return Value{Ref: result, Ty: TypeI64}, nil
}

func (e *Emitter) emitMathAbs(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.abs expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if val.Ty.Float {
		e.ensureMathFuncs()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @fabs(double %s)", r, val.Ref))
		return Value{Ref: r, Ty: TypeF64}, nil
	}
	iVal := e.coerce(val, TypeI64)
	neg := e.freshReg()
	cmp := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 0, %s", neg, iVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", cmp, iVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, cmp, iVal.Ref, neg))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitMathUnaryFloat(fn string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.%s expects 1 argument", pos.Line, pos.Col, fn)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	fval := e.coerce(val, TypeF64)
	e.ensureMathFuncs()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @%s(double %s)", r, fn, fval.Ref))
	return Value{Ref: r, Ty: TypeF64}, nil
}

func (e *Emitter) emitMathBinaryFloat(fn string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Math.%s expects 2 arguments", pos.Line, pos.Col, fn)
	}
	v1, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	v2, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	f1 := e.coerce(v1, TypeF64)
	f2 := e.coerce(v2, TypeF64)
	e.ensureMathFuncs()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @%s(double %s, double %s)", r, fn, f1.Ref, f2.Ref))
	return Value{Ref: r, Ty: TypeF64}, nil
}

func (e *Emitter) emitMathMinMax(fn string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 {
		return Value{}, fmt.Errorf("%d:%d: Math.%s expects at least 2 arguments", pos.Line, pos.Col, fn)
	}
	result, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	for _, arg := range args[1:] {
		next, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		next = e.coerce(next, result.Ty)
		cmp := e.freshReg()
		r := e.freshReg()
		if result.Ty.Float {
			op := "fcmp olt"
			if fn == "max" {
				op = "fcmp ogt"
			}
			e.emitInstr(fmt.Sprintf("%s = %s double %s, %s", cmp, op, result.Ref, next.Ref))
		} else {
			op := "icmp slt"
			if fn == "max" {
				op = "icmp sgt"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s, %s", cmp, op, result.Ty.IR, result.Ref, next.Ref))
		}
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, %s %s, %s %s", r, cmp, result.Ty.IR, result.Ref, result.Ty.IR, next.Ref))
		result = Value{Ref: r, Ty: result.Ty}
	}
	return result, nil
}

func (e *Emitter) emitMathSign(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Math.sign expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	iVal := e.coerce(val, TypeI64)
	isPos := e.freshReg()
	isNeg := e.freshReg()
	fromPos := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", isPos, iVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, iVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 1, i64 0", fromPos, isPos))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 -1, i64 %s", r, isNeg, fromPos))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitMathRandom(_ ast.Pos) (Value, error) {
	switch runtime.GOOS {
	case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
		// arc4random() — cryptographic quality, no seeding required (BSD/macOS).
		e.ensureArc4Random()
		raw := e.freshReg()
		asFloat := e.freshReg()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @arc4random()", raw))
		e.emitInstr(fmt.Sprintf("%s = uitofp i32 %s to double", asFloat, raw))
		e.emitInstr(fmt.Sprintf("%s = fdiv double %s, 4294967295.0", result, asFloat))
		return Value{Ref: result, Ty: TypeF64}, nil
	default:
		// Portable fallback: a helper defined entirely in IR using C89 rand()/srand()/time().
		e.ensureRandRandom()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__klain_math_random()", result))
		return Value{Ref: result, Ty: TypeF64}, nil
	}
}

// Math.clamp(x, lo, hi) — not in the JS spec but very handy.
func (e *Emitter) emitMathClamp(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: Math.clamp expects 3 arguments (value, min, max)", pos.Line, pos.Col)
	}
	vVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	loVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	hiVal, err := e.emitExpr(args[2])
	if err != nil {
		return Value{}, err
	}
	loVal = e.coerce(loVal, vVal.Ty)
	hiVal = e.coerce(hiVal, vVal.Ty)

	cmpLo := e.freshReg()
	clampedLo := e.freshReg()
	cmpHi := e.freshReg()
	r := e.freshReg()
	if vVal.Ty.Float {
		e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, %s", cmpLo, vVal.Ref, loVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double %s", clampedLo, cmpLo, vVal.Ref, loVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, %s", cmpHi, clampedLo, hiVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double %s", r, cmpHi, clampedLo, hiVal.Ref))
	} else {
		iV := e.coerce(vVal, TypeI64)
		iLo := e.coerce(loVal, TypeI64)
		iHi := e.coerce(hiVal, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", cmpLo, iV.Ref, iLo.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", clampedLo, cmpLo, iV.Ref, iLo.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", cmpHi, clampedLo, iHi.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, cmpHi, clampedLo, iHi.Ref))
	}
	return Value{Ref: r, Ty: vVal.Ty}, nil
}


func (e *Emitter) emitNumberStaticCall(property string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch property {
	case "isInteger":
		return e.emitNumberIsInteger(args, pos)
	case "isFinite":
		return e.emitNumberIsFinite(args, pos)
	case "isNaN":
		return e.emitNumberIsNaN(args, pos)
	case "isSafeInteger":
		return e.emitNumberIsSafeInteger(args, pos)
	case "parseInt":
		return e.emitParseInt(args, pos)
	case "parseFloat":
		return e.emitParseFloat(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: Number.%s is not supported", pos.Line, pos.Col, property)
}

func (e *Emitter) emitNumberIsInteger(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Number.isInteger expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.Float {
		return Value{Ref: "1", Ty: TypeBool}, nil
	}
	e.ensureMathFuncs()
	floored := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @floor(double %s)", floored, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", r, val.Ref, floored))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitNumberIsNaN(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: isNaN expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.Float {
		return Value{Ref: "0", Ty: TypeBool}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp uno double %s, %s", r, val.Ref, val.Ref))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitNumberIsFinite(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: isFinite expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.Float {
		return Value{Ref: "1", Ty: TypeBool}, nil
	}
	// x - x == 0.0 is true only for finite values (Inf → NaN, NaN → NaN)
	diff := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", diff, val.Ref, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, 0.0", r, diff))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitNumberIsSafeInteger(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Number.isSafeInteger expects 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	const maxSafe = "9007199254740991"
	if !val.Ty.Float {
		neg := e.freshReg()
		cmpNeg := e.freshReg()
		absVal := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 0, %s", neg, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", cmpNeg, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", absVal, cmpNeg, val.Ref, neg))
		e.emitInstr(fmt.Sprintf("%s = icmp sle i64 %s, %s", r, absVal, maxSafe))
		return Value{Ref: r, Ty: TypeBool}, nil
	}
	e.ensureMathFuncs()
	floored := e.freshReg()
	isInt := e.freshReg()
	absVal := e.freshReg()
	inRange := e.freshReg()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @floor(double %s)", floored, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", isInt, val.Ref, floored))
	e.emitInstr(fmt.Sprintf("%s = call double @fabs(double %s)", absVal, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp ole double %s, 9.007199254740991e+15", inRange, absVal))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", r, isInt, inRange))
	return Value{Ref: r, Ty: TypeBool}, nil
}

// emitStringToStringBuiltin implements any global builtin with the shape
// `name(s: string): string` (btoa/atob/encodeURIComponent/etc.) — evaluates
// and coerces the single string argument, ensures the given runtime helper
// is declared, and calls it.
func (e *Emitter) emitStringToStringBuiltin(args []ast.Expression, pos ast.Pos, name, runtimeFn string, ensure func()) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: %s takes exactly 1 argument", pos.Line, pos.Col, name)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	val = e.coerce(val, TypePtr)
	ensure()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr %s(ptr %s)", r, runtimeFn, val.Ref))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitCryptoGetRandomValues implements crypto.getRandomValues(arr), filling
// an existing number[] array's elements with random byte values (0-255
// each) — a deliberate deviation from the real TypedArray-based API, since
// this compiler has no ArrayBuffer/TypedArrays yet (see
// ensureCryptoFillNumberArray's doc in runtime.go). Requires a named array
// variable (not an arbitrary expression), matching the same restriction
// emitPush already has for array mutation (emit_arrays.go) — there's no
// heap location to write into otherwise.
func (e *Emitter) emitCryptoGetRandomValues(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues takes exactly 1 argument", pos.Line, pos.Col)
	}
	id, ok := args[0].(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues requires a named number[] array variable", pos.Line, pos.Col)
	}
	sym, ok := e.lookup(id.Name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", pos.Line, pos.Col, id.Name)
	}
	if !sym.Ty.IsArray || sym.Ty.ElemType == nil || sym.Ty.ElemType.IR != "i64" {
		return Value{}, fmt.Errorf("%d:%d: crypto.getRandomValues requires a number[] array (this compiler has no TypedArrays yet)", pos.Line, pos.Col)
	}

	e.ensureCryptoFillNumberArray()
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, sym.Ptr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, sym.LenPtr))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_fill_number_array(ptr %s, i64 %s)", ptrReg, lenReg))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", r0, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: sym.Ty}, nil
}

// emitCryptoRandomUUID implements crypto.randomUUID().
func (e *Emitter) emitCryptoRandomUUID(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: crypto.randomUUID takes no arguments", pos.Line, pos.Col)
	}
	e.ensureCryptoRandomUUID()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_crypto_random_uuid()", r))
	return Value{Ref: r, Ty: TypePtr}, nil
}

func (e *Emitter) emitParseInt(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: parseInt expects 1 or 2 arguments", pos.Line, pos.Col)
	}
	e.ensureStrtoll()
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	radixRef := "10"
	if len(args) == 2 {
		rv, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		r32 := e.coerce(rv, TypeI32)
		radixRef = r32.Ref
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strtoll(ptr %s, ptr null, i32 %s)", r, strVal.Ref, radixRef))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitParseFloat(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: parseFloat expects 1 argument", pos.Line, pos.Col)
	}
	e.ensureStrtod()
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @strtod(ptr %s, ptr null)", r, strVal.Ref))
	return Value{Ref: r, Ty: TypeF64}, nil
}

