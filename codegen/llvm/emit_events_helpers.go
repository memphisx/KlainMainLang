package llvm

// emit_events_helpers.go — the `events` module's static helper `once`
// (TDD-00167). `events.once(emitter, name)` returns a Promise that resolves with
// the event's arguments the first time it fires; implemented by desugaring to
// `new Promise((res) => emitter.once(name, (…args) => res([…args])))`, reusing
// the existing Promise + EventEmitter machinery. (Requires the Promise<T[]>
// value fix, ADR-00674.)

import (
	"fmt"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

// eventsOnceArgShape returns the element type and arity of the args array `once`
// resolves with — the payload type for a single-value event, or the (homogeneous)
// tuple element type for a multi-argument event. A mixed-type tuple has no single
// array element type (this compiler's arrays are homogeneous), so it is a clean
// V1 rejection.
func (e *Emitter) eventsOnceArgShape(evTy Type, pos ast.Pos) (Type, int, error) {
	if !evTy.IsTuple {
		return evTy, 1, nil
	}
	elems := tupleElemTypes(evTy)
	if len(elems) == 0 {
		return TypeVoid, 0, fmt.Errorf("%d:%d: events.once() on an empty-tuple event is not yet supported (V1)", pos.Line, pos.Col)
	}
	for _, el := range elems {
		if el.IR != elems[0].IR {
			return Type{}, 0, fmt.Errorf("%d:%d: events.once() on a mixed-type multi-argument event is not yet supported — this compiler's arrays are homogeneous (V1)", pos.Line, pos.Col)
		}
	}
	return elems[0], len(elems), nil
}

// eventsElemAnnotationName is the source-level type name whose annotation
// round-trips to elem's *exact* representation (listenerTypeName collapses
// i64/double to "number", but a bare "number" resolves to a double, so the
// width-exact name is needed for the resolved array's element type to match).
func eventsElemAnnotationName(elem Type) string {
	switch {
	case isStringTy(elem):
		return "string"
	case elem.IR == "double":
		return "float64"
	case elem.IR == "i1":
		return "boolean"
	case elem.IR == "i64":
		return "number"
	}
	return ""
}

// eventsOncePromiseType is the inferred type of `events.once(emitter, name)` —
// Promise<elem[]> — or (Type{}, false) when it can't be determined statically.
func (e *Emitter) eventsOncePromiseType(args []ast.Expression) (Type, bool) {
	if len(args) != 2 {
		return Type{}, false
	}
	eeTy := e.inferExprType(args[0])
	if !eeTy.IsEventEmitter || eeTy.EventEmitterPayload == nil {
		return Type{}, false
	}
	evTy, evVoid, err := e.resolveEventPayload(*eeTy.EventEmitterPayload, args[1], args[1].GetPos())
	if err != nil || evVoid {
		return Type{}, false
	}
	elem, _, err := e.eventsOnceArgShape(evTy, args[1].GetPos())
	if err != nil {
		return Type{}, false
	}
	return PromiseOf(ArrayOf(elem)), true
}

// emitEventsOnce emits `events.once(emitter, name)`.
func (e *Emitter) emitEventsOnce(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: events.once(emitter, eventName) takes 2 arguments", pos.Line, pos.Col)
	}
	eeTy := e.inferExprType(args[0])
	if !eeTy.IsEventEmitter || eeTy.EventEmitterPayload == nil {
		return Value{}, fmt.Errorf("%d:%d: events.once()'s first argument must be an EventEmitter", pos.Line, pos.Col)
	}
	evTy, evVoid, err := e.resolveEventPayload(*eeTy.EventEmitterPayload, args[1], pos)
	if err != nil {
		return Value{}, err
	}
	if evVoid {
		return Value{}, fmt.Errorf("%d:%d: events.once() on a payload-less event is not yet supported (V1)", pos.Line, pos.Col)
	}
	elem, arity, err := e.eventsOnceArgShape(evTy, pos)
	if err != nil {
		return Value{}, err
	}
	elemName := eventsElemAnnotationName(elem)
	if elemName == "" {
		return Value{}, fmt.Errorf("%d:%d: events.once() on an event whose payload isn't a string/number/boolean is not yet supported (V1)", pos.Line, pos.Col)
	}

	// Desugar: new Promise<elem[]>((__once_res, __once_rej) =>
	//            emitter.once(name, (__once_v0, …) => __once_res([__once_v0, …])))
	params := make([]ast.Param, arity)
	elems := make([]ast.Expression, arity)
	for i := 0; i < arity; i++ {
		n := fmt.Sprintf("__once_v%d", i)
		params[i] = ast.Param{Name: n}
		elems[i] = ast.NewIdentifier(n, pos)
	}
	resCall := ast.NewCallExpression(ast.NewIdentifier("__once_res", pos), []ast.Expression{ast.NewArrayLiteral(elems, pos)}, pos)
	listener := ast.NewArrowFunction(params, nil, nil, ast.NewBlockStatement([]ast.Statement{ast.NewExpressionStatement(resCall, pos)}, pos), pos)
	onceCall := ast.NewCallExpression(ast.NewMemberExpression(args[0], "once", pos), []ast.Expression{args[1], listener}, pos)
	executor := ast.NewArrowFunction(
		[]ast.Param{{Name: "__once_res"}, {Name: "__once_rej"}}, nil, nil,
		ast.NewBlockStatement([]ast.Statement{ast.NewExpressionStatement(onceCall, pos)}, pos), pos)
	promiseExpr := ast.NewNewExpression("Promise", []ast.Expression{executor}, pos)
	// Promise<elem[]> so the resolve function accepts the args array (the value
	// type isn't inferrable through the nested listener).
	promiseExpr.TypeArgs = []*ast.TypeAnnotation{{ElemType: &ast.TypeAnnotation{Name: elemName}}}
	return e.emitExpr(promiseExpr)
}

