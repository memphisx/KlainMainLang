package checker

import (
	"math"
	"strings"
	"unicode"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/diag"
	"KlainMainLang/lib"
)

// unresolvedValue reports a value reference to a name nothing declares,
// with the diagnostic tsc's onFailedToResolveSymbol picks: a missing `C.` or
// `this.` prefix (TS2662, TS2663), a constructor's local read by a field
// initializer (TS2301), a namespace or a type used as a value (TS2708,
// TS2693), a spelling suggestion (TS2552), a missing test-runner or jQuery
// declaration (TS2593, TS2592), a shorthand property (TS18004), and
// otherwise TS2304. at is the reference (an identifier, or the `new`
// expression naming a class); scope is where its lookup started.
func (c *Checker) unresolvedValue(name string, at ast.Node, pos ast.Pos, scope *binder.Scope) {
	if c.opts.CompatJS() || strings.HasSuffix(name, "_kml_builtin") || strings.HasPrefix(name, "__kml") {
		return
	}
	src := sourceName(name)
	if intrinsicNames[src] || c.b.Program.BuiltinImports[src] || c.nsAlias(src) || c.ambientName(src) {
		return
	}
	if lib.KnownGlobal(src) {
		if !lib.GlobalValue(src) && c.b.Globals != nil && lookupName(src, c.b.Globals) == nil {
			// A library type the program reads as a value (`HTMLElementTagNameMap`).
			c.reportOnce(diag.TypeAsValue, pos, src)
		}
		return
	}
	key := cannotFindKey{src, pos}
	if c.cannotFound[key] {
		return
	}
	c.cannotFound[key] = true
	id, _ := at.(*ast.Identifier)
	if id != nil && c.exportDefaultName(id) && lookupName(src, scope) != nil {
		return // `export default T` exports whatever T names: a type, a namespace
	}
	if id != nil {
		if cls, static := c.missingPrefix(id, src); cls != "" {
			if static {
				c.report(diag.MissingStaticPrefix, pos, src, cls)
			} else {
				c.report(diag.MissingThisPrefix, pos, src)
			}
			return
		}
		if field := c.invalidInitializer(id, src); field != "" {
			c.report(diag.CtorLocalInInitializer, pos, field, src)
			return
		}
	}
	for s := scope; s != nil; s = s.Parent {
		sym := s.Symbols.Get(src)
		if sym == nil {
			continue
		}
		switch {
		case sym.Flags&binder.TypeModule != 0:
			c.report(diag.NamespaceAsValue, pos, src)
			return
		case sym.Flags&binder.Type != 0 && sym.Flags&binder.Value == 0:
			c.report(diag.TypeAsValue, pos, src)
			return
		}
	}
	if primitiveTypeNames[src] || c.b.Program.TypeParamNames[src] && c.typeParamInScope(at, src) {
		c.report(diag.TypeAsValue, pos, src)
		return
	}
	suggest := c.suggestionCount < 10 // tsc's maximumSuggestionCount
	c.suggestionCount++
	if suggest {
		if sug := c.spellingSuggestion(src, scope); sug != "" {
			c.report(diag.CannotFindNameDidYouMean, pos, src, sug)
			return
		}
	}
	switch src {
	case "beforeEach", "describe", "suite", "it", "test":
		c.report(diag.CannotFindTestRunner, pos, src)
		return
	case "$":
		c.report(diag.CannotFindJQuery, pos, src)
		return
	}
	if c.shorthandValue(at) {
		c.report(diag.ShorthandNoValue, pos, src)
		return
	}
	c.report(diag.CannotFindName, pos, src)
}

// exportDefaultName reports whether id is the whole of `export default id`.
func (c *Checker) exportDefaultName(id *ast.Identifier) bool {
	v, ok := c.parentOf(id).(*ast.VarDeclaration)
	if !ok || v.Name != "default" || v.Init != ast.Expression(id) {
		return false
	}
	ed, ok := c.parentOf(v).(*ast.ExportDeclaration)
	return ok && ed.IsDefault
}

// reportOnce reports m at pos once per name and position.
func (c *Checker) reportOnce(m *diag.Message, pos ast.Pos, name string) {
	key := cannotFindKey{name, pos}
	if c.cannotFound[key] {
		return
	}
	c.cannotFound[key] = true
	c.report(m, pos, name)
}

// primitiveTypeNames are the type keywords tsc names in TS2693 when one is
// read as a value (isPrimitiveTypeName).
var primitiveTypeNames = map[string]bool{"any": true, "string": true, "number": true, "boolean": true, "never": true, "unknown": true}

// typeParamInScope reports whether a type parameter named name encloses at
// (a generic function's, method's or class's).
func (c *Checker) typeParamInScope(at ast.Node, name string) bool {
	for n := c.parentOf(at); n != nil; n = c.parentOf(n) {
		var tps []string
		switch x := n.(type) {
		case *ast.FunctionDeclaration:
			tps = x.TypeParams
		case *ast.ClassDeclaration:
			tps = x.TypeParams
		}
		for _, tp := range tps {
			if tp == name {
				return true
			}
		}
	}
	return false
}

