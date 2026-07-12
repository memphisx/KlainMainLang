package llvm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"KlainMainLang/ast"
)

// Value is an LLVM value reference: either a register (%t0) or an inline constant (42).
type Value struct {
	Ref string
	Ty  Type
}

// Symbol represents a local variable in the symbol table.
type Symbol struct {
	Ptr    string // alloca for the value (scalars) or the data pointer (arrays)
	LenPtr string // alloca for the array length; empty for scalars
	Ty     Type
	Boxed  bool // true once Ptr points to a heap cell shared with closures that capture it
}

type scope struct {
	syms map[string]Symbol
}

// Emitter walks an AST and produces LLVM IR text.
type Emitter struct {
	globals   strings.Builder // global declarations (string constants, printf decl, …)
	functions strings.Builder // emitted user-defined function bodies
	allocas   strings.Builder // alloca instructions for the current function
	body      strings.Builder // body instructions for the current function
	scopes    []scope
	regCtr    int
	labelCtr  int
	strConsts  map[string]string // Go string value → @.s<n> name
	strIdx     int
	linkLibs   map[string]bool // external non-libc libraries the compiled program needs (e.g. "curl")
	usedPrintf  bool
	usedDprintf bool
	usedMalloc  bool
	usedCalloc  bool
	usedRealloc bool
	usedMemmove bool
	funcs          map[string]FuncSig         // registered function signatures
	interfaces     map[string]Type            // named interface and type alias registry
	enums          map[string]map[string]Value // enum name → member name → constant value
	currentRetType Type               // return type of the function being emitted
	blockDone      bool               // true after a terminator (ret/br) in the current block
	closureCtr     int                // monotonically increasing counter for unique closure names
	usedStrlen     bool
	usedMemcpy     bool
	usedStrcmp     bool
	usedSprintf    bool
	usedStrstr     bool
	usedStrncmp          bool
	usedStringTrim       bool
	usedStringTrimStart  bool
	usedStringTrimEnd    bool
	usedStringToUpper    bool
	usedStringToLower    bool
	usedStringReplace    bool
	usedStringReplaceAll bool
	usedStringSplit         bool
	usedAtoll               bool
	usedJSONStringifyNum    bool
	usedJSONStringifyStr    bool
	usedJSONParseStr        bool
	usedJSONFindValue       bool
	usedJSONParseFieldStr   bool
	usedAnyEq               bool
	usedClockGettime        bool
	usedDateNow             bool
	usedPerformanceNow      bool
	usedDateDecompose       bool
	usedSscanf              bool
	usedDaysFromCivil       bool
	usedDateParse           bool
	usedDateCompose         bool
	usedDateNameTables      bool
	usedFetch               bool
	usedFopen               bool
	usedFclose              bool
	usedFwrite              bool
	usedFsThrow             bool
	usedFsReadFile          bool
	usedFsWriteFile         bool
	usedFsAppendFile        bool
	usedFsExists            bool
	usedFsUnlink            bool
	usedBase64Encode        bool
	usedBase64Decode        bool
	usedHexDigits           bool
	usedHexDecodeTable      bool
	usedEncodeURIComponent  bool
	usedEncodeURI           bool
	usedDecodeURIComponent  bool
	usedDecodeURI           bool
	usedCryptoRandomBytes   bool
	usedCryptoFillNumArray  bool
	usedCryptoRandomUUID    bool
	usedReadLineSync        bool
	usedExecFileSync        bool
	usedProcessCwd          bool
	usedProcessChdir        bool
	usedGetpid              bool
	usedProcessKill         bool
	usedErrnoAccessor       bool
	usedStrerror            bool
	usedFsMkdir             bool
	usedFsRmdir             bool
	usedFsRename            bool
	usedFsReaddir           bool
	usedConsoleGroupDepth   bool
	usedConsoleTimer        bool
	usedConsoleCountMap     bool
	usedMapFree             bool
	usedClosureFree         bool
	usedTimers              bool
	usedMathFuncs           bool
	usedArc4Random          bool
	usedStrtoll             bool
	usedStrtod              bool
	usedGroupMapHelpers     bool
	usedQsort               bool
	usedSortCmpI64          bool
	usedSortCmpF64          bool
	usedSortCmpStr          bool
	usedSortTrampolineI64   bool
	usedSortTrampolineF64   bool
	usedSortTrampolineStr   bool
	usedSortClosGlobal      bool
	usedMapStrHelpers       bool
	usedMapNumHelpers       bool
	usedExceptionHelpers    bool
	breakStack    []string // end labels for enclosing loops / switch
	continueStack []string // continue-target labels for enclosing loops
	// pendingLabel is set by a LabeledStatement just before emitting its body;
	// the next loop to start consumes it via pushPendingLabel. Non-loop bodies
	// leave it unconsumed, so the label is simply never registered.
	pendingLabel    string
	namedLabelStack []namedLabel // labeled break/continue targets, innermost last
	usedFree bool
	usedExit   bool
	usedGetenv bool
	// Async function state (reset per function, like currentRetType).
	isAsync          bool
	coroHdl          string // register holding the malloc'd promise slot
	currentPromiseTy Type   // T in Promise<T>; void if Promise<void>
	coroRetLabel     string // label for the async-return block
}

