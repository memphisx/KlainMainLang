// emit_dns.go — codegen for Node's `dns`: dns.lookup. Backed by runtime_dns.go's
// getaddrinfo helper. The callback fires synchronously at the call site (the
// same posture as zlib's callback forms), not deferred to a later loop tick.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// dnsLookupObjType is the { address, family } object dns.promises.lookup
// resolves to (a { ptr, i64 } struct).
func dnsLookupObjType() Type {
	return ObjectType([]Field{
		{Name: "address", Ty: TypePtr},
		{Name: "family", Ty: TypeI64},
	})
}

// emitDnsModuleCall dispatches dns.lookup / dns.resolve4 / dns.resolve.
func (e *Emitter) emitDnsModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "lookup":
		return e.emitDnsLookup(args, pos)
	case "resolve4", "resolve":
		return e.emitDnsResolve4(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: dns.%s is not supported", pos.Line, pos.Col, method)
}

// emitDnsResolve4 implements dns.resolve4(host, (err, addresses: string[]) =>
// ...): resolves every A record and fires the callback with the address array
// (empty + Error on failure). `resolve` aliases `resolve4` in V1.
func (e *Emitter) emitDnsResolve4(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: dns.resolve4 takes (hostname, callback)", pos.Line, pos.Col)
	}
	e.ensureDNSRuntime()
	hostVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	hostVal = e.coerce(hostVal, TypePtr)
	arrTy := ArrayOf(TypePtr)
	cb, err := e.resolveCallbackWithHints(args[1], []Type{errorObjType, arrTy})
	if err != nil {
		return Value{}, err
	}

	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_dns_resolve4(ptr %s)", agg, hostVal.Ref))
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", count, agg))
	isEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, count))
	arrVal := Value{Ref: agg, Ty: arrTy}

	failL := e.freshLabel("dnsr4fail")
	okL := e.freshLabel("dnsr4ok")
	doneL := e.freshLabel("dnsr4done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEmpty, failL, okL))

	e.emitLabel(okL)
	if _, err := e.emitCBCall(cb, []Value{{Ref: "null", Ty: errorObjType}, arrVal}); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(failL)
	errObj := e.buildErrorObj(0, e.internString("queryA ENOTFOUND"), e.internString("Error"))
	if _, err := e.emitCBCall(cb, []Value{{Ref: errObj, Ty: errorObjType}, arrVal}); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// emitDnsPromisesLookup implements dns.promises.lookup(host): resolves to a
// { address, family } object, or rejects with an Error on failure.
func (e *Emitter) emitDnsPromisesLookup(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: dns.promises.lookup takes (hostname)", pos.Line, pos.Col)
	}
	e.ensureDNSRuntime()
	e.ensurePromiseRuntime()
	e.ensureExceptionHelpers()
	hostVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	hostVal = e.coerce(hostVal, TypePtr)

	objTy := dnsLookupObjType()
	q := e.emitAllocSettledPromise()
	addr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dns_lookup(ptr %s)", addr, hostVal.Ref))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, addr))

	rejL := e.freshLabel("dnsprej")
	resL := e.freshLabel("dnspres")
	doneL := e.freshLabel("dnspdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, rejL, resL))

	e.emitLabel(resL)
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", obj))
	af := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", af, objTy.StructIR(), obj))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", addr, af))
	ff := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", ff, objTy.StructIR(), obj))
	e.emitInstr(fmt.Sprintf("store i64 4, ptr %s, align 8", ff))
	e.storePromiseValue(q, Value{Ref: obj, Ty: objTy})
	e.emitSetPromiseState(q, 1)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(rejL)
	errObj := e.buildErrorObj(0, e.internString("getaddrinfo ENOTFOUND"), e.internString("Error"))
	e.emitAsyncGenRejectPromise(q, errObj)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	qt := PromiseOf(objTy)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}

// emitDnsLookup implements dns.lookup(hostname, (err, address, family) => ...):
// resolves hostname to an IPv4 address string and fires the callback with
// (null, address, 4) on success or (Error, null, 0) on failure.
func (e *Emitter) emitDnsLookup(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: dns.lookup takes (hostname, callback)", pos.Line, pos.Col)
	}
	e.ensureDNSRuntime()
	hostVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	hostVal = e.coerce(hostVal, TypePtr)
	cb, err := e.resolveCallbackWithHints(args[1], []Type{errorObjType, TypePtr, TypeI64})
	if err != nil {
		return Value{}, err
	}

	addr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dns_lookup(ptr %s)", addr, hostVal.Ref))
	isnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isnull, addr))

	okL := e.freshLabel("dnsok")
	failL := e.freshLabel("dnsfail")
	doneL := e.freshLabel("dnsdone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isnull, failL, okL))

	e.emitLabel(okL)
	if _, err := e.emitCBCall(cb, []Value{
		{Ref: "null", Ty: errorObjType},
		{Ref: addr, Ty: TypePtr},
		{Ref: "4", Ty: TypeI64},
	}); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(failL)
	errObj := e.buildErrorObj(0, e.internString("getaddrinfo ENOTFOUND"), e.internString("Error"))
	if _, err := e.emitCBCall(cb, []Value{
		{Ref: errObj, Ty: errorObjType},
		{Ref: "null", Ty: TypePtr},
		{Ref: "0", Ty: TypeI64},
	}); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}