// shorthandValue reports whether at is the value of a shorthand property
// (`{ b }`).
func (c *Checker) shorthandValue(at ast.Node) bool {
	lit, ok := c.parentOf(at).(*ast.ObjectLiteral)
	if !ok {
		return false
	}
	for _, p := range lit.Properties {
		if p.Shorthand && p.Value == at {
			return true
		}
	}
	return false
}

// thisContainer is the function-like node that gives id its `this`: a
// method or constructor, a function, a static block, or the class whose
// field initializer id is in (arrow functions are looked through); with the
// class declaring it, when it is a class member.
func (c *Checker) thisContainer(id ast.Node) (container ast.Node, cls *ast.ClassDeclaration) {
	child := id
	for n := c.parentOf(id); n != nil; child, n = n, c.parentOf(n) {
		switch x := n.(type) {
		case *ast.ArrowFunction:
			continue
		case *ast.FunctionExpression:
			return x, nil
		case *ast.FunctionDeclaration:
			cls, _ := c.parentOf(x).(*ast.ClassDeclaration)
			return x, cls
		case *ast.ClassDeclaration:
			if blk, ok := child.(*ast.BlockStatement); ok {
				for _, sb := range x.StaticBlocks {
					if sb == blk {
						return blk, x
					}
				}
			}
			return x, x // a field initializer (or a decorator)
		}
	}
	return nil, nil
}

// fieldOf is the field of cls whose initializer holds id, or nil.
func (c *Checker) fieldOf(cls *ast.ClassDeclaration, id ast.Node) *ast.AnnotField {
	for n := ast.Node(id); n != nil; n = c.parentOf(n) {
		for i := range cls.Fields {
			if f := &cls.Fields[i]; f.Initializer != nil && ast.Node(f.Initializer) == n {
				return f
			}
		}
		if n == ast.Node(cls) {
			break
		}
	}
	return nil
}

// isStaticContainer reports whether container, a member of cls, is static.
func (c *Checker) isStaticContainer(container ast.Node, cls *ast.ClassDeclaration, id ast.Node) bool {
	switch x := container.(type) {
	case *ast.FunctionDeclaration:
		return x.IsStatic
	case *ast.BlockStatement:
		return true
	case *ast.ClassDeclaration:
		if f := c.fieldOf(cls, id); f != nil {
			return f.Static
		}
	}
	return false
}

// missingPrefix is tsc's checkAndReportErrorForMissingPrefix: an unresolved
// name that is a static member of an enclosing class (`C.name`), or an
// instance member when the reference is in that class's own instance member
// (`this.name`). It returns the class's name and whether the member is
// static; "" when neither.
func (c *Checker) missingPrefix(id *ast.Identifier, name string) (string, bool) {
	container, cls := c.thisContainer(id)
	if container == nil {
		return "", false
	}
	// Each class the container, or a node around it, is a member of; the
	// container's own class first.
	loc := container
	if cls != nil {
		if c.classHas(cls, name, true, map[*ast.ClassDeclaration]bool{}) {
			return cls.Name, true
		}
		if !c.isStaticContainer(container, cls, id) && c.classHas(cls, name, false, map[*ast.ClassDeclaration]bool{}) {
			return cls.Name, false
		}
		loc = cls
	}
	for n := loc; n != nil; n = c.parentOf(n) {
		if p, ok := c.parentOf(n).(*ast.ClassDeclaration); ok && c.classHas(p, name, true, map[*ast.ClassDeclaration]bool{}) {
			return p.Name, true
		}
	}
	return "", false
}

// functionMembers and objectMembers are the members every constructor has
// through Function, and every object through Object (tsc's getPropertyOfType
// falls back to them).
var (
	functionMembers = map[string]bool{"apply": true, "call": true, "bind": true, "toString": true, "prototype": true, "length": true, "arguments": true, "caller": true, "name": true}
	objectMembers   = map[string]bool{"constructor": true, "toString": true, "toLocaleString": true, "valueOf": true, "hasOwnProperty": true, "isPrototypeOf": true, "propertyIsEnumerable": true}
)

// classHas reports whether cls, or a class it extends, has a static (or
// instance) member named name, the members its constructor type (or
// instance type) inherits from Function and Object included.
func (c *Checker) classHas(cls *ast.ClassDeclaration, name string, static bool, seen map[*ast.ClassDeclaration]bool) bool {
	if seen[cls] {
		return false
	}
	seen[cls] = true
	if static && functionMembers[name] || objectMembers[name] {
		return true
	}
	for _, f := range cls.Fields {
		if f.Name == name && f.Static == static {
			return true
		}
	}
	for _, m := range cls.Methods {
		if m.Name == name && m.IsStatic == static {
			return true
		}
	}
	for _, a := range cls.AutoAccessors {
		if a.Name == name && !static {
			return true
		}
	}
	if cls.BaseClass == "" {
		return false
	}
	sym := c.symbolOf(cls)
	if sym == nil {
		return false
	}
	base := lookupName(cls.BaseClass, sym.Scope)
	if base == nil {
		return false
	}
	for _, d := range base.Declarations {
		if bc, ok := d.Node.(*ast.ClassDeclaration); ok && c.classHas(bc, name, static, seen) {
			return true
		}
	}
	return false
}