func NewEmitter() *Emitter {
	e := &Emitter{
		strConsts:      make(map[string]string),
		funcs:          make(map[string]FuncSig),
		interfaces:     make(map[string]Type),
		enums:          make(map[string]map[string]Value),
		currentRetType: TypeI32, // main returns i32
	}
	e.pushScope()
	return e
}

// --- Scope ---

func (e *Emitter) pushScope() { e.scopes = append(e.scopes, scope{syms: make(map[string]Symbol)}) }
func (e *Emitter) popScope()  { e.scopes = e.scopes[:len(e.scopes)-1] }

func (e *Emitter) define(name string, sym Symbol) {
	e.scopes[len(e.scopes)-1].syms[name] = sym
}

func (e *Emitter) lookup(name string) (Symbol, bool) {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if s, ok := e.scopes[i].syms[name]; ok {
			return s, true
		}
	}
	return Symbol{}, false
}

// updateSymbolInPlace overwrites name's entry in whichever scope currently
// holds it (rather than shadowing it in the innermost scope), so the update
// stays visible after that scope's block exits.
func (e *Emitter) updateSymbolInPlace(name string, sym Symbol) bool {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if _, ok := e.scopes[i].syms[name]; ok {
			e.scopes[i].syms[name] = sym
			return true
		}
	}
	return false
}

// --- Name generation ---

func (e *Emitter) freshReg() string {
	n := e.regCtr
	e.regCtr++
	return fmt.Sprintf("%%t%d", n)
}

func (e *Emitter) freshLabel(prefix string) string {
	n := e.labelCtr
	e.labelCtr++
	return fmt.Sprintf("%s.%d", prefix, n)
}

// --- Emission helpers ---

func (e *Emitter) emitGlobal(line string) { e.globals.WriteString(line + "\n") }
func (e *Emitter) emitAlloca(line string) { e.allocas.WriteString("  " + line + "\n") }
func (e *Emitter) emitInstr(line string) {
	if e.blockDone {
		return // skip dead code after a terminator
	}
	e.body.WriteString("  " + line + "\n")
}

// emitTerminator emits a terminator instruction and marks the block as done.
func (e *Emitter) emitTerminator(line string) {
	if e.blockDone {
		return
	}
	e.body.WriteString("  " + line + "\n")
	e.blockDone = true
}

// emitLabel starts a new basic block, resetting the terminator flag.
func (e *Emitter) emitLabel(label string) {
	e.body.WriteString(label + ":\n")
	e.blockDone = false
}

// --- String constants ---

func (e *Emitter) internString(s string) string {
	if name, ok := e.strConsts[s]; ok {
		return name
	}
	name := fmt.Sprintf("@.s%d", e.strIdx)
	e.strIdx++
	esc, length := escapeLLVM(s)
	e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant [%d x i8] c\"%s\", align 1", name, length, esc))
	e.strConsts[s] = name
	return name
}

// --- Link flags ---

// requireLink marks that the compiled program needs an external, non-libc
// library at link time (e.g. "curl" for -lcurl). Every C dependency before
// fetch (malloc, sscanf, gmtime, …) was plain libc, implicitly linked by
// clang's default driver behavior with no extra flag needed — fetch is the
// first feature that needs anything beyond that, so this is a new,
// deliberately general mechanism (not a one-off special case for curl):
// the next native-library-backed feature (WebSocket, crypto.subtle, …) just
// calls this too. main.go reads LinkLibs() after EmitProgram and only adds
// -l<lib> flags for libraries a given program actually ended up using.
func (e *Emitter) requireLink(lib string) {
	if e.linkLibs == nil {
		e.linkLibs = map[string]bool{}
	}
	e.linkLibs[lib] = true
}

