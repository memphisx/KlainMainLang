package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// Call dispatch (emitCall router): routes every call expression to the
// relevant built-in implementation, most of which live in their own
// emit_<domain>.go files (emit_strings.go, emit_date.go, emit_fetch.go,
// emit_classes.go, emit_objects.go, emit_promise.go, emit_process.go,
// emit_fs.go, emit_memory.go, emit_http.go, emit_timers.go, emit_func.go,
// emit_collections.go, emit_arrays_*.go, emit_call_console.go,
// emit_call_json.go, emit_call_math.go, emit_call_number.go,
// emit_call_encoding.go) — this file is only the dispatcher itself plus the
// named (top-level) function / closure call-site machinery that has nowhere
// else to live.

// desugarTaggedTemplate builds the plain call “ tag`a${x}b` “ is
// equivalent to — `tag(["a","b"], x)` — as a synthetic *ast.CallExpression:
// a real array literal of the cooked quasis as the first argument, then
// every interpolated expression untouched (no implicit stringification —
// unlike a plain, un-tagged template literal's own interpolation) as the
// remaining arguments. See TDD-00059: this is the only new logic tagged
// templates need — every existing call-dispatch/coercion/rest-param-
// packing path handles the result exactly like a hand-written call.
func desugarTaggedTemplate(tt *ast.TaggedTemplateExpression) *ast.CallExpression {
	quasiExprs := make([]ast.Expression, len(tt.Quasis))
	for i, q := range tt.Quasis {
		quasiExprs[i] = ast.NewStringLiteral(q, tt.GetPos())
	}
	args := append([]ast.Expression{ast.NewArrayLiteral(quasiExprs, tt.GetPos())}, tt.Exprs...)
	return ast.NewCallExpression(tt.Tag, args, tt.GetPos())
}