// --- events.on(emitter, name) — an async iterator over an event (TDD-00167) ---
//
// `events.on(ee, name)` returns an async-iterable that yields the event's
// argument array on each emission, buffering events that arrive between
// iterations (an unbounded queue, Node's default) and parking the consumer
// when the queue is empty until the next emission. A naive
// `while (true) yield await once(...)` loses events emitted while the body is
// between yields, so it is not used.
//
// The faithful shape needs the listener registered *eagerly* — at the `on(...)`
// call, before any consumption — so events emitted before the first `.next()`
// pull are still captured. A plain async generator registers its listener only
// when its body first runs (on the first `.next()`), which is too late. So the
// desugar is a synthesized pair (memoized per element type + arity):
//
//	function __kml_events_on_setup_<key>(ee, name): <state> {
//	  const st = { q: [], resolve: null };
//	  ee.on(name, (...a) => {           // registered NOW, eagerly
//	    const item = [...a];
//	    if (st.resolve !== null) { const r = st.resolve; st.resolve = null; r(item); }
//	    else { st.q.push(item); }
//	  });
//	  return st;
//	}
//	async function* __kml_events_on_iter_<key>(st): elem[] {
//	  while (true) {
//	    if (st.q.length > 0) { yield st.q.shift(); }
//	    else { yield await new Promise((res) => { st.resolve = res; }); }
//	  }
//	}
//
// and `on(ee, name)` becomes `__kml_events_on_iter_<key>(__kml_events_on_setup_<key>(ee, name))`
// — the setup call runs eagerly (constructing the generator instance evaluates
// its argument), attaching the listener; the returned generator instance is
// what `for await` drives. The shared `st` object (buffer + pending-waiter
// resolver) is captured by the listener and read by the iterator, so a burst of
// synchronous emissions buffers and a later emission resumes a parked consumer.
// This builds directly on the array-yielding generators of ADR-00676 (the
// iterator yields `elem[]`) and the escaping-Promise-resolver support.

// eventsOnHelper is one memoized `events.on` desugar — the setup + iterator
// function pair for a given element type and arity, and whether their bodies
// have been emitted yet.
type eventsOnHelper struct {
	setupName string
	iterName  string
	iterInfo  *GeneratorInfo
	setupFd   *ast.FunctionDeclaration
	iterFd    *ast.FunctionDeclaration
	emitted   bool
}