// LinkLibs returns the external libraries this program's compiled code
// needs, sorted for a reproducible build command.
func (e *Emitter) LinkLibs() []string {
	if len(e.linkLibs) == 0 {
		return nil
	}
	libs := make([]string, 0, len(e.linkLibs))
	for lib := range e.linkLibs {
		libs = append(libs, lib)
	}
	sort.Strings(libs)
	return libs
}

func escapeLLVM(s string) (string, int) {
	var b strings.Builder
	n := 0
	for _, c := range []byte(s) {
		switch c {
		case '\n':
			b.WriteString("\\0A")
		case '\r':
			b.WriteString("\\0D")
		case '\t':
			b.WriteString("\\09")
		case '"':
			b.WriteString("\\22")
		case '\\':
			b.WriteString("\\5C")
		default:
			if c < 32 || c > 126 {
				b.WriteString(fmt.Sprintf("\\%02X", c))
			} else {
				b.WriteByte(c)
			}
		}
		n++
	}
	b.WriteString("\\00")
	return b.String(), n + 1 // +1 for null terminator
}

// --- Type resolution ---

func (e *Emitter) resolveType(ta *ast.TypeAnnotation) Type {
	if ta == nil {
		return TypeI64 // default for untyped numeric variables
	}
	if ta.IsFuncType {
		params := make([]Type, len(ta.FuncParams))
		for i := range ta.FuncParams {
			params[i] = e.resolveType(&ta.FuncParams[i])
		}
		ret := TypeVoid
		if ta.FuncRetType != nil {
			ret = e.resolveType(ta.FuncRetType)
		}
		return FuncType(params, ret)
	}
	// Promise<T> must be checked before ElemType (which is also used for the type param).
	if ta.Name == "Promise" {
		if ta.ElemType != nil {
			inner := e.resolveType(ta.ElemType)
			return PromiseOf(inner)
		}
		return PromiseOf(TypeVoid)
	}
	if ta.ElemType != nil {
		return ArrayOf(e.resolveType(ta.ElemType))
	}
	if len(ta.Fields) > 0 {
		fields := make([]Field, len(ta.Fields))
		for i, af := range ta.Fields {
			fields[i] = Field{Name: af.Name, Ty: e.resolveType(af.Type)}
		}
		return ObjectType(fields)
	}

	// Named type: check interface registry before falling back to built-ins.
	name := ta.Name
	// Handle T[] where T is a named interface (e.g. "User[]").
	if len(name) > 2 && name[len(name)-2:] == "[]" {
		base := name[:len(name)-2]
		if ty, ok := e.interfaces[base]; ok {
			return ArrayOf(ty)
		}
	}
	if ty, ok := e.interfaces[name]; ok {
		if ta.Nullable {
			ty.Nullable = true
		}
		return ty
	}
	ty := ResolveTypeName(ta.Name)
	if ta.Nullable {
		ty.Nullable = true
	}
	return ty
}

// --- Top-level entry ---

// EmitProgram generates LLVM IR for an entire program (script-style: top-level → main).
func (e *Emitter) EmitProgram(prog *ast.Program) (string, error) {
	// Pass -1: register enums so members are available as constants everywhere.
	e.registerEnums(prog)

	// Pass 0: register interfaces and type aliases so they're available to function signatures.
	e.registerInterfaces(prog)

	// Pass 1: register all top-level function signatures so calls work regardless of order.
	e.registerFunctions(prog)

	// Pass 2: emit each function declaration.
	for _, stmt := range prog.Body {
		if fd, ok := stmt.(*ast.FunctionDeclaration); ok {
			if err := e.emitFunctionDecl(fd); err != nil {
				return "", err
			}
		}
	}

	// Pass 3: emit remaining statements into main().
	// process.argv is backed by two globals set from main's own argc/argv
	// parameters, so any expression (top-level code, or any function/closure)
	// can read it later without needing to be threaded through explicitly.
	e.emitGlobal("@__argv_ptr = internal global ptr null, align 8")
	e.emitGlobal("@__argv_len = internal global i64 0, align 8")
	argc64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i32 %%argc to i64", argc64))
	e.emitInstr(fmt.Sprintf("store ptr %%argv, ptr @__argv_ptr, align 8"))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__argv_len, align 8", argc64))
	for _, stmt := range prog.Body {
		if _, ok := stmt.(*ast.FunctionDeclaration); ok {
			continue
		}
		if err := e.emitStmt(stmt); err != nil {
			return "", err
		}
	}
	// If the program ever called setTimeout/setInterval/clearTimeout/
	// clearInterval, drain any still-pending timers after the top-level
	// script finishes — the same place real Node keeps the process alive
	// for. Skipped entirely (via emitInstr's own dead-code check) if the
	// last top-level statement already terminated the block, e.g.
	// process.exit() — matching real Node, which also never drains
	// pending timers after an explicit exit.
	if e.usedTimers {
		e.emitInstr("call void @__kml_timer_drain()")
	}
	e.emitTerminator("ret i32 0")

	var out strings.Builder
	out.WriteString("; Generated by KlainMainLang\n\n")
	out.WriteString(e.globals.String())
	if e.globals.Len() > 0 {
		out.WriteString("\n")
	}
	out.WriteString(e.functions.String())
	if e.functions.Len() > 0 {
		out.WriteString("\n")
	}
	out.WriteString("define i32 @main(i32 %argc, ptr %argv) {\nentry:\n")
	out.WriteString(e.allocas.String())
	out.WriteString(e.body.String())
	out.WriteString("}\n")
	return out.String(), nil
}