func (e *Emitter) emitCall(ex *ast.CallExpression) (Value, error) {
	// super(args) / super.method(args) (TDD-00009 Stage 3) — checked first,
	// since a SuperExpression callee/receiver never reaches the generic
	// mem.Object-based dispatch chain below (inferExprType has no case for
	// it, and it shouldn't: super is only ever meaningful directly in call
	// position).
	if _, ok := ex.Callee.(*ast.SuperExpression); ok {
		return e.emitSuperCall(ex)
	}
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if _, ok := mem.Object.(*ast.SuperExpression); ok {
			return e.emitSuperMethodCall(mem.Property, ex.Args, ex.GetPos())
		}
	}
	// Static method call: ClassName.staticMethod(args) (TDD-00009 Stage
	// 4). Checked before every mem.Property-name-based/inferExprType-based
	// dispatch below, for the same reason super's own checks above are: a
	// bare class-name identifier is a compile-time namespace, never a real
	// runtime value bindable via e.lookup/inferExprType.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if id, ok := mem.Object.(*ast.Identifier); ok {
			if info, found := e.classes[id.Name]; found {
				return e.emitStaticMethodCall(info, id.Name, mem.Property, ex.Args, ex.GetPos())
			}
		}
	}
	// Special-case: console.log(...) and array.push(...)
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "String" && !e.isShadowedByLocal(id.Name) {
			return e.emitStringStaticCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Number" && !e.isShadowedByLocal(id.Name) {
			return e.emitNumberStaticCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Math" && !e.isShadowedByLocal(id.Name) {
			return e.emitMathCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "JSON" && !e.isShadowedByLocal(id.Name) {
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
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "performance" && !e.isShadowedByLocal(id.Name) && mem.Property == "now" {
			e.ensurePerformanceNow()
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call double @__kml_performance_now()", r))
			return Value{Ref: r, Ty: TypeF64}, nil
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "performance" && !e.isShadowedByLocal(id.Name) && mem.Property == "mark" {
			return e.emitPerformanceMark(ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "performance" && !e.isShadowedByLocal(id.Name) && mem.Property == "measure" {
			return e.emitPerformanceMeasure(ex.Args, ex.GetPos())
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
		if mem.Property == "toString" && e.inferExprType(mem.Object).IsSymbol {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitSymbolToString(objVal)
		}
		if isResponseMethodName(mem.Property) && e.inferExprType(mem.Object).IsResponse {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitResponseCall(objVal, mem.Property, ex.GetPos())
		}
		if mem.Property == "encode" && e.inferExprType(mem.Object).IsTextEncoder {
			return e.emitTextEncoderEncode(mem.Object, ex.Args, ex.GetPos())
		}
		if mem.Property == "decode" && e.inferExprType(mem.Object).IsTextDecoder {
			return e.emitTextDecoderDecode(mem.Object, ex.Args, ex.GetPos())
		}
		if mem.Property == "test" && e.inferExprType(mem.Object).IsRegExp {
			return e.emitRegexTest(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "exec" && e.inferExprType(mem.Object).IsRegExp {
			return e.emitRegexExec(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "close" && e.inferExprType(mem.Object).IsEventSource {
			return e.emitEventSourceClose(mem.Object, ex.GetPos())
		}
		if mem.Property == "addEventListener" && e.inferExprType(mem.Object).IsEventSource {
			return e.emitEventSourceAddListener(mem.Object, ex.Args, ex.GetPos())
		}
		if mem.Property == "removeEventListener" && e.inferExprType(mem.Object).IsEventSource {
			return e.emitEventSourceRemoveListener(mem.Object, ex.Args, ex.GetPos())
		}
		if mem.Property == "send" && e.inferExprType(mem.Object).IsWSConnection {
			return e.emitWSConnectionSend(mem.Object, ex.Args, ex.GetPos())
		}
		if mem.Property == "close" && e.inferExprType(mem.Object).IsWSConnection {
			return e.emitWSConnectionCloseMethod(mem.Object, ex.GetPos())
		}
		if mem.Property == "send" && e.inferExprType(mem.Object).IsWebSocketClient {
			return e.emitWSClientSend(mem.Object, ex.Args, ex.GetPos())
		}
		if mem.Property == "close" && e.inferExprType(mem.Object).IsWebSocketClient {
			return e.emitWSClientClose(mem.Object, ex.GetPos())
		}
		if mem.Property == "bodyBytes" && e.inferExprType(mem.Object).IsRequest {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitRequestBodyBytes(objVal, ex.GetPos())
		}
		// User-defined class method call: instance.method(args). Checked
		// before the long unguarded mem.Property == "<name>" chain below
		// (push/slice/map/join/...), several of which match purely on
		// property name with no receiver-type guard at all — a class method
		// sharing a name with one of those built-ins must not be shadowed.
		// Only fires when the class actually declares a method by that name,
		// so a field that happens to hold a closure (cb: () => void) still
		// falls through to the generic IsFunc field-call dispatch below.
		// Also — crucially — checked before the generic hasOwnProperty/
		// toString checks right below, since a class instance is IsObject
		// too: a class that declares its own toString()/hasOwnProperty()
		// must win over the generic built-in behavior, exactly like real JS
		// prototype-chain method resolution would.
		if objTy := e.inferExprType(mem.Object); objTy.IsClass {
			if info, ok := e.classes[objTy.ClassName]; ok {
				if _, ok := info.MethodSigs[mem.Property]; ok {
					return e.emitClassMethodCall(objTy, mem.Object, mem.Property, ex.Args, ex.GetPos())
				}
				// EventEmitter-embedded dispatch (TDD-00023): a class
				// extending EventEmitter<T> reaches its on/once/emit/off/...
				// surface through this hand-written dispatch, never a real
				// vtable slot (registerClasses already rejects any user
				// method sharing one of these names, so the MethodSigs check
				// above never shadows this).
				if info.HasEventEmitter && isEventEmitterMethodName(mem.Property) {
					thisVal, err := e.emitExpr(mem.Object)
					if err != nil {
						return Value{}, err
					}
					eeGep := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", eeGep, info.Ty.StructIR(), thisVal.Ref, classEventEmitterFieldIndex(info.Ty)))
					listenersPtr := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", listenersPtr, eeGep))
					return e.emitEventEmitterCall(info.EventEmitterPayload, listenersPtr, mem.Property, ex.Args, ex.GetPos(), thisVal)
				}
			}
		}
		if mem.Property == "hasOwnProperty" && e.inferExprType(mem.Object).IsObject {
			if len(ex.Args) != 1 {
				return Value{}, fmt.Errorf("%d:%d: hasOwnProperty takes 1 argument", ex.GetPos().Line, ex.GetPos().Col)
			}
			return e.emitHasOwnProperty(mem.Object, ex.Args[0], "hasOwnProperty", ex.GetPos())
		}
		if mem.Property == "toString" && isNumberTy(e.inferExprType(mem.Object)) {
			return e.emitNumberToStringRadix(mem, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Array" && !e.isShadowedByLocal(id.Name) {
			switch mem.Property {
			case "isArray":
				if len(ex.Args) != 1 {
					return Value{}, fmt.Errorf("%d:%d: Array.isArray takes exactly 1 argument", ex.GetPos().Line, ex.GetPos().Col)
				}
				isArr := e.inferExprType(ex.Args[0]).IsArray
				if isArr {
					return Value{Ref: "true", Ty: TypeBool}, nil
				}
				return Value{Ref: "false", Ty: TypeBool}, nil
			case "of":
				return e.emitArrayOf(ex.Args, ex.GetPos())
			case "from":
				return e.emitArrayFrom(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Promise" {
			switch mem.Property {
			case "all":
				return e.emitPromiseAll(ex.Args, ex.GetPos())
			case "race":
				return e.emitPromiseRace(ex.Args, ex.GetPos())
			case "allSettled":
				return e.emitPromiseAllSettled(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Object" && !e.isShadowedByLocal(id.Name) {
			switch mem.Property {
			case "groupBy":
				return e.emitObjectGroupBy(ex.Args, ex.GetPos())
			case "keys":
				return e.emitObjectKeys(ex.Args, ex.GetPos())
			case "values":
				return e.emitObjectValues(ex.Args, ex.GetPos())
			case "entries":
				return e.emitObjectEntries(ex.Args, ex.GetPos())
			case "assign":
				return e.emitObjectAssign(ex.Args, ex.GetPos())
			case "freeze":
				return e.emitObjectFreeze(ex.Args, ex.GetPos())
			case "seal":
				return e.emitObjectSeal(ex.Args, ex.GetPos())
			case "hasOwn":
				if len(ex.Args) != 2 {
					return Value{}, fmt.Errorf("%d:%d: Object.hasOwn takes 2 arguments", ex.GetPos().Line, ex.GetPos().Col)
				}
				return e.emitHasOwnProperty(ex.Args[0], ex.Args[1], "Object.hasOwn", ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
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
			case "on":
				return e.emitProcessOn(ex.Args, ex.GetPos())
			}
		}
		// process.stdout.write(s) / process.stderr.write(s): a nested
		// two-level member chain (process.stdout is a pseudo-namespace, not
		// a real bindable value), so this needs its own shape check rather
		// than fitting the single-level `id.Name == "process" && !e.isShadowedByLocal(id.Name)` switch above.
		if inner, ok := mem.Object.(*ast.MemberExpression); ok && mem.Property == "write" {
			if id, ok := inner.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
				switch inner.Property {
				case "stdout":
					return e.emitProcessStreamWrite(ex.Args, "stdout", 1, ex.GetPos())
				case "stderr":
					return e.emitProcessStreamWrite(ex.Args, "stderr", 2, ex.GetPos())
				}
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "fs__kml_builtin" {
			switch mem.Property {
			case "readFileSync":
				return e.emitFsReadFileSync(ex.Args, ex.GetPos())
			case "readFileSyncBytes":
				return e.emitFsReadFileSyncBytes(ex.Args, ex.GetPos())
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
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "path__kml_builtin" {
			switch mem.Property {
			case "join":
				return e.emitPathJoin(ex.Args, ex.GetPos())
			case "resolve":
				return e.emitPathResolve(ex.Args, ex.GetPos())
			case "dirname":
				return e.emitPathDirname(ex.Args, ex.GetPos())
			case "basename":
				return e.emitPathBasename(ex.Args, ex.GetPos())
			case "extname":
				return e.emitPathExtname(ex.Args, ex.GetPos())
			case "isAbsolute":
				return e.emitPathIsAbsolute(ex.Args, ex.GetPos())
			case "parse":
				return e.emitPathParse(ex.Args, ex.GetPos())
			case "format":
				return e.emitPathFormat(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "os__kml_builtin" {
			switch mem.Property {
			case "platform":
				return Value{Ref: e.internString(nodePlatformName()), Ty: TypePtr}, nil
			case "homedir":
				return e.emitOSHomedir(ex.Args, ex.GetPos())
			case "tmpdir":
				return e.emitOSTmpdir(ex.Args, ex.GetPos())
			case "hostname":
				return e.emitOSHostname(ex.Args, ex.GetPos())
			case "totalmem":
				return e.emitOSTotalmem(ex.Args, ex.GetPos())
			case "freemem":
				return e.emitOSFreemem(ex.Args, ex.GetPos())
			case "cpus":
				return e.emitOSCpus(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "querystring__kml_builtin" {
			switch mem.Property {
			case "parse":
				return e.emitQuerystringParse(ex.Args, ex.GetPos())
			case "stringify":
				return e.emitQuerystringStringify(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "assert__kml_builtin" {
			return e.emitAssertModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "crypto" && !e.isShadowedByLocal(id.Name) {
			switch mem.Property {
			case "getRandomValues":
				return e.emitCryptoGetRandomValues(ex.Args, ex.GetPos())
			case "randomUUID":
				return e.emitCryptoRandomUUID(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Memory__kml_builtin" && mem.Property == "free" {
			return e.emitMemoryFree(ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "http__kml_builtin" {
			switch mem.Property {
			case "listen":
				return e.emitHTTPListen(ex.Args, ex.GetPos())
			case "close":
				return e.emitHTTPClose(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "console" && !e.isShadowedByLocal(id.Name) {
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
		if mem.Property == "findLast" {
			return e.emitArrayFindLast(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "findLastIndex" {
			return e.emitArrayFindLastIndex(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "reverse" {
			return e.emitArrayReverse(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "toReversed" {
			return e.emitArrayToReversed(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "toSorted" {
			return e.emitArrayToSorted(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "toSpliced" {
			return e.emitArrayToSpliced(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "with" {
			return e.emitArrayWith(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "copyWithin" {
			return e.emitArrayCopyWithin(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "fill" {
			return e.emitArrayFill(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "toFixed" {
			return e.emitNumberToFixed(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "toPrecision" {
			return e.emitNumberToPrecision(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "toExponential" {
			return e.emitNumberToExponential(mem, ex.Args, ex.GetPos())
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
		if mem.Property == "match" {
			return e.emitStringMatch(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "matchAll" {
			return e.emitStringMatchAll(mem, ex.Args, ex.GetPos())
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
		if mem.Property == "flat" {
			return e.emitArrayFlat(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "flatMap" {
			return e.emitArrayFlatMap(mem, ex.Args, ex.GetPos())
		}
		// TypedArray-only methods. TypedArray IS a plain array (IsArray/
		// ElemType — see IsTypedArray's doc comment), so indexing/.length/
		// .fill/.slice/.reverse/.at/.indexOf/.includes/.map/.filter/
		// .reduce/.forEach/.some/.every/for-of/.keys()/.values()/.entries()
		// all already dispatch correctly via the unguarded array-property
		// checks above and the generic array-HOF checks below with zero
		// changes; only these two names (no `number[]` equivalent to
		// collide with) need TypedArray-specific behavior.
		if objTy := e.inferExprType(mem.Object); objTy.IsTypedArray {
			switch mem.Property {
			case "set":
				return e.emitTypedArraySet(mem, ex.Args, ex.GetPos())
			case "subarray":
				return e.emitTypedArraySubarray(mem, ex.Args, ex.GetPos())
			}
		}
		// URLSearchParams-only methods, checked before the generic Map
		// dispatch right below (URLSearchParams IS a Map<string,string> —
		// see IsURLSearchParams's doc comment — so get/set/has/delete/etc.
		// all fall through to that generic path unchanged; only these two
		// names need URLSearchParams-specific behavior).
		if objTy := e.inferExprType(mem.Object); objTy.IsURLSearchParams {
			switch mem.Property {
			case "toString":
				return e.emitURLSearchParamsToString(mem.Object, ex.GetPos())
			case "getAll":
				return e.emitURLSearchParamsGetAll(mem, ex.Args, ex.GetPos())
			}
		}
		// XMLHttpRequest-only methods (TDD-00040).
		if e.inferExprType(mem.Object).IsXHR {
			switch mem.Property {
			case "open":
				return e.emitXHROpen(mem.Object, ex.Args, ex.GetPos())
			case "setRequestHeader":
				return e.emitXHRSetRequestHeader(mem.Object, ex.Args, ex.GetPos())
			case "send":
				return e.emitXHRSend(mem.Object, ex.Args, ex.GetPos())
			case "abort":
				return e.emitXHRAbort(mem.Object, ex.Args, ex.GetPos())
			}
		}
		// Headers-only methods (TDD-00040), checked before the generic Map
		// dispatch right below — same "narrower flag first" ordering
		// IsURLSearchParams already establishes just above (Headers IS a
		// Map<string,string> too, so forEach/entries/keys/values fall
		// through to that generic path unchanged; only get/set/has/delete
		// (case-insensitive) and append (no Map equivalent) need
		// Headers-specific behavior).
		if objTy := e.inferExprType(mem.Object); objTy.IsHeaders {
			switch mem.Property {
			case "get", "set", "has", "delete", "append":
				return e.emitHeadersCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
			}
		}
		// Map<K,V> and Set<T> method dispatch. Checked before the generic
		// "forEach" name below, since both Array and Map/Set have a
		// forEach — the array codegen must not run for a Map/Set receiver.
		// Not limited to a plain named variable (`m.get(...)`) — a cheap
		// inferExprType pre-check (no side effects, same idiom "slice"/
		// "indexOf"/"at" already use to disambiguate array vs. string) also
		// catches a Map/Set-typed field access, array index, or call result
		// (e.g. `c.scores.get(...)` where `scores: Map<K,V>`), which
		// resolveMapOrSetForCall then evaluates for real.
		if objTy := e.inferExprType(mem.Object); objTy.IsMap || objTy.IsSet {
			ty, ptr, err := e.resolveMapOrSetForCall(mem.Object, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			if ty.IsMap {
				return e.emitMapCall(ty, ptr, mem.Property, ex.Args, ex.GetPos())
			}
			return e.emitSetCall(ty, ptr, mem.Property, ex.Args, ex.GetPos())
		}
		// Standalone EventEmitter<T> method dispatch (TDD-00023) — the
		// class-embedded case is handled separately, above, since it needs
		// a GEP off the receiver's hidden field rather than
		// resolveEventEmitterForCall's named-variable-vs-arbitrary-
		// expression handling.
		if objTy := e.inferExprType(mem.Object); objTy.IsEventEmitter {
			ty, ptr, err := e.resolveEventEmitterForCall(mem.Object, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			return e.emitEventEmitterCall(*ty.EventEmitterPayload, ptr, mem.Property, ex.Args, ex.GetPos(), Value{Ref: ptr, Ty: ty})
		}
		// gen.next(value) (TDD-00061/ADR-00172) — gated the same way every
		// other type-tag-dispatched method above is, before the unguarded
		// generic chain below (a `.next` name has no other meaning
		// elsewhere in this compiler today, but matching the established
		// pattern here rather than assuming that stays true).
		if mem.Property == "next" && e.inferExprType(mem.Object).IsGenerator {
			return e.emitGeneratorNext(mem.Object, e.inferExprType(mem.Object), ex.Args, ex.GetPos())
		}
		if mem.Property == "forEach" {
			return e.emitArrayForEach(mem, ex.Args, ex.GetPos())
		}
		// arr.keys()/.values()/.entries() — same names Map/Set already use
		// above (handled there for Map/Set receivers), so guard on IsArray
		// the same way "slice"/"indexOf"/"at" already disambiguate against
		// their string-method namesakes.
		if mem.Property == "keys" && e.inferExprType(mem.Object).IsArray {
			return e.emitArrayKeys(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "values" && e.inferExprType(mem.Object).IsArray {
			return e.emitArrayValues(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "entries" && e.inferExprType(mem.Object).IsArray {
			return e.emitArrayEntries(mem, ex.Args, ex.GetPos())
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
	if id, ok := ex.Callee.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) {
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
		case "setImmediate":
			return e.emitSetImmediate(ex.Args, ex.GetPos())
		case "clearTimeout":
			return e.emitClearTimer(ex.Args, "clearTimeout", ex.GetPos())
		case "clearInterval":
			return e.emitClearTimer(ex.Args, "clearInterval", ex.GetPos())
		case "clearImmediate":
			return e.emitClearTimer(ex.Args, "clearImmediate", ex.GetPos())
		case "structuredClone":
			return e.emitStructuredClone(ex.Args, ex.GetPos())
		case "Symbol":
			return e.emitSymbolConstructor(ex.Args, ex.GetPos())
		case "assert__kml_builtin":
			// Bare `assert(cond, msg?)` — real Node's assert module is
			// itself callable, equivalent to assert.ok.
			return e.emitAssertModuleCall("ok", ex.Args, ex.GetPos())
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

	// Immediately-invoked function expression: (function(x: number) { return x+1; })(5)
	if fe, ok := ex.Callee.(*ast.FunctionExpression); ok {
		closureVal, err := e.emitFunctionExpression(fe, nil)
		if err != nil {
			return Value{}, err
		}
		return e.emitClosureCallByPtr(closureVal.Ref, closureVal.Ty, ex.Args, ex.GetPos())
	}

	// Call via bare identifier: named function or closure variable.
	if id, ok := ex.Callee.(*ast.Identifier); ok {
		// Generator construction (TDD-00061/ADR-00172, top-level only in
		// V1) — checked before the ordinary named-function dispatch just
		// below, since a generator is never entered into e.funcs/
		// resolveFuncRef at all: calling one doesn't emit an ordinary
		// `call`, it builds a fiber-backed instance struct instead.
		if info, found := e.generators[id.Name]; found {
			return e.emitGeneratorConstruction(info, ex.Args, ex.GetPos())
		}
		// Named function — a nested one (TDD-00057) shadows an outer/
		// top-level function of the same name, same as real JS/TS scoping.
		if mangled, sig, found := e.resolveFuncRef(id.Name); found {
			return e.emitCallToFuncSig(mangled, sig, ex.Args, ex.GetPos())
		}
		// Generic (TDD-00010 V1) function: infer the type argument from
		// whichever call-site argument lines up with the generic's own
		// type-parameter-typed parameter, instantiate (or reuse a memoized
		// prior instantiation) on demand, then dispatch exactly like a
		// concrete named function.
		if decl, found := e.genericFuncs[id.Name]; found {
			return e.emitGenericFuncCall(decl, ex.Args, ex.GetPos())
		}
		// Closure variable — including a named function expression's own
		// self-reference binding (TDD-00060).
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsFunc {
			return e.emitClosureCall(sym, ex.Args, ex.GetPos())
		}
		return Value{}, fmt.Errorf("%d:%d: undefined function or closure '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name)
	}

	// TDD-00049: a friendlier diagnostic for the single most likely cause of
	// reaching this fallback — writing e.g. `fs.readFileSync(...)` without
	// the now-required `import fs from 'fs'`. Checked last, only once every
	// real dispatch path (including a legitimately-imported builtin, which
	// never reaches here at all) has already failed to match.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if id, ok := mem.Object.(*ast.Identifier); ok {
			if specifier, known := builtinModuleSpecifiers[id.Name]; known {
				return Value{}, fmt.Errorf("%d:%d: '%s' is not defined — did you forget \"import %s from '%s'\"?",
					ex.GetPos().Line, ex.GetPos().Col, id.Name, id.Name, specifier)
			}
		}
	}

	// General fallback: call the result of any other expression whose
	// static type is a function value — `f()()`, `(cond ? f : g)()`,
	// `obj.getHandler()()`, a parenthesized expression of any of the
	// above (parens have no wrapper node in this parser, so the callee
	// here is already whatever was inside them), and so on. The dispatch
	// mechanism itself (emitClosureCallByPtr) already handles any
	// function-typed value regardless of which expression shape produced
	// it — every branch above this one is a narrower, more specific
	// pattern checked first only because it can skip the general
	// inferExprType call, not because the general path can't handle it
	// too. Checked last so a more specific/helpful error (like the
	// import-forgot diagnostic just above) still wins when both could
	// apply.
	if e.inferExprType(ex.Callee).IsFunc {
		val, err := e.emitExpr(ex.Callee)
		if err != nil {
			return Value{}, err
		}
		return e.emitClosureCallByPtr(val.Ref, val.Ty, ex.Args, ex.GetPos())
	}

	return Value{}, fmt.Errorf("%d:%d: only simple function calls are supported", ex.GetPos().Line, ex.GetPos().Col)
}

// builtinModuleSpecifiers maps the conventional bare identifier name a
// program would write for a built-in module (fs.*, path.*, ...) to the
// virtual specifier it must now be imported from (TDD-00049) — used only to
// build a helpful "did you forget to import this?" diagnostic above, not
// for any real dispatch decision (that's resolver/virtual_modules.go's
// job, entirely before codegen ever runs).
var builtinModuleSpecifiers = map[string]string{
	"fs":          "fs",
	"path":        "path",
	"os":          "os",
	"querystring": "querystring",
	"assert":      "assert",
	"http":        "http",
	"cluster":     "cluster",
	"Memory":      "memory",
}

// emitCallToFuncSig emits a call to name (a concrete, already-registered
// LLVM function — either a plain top-level function or a TDD-00010 V1
// generic function's specific instantiation) against sig, evaluating args
// and applying the same per-parameter rules a named top-level call always
// has: array-parameter special handling, per-parameter coercion, an
// unannotated ("Inferred") parameter rejecting a non-numeric argument,
// default-expression fallback for a missing trailing argument, and rest-
// parameter packing into a temporary heap array.
func (e *Emitter) emitCallToFuncSig(name string, sig FuncSig, args []ast.Expression, pos ast.Pos) (Value, error) {
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
		if i < len(args) && !(sig.HasRest && i >= regularCount) {
			arg := args[i]
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
					// Hint-aware (TDD-00028): an array-literal argument
					// (or `new Array<T>(n)` with no explicit `<T>`) is
					// built/coerced against paramTy directly instead of
					// self-inferring its own element type — the exact bug
					// class TDD-00007 already fixed for object literals.
					// Found via a genuinely wrong result (not just a
					// compile error): `sum([1, 2])` against a
					// `float64[]` parameter silently built an i64 array
					// and reinterpreted its raw bit pattern as a double.
					val, err := e.emitExprWithObjectHint(arg, paramTy)
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
				val, err := e.emitExprWithObjectHint(arg, paramTy)
				if err != nil {
					return Value{}, err
				}
				if paramTy.Inferred && !isSafeNumericArg(val.Ty) {
					paramName := fmt.Sprintf("%d", i+1)
					if i < len(sig.ParamNames) {
						paramName = "'" + sig.ParamNames[i] + "'"
					}
					return Value{}, fmt.Errorf("%d:%d: parameter %s of '%s' has no type annotation (defaults to number) but was called with a non-numeric argument here — add an explicit type annotation", arg.GetPos().Line, arg.GetPos().Col, paramName, name)
				}
				if paramTy.IsDynamic {
					if paramTy.UnionMembers != nil && !unionAllowsAssignmentFrom(paramTy, val.Ty) {
						paramName := fmt.Sprintf("%d", i+1)
						if i < len(sig.ParamNames) {
							paramName = "'" + sig.ParamNames[i] + "'"
						}
						return Value{}, fmt.Errorf("%d:%d: argument's type is not a member of parameter %s's declared union type", arg.GetPos().Line, arg.GetPos().Col, paramName)
					}
					// TDD-00010 V2: a call to an `@erased` generic function —
					// coerce (unlike this) has no notion of boxing, it only
					// converts between concrete scalar IR types, so a bare-T
					// param must be boxed explicitly instead.
					if val, err = e.emitBoxValue(val); err != nil {
						return Value{}, err
					}
				} else if paramTy.IR != "" {
					val = e.coerce(val, paramTy)
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
			}
		} else if i < len(sig.Defaults) && sig.Defaults[i] != nil {
			// Evaluate default expression at call site. Array-typed
			// defaults need the same {ptr,i64} -> (ptr, i64) decomposition
			// the direct-arg path above uses — found in passing while
			// wiring optional params below: an array-typed default
			// (`a: number[] = [1,2,3]`) was passing the whole aggregate
			// struct where the callee's LLVM signature expects two scalar
			// params, a hard clang-stage type mismatch, not a silent bug.
			if paramTy.IsArray {
				val, err := e.emitExprWithObjectHint(sig.Defaults[i], paramTy)
				if err != nil {
					return Value{}, fmt.Errorf("default value for param %d: %w", i, err)
				}
				if !val.Ty.IsArray {
					return Value{}, fmt.Errorf("default value for param %d does not yield an array", i)
				}
				ptrReg := e.freshReg()
				lenReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
				argParts = append(argParts, "ptr "+ptrReg, "i64 "+lenReg)
			} else {
				val, err := e.emitExprWithObjectHint(sig.Defaults[i], paramTy)
				if err != nil {
					return Value{}, fmt.Errorf("default value for param %d: %w", i, err)
				}
				if paramTy.IsDynamic {
					if val, err = e.emitBoxValue(val); err != nil {
						return Value{}, err
					}
				} else if paramTy.IR != "" {
					val = e.coerce(val, paramTy)
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
			}
		} else if i < len(sig.Optional) && sig.Optional[i] {
			// ADR-00164: an omitted `param?: T` argument gets T's zero
			// value, the same undefined stand-in ADR-00157/ADR-00158 use.
			// Array-typed params decompose into two LLVM params (ptr, i64
			// len) at the callee side, so their "zero value" is an empty
			// array (null ptr, 0 len), not a single zeroLiteral() operand.
			if paramTy.IsArray {
				argParts = append(argParts, "ptr null", "i64 0")
			} else {
				argParts = append(argParts, fmt.Sprintf("%s %s", paramTy.IR, paramTy.zeroLiteral()))
			}
		} else {
			return Value{}, fmt.Errorf("%d:%d: missing argument %d with no default", pos.Line, pos.Col, i+1)
		}
	}
	// Pack rest args into a temporary heap array.
	if sig.HasRest {
		restStart := regularCount
		if restStart > len(args) {
			restStart = len(args)
		}
		restArgs := args[restStart:]
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
				val, err := e.emitExprWithObjectHint(arg, elemTy)
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
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", name, argsStr))
		return Value{Ty: TypeVoid}, nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", reg, sig.RetType.LLVMRetType(), name, argsStr))
	return Value{Ref: reg, Ty: sig.RetType}, nil
}
