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
	// `globalThis.setTimeout(...)` / `globalThis.JSON.stringify(...)` — peel the
	// `globalThis.` alias off the callee so it dispatches as the bare global.
	if unwrapped := e.unwrapGlobalThis(ex.Callee); unwrapped != ex.Callee {
		rewritten := ast.NewCallExpression(unwrapped, ex.Args, ex.GetPos())
		rewritten.TypeArgs = ex.TypeArgs
		return e.emitCall(rewritten)
	}
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if _, ok := mem.Object.(*ast.SuperExpression); ok {
			return e.emitSuperMethodCall(mem.Property, ex.Args, ex.GetPos())
		}
	}
	// Node's chained `http.createServer((req, res) => …).listen(port[, cb])`
	// (TDD-00131) — the callee is `<createServer call>.listen`. Routed through
	// the bound-handle machinery; listen() returns the server handle.
	if createArgs, listenArgs, ok := chainedCreateServerListen(ex); ok {
		return e.emitChainedCreateServerListen(createArgs, listenArgs, nil, ex.GetPos())
	}
	// A `res.writeHead/setHeader/write/end(...)` call on Node's http.createServer
	// `res` object (TDD-00131).
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if e.inferExprType(mem.Object).IsServerResponse {
			return e.emitServerResponseMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
	}
	// http.get/request response (TDD-00138): res.on('data'|'end'), setEncoding, …
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if e.inferExprType(mem.Object).IsIncomingMessage {
			return e.emitIncomingMessageCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
	}
	// node:sqlite (ADR-00540): db.exec/prepare/close and stmt.get/all/run.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if e.inferExprType(mem.Object).IsSQLiteDatabase {
			return e.emitSQLiteDatabaseMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsSQLiteStatement {
			return e.emitSQLiteStatementMethod(mem.Object, mem.Property, ex.TypeArgs, ex.Args, ex.GetPos())
		}
	}
	// Static method call: ClassName.staticMethod(args) (TDD-00009 Stage
	// 4). Checked before every mem.Property-name-based/inferExprType-based
	// dispatch below, for the same reason super's own checks above are: a
	// bare class-name identifier is a compile-time namespace, never a real
	// runtime value bindable via e.lookup/inferExprType.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if id, ok := mem.Object.(*ast.Identifier); ok {
			// TS namespace member call `X.member(args)` (TDD-00095): resolve
			// through the desugared flat function — checked before every
			// other member dispatch, since a namespace name (like a class
			// name) is a compile-time construct, never a runtime value. A
			// local binding shadowing the namespace name wins.
			if members, nsName := e.namespaceMembers(id.Name); members != nil {
				if exported, present := members[mem.Property]; present && !e.isShadowedByLocal(id.Name) {
					if !exported && e.curNamespace != nsName {
						return Value{}, fmt.Errorf("%d:%d: '%s.%s' is not exported from namespace '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name, mem.Property, nsName)
					}
					mangled := ast.NamespaceMangle(nsName, mem.Property)
					rewritten := ast.NewCallExpression(ast.NewIdentifier(mangled, ex.GetPos()), ex.Args, ex.GetPos())
					return e.emitCall(rewritten)
				}
			}
			if info, found := e.classes[id.Name]; found {
				return e.emitStaticMethodCall(info, id.Name, mem.Property, ex.Args, ex.GetPos())
			}
		}
		// Symbol.for / Symbol.keyFor (ADR-00488) — before class dispatch, a
		// user binding named Symbol still wins via the shadow check.
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Symbol" && !e.isShadowedByLocal("Symbol") &&
			(mem.Property == "for" || mem.Property == "keyFor") {
			return e.emitSymbolStatic(mem.Property, ex.Args, ex.GetPos())
		}
		// A namespace-qualified static call (`X.C.method()` — ADR-00480).
		if bare := e.stripNSTypeQualifier(mem.Object); bare != nil {
			if bid, ok := bare.(*ast.Identifier); ok {
				if info, found := e.classes[bid.Name]; found {
					return e.emitStaticMethodCall(info, bid.Name, mem.Property, ex.Args, ex.GetPos())
				}
			}
		}
		// Nested-namespace member call `A.B.f(args)` (TDD-00148 V3).
		if members, nsName := e.namespaceByChain(mem.Object); members != nil {
			if exported, present := members[mem.Property]; present {
				if !exported && e.curNamespace != nsName {
					return Value{}, fmt.Errorf("%d:%d: '%s.%s' is not exported from namespace '%s'", ex.GetPos().Line, ex.GetPos().Col, nsName, mem.Property, nsName)
				}
				mangled := ast.NamespaceMangle(nsName, mem.Property)
				rewritten := ast.NewCallExpression(ast.NewIdentifier(mangled, ex.GetPos()), ex.Args, ex.GetPos())
				return e.emitCall(rewritten)
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
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Buffer" && !e.isShadowedByLocal(id.Name) {
			return e.emitBufferStaticCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Atomics" && !e.isShadowedByLocal(id.Name) {
			return e.emitAtomicsCall(mem.Property, ex.Args, ex.GetPos())
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
		if mem.Property == "toString" && e.inferExprType(mem.Object).IsBigInt {
			return e.emitBigIntToStringMethod(mem.Object, ex.Args, ex.GetPos())
		}
		if objTy := e.inferExprType(mem.Object); objTy.IsReadableStream || objTy.IsStreamReader || objTy.IsRSController {
			return e.emitStreamMethodCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if objTy := e.inferExprType(mem.Object); objTy.IsWritableStream || objTy.IsStreamWriter || objTy.IsWSController {
			return e.emitWStreamMethodCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if objTy := e.inferExprType(mem.Object); objTy.IsNodeReadable || objTy.IsNodeWritable {
			return e.emitNodeStreamCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if (mem.Property == "then" || mem.Property == "catch" || mem.Property == "finally") && e.inferExprType(mem.Object).IsPromise {
			return e.emitPromiseThen(mem.Object, mem.Property, ex.Args, ex.GetPos())
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
		if mem.Property == "test" && e.inferExprType(mem.Object).IsURLPattern {
			return e.emitURLPatternTest(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "exec" && e.inferExprType(mem.Object).IsURLPattern {
			return e.emitURLPatternExec(mem, ex.Args, ex.GetPos())
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
		// Event/CustomEvent methods (TDD-00081): preventDefault / stop*.
		if e.inferExprType(mem.Object).IsEvent &&
			(mem.Property == "preventDefault" || mem.Property == "stopPropagation" || mem.Property == "stopImmediatePropagation") {
			return e.emitEventMethod(mem.Object, mem.Property, ex.GetPos())
		}
		// EventTarget bus methods (TDD-00081 Stage 2) — an AbortSignal is an
		// EventTarget too (Stage 3), reached via its hidden listeners map.
		if objTy := e.inferExprType(mem.Object); objTy.IsEventTarget || objTy.IsAbortSignal {
			switch mem.Property {
			case "addEventListener":
				return e.emitEventTargetAddListener(mem.Object, ex.Args, ex.GetPos())
			case "removeEventListener":
				return e.emitEventTargetRemoveListener(mem.Object, ex.Args, ex.GetPos())
			case "dispatchEvent":
				return e.emitEventTargetDispatch(mem.Object, ex.Args, ex.GetPos())
			}
		}
		// AbortController.abort(reason?) (TDD-00081 Stage 3).
		if mem.Property == "abort" && e.inferExprType(mem.Object).IsAbortController {
			return e.emitAbortControllerAbort(mem.Object, ex.Args, ex.GetPos())
		}
		// AbortSignal.timeout(ms) — static (TDD-00081 Stage 3c).
		if mem.Property == "timeout" {
			if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "AbortSignal" && !e.isShadowedByLocal(id.Name) {
				return e.emitAbortSignalTimeout(ex.Args, ex.GetPos())
			}
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
		// TDD-00098: parentPort.on/postMessage inside a worker module, and
		// the parent-side Worker method surface.
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "parentPort" && !e.isShadowedByLocal(id.Name) {
			return e.emitParentPortCall(mem.Property, ex.Args, ex.GetPos())
		}
		// Browser worker surface (TDD-00098 stage 6): self.postMessage(...)
		// inside a worker module is parentPort.postMessage.
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "self" && mem.Property == "postMessage" && e.currentWorkerMod != "" && !e.isShadowedByLocal(id.Name) {
			return e.emitParentPortCall("postMessage", ex.Args, ex.GetPos())
		}
		if (mem.Property == "postMessage" || mem.Property == "on" || mem.Property == "terminate") && e.inferExprType(mem.Object).IsWorker {
			return e.emitWorkerMethodCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if objTy := e.inferExprType(mem.Object); objTy.IsChildProcess || objTy.IsCPStream || objTy.IsCPStdin {
			return e.emitChildProcessMethodCall(mem.Object, objTy, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsClusterWorker {
			return e.emitClusterWorkerMethodCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsClientRequest {
			return e.emitClientRequestMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsHTTPAgent {
			return e.emitHTTPAgentMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsWebview {
			return e.emitWebviewMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsEmbeddedAssets {
			return e.emitEmbeddedAssetsMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "assets__kml_builtin" {
			return e.emitAssetsModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "tty__kml_builtin" {
			return e.emitTtyModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "tui__kml_builtin" {
			return e.emitTuiModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "sync__kml_builtin" {
			return e.emitSyncModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsChannel {
			return e.emitChannelMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsReadline {
			return e.emitReadlineMethodCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsStdin {
			return e.emitStdinMethodCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsNetServer {
			return e.emitNetServerMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsHTTPServer {
			return e.emitHTTPServerMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsH2ServerStream {
			return e.emitH2StreamMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsH2ClientSession {
			return e.emitH2ClientSessionMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsH2ClientStream {
			return e.emitH2ClientStreamMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsTestContext {
			return e.emitTestContextMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsDCChannel {
			return e.emitDiagChannelMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsNetSocket {
			return e.emitNetSocketMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsDgramSocket {
			return e.emitDgramMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		// TDD-00099: BroadcastChannel / MessagePort surfaces.
		if mem.Property == "postMessage" || mem.Property == "close" {
			if objTy := e.inferExprType(mem.Object); objTy.IsBroadcastChannel {
				return e.emitBroadcastChannelMethodCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
			} else if objTy.IsMessagePort {
				return e.emitMessagePortMethodCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
			}
		}
		if mem.Property == "close" && e.inferExprType(mem.Object).IsWebSocketClient {
			return e.emitWSClientClose(mem.Object, ex.GetPos())
		}
		if mem.Property == "stream" && e.inferExprType(mem.Object).IsRequest {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitRequestStream(objVal, ex.GetPos())
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
				// Node-stream class (TDD-00132): on/push/pipe/write/end/… are
				// hand-dispatched against the hidden stream handle, mirroring
				// the options-form `new Readable(...)` surface. Checked before
				// MethodSigs — these names are reserved (never real methods on
				// a stream class), while `_read`/`_write` fall through to the
				// ordinary MethodSigs dispatch below.
				if (info.HasNodeReadable || info.HasNodeWritable) && isNodeStreamMethodName(mem.Property) {
					return e.emitClassNodeStreamCall(objTy, info, mem.Object, mem.Property, ex.Args, ex.GetPos())
				}
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
		// str.toString() is the identity — Node code calls it habitually on
		// values that are Buffers there but strings here (spawnSync results,
		// stream chunks), so this keeps that idiom compiling.
		if mem.Property == "toString" && len(ex.Args) == 0 && isPlainStringType(e.inferExprType(mem.Object)) {
			return e.emitExpr(mem.Object)
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
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "ReadableStream" && mem.Property == "from" {
			if _, found := e.lookup(id.Name); !found {
				return e.emitReadableStreamFrom(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Readable" && (mem.Property == "from" || mem.Property == "fromWeb") {
			if _, found := e.lookup(id.Name); !found {
				return e.emitNodeReadableStatic(mem.Property, ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "streampromises__kml_builtin" {
			return e.emitStreamPromisesCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "stream__kml_builtin" {
			return e.emitStreamModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Promise" {
			switch mem.Property {
			case "all":
				return e.emitPromiseAll(ex.Args, ex.GetPos())
			case "race":
				return e.emitPromiseRace(ex.Args, ex.GetPos())
			case "allSettled":
				return e.emitPromiseAllSettled(ex.Args, ex.GetPos())
			case "any":
				return e.emitPromiseAny(ex.Args, ex.GetPos())
			case "resolve":
				return e.emitPromiseResolve(ex.Args, ex.GetPos())
			case "reject":
				return e.emitPromiseReject(ex.Args, ex.GetPos())
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
			case "fromEntries":
				return e.emitObjectFromEntries(ex.Args, ex.GetPos())
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
			case "nextTick":
				return e.emitProcessNextTick(ex.Args, ex.GetPos())
			case "uptime":
				return e.emitProcessUptime(ex.Args, ex.GetPos())
			case "hrtime":
				return e.emitProcessHrtime(ex.Args, ex.GetPos())
			case "memoryUsage":
				return e.emitProcessMemoryUsage(ex.Args, ex.GetPos())
			case "emitWarning":
				return e.emitProcessEmitWarning(ex.Args, ex.GetPos())
			case "send":
				return e.emitProcessSend(ex.Args, ex.GetPos())
			case "getuid", "geteuid", "getgid", "getegid":
				if len(ex.Args) != 0 {
					return Value{}, fmt.Errorf("%d:%d: process.%s takes no arguments", ex.GetPos().Line, ex.GetPos().Col, mem.Property)
				}
				return e.emitProcessGetID(mem.Property), nil
			case "disconnect":
				if len(ex.Args) != 0 {
					return Value{}, fmt.Errorf("%d:%d: process.disconnect takes no arguments", ex.GetPos().Line, ex.GetPos().Col)
				}
				e.ensureIPCChildRuntime()
				e.emitInstr("call void @__kml_ipcc_disconnect()")
				return Value{Ty: TypeVoid}, nil
			}
		}
		// process.hrtime.bigint(): a nested two-level member call.
		if inner, ok := mem.Object.(*ast.MemberExpression); ok && mem.Property == "bigint" {
			if id, ok := inner.Object.(*ast.Identifier); ok && id.Name == "process" && inner.Property == "hrtime" && !e.isShadowedByLocal(id.Name) {
				return e.emitProcessHrtimeBigint(ex.Args, ex.GetPos())
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
			case "statSync":
				return e.emitFsStatSync(ex.Args, ex.GetPos())
			case "lstatSync":
				return e.emitFsLstatSync(ex.Args, ex.GetPos())
			case "realpathSync", "mkdtempSync", "readlinkSync", "symlinkSync", "chmodSync", "truncateSync", "accessSync":
				return e.emitFsPathOp(mem.Property, ex.Args, ex.GetPos())
			case "rmSync":
				return e.emitFsRmSync(ex.Args, ex.GetPos())
			case "openSync":
				return e.emitFsOpenSync(ex.Args, ex.GetPos())
			case "closeSync":
				return e.emitFsCloseSync(ex.Args, ex.GetPos())
			case "writeSync":
				return e.emitFsWriteSync(ex.Args, ex.GetPos())
			case "readSync":
				return e.emitFsReadSync(ex.Args, ex.GetPos())
			case "fstatSync":
				return e.emitFsFstatSync(ex.Args, ex.GetPos())
			case "createReadStream":
				return e.emitFsCreateReadStream(ex.Args, ex.GetPos())
			case "createWriteStream":
				return e.emitFsCreateWriteStream(ex.Args, ex.GetPos())
			default:
				// Async callback form: fs.readFile(path, cb), fs.writeFile(...),
				// … (TDD-00107). The trailing callback receives (err[, data]).
				if _, ok := fsAsyncOps()[mem.Property]; ok {
					return e.emitFsAsyncCallback(mem.Property, ex.Args, ex.GetPos())
				}
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
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "zlib__kml_builtin" {
			return e.emitZlibModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "childprocess__kml_builtin" {
			return e.emitChildProcessModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "readline__kml_builtin" {
			return e.emitReadlineModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "net__kml_builtin" {
			return e.emitNetModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "tls__kml_builtin" {
			return e.emitTLSModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "util__kml_builtin" {
			return e.emitUtilModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		// fs.promises.<op>(...) — a two-level member chain (fs.promises is a
		// pseudo-namespace, like dns.promises below), the Promise form of the
		// async fs operations (TDD-00107).
		if inner, ok := mem.Object.(*ast.MemberExpression); ok {
			if id, ok := inner.Object.(*ast.Identifier); ok && id.Name == "fs__kml_builtin" && inner.Property == "promises" {
				if _, ok := fsAsyncOps()[mem.Property]; ok {
					return e.emitFsAsyncPromise(mem.Property, ex.Args, ex.GetPos())
				}
				return Value{}, fmt.Errorf("%d:%d: fs.promises.%s is not supported", mem.GetPos().Line, mem.GetPos().Col, mem.Property)
			}
		}
		// `import { readFile } from 'fs/promises'` — the same Promise form via
		// the fs/promises virtual module marker.
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "fspromises__kml_builtin" {
			if _, ok := fsAsyncOps()[mem.Property]; ok {
				return e.emitFsAsyncPromise(mem.Property, ex.Args, ex.GetPos())
			}
			return Value{}, fmt.Errorf("%d:%d: fs/promises.%s is not supported", mem.GetPos().Line, mem.GetPos().Col, mem.Property)
		}
		// dns.promises.lookup(...) — a two-level member chain (dns.promises is a
		// pseudo-namespace, not a bindable value), so it needs its own shape
		// check like process.stdout.write above.
		if inner, ok := mem.Object.(*ast.MemberExpression); ok && mem.Property == "lookup" {
			if id, ok := inner.Object.(*ast.Identifier); ok && id.Name == "dns__kml_builtin" && inner.Property == "promises" {
				return e.emitDnsPromisesLookup(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "dns__kml_builtin" {
			return e.emitDnsModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "dgram__kml_builtin" {
			return e.emitDgramModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "cluster__kml_builtin" {
			return e.emitClusterModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "assert__kml_builtin" {
			return e.emitAssertModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "test__kml_builtin" {
			return e.emitTestModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "nodecrypto__kml_builtin" {
			return e.emitNodeCryptoModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if e.isCryptoSubtle(mem.Object) {
			return e.emitCryptoSubtleCall(mem.Property, ex.Args, ex.GetPos())
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
			case "closeAllConnections":
				return e.emitHTTPCloseAllConnections(ex.Args, ex.GetPos())
			case "get", "request":
				return e.emitHTTPClientGet(ex.Args, ex.GetPos(), mem.Property == "request")
			case "createServer":
				// The chained createServer(cb).listen(...) expression is
				// intercepted earlier; reaching here means the variable-bound
				// handle form (TDD-00131 follow-on).
				return e.emitHTTPCreateServer(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "diagch__kml_builtin" {
			return e.emitDiagChModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "https__kml_builtin" {
			switch mem.Property {
			case "get", "request":
				// TLS is the libcurl client's native ground — same emitter as
				// http.get/request; the options-object form composes https URLs.
				return e.emitHTTPClientGetScheme(ex.Args, ex.GetPos(), "https", mem.Property == "request")
			case "createServer":
				// HTTPS/1.1 server (TDD-00111): a TLS-wrapped accept path serving
				// the same (req,res) core as http.createServer over the SSL shims.
				return e.emitHTTPSCreateServer(ex.Args, ex.GetPos())
			}
			return Value{}, fmt.Errorf("%d:%d: https has no method '%s' (supported: get, request)", ex.GetPos().Line, ex.GetPos().Col, mem.Property)
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "http2__kml_builtin" {
			switch mem.Property {
			case "createServer":
				// TDD-00139 Stage 1: shares the http server core — which
				// already serves h2c (prior-knowledge cleartext HTTP/2) on the
				// same port — so the handle, listen/close/address, and the
				// compat (req, res) API are all inherited.
				return e.emitHTTPCreateServer(ex.Args, ex.GetPos())
			case "createSecureServer":
				// TDD-00111 Stage 3b: h2-over-TLS. Serves h2 only (ALPN),
				// matching Node's allowHTTP1:false default; a non-h2 client is
				// closed after the handshake.
				return e.emitHTTP2CreateSecureServer(ex.Args, ex.GetPos())
			case "connect":
				return e.emitH2Connect(ex.Args, ex.GetPos())
			case "getDefaultSettings":
				return e.emitH2GetDefaultSettings(ex.Args, ex.GetPos())
			case "getPackedSettings":
				return e.emitH2GetPackedSettings(ex.Args, ex.GetPos())
			case "getUnpackedSettings":
				return e.emitH2GetUnpackedSettings(ex.Args, ex.GetPos())
			}
			return Value{}, fmt.Errorf("%d:%d: http2 has no method '%s'", ex.GetPos().Line, ex.GetPos().Col, mem.Property)
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "console" && !e.isShadowedByLocal(id.Name) {
			switch mem.Property {
			case "log", "info", "debug":
				return e.emitConsolePrint(ex.Args, 1, "")
			case "error":
				return e.emitConsolePrint(ex.Args, 2, "")
			case "warn":
				// Real console.warn prints the arguments to stderr with no
				// prefix of any kind — identical to console.error.
				return e.emitConsolePrint(ex.Args, 2, "")
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
		// TDD-00101: a BigInt64Array/BigUint64Array supports only an explicit
		// allow-list of array methods — the generic HOF/search/sort/mutator
		// machinery passes raw i64 scalars into callbacks and comparisons, so
		// any unlisted method is rejected here rather than silently surfacing
		// a raw scalar as if it were a bigint.
		if bigIntElemRejectedMethods[mem.Property] && e.inferExprType(mem.Object).BigIntElem {
			return Value{}, fmt.Errorf("%d:%d: .%s() is not supported on a BigInt64Array/BigUint64Array (supported: indexing, .length/.byteLength, .at/.set/.subarray/.slice/.fill/.reverse, for-of, Atomics.*)", ex.GetPos().Line, ex.GetPos().Col, mem.Property)
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
		// fs.statSync Stats methods (ADR-00495).
		if mem.Property == "isFile" || mem.Property == "isDirectory" || mem.Property == "isSymbolicLink" {
			if objTy := e.inferExprType(mem.Object); objTy.IsStats {
				return e.emitStatsKindCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
			}
		}
		// SharedArrayBuffer.grow / ArrayBuffer.resize (ADR-00494) — only on
		// buffers constructed with {maxByteLength}.
		if mem.Property == "grow" || mem.Property == "resize" {
			if objTy := e.inferExprType(mem.Object); objTy.IsArrayBuffer {
				objVal, err := e.emitExpr(mem.Object)
				if err != nil {
					return Value{}, err
				}
				return e.emitBufferGrow(objVal, ex.Args, ex.GetPos())
			}
		}
		if mem.Property == "slice" {
			objTy := e.inferExprType(mem.Object)
			if objTy.IsBlob {
				return e.emitBlobCall(mem, "slice", ex.Args, ex.GetPos())
			}
			if objTy.IsArrayBuffer {
				return e.emitArrayBufferSlice(mem, ex.Args, ex.GetPos())
			}
			if objTy.IsArray {
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
		// Buffer instance methods (TDD-00103) — checked before the generic
		// string/array chains can claim .toString/.write/.copy/.equals/
		// .compare; everything not named here (indexing, .fill, .indexOf,
		// .slice, HOFs, …) deliberately falls through to the shared
		// TypedArray/array machinery.
		if isBufferMethodName(mem.Property) && e.inferExprType(mem.Object).IsBuffer {
			return e.emitBufferInstanceCall(mem, mem.Property, ex.Args, ex.GetPos())
		}
		// Blob-only methods (TDD-00102) — checked before Response's own
		// .arrayBuffer()/.text() dispatch below can claim the same names.
		if e.inferExprType(mem.Object).IsBlob {
			switch mem.Property {
			case "arrayBuffer", "bytes", "text", "stream":
				return e.emitBlobCall(mem, mem.Property, ex.Args, ex.GetPos())
			}
		}
		// DataView accessors (getInt16/setFloat64/..., emit_dataview.go).
		if op, kind, ok := dataViewMethodKind(mem.Property); ok {
			if e.inferExprType(mem.Object).IsDataView {
				if op == "get" {
					return e.emitDataViewGet(mem, kind, ex.Args, ex.GetPos())
				}
				return e.emitDataViewSet(mem, kind, ex.Args, ex.GetPos())
			}
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
			case "getResponseHeader":
				return e.emitXHRGetResponseHeader(mem.Object, ex.Args, ex.GetPos())
			case "getAllResponseHeaders":
				return e.emitXHRGetAllResponseHeaders(mem.Object, ex.Args, ex.GetPos())
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
		// Weak collections (TDD-00112) — checked before the plain Map/Set
		// dispatch below, since WeakMap/WeakSet also carry IsMap/IsSet. WeakRef
		// carries neither, so it gets its own check.
		if objTy := e.inferExprType(mem.Object); objTy.IsWeakRef {
			ptr, err := e.resolveWeakRefForCall(mem.Object, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			return e.emitWeakRefCall(objTy, ptr, mem.Property, ex.Args, ex.GetPos())
		}
		if objTy := e.inferExprType(mem.Object); (objTy.IsMap || objTy.IsSet) && objTy.Weak {
			ty, ptr, err := e.resolveMapOrSetForCall(mem.Object, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			return e.emitWeakCall(ty, ptr, mem.Property, ex.Args, ex.GetPos())
		}
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
		// gen.throw(e) / gen.return(v) (TDD-00086) — the rest of the iterator
		// protocol, dispatched the same way as .next above.
		if mem.Property == "throw" && e.inferExprType(mem.Object).IsGenerator {
			return e.emitGeneratorThrow(mem.Object, e.inferExprType(mem.Object), ex.Args, ex.GetPos())
		}
		if mem.Property == "return" && e.inferExprType(mem.Object).IsGenerator {
			return e.emitGeneratorReturnMethod(mem.Object, e.inferExprType(mem.Object), ex.Args, ex.GetPos())
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
		// Function.prototype.call / .apply on a first-class function value
		// (TDD-00137 Stage A): fn.call(thisArg, a, b) and fn.apply(thisArg,
		// [a, b]) lower to a direct fn(a, b). Checked before the generic
		// field-call below so `fn.call`/`fn.apply` aren't misread as calling a
		// field literally named "call"/"apply".
		if (mem.Property == "call" || mem.Property == "apply") && e.inferExprType(mem.Object).IsFunc {
			return e.emitFunctionCallApply(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		// Function.prototype.bind (TDD-00137 Stage C): fn.bind(thisArg, …bound)
		// returns a new partially-applied function value.
		if mem.Property == "bind" && e.inferExprType(mem.Object).IsFunc {
			return e.emitFunctionBind(mem.Object, ex.Args, ex.GetPos())
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
		case "String":
			return e.emitGlobalStringConv(ex.Args, ex.GetPos())
		case "Number":
			return e.emitGlobalNumberConv(ex.Args, ex.GetPos())
		case "Boolean":
			return e.emitGlobalBooleanConv(ex.Args, ex.GetPos())
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
		case "queueMicrotask":
			return e.emitQueueMicrotask(ex.Args, ex.GetPos())
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
		case "gc":
			return e.emitGlobalGC(ex.Args, ex.GetPos())
		case "structuredClone":
			return e.emitStructuredClone(ex.Args, ex.GetPos())
		case "Symbol":
			return e.emitSymbolConstructor(ex.Args, ex.GetPos())
		case "BigInt":
			return e.emitBigIntConstructor(ex.Args, ex.GetPos())
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
		if info, found := e.lookupGenerator(id.Name); found {
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
			return e.emitGenericFuncCall(decl, ex.Args, ex.TypeArgs, ex.GetPos())
		}
		// Closure variable — including a named function expression's own
		// self-reference binding (TDD-00060).
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsFunc {
			return e.emitClosureCall(sym, ex.Args, ex.GetPos())
		}
		// Static-string eval fast path (TDD-00046 static subset): a
		// compile-time-constant `eval("<expression>")` is compiled through
		// this compiler's own parser + codegen, in place — no embedded
		// engine. Checked after every user-binding lookup, so a
		// user-defined function named `eval` still wins.
		if id.Name == "eval" {
			return e.emitStaticEval(ex.Args, ex.GetPos())
		}
		// Browser worker surface (TDD-00098 stage 6): a bare postMessage(x)
		// inside a worker module is parentPort.postMessage(x). Checked after
		// every user-binding lookup, so a user function named postMessage
		// still wins.
		if id.Name == "postMessage" && e.currentWorkerMod != "" {
			return e.emitParentPortCall("postMessage", ex.Args, ex.GetPos())
		}
		// Sibling namespace member call by bare name from inside a member
		// function body (TDD-00148 Stage 4) — retried under the mangled
		// name. Checked after every user-binding lookup, so locals shadow.
		if m := e.nsSibling(id.Name); m != "" {
			rewritten := ast.NewCallExpression(ast.NewIdentifier(m, ex.GetPos()), ex.Args, ex.GetPos())
			return e.emitCall(rewritten)
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

	// A method call whose receiver type doesn't have that method reaches here
	// too (the callee `obj.prop` isn't function-typed). Report it as the real
	// missing-method gap it is — a far more useful diagnostic than the generic
	// "only simple function calls" fallback, and (for the conformance leverage
	// map) it splits that catch-all bucket by the receiver's type instead of
	// lumping every unrecognized method call together.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		recv := describeReceiverType(e.inferExprType(mem.Object))
		// A bare namespace/global identifier receiver (Object.setPrototypeOf,
		// process.send, cluster.fork, Math.foo, …) infers as the i64 default;
		// name it explicitly so the diagnostic — and the conformance histogram —
		// attributes the missing member to the right namespace.
		if id, ok := mem.Object.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) {
			if n := strings.TrimSuffix(id.Name, "__kml_builtin"); n != id.Name {
				recv = n
			} else if knownGlobalNamespace[id.Name] {
				recv = id.Name
			}
		}
		return Value{}, fmt.Errorf("%d:%d: %s has no method '%s'", ex.GetPos().Line, ex.GetPos().Col, recv, mem.Property)
	}

	return Value{}, fmt.Errorf("%d:%d: only simple function calls are supported (the callee is not a named function, a function value, or a supported method)", ex.GetPos().Line, ex.GetPos().Col)
}

// knownGlobalNamespace lists the bare-identifier globals/namespaces whose
// members are static (so a receiver of this name infers as the numeric default,
// not a real value) — named explicitly in the "no method" diagnostic.
var knownGlobalNamespace = map[string]bool{
	"Object": true, "Math": true, "JSON": true, "Reflect": true, "Number": true,
	"Array": true, "String": true, "Boolean": true, "Symbol": true, "Promise": true,
	"process": true, "console": true, "Date": true, "RegExp": true, "Proxy": true,
}

// describeReceiverType names a value's type for a "<type> has no method 'x'"
// diagnostic — short human phrases for the common builtin/receiver types, so
// the error (and the conformance histogram it feeds) attributes a missing
// method to the right type rather than a generic catch-all.
func describeReceiverType(ty Type) string {
	switch {
	case ty.IsBuffer:
		return "Buffer"
	case ty.IsTypedArray:
		return "a TypedArray"
	case ty.IsArrayBuffer:
		return "an ArrayBuffer"
	case ty.IsDataView:
		return "a DataView"
	case ty.IsArray:
		return "an array"
	case ty.IsMap:
		return "a Map"
	case ty.IsSet:
		return "a Set"
	case ty.IsPromise:
		return "a Promise"
	case ty.IsDate:
		return "a Date"
	case ty.IsRegExp:
		return "a RegExp"
	case ty.IsBigInt:
		return "a bigint"
	case ty.IsNodeReadable || ty.IsNodeWritable:
		return "a Node stream"
	case ty.IsNetServer:
		return "a net.Server"
	case ty.IsNetSocket:
		return "a net socket"
	case ty.IsChildProcess:
		return "a ChildProcess"
	case ty.IsWorker:
		return "a Worker"
	case ty.HasEventEmitter:
		return "an EventEmitter"
	case ty.IsResponse:
		return "a Response"
	case ty.IsRequest:
		return "a Request"
	case ty.IsClass:
		return "class '" + ty.ClassName + "'"
	case isStringTy(ty):
		return "a string"
	case ty.IsObject:
		return "an object"
	case ty.IR != "ptr":
		return "a number"
	}
	return "a value of this type"
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
// checkSpreadArgs enforces TDD-00106's V1 spread rule: at most one spread
// argument, which must be the last argument and land exactly on a rest
// parameter (after the fixed arguments). Returns nil when there is no spread.
// singleSpread reports whether restArgs is exactly one spread argument
// (`f(...arr)`), returning it — the case a rest slot forwards directly.
func singleSpread(restArgs []ast.Expression) (*ast.SpreadElement, bool) {
	if len(restArgs) == 1 {
		if sp, ok := restArgs[0].(*ast.SpreadElement); ok {
			return sp, true
		}
	}
	return nil, false
}

// anySpread reports whether restArgs contains at least one spread argument —
// used to pick the runtime-concat rest buffer (TDD-00106 V2) over the plain
// malloc-and-store-N-scalars path a spread-free trailing arg list uses.
func anySpread(restArgs []ast.Expression) bool {
	for _, a := range restArgs {
		if _, ok := a.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}

// emitRestArgBuffer builds the rest-parameter backing buffer for a call whose
// rest region mixes ordinary positional arguments with one or more spread
// arguments (`f(...a, ...b)`, `f(x, ...arr, y)` — TDD-00106 V2). It allocates
// one contiguous buffer sized at runtime (each static arg counts as 1, each
// spread adds its runtime length) and fills it with a write cursor — memcpy per
// spread, store per static arg — returning the (ptr, len) operands the rest ABI
// takes. Every argument is evaluated once, left to right, before any copy, so
// JS evaluation order is preserved. Mirrors emitSpreadArrayLitData's cursor
// technique, but keyed off a call's arg list (resolveArrayForHOF spreads, so an
// array-returning expression works, not only a bare array variable).
func (e *Emitter) emitRestArgBuffer(restArgs []ast.Expression, elemTy Type) (dataReg, lenReg string, err error) {
	type restItem struct {
		spread bool
		ptr    string // spread: source data pointer
		length string // spread: source length register
		val    Value  // static: coerced element value
	}
	// Pass 1: evaluate every argument once, in source order.
	items := make([]restItem, 0, len(restArgs))
	staticCount := int64(0)
	for _, arg := range restArgs {
		if sp, ok := arg.(*ast.SpreadElement); ok {
			ptrReg, srcLenReg, srcElemTy, rerr := e.resolveArrayForHOF(sp.Arg, sp.Arg.GetPos())
			if rerr != nil {
				return "", "", rerr
			}
			if srcElemTy.IR != elemTy.IR || srcElemTy.IsArray != elemTy.IsArray || srcElemTy.IsObject != elemTy.IsObject {
				return "", "", fmt.Errorf("%d:%d: spread array's element type does not match the rest parameter's element type", sp.Arg.GetPos().Line, sp.Arg.GetPos().Col)
			}
			items = append(items, restItem{spread: true, ptr: ptrReg, length: srcLenReg})
		} else {
			val, verr := e.emitExprWithObjectHint(arg, elemTy)
			if verr != nil {
				return "", "", verr
			}
			val = e.coerce(val, elemTy)
			items = append(items, restItem{val: val})
			staticCount++
		}
	}
	// Total length = staticCount + sum(spread lengths).
	totalReg := fmt.Sprintf("%d", staticCount)
	for _, it := range items {
		if !it.spread {
			continue
		}
		nt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", nt, totalReg, it.length))
		totalReg = nt
	}
	// Allocate one contiguous buffer.
	e.ensureMalloc()
	bytesReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytesReg, totalReg, elemTy.Align()))
	dataReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, bytesReg))
	// Fill via a write cursor.
	cursorPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", cursorPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", cursorPtr))
	for _, it := range items {
		cVal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cVal, cursorPtr))
		dstReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dstReg, elemTy.IR, dataReg, cVal))
		if it.spread {
			copyBytes := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", copyBytes, it.length, elemTy.Align()))
			e.ensureMemcpy()
			e.emitInstr(fmt.Sprintf("call void @memcpy(ptr %s, ptr %s, i64 %s)", dstReg, it.ptr, copyBytes))
			newC := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newC, cVal, it.length))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newC, cursorPtr))
		} else {
			e.storeArrayElem(dstReg, elemTy, it.val)
			newC := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newC, cVal))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newC, cursorPtr))
		}
	}
	return dataReg, totalReg, nil
}

func (e *Emitter) checkSpreadArgs(args []ast.Expression, hasRest bool, regularCount int, pos ast.Pos) error {
	// V2 (TDD-00106): any number of spreads, in any order, freely mixed with
	// ordinary positional arguments — but only within the rest region. A spread
	// still cannot fill a fixed parameter slot (that needs a runtime split
	// against static arity), and a callee with no rest parameter can't take a
	// spread at all.
	for i, a := range args {
		sp, ok := a.(*ast.SpreadElement)
		if !ok {
			continue
		}
		if !hasRest {
			return fmt.Errorf("%d:%d: spread requires the called function to have a rest parameter (`...`) — spreading into a fixed-arity function is not supported", sp.Arg.GetPos().Line, sp.Arg.GetPos().Col)
		}
		if i < regularCount {
			return fmt.Errorf("%d:%d: a spread argument may only fill the rest parameter, not a fixed parameter slot — place it after the %d fixed argument(s)", sp.Arg.GetPos().Line, sp.Arg.GetPos().Col, regularCount)
		}
	}
	return nil
}

func (e *Emitter) emitCallToFuncSig(name string, sig FuncSig, args []ast.Expression, pos ast.Pos) (Value, error) {
	// A may-suspend async function is not called directly — it is spawned as a
	// coroutine task, returning a pending promise (TDD-00083 Stage 2).
	if sig.MaySuspend {
		return e.emitMaySuspendCall(name, sig, args, pos)
	}
	var argParts []string
	// How many args map to regular (non-rest) params.
	regularCount := len(sig.ParamTypes)
	if sig.HasRest {
		regularCount-- // last param slot is the rest array
	}
	// Spread argument (TDD-00106): V1 supports a spread only as the sole filler
	// of a rest parameter, after exactly the fixed arguments — f(...arr),
	// f(a, b, ...arr). Anything else (spread into a fixed-arity callee, a
	// non-last spread, multiple spreads) is a clean error rather than a
	// miscompile now that the parser accepts it in any argument position.
	if err := e.checkSpreadArgs(args, sig.HasRest, regularCount, pos); err != nil {
		return Value{}, err
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
					// Object-reference model (TDD-00127): pass the array's header
					// pointer so mutations inside the callee (push/splice)
					// propagate back to this caller. The i64 length is redundant
					// (kept for ABI stability).
					header, lenSlot := e.arrayDataLenSlots(sym)
					lenReg := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
					argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
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
					// A transient array expression is materialized into a fresh
					// header (TDD-00127); the callee may mutate it, but it has no
					// caller-visible identity, so that is correct.
					header := e.boxArrayValue(val)
					lenReg := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
					argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
				}
			} else if isNullableScalar(paramTy) {
				// A nullable-scalar parameter takes its boxed { i1, T }
				// aggregate (TDD-00064 Stage 3).
				argStr, err := e.emitNullableScalarArg(arg, paramTy)
				if err != nil {
					return Value{}, err
				}
				argParts = append(argParts, argStr)
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
					if !coerciblePure(val.Ty, paramTy) {
						return Value{}, fmt.Errorf("%d:%d: argument %d has a type incompatible with the parameter's declared type — this compiler is a typed subset", arg.GetPos().Line, arg.GetPos().Col, i+1)
					}
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
				header, lenReg := e.arrayArgFromAggregate(val)
				argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
			} else if isNullableScalar(paramTy) {
				argStr, err := e.emitNullableScalarArg(sig.Defaults[i], paramTy)
				if err != nil {
					return Value{}, fmt.Errorf("default value for param %d: %w", i, err)
				}
				argParts = append(argParts, argStr)
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
			// A nullable scalar's omitted value is a genuinely absent
			// { i1, T } aggregate (present = false).
			if paramTy.IsArray {
				argParts = append(argParts, "ptr "+e.emptyArrayArgHeader(), "i64 0")
			} else if isNullableScalar(paramTy) {
				argParts = append(argParts, nullableScalarStorageIR(paramTy)+" zeroinitializer")
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
		if spread, ok := singleSpread(restArgs); ok {
			// f(fixed..., ...arr): forward the array's own (ptr, len) buffer
			// straight into the rest slot — the rest-param ABI is (ptr, i64),
			// exactly what an array argument already lowers to (TDD-00106).
			ptrReg, lenReg, srcElemTy, err := e.resolveArrayForHOF(spread.Arg, spread.Arg.GetPos())
			if err != nil {
				return Value{}, err
			}
			if srcElemTy.IR != elemTy.IR || srcElemTy.IsArray != elemTy.IsArray || srcElemTy.IsObject != elemTy.IsObject {
				return Value{}, fmt.Errorf("%d:%d: spread array's element type does not match the rest parameter's element type", spread.Arg.GetPos().Line, spread.Arg.GetPos().Col)
			}
			restHdr := e.newArrayHeader(ptrReg, lenReg)
			argParts = append(argParts, "ptr "+restHdr, "i64 "+lenReg)
		} else if len(restArgs) == 0 {
			argParts = append(argParts, "ptr "+e.emptyArrayArgHeader(), "i64 0")
		} else if anySpread(restArgs) {
			// f(fixed..., ...a, x, ...b): a runtime-length mix of spreads and
			// positional args feeding the rest slot — concat into one buffer.
			dataReg, lenReg, err := e.emitRestArgBuffer(restArgs, elemTy)
			if err != nil {
				return Value{}, err
			}
			restHdr := e.newArrayHeader(dataReg, lenReg)
			argParts = append(argParts, "ptr "+restHdr, "i64 "+lenReg)
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
			restHdr := e.newArrayHeader(dataReg, fmt.Sprintf("%d", n))
			argParts = append(argParts, "ptr "+restHdr, fmt.Sprintf("i64 %d", n))
		}
	}
	argsStr := strings.Join(argParts, ", ")
	if sig.RetType.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", name, argsStr))
		return Value{Ty: TypeVoid}, nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", reg, sig.RetType.LLVMRetType(), name, argsStr))
	retTy := sig.RetType
	// A non-suspending async fn (didn't take the MaySuspend fiber path above)
	// now returns a settled task-shaped promise (TDD-00084 Part A) — tag it so
	// `await`/`.then` take the task path, matching the may-suspend result.
	if sig.IsAsync && retTy.IsPromise {
		retTy.PromiseTask = true
	}
	return Value{Ref: reg, Ty: retTy}, nil
}

// namespaceMembers resolves name against the TS-namespace table (TDD-00095).
// The resolver rewrites references to a merged function+namespace name with
// its per-file `__kml_modN` suffix (`greet` → `greet__kml_mod0`), while the
// table stays keyed by the source name — so a miss retries with that suffix
// stripped. Returns the member set (or nil) and the source-level namespace
// name to mangle members against.
// nsDottedChain flattens a member-expression chain of pure identifiers
// (`A.B.C`) into its dotted name (TDD-00148 V3). ok is false when any link
// is not a plain identifier/member step.
func nsDottedChain(expr ast.Expression) (string, bool) {
	switch ex := expr.(type) {
	case *ast.Identifier:
		return ex.Name, true
	case *ast.MemberExpression:
		if base, ok := nsDottedChain(ex.Object); ok {
			return base + "." + ex.Property, true
		}
	}
	return "", false
}

// namespaceByChain resolves a nested-namespace member-expression object
// (`A.B` in `A.B.f()`) against the namespace table, including the
// resolver's `__kml_modN` suffix on the root segment. Returns the member
// table and the dotted namespace name, or nil. Single identifiers are
// namespaceMembers' job — this only matches genuinely dotted chains.
func (e *Emitter) namespaceByChain(obj ast.Expression) (map[string]bool, string) {
	chain, ok := nsDottedChain(obj)
	if !ok || !strings.Contains(chain, ".") {
		return nil, ""
	}
	if m, ok := e.namespaces[chain]; ok {
		return m, chain
	}
	if i := strings.Index(chain, "."); i > 0 {
		root := chain[:i]
		if j := strings.LastIndex(root, "__kml_mod"); j > 0 {
			c2 := root[:j] + chain[i:]
			if m, ok := e.namespaces[c2]; ok {
				return m, c2
			}
			chain = c2
		}
	}
	// Expand the longest alias prefix (`M.X.f` where `M.X` aliases `M.N` —
	// ADR-00456), up to a small fixed number of expansions.
	for range [4]int{} {
		expanded := false
		for p := chain; p != ""; {
			if t, ok := e.nsAliases[p]; ok {
				chain = t + chain[len(p):]
				expanded = true
				break
			}
			i := strings.LastIndex(p, ".")
			if i < 0 {
				break
			}
			p = p[:i]
		}
		if !expanded {
			break
		}
		if m, ok := e.namespaces[chain]; ok {
			return m, chain
		}
	}
	return nil, ""
}

// stripNSTypeQualifier rewrites `ns.TypeName` — a namespace-qualified
// reference to a *type* member (enum/class), which desugars to a bare
// top-level name (ADR-00450) — to the bare `TypeName` identifier, so
// chains like `X.Color.Red` and `X.C.staticMethod()` resolve (ADR-00480).
// Returns nil when expr isn't that shape (value members, shadowed names,
// and unknown properties are untouched).
func (e *Emitter) stripNSTypeQualifier(expr ast.Expression) ast.Expression {
	mem, ok := expr.(*ast.MemberExpression)
	if !ok {
		return nil
	}
	id, ok := mem.Object.(*ast.Identifier)
	if !ok || e.isShadowedByLocal(id.Name) {
		return nil
	}
	members, _ := e.namespaceMembers(id.Name)
	if members == nil {
		return nil
	}
	if _, isValueMember := members[mem.Property]; isValueMember {
		return nil
	}
	// The type member's desugared bare name carries the resolver's
	// per-file suffix (`Color__kml_mod0`) while the source chain holds the
	// written name (the namespace root itself is never renamed, so there is
	// no suffix to borrow) — match exact first, then by mangled prefix.
	if _, isEnum := e.enums[mem.Property]; isEnum {
		return ast.NewIdentifier(mem.Property, mem.GetPos())
	}
	if _, isClass := e.classes[mem.Property]; isClass {
		return ast.NewIdentifier(mem.Property, mem.GetPos())
	}
	prefix := mem.Property + "__kml_mod"
	for name := range e.enums {
		if strings.HasPrefix(name, prefix) {
			return ast.NewIdentifier(name, mem.GetPos())
		}
	}
	for name := range e.classes {
		if strings.HasPrefix(name, prefix) {
			return ast.NewIdentifier(name, mem.GetPos())
		}
	}
	return nil
}

// nsSibling maps a bare identifier referenced inside a namespace member
// function to its sibling's mangled name (TDD-00148 Stage 4), or "" when
// not in a namespace context or no such member exists. Exportedness is
// irrelevant inside the namespace, so presence alone matches.
func (e *Emitter) nsSibling(name string) string {
	if e.curNamespace == "" {
		return ""
	}
	if members, ok := e.namespaces[e.curNamespace]; ok {
		if _, present := members[name]; present {
			return ast.NamespaceMangle(e.curNamespace, name)
		}
	}
	return ""
}

func (e *Emitter) namespaceMembers(name string) (map[string]bool, string) {
	if m, ok := e.namespaces[name]; ok {
		return m, name
	}
	// An import-equals alias for a namespace (ADR-00456).
	if t, ok := e.nsAliases[name]; ok {
		if m, ok := e.namespaces[t]; ok {
			return m, t
		}
	}
	if i := strings.LastIndex(name, "__kml_mod"); i > 0 {
		base := name[:i]
		if m, ok := e.namespaces[base]; ok {
			return m, base
		}
	}
	// Relative resolution from inside a namespace member (TDD-00148 V3):
	// `B.f()` written inside namespace `A` resolves to `A.B`, innermost
	// enclosing scope outward, matching TS's own lookup order.
	if e.curNamespace != "" {
		parts := strings.Split(e.curNamespace, ".")
		for k := len(parts); k >= 1; k-- {
			cand := strings.Join(parts[:k], ".") + "." + name
			if m, ok := e.namespaces[cand]; ok {
				return m, cand
			}
			if t, ok := e.nsAliases[cand]; ok {
				if m, ok := e.namespaces[t]; ok {
					return m, t
				}
			}
		}
	}
	return nil, ""
}