// registerEnums pre-scans all top-level enum declarations and resolves each member
// to a compile-time constant Value. Numeric members auto-increment from 0 (or from
// the last explicit value); string members require an explicit string literal.
func (e *Emitter) registerEnums(prog *ast.Program) {
	for _, stmt := range prog.Body {
		ed, ok := stmt.(*ast.EnumDeclaration)
		if !ok {
			continue
		}
		members := make(map[string]Value, len(ed.Members))

		// Detect string enum: any member has an explicit string value.
		isString := false
		for _, m := range ed.Members {
			if _, ok := m.Value.(*ast.StringLiteral); ok {
				isString = true
				break
			}
		}

		if isString {
			for _, m := range ed.Members {
				if sl, ok := m.Value.(*ast.StringLiteral); ok {
					ptr := e.internString(sl.Value)
					members[m.Name] = Value{Ref: ptr, Ty: TypePtr}
				}
			}
		} else {
			var counter int64
			for _, m := range ed.Members {
				if m.Value != nil {
					if nl, ok := m.Value.(*ast.NumberLiteral); ok {
						n, _ := strconv.ParseInt(nl.Value, 0, 64)
						counter = n
					}
				}
				members[m.Name] = Value{Ref: fmt.Sprintf("%d", counter), Ty: TypeI64}
				counter++
			}
		}
		e.enums[ed.Name] = members
	}
}

// registerInterfaces pre-scans all top-level interface and type alias declarations
// and records them in e.interfaces so resolveType can resolve named object types.
func (e *Emitter) registerInterfaces(prog *ast.Program) {
	for _, stmt := range prog.Body {
		switch s := stmt.(type) {
		case *ast.InterfaceDeclaration:
			fields := make([]Field, len(s.Fields))
			for i, f := range s.Fields {
				fields[i] = Field{Name: f.Name, Ty: e.resolveType(f.Type)}
			}
			e.interfaces[s.Name] = ObjectType(fields)
		case *ast.TypeAliasDeclaration:
			e.interfaces[s.Name] = e.resolveType(s.Type)
		}
	}
}

// registerFunctions pre-scans all top-level function declarations and records
// their signatures so calls can be resolved before the function body is emitted.
func (e *Emitter) registerFunctions(prog *ast.Program) {
	for _, stmt := range prog.Body {
		fd, ok := stmt.(*ast.FunctionDeclaration)
		if !ok {
			continue
		}
		retType := TypeVoid
		if fd.ReturnType != nil {
			retType = e.resolveType(fd.ReturnType)
		}
		sig := FuncSig{RetType: retType}
		for _, p := range fd.Params {
			pty := TypeI64
			if p.Type != nil {
				pty = e.resolveType(p.Type)
			} else if p.Rest {
				pty = ArrayOf(TypeI64) // default rest element type: number
			}
			sig.ParamTypes = append(sig.ParamTypes, pty)
			sig.Defaults = append(sig.Defaults, p.Default) // nil when no default
		}
		if len(fd.Params) > 0 && fd.Params[len(fd.Params)-1].Rest {
			sig.HasRest = true
		}
		e.funcs[fd.Name] = sig
	}
}

// emitFunctionDecl emits one user-defined function into e.functions.