// eventsOnStateAnnotation is the inline object type of the shared `st` cell:
// a queue of argument arrays plus a nullable pending-waiter resolver.
func eventsOnStateAnnotation(elemName string) string {
	return fmt.Sprintf("{ q: %s[][]; resolve: ((v: %s[]) => void) | null }", elemName, elemName)
}

// eventsOnPayloadAnnotation is the `EventEmitter<...>` type argument the setup
// function's emitter parameter carries — a single element for a one-argument
// event, or a homogeneous tuple for a multi-argument one (so `ee.on`'s existing
// per-payload listener hinting types the listener's parameters).
func eventsOnPayloadAnnotation(elemName string, arity int) string {
	if arity == 1 {
		return elemName
	}
	parts := make([]string, arity)
	for i := range parts {
		parts[i] = elemName
	}
	return "[" + joinComma(parts) + "]"
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// eventsOnHelperSource is the TypeScript source of one setup + iterator pair.
func eventsOnHelperSource(setupName, iterName, elemName string, arity int) string {
	lp := make([]string, arity)  // listener params (untyped — hinted from the payload)
	el := make([]string, arity)  // items packed into the args array
	for i := 0; i < arity; i++ {
		lp[i] = fmt.Sprintf("__on_v%d", i)
		el[i] = fmt.Sprintf("__on_v%d", i)
	}
	state := eventsOnStateAnnotation(elemName)
	payload := eventsOnPayloadAnnotation(elemName, arity)
	return fmt.Sprintf(`
function %s(__on_ee: EventEmitter<%s>, __on_name: string): %s {
  const __on_st: %s = { q: [], resolve: null };
  __on_ee.on(__on_name, (%s) => {
    const __on_item = [%s];
    if (__on_st.resolve !== null) { const __on_r = __on_st.resolve; __on_st.resolve = null; __on_r(__on_item); }
    else { __on_st.q.push(__on_item); }
  });
  return __on_st;
}
async function* %s(__on_st: %s): %s[] {
  while (true) {
    if (__on_st.q.length > 0) {
      yield __on_st.q.shift();
    } else {
      yield await new Promise<%s[]>((__on_res) => { __on_st.resolve = __on_res; });
    }
  }
}
`, setupName, payload, state, state, joinComma(lp), joinComma(el),
		iterName, state, elemName, elemName)
}

// eventsOnArgShape resolves `events.on(emitter, name)`'s element type and arity
// from the first argument's EventEmitter payload and the event name — reusing
// eventsOnceArgShape's homogeneity rules (a mixed-type multi-argument event has
// no single array element type). Returns a clear error the caller surfaces.
func (e *Emitter) eventsOnArgShape(args []ast.Expression, pos ast.Pos) (elem Type, arity int, elemName string, err error) {
	if len(args) != 2 {
		return Type{}, 0, "", fmt.Errorf("%d:%d: events.on(emitter, eventName) takes 2 arguments", pos.Line, pos.Col)
	}
	eeTy := e.inferExprType(args[0])
	if !eeTy.IsEventEmitter || eeTy.EventEmitterPayload == nil {
		return Type{}, 0, "", fmt.Errorf("%d:%d: events.on()'s first argument must be an EventEmitter", pos.Line, pos.Col)
	}
	evTy, evVoid, err := e.resolveEventPayload(*eeTy.EventEmitterPayload, args[1], pos)
	if err != nil {
		return Type{}, 0, "", err
	}
	if evVoid {
		return Type{}, 0, "", fmt.Errorf("%d:%d: events.on() on a payload-less event is not yet supported (V1)", pos.Line, pos.Col)
	}
	elem, arity, err = e.eventsOnceArgShape(evTy, pos)
	if err != nil {
		return Type{}, 0, "", err
	}
	elemName = eventsElemAnnotationName(elem)
	if elemName == "" {
		return Type{}, 0, "", fmt.Errorf("%d:%d: events.on() on an event whose payload isn't a string/number/boolean is not yet supported (V1)", pos.Line, pos.Col)
	}
	return elem, arity, elemName, nil
}

// ensureEventsOnHelperSigs synthesizes (once per element type + arity) the
// setup + iterator function pair, parses it, and registers their signatures so
// call sites and type inference resolve — without emitting their bodies. The
// body emission is deferred to emitEventsOnHelperBodies (driven from the emit
// path), keeping type inference free of IR emission.
func (e *Emitter) ensureEventsOnHelperSigs(elemName string, arity int) (*eventsOnHelper, error) {
	key := fmt.Sprintf("%s_%d", elemName, arity)
	if h, ok := e.eventsOnHelpers[key]; ok {
		return h, nil
	}
	setupName := "__kml_events_on_setup_" + key
	iterName := "__kml_events_on_iter_" + key
	prog, err := parser.Parse(eventsOnHelperSource(setupName, iterName, elemName, arity))
	if err != nil {
		return nil, fmt.Errorf("internal: events.on helper for %s failed to parse: %w", key, err)
	}
	var setupFd, iterFd *ast.FunctionDeclaration
	for _, st := range prog.Body {
		fd, ok := st.(*ast.FunctionDeclaration)
		if !ok {
			continue
		}
		if fd.Name == setupName {
			setupFd = fd
		} else if fd.Name == iterName {
			iterFd = fd
		}
	}
	if setupFd == nil || iterFd == nil {
		return nil, fmt.Errorf("internal: events.on helper for %s did not synthesize both functions", key)
	}
	info, err := e.buildGeneratorSig(iterFd)
	if err != nil {
		return nil, err
	}
	e.generators[iterName] = info
	e.funcs[setupName] = e.buildFunctionSig(setupFd)
	h := &eventsOnHelper{setupName: setupName, iterName: iterName, iterInfo: info, setupFd: setupFd, iterFd: iterFd}
	e.eventsOnHelpers[key] = h
	return h, nil
}

// emitEventsOnHelperBodies emits the setup + iterator function bodies for a
// helper exactly once. Safe to call mid-emission: emitFunctionDecl /
// emitGeneratorFunctionDecl each fully save and restore the current function's
// emission context and write the completed function into e.functions.
func (e *Emitter) emitEventsOnHelperBodies(h *eventsOnHelper) error {
	if h.emitted {
		return nil
	}
	h.emitted = true
	if err := e.emitFunctionDecl(h.setupFd); err != nil {
		return err
	}
	return e.emitGeneratorFunctionDecl(h.iterFd, h.iterInfo)
}

// eventsOnGeneratorType is `events.on(emitter, name)`'s inferred type — the
// iterator generator instance (an async generator of `elem[]`), so `for await`
// consumes it. (Type{}, false) when the shape can't be resolved statically.
func (e *Emitter) eventsOnGeneratorType(args []ast.Expression, pos ast.Pos) (Type, bool) {
	_, arity, elemName, err := e.eventsOnArgShape(args, pos)
	if err != nil {
		return Type{}, false
	}
	h, err := e.ensureEventsOnHelperSigs(elemName, arity)
	if err != nil {
		return Type{}, false
	}
	return h.iterInfo.GenTy, true
}

// emitEventsOn emits `events.on(emitter, name)` as
// `__kml_events_on_iter_<key>(__kml_events_on_setup_<key>(emitter, name))`.
func (e *Emitter) emitEventsOn(args []ast.Expression, pos ast.Pos) (Value, error) {
	_, arity, elemName, err := e.eventsOnArgShape(args, pos)
	if err != nil {
		return Value{}, err
	}
	h, err := e.ensureEventsOnHelperSigs(elemName, arity)
	if err != nil {
		return Value{}, err
	}
	if err := e.emitEventsOnHelperBodies(h); err != nil {
		return Value{}, err
	}
	setupCall := ast.NewCallExpression(ast.NewIdentifier(h.setupName, pos), []ast.Expression{args[0], args[1]}, pos)
	iterCall := ast.NewCallExpression(ast.NewIdentifier(h.iterName, pos), []ast.Expression{setupCall}, pos)
	return e.emitExpr(iterCall)
}