// invalidInitializer is tsc's check of a name an instance field initializer
// reads that the class's constructor declares (a parameter, a body local):
// with the legacy class-field semantics (a target before ES2022) the
// initializer runs in the constructor, where the name would be captured, so
// tsc reports TS2301. It returns the field's name, or "".
func (c *Checker) invalidInitializer(id *ast.Identifier, name string) string {
	if !c.LegacyClassFields {
		return ""
	}
	container, cls := c.thisContainer(id)
	if cls == nil || container != ast.Node(cls) {
		return ""
	}
	f := c.fieldOf(cls, id)
	if f == nil || f.Static || cls.Constructor == nil {
		return ""
	}
	if scope := c.b.ScopeOf(cls.Constructor); scope != nil {
		if sym := scope.Symbols.Get(name); sym != nil && sym.Flags&binder.Value != 0 {
			return f.Name
		}
		if body := cls.Constructor.Body; body != nil {
			for _, st := range body.Body {
				if v, ok := st.(*ast.VarDeclaration); ok && v.Name == name {
					return f.Name
				}
			}
		}
	}
	return ""
}

// spellingSuggestion is the name tsc suggests for an unresolved value
// reference (getSuggestedSymbolForNonexistentSymbol): scope by scope from
// the reference outwards, the first table with a close enough value name
// gives it; the globals are one table, a script's top-level declarations
// among them.
func (c *Checker) spellingSuggestion(name string, scope *binder.Scope) string {
	var globals []string
	for s := scope; s != nil; s = s.Parent {
		var cands []string
		s.Symbols.Each(func(sym *binder.Symbol) {
			if sym.Flags&binder.Value != 0 && !strings.Contains(sym.Name, "__kml") &&
				(s != c.b.Globals || c.LibGlobal == nil || c.LibGlobal(sym.Name)) {
				cands = append(cands, sourceName(sym.Name))
			}
		})
		if s == c.b.Globals || s == c.b.Module && c.b.Script || s.Parent == nil {
			globals = append(globals, cands...)
			continue
		}
		if sug := spellingSuggestion(name, cands); sug != "" {
			return sug
		}
	}
	names, values := lib.GlobalNames()
	for i, n := range names {
		if values[i] && (c.LibGlobal == nil || c.LibGlobal(n)) {
			globals = append(globals, n)
		}
	}
	return spellingSuggestion(name, globals)
}

// spellingSuggestion is tsc's getSpellingSuggestion over candidate names:
// the closest by levenshteinWithMax, within 0.4 of name's length, a name of
// fewer than 3 characters only when it differs by case.
func spellingSuggestion(name string, cands []string) string {
	rn := []rune(name)
	maxLenDiff := max(2, int(float64(len(rn))*0.34))
	best := math.Floor(float64(len(rn))*0.4) + 0.9
	found := ""
	for _, cand := range cands {
		if cand == "" || cand == name || cand[0] == '"' {
			continue
		}
		if abs(len(cand)-len(rn)) > maxLenDiff {
			continue
		}
		if len(cand) < 3 && !strings.EqualFold(cand, name) {
			continue
		}
		d := levenshteinWithMax(rn, []rune(cand), best)
		if d < 0 {
			continue
		}
		if d < best || found == "" || cand < found {
			best, found = d, cand
		}
	}
	return found
}

// levenshteinWithMax is tsc's edit distance: a case change costs 0.1, a
// substitution 2, an insertion or deletion 1; -1 past maxValue.
func levenshteinWithMax(s1, s2 []rune, maxValue float64) float64 {
	previous := make([]float64, len(s2)+1)
	current := make([]float64, len(s2)+1)
	big := maxValue + 0.01
	for i := range previous {
		previous[i] = float64(i)
	}
	for i := 1; i <= len(s1); i++ {
		c1 := s1[i-1]
		minJ := max(int(math.Ceil(float64(i)-maxValue)), 1)
		maxJ := min(int(math.Floor(maxValue+float64(i))), len(s2))
		colMin := float64(i)
		current[0] = colMin
		for j := 1; j < minJ; j++ {
			current[j] = big
		}
		for j := minJ; j <= maxJ; j++ {
			sub := previous[j-1] + 2
			if unicode.ToLower(s1[i-1]) == unicode.ToLower(s2[j-1]) {
				sub = previous[j-1] + 0.1
			}
			dist := previous[j-1]
			if c1 != s2[j-1] {
				dist = math.Min(previous[j]+1, math.Min(current[j-1]+1, sub))
			}
			current[j] = dist
			colMin = math.Min(colMin, dist)
		}
		for j := maxJ + 1; j <= len(s2); j++ {
			current[j] = big
		}
		if colMin > maxValue {
			return -1
		}
		previous, current = current, previous
	}
	if res := previous[len(s2)]; res <= maxValue {
		return res
	}
	return -1
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
