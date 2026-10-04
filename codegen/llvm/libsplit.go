package llvm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// libsplit.go — a program's IR split into one unit per builtin library
// module and the program's own unit (TDD-00238 Stage 4). Library code
// compiles to the same IR in every program (ADR-01326), so a module's unit
// is compiled once and cached by its text. Ownership is read from a
// definition's name: a library function, module global or literal carries
// its module's key. A definition named from another unit gets hidden
// linkage, so it stays one instance in its owner's unit; a linkonce_odr or
// weak_odr definition is copied into each unit that names it, and the
// linker keeps one.

// defineKw is the IR keyword the splitter parses (spelled so the IR-text
// ratchet does not count it as a definition).
const defineKw = "def" + "ine "

// irEntity is one top-level IR definition or declaration.
type irEntity struct {
	name  string // the @symbol, "" for types, attributes and metadata
	kind  byte   // 'f' function, 'g' global, 'd' declare, 't' type, 'o' other
	text  string
	owner string // library module key, "" for the program
}

var (
	irSymRe     = regexp.MustCompile(`@([-a-zA-Z$._0-9]+|"[^"]*")`)
	libModKeyRe = regexp.MustCompile(`__kml_modL(\d+)`)
	libInitRe   = regexp.MustCompile(`^__kml_lib_(.+)_(init|done)$`)
	// dynTargetRe: a lazy import() target's init, body, start and fulfill;
	// dynFileRe: a dynamic-only file's run-once flag (emit_dynmodule.go).
	dynTargetRe = regexp.MustCompile(`^__kml_dyn_([0-9a-f]{16})_`)
	dynFileRe   = regexp.MustCompile(`^__kml_dynfile_([0-9a-f]{16})_`)
)

// IslandTargetKey is the unit key of the import() target with hash h: its
// init, which always goes into the target's own shared library. A file's
// unit is `isl_<h>` (ast.DynModuleKey).
func IslandTargetKey(h string) string { return "isl_t_" + h }

// islandOwner reports whether owner is a lazy import() island unit.
func islandOwner(owner string) bool { return strings.HasPrefix(owner, "isl_") }

// libOwner is the library module that owns symbol name, "" for the program.
// A name embeds its module as __kml_modL<n><key> (the key's length, then
// the key); a module init is __kml_lib_<key>_init. What the emitter
// generates about a library declaration (a class-reference thunk, a record
// view, a function-value wrapper) depends on what the program uses, so it
// belongs to the program: of the __ names, only module globals and inits
// are a module's. That includes a library class's shape method adapters,
// emitted only for the methods the program reaches dynamically.
func libOwner(name string) string {
	if m := libInitRe.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	if m := dynTargetRe.FindStringSubmatch(name); m != nil {
		return IslandTargetKey(m[1])
	}
	if m := dynFileRe.FindStringSubmatch(name); m != nil {
		return "isl_" + m[1]
	}
	if strings.HasPrefix(name, "__") && !strings.HasPrefix(name, "__kml_global_") {
		return ""
	}
	loc := libModKeyRe.FindStringSubmatchIndex(name)
	if loc == nil {
		return ""
	}
	n := 0
	fmt.Sscanf(name[loc[2]:loc[3]], "%d", &n)
	start := loc[1]
	if n <= 0 || start+n > len(name) {
		return ""
	}
	return name[start : start+n]
}

// parseIREntities splits module text into top-level entities.
func parseIREntities(ll string) []irEntity {
	var out []irEntity
	lines := strings.Split(ll, "\n")
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		switch {
		case strings.HasPrefix(ln, defineKw):
			j := i
			for j < len(lines) && lines[j] != "}" {
				j++
			}
			out = append(out, irEntity{name: firstSym(ln), kind: 'f', text: strings.Join(lines[i:j+1], "\n")})
			i = j
		case strings.HasPrefix(ln, "declare "):
			out = append(out, irEntity{name: firstSym(ln), kind: 'd', text: ln})
		case strings.HasPrefix(ln, "@") && strings.Contains(ln, "= external "):
			out = append(out, irEntity{name: firstSym(ln), kind: 'd', text: ln})
		case strings.HasPrefix(ln, "@"):
			out = append(out, irEntity{name: firstSym(ln), kind: 'g', text: ln})
		case strings.HasPrefix(ln, "%") && strings.Contains(ln, "= type"):
			out = append(out, irEntity{kind: 't', text: ln})
		case strings.TrimSpace(ln) == "" || strings.HasPrefix(ln, ";"):
		default:
			out = append(out, irEntity{kind: 'o', text: ln})
		}
	}
	return out
}

func firstSym(ln string) string {
	if m := irSymRe.FindStringSubmatch(ln); m != nil {
		return m[1]
	}
	return ""
}

// irLinkage is a definition's linkage keyword ("" for external).
func irLinkage(e irEntity) string {
	rest := e.text
	if e.kind == 'f' {
		rest = strings.TrimPrefix(rest, defineKw)
	} else if i := strings.Index(rest, "= "); i >= 0 {
		rest = rest[i+2:]
	}
	for _, l := range []string{"private", "internal", "linkonce_odr", "linkonce", "weak_odr", "weak", "external", "available_externally"} {
		if strings.HasPrefix(rest, l+" ") {
			return l
		}
	}
	return ""
}

// setReachable gives an internal or private definition external linkage
// with visibility vis ("hidden " within one executable, "" when a shared
// library binds to it).
func setReachable(e irEntity, vis string) irEntity {
	switch irLinkage(e) {
	case "internal", "private":
	default:
		return e
	}
	l := irLinkage(e)
	if e.kind == 'f' {
		e.text = strings.Replace(e.text, defineKw+l+" ", defineKw+vis, 1)
	} else {
		e.text = strings.Replace(e.text, "= "+l+" ", "= "+vis, 1)
	}
	return e
}

// declOf is the declaration another unit uses for definition e.
func declOf(e irEntity) (string, error) { return declOfVis(e, "hidden ") }

func declOfVis(e irEntity, vis string) (string, error) {
	if e.kind == 'g' {
		// @x = [linkage] [thread_local] [unnamed_addr] global|constant <ty> <init>, align N
		rhs := e.text[strings.Index(e.text, "= ")+2:]
		tl := ""
		if strings.Contains(rhs, "thread_local") {
			tl = "thread_local "
		}
		var ty string
		for _, kw := range []string{"global ", "constant "} {
			if i := strings.Index(rhs, kw); i >= 0 {
				ty = irLeadingType(rhs[i+len(kw):])
				break
			}
		}
		if ty == "" {
			return "", fmt.Errorf("libsplit: cannot type global @%s", e.name)
		}
		return fmt.Sprintf("@%s = external %s%sglobal %s", e.name, vis, tl, ty), nil
	}
	// define [linkage] [attrs] <ret> @name(<params>) [attrs] {
	head := strings.SplitN(e.text, "\n", 2)[0]
	at := strings.Index(head, "@"+e.name+"(")
	if at < 0 {
		return "", fmt.Errorf("libsplit: cannot parse @%s", e.name)
	}
	pre := strings.Fields(strings.TrimPrefix(head[:at], defineKw))
	for len(pre) > 0 && irDefineKeyword(pre[0]) {
		pre = pre[1:]
	}
	ret := strings.Join(pre, " ")
	params := irParamList(head[at+len(e.name)+2:])
	return fmt.Sprintf("declare %s @%s(%s)", ret, e.name, params), nil
}

func irDefineKeyword(w string) bool {
	switch w {
	case "private", "internal", "linkonce_odr", "linkonce", "weak_odr", "weak", "external",
		"hidden", "protected", "default", "dso_local", "fastcc", "ccc", "noundef", "zeroext", "signext", "inreg":
		return true
	}
	return false
}

// irLeadingType is the IR type at the start of s ({...}, [...], or a word).
func irLeadingType(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s[0] == '{' || s[0] == '[' || s[0] == '<' {
		depth := 0
		for i, c := range s {
			switch c {
			case '{', '[', '<':
				depth++
			case '}', ']', '>':
				depth--
				if depth == 0 {
					return s[:i+1]
				}
			}
		}
		return ""
	}
	return strings.TrimSuffix(strings.Fields(s)[0], ",")
}

// irParamList is a define's parameter types, without names: "(ptr %a, i64
// %b) …" -> "ptr, i64".
func irParamList(s string) string {
	depth, end := 1, -1
	for i, c := range s {
		switch c {
		case '(', '{', '[', '<':
			depth++
		case ')', '}', ']', '>':
			depth--
			if depth == 0 && c == ')' {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return ""
	}
	var out []string
	for _, p := range splitTopLevel(s[:end], ',') {
		p = strings.TrimSpace(p)
		if p == "..." {
			out = append(out, p)
			continue
		}
		if i := strings.LastIndex(p, " %"); i >= 0 {
			p = p[:i]
		}
		out = append(out, p)
	}
	return strings.Join(out, ", ")
}

func splitTopLevel(s string, sep rune) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(', '{', '[', '<':
			depth++
		case ')', '}', ']', '>':
			depth--
		case sep:
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	if strings.TrimSpace(s[start:]) != "" {
		out = append(out, s[start:])
	}
	return out
}

// LibUnits is a program split for separate compilation.
type LibUnits struct {
	Program string            // the program's own unit
	Modules map[string]string // library module key -> its unit
}

// SplitLibUnits splits program IR ll into the program's unit and one unit
// per library module.
func SplitLibUnits(ll string) (LibUnits, error) { return splitUnits(ll, nil) }

// SplitIslandUnits is SplitLibUnits for a -dynamic-import=lazy program
// (TDD-00238 Stage 5). place maps a dynamic-only file's key (or a target's
// init key) to the hash of the import() target whose shared library holds
// it, "" for the executable; each target's definitions form one unit,
// `isl_t_<hash>`, which binds to the executable's symbols. A program
// definition only one island's code reaches moves into that island; a
// definition the executable reaches may not name island code, or the split
// fails.
func SplitIslandUnits(ll string, place func(key string) string) (LibUnits, error) {
	return splitUnits(ll, place)
}

func splitUnits(ll string, place func(key string) string) (LibUnits, error) {
	islands := place != nil
	vis := "hidden "
	if islands {
		vis = "" // a shared library binds to the executable's definitions
	}
	ents := parseIREntities(ll)
	defs := map[string]int{}
	for i := range ents {
		e := &ents[i]
		if e.kind == 'f' || e.kind == 'g' {
			if l := irLinkage(*e); l == "linkonce_odr" || l == "weak_odr" {
				// shared: every unit that names it carries a copy
				defs[e.name] = i
				continue
			}
			// A library definition is hidden in every program, so its unit's
			// text does not depend on what the program names.
			e.owner = libOwner(e.name)
			if islandOwner(e.owner) {
				// One unit per target's shared library, or the program's.
				e.owner = ""
				if islands {
					if h := place(libOwner(e.name)); h != "" {
						e.owner = IslandTargetKey(h)
					}
				}
			}
			if e.owner != "" {
				*e = setReachable(*e, vis)
			}
			defs[e.name] = i
		}
	}
	if islands {
		if err := moveIslandHelpers(ents, defs); err != nil {
			return LibUnits{}, err
		}
	}
	var header, types []string
	decls := map[string]string{}
	for _, e := range ents {
		switch e.kind {
		case 'o':
			if strings.HasPrefix(e.text, "target ") || strings.HasPrefix(e.text, "source_filename") {
				header = append(header, e.text)
			}
		case 't':
			types = append(types, e.text)
		case 'd':
			decls[e.name] = e.text
		}
	}
	units := map[string][]int{} // owner -> entity indexes
	for i, e := range ents {
		if l := irLinkage(e); (e.kind == 'f' || e.kind == 'g') && l != "linkonce_odr" && l != "weak_odr" {
			units[e.owner] = append(units[e.owner], i)
		}
	}
	// A module's unit lists its definitions by name: the order they were
	// emitted in depends on the program.
	for owner, idxs := range units {
		if owner != "" {
			sort.Slice(idxs, func(a, b int) bool { return ents[idxs[a]].name < ents[idxs[b]].name })
		}
	}
	var others []string // attributes, metadata: the program keeps them
	for _, e := range ents {
		if e.kind == 'o' && !strings.HasPrefix(e.text, "target ") && !strings.HasPrefix(e.text, "source_filename") {
			others = append(others, e.text)
		}
	}
	// copyable: carried by each unit that names it.
	copyable := func(de irEntity) bool {
		l := irLinkage(de)
		// A private constant is not copied: its address can be an identity
		// (a function value's header), so it stays one instance.
		return l == "linkonce_odr" || l == "weak_odr"
	}
	// Each unit's closure over its references: definitions copied in, and
	// the names it reaches in other units.
	type closure struct {
		body, copied []string
		reach        []string
	}
	closures := map[string]*closure{}
	for owner, idxs := range units {
		c := &closure{}
		have := map[string]bool{}
		for _, i := range idxs {
			have[ents[i].name] = true
			c.body = append(c.body, ents[i].text)
		}
		seen := map[string]bool{}
		queue := append([]string{}, c.body...)
		if owner == "" {
			// C objects may name a weak_odr helper the program's own IR does
			// not: the program's unit always carries them.
			for _, e := range ents {
				if irLinkage(e) == "weak_odr" && !seen[e.name] {
					seen[e.name] = true
					c.copied = append(c.copied, e.text)
					queue = append(queue, e.text)
				}
			}
		}
		for len(queue) > 0 {
			txt := queue[0]
			queue = queue[1:]
			for _, m := range irSymRe.FindAllStringSubmatch(txt, -1) {
				sym := m[1]
				if have[sym] || seen[sym] {
					continue
				}
				seen[sym] = true
				if _, ok := decls[sym]; ok {
					c.reach = append(c.reach, sym)
					continue
				}
				di, ok := defs[sym]
				if !ok {
					continue // an intrinsic, or a name inside a string
				}
				if copyable(ents[di]) {
					c.copied = append(c.copied, ents[di].text)
					queue = append(queue, ents[di].text)
					continue
				}
				c.reach = append(c.reach, sym)
			}
		}
		closures[owner] = c
	}
	// A definition reached from another unit becomes hidden (visible
	// under islands).
	for owner, c := range closures {
		for _, sym := range c.reach {
			di, ok := defs[sym]
			if !ok {
				continue
			}
			if islands && !islandOwner(owner) && islandOwner(ents[di].owner) {
				return LibUnits{}, fmt.Errorf("libsplit: %s names @%s of island %s: the executable may reach an island only through dlsym", unitName(owner), sym, ents[di].owner)
			}
			ents[di] = setReachable(ents[di], vis)
		}
	}
	out := LibUnits{Modules: map[string]string{}}
	for owner, c := range closures {
		var b strings.Builder
		for _, h := range header {
			b.WriteString(h + "\n")
		}
		for _, t := range types {
			b.WriteString(t + "\n")
		}
		var declared []string
		for _, sym := range c.reach {
			if d, ok := decls[sym]; ok {
				declared = append(declared, d)
				continue
			}
			d, err := declOfVis(ents[defs[sym]], vis)
			if err != nil {
				return LibUnits{}, err
			}
			declared = append(declared, d)
		}
		sort.Strings(declared)
		sort.Strings(c.copied)
		for _, d := range declared {
			b.WriteString(d + "\n")
		}
		for _, t := range c.copied {
			b.WriteString(t + "\n")
		}
		for _, i := range units[owner] {
			b.WriteString(ents[i].text + "\n")
		}
		if owner == "" {
			for _, o := range others {
				b.WriteString(o + "\n")
			}
			out.Program = b.String()
		} else {
			out.Modules[owner] = b.String()
		}
	}
	return out, nil
}

// moveIslandHelpers gives each program definition only one island's code
// reaches (a closure, a generated helper) to that island.
func moveIslandHelpers(ents []irEntity, defs map[string]int) error {
	refs := func(i int) []int {
		var out []int
		for _, m := range irSymRe.FindAllStringSubmatch(ents[i].text, -1) {
			if di, ok := defs[m[1]]; ok && di != i {
				out = append(out, di)
			}
		}
		return out
	}
	program := func(i int) bool {
		l := irLinkage(ents[i])
		return ents[i].owner == "" && l != "linkonce_odr" && l != "weak_odr"
	}
	// walk marks the program definitions reachable from seeds through
	// program definitions.
	walk := func(seeds []int) map[int]bool {
		seen := map[int]bool{}
		queue := seeds
		for len(queue) > 0 {
			i := queue[0]
			queue = queue[1:]
			for _, d := range refs(i) {
				if !seen[d] && program(d) {
					seen[d] = true
					queue = append(queue, d)
				}
			}
		}
		return seen
	}
	islandSeeds := map[string][]int{}
	for i, e := range ents {
		if (e.kind == 'f' || e.kind == 'g') && islandOwner(e.owner) {
			islandSeeds[e.owner] = append(islandSeeds[e.owner], i)
		}
	}
	if len(islandSeeds) == 0 {
		return nil
	}
	reachedBy := map[int][]string{}
	for owner, seeds := range islandSeeds {
		for i := range walk(seeds) {
			reachedBy[i] = append(reachedBy[i], owner)
		}
	}
	// The executable's roots: the library, and every program definition no
	// island reaches (main, constructors, what C names).
	var hostSeeds []int
	for i, e := range ents {
		if e.kind != 'f' && e.kind != 'g' {
			continue
		}
		if (e.owner != "" && !islandOwner(e.owner)) || (program(i) && len(reachedBy[i]) == 0) {
			hostSeeds = append(hostSeeds, i)
		}
	}
	host := walk(hostSeeds)
	for i, owners := range reachedBy {
		if host[i] || len(owners) != 1 {
			continue
		}
		ents[i].owner = owners[0]
		ents[i] = setReachable(ents[i], "")
	}
	return nil
}

func unitName(owner string) string {
	if owner == "" {
		return "program"
	}
	return owner
}

// LibObjects splits the program IR in llFile, compiles each library
// module's unit once into cacheDir (keyed by its text, the compile flags and
// the host clang arguments), rewrites llFile to the program's own unit, and
// returns the module objects to link. hits counts the units already cached.
func LibObjects(llFile, cacheDir string, compileFlags []string) (objs []string, hits int, err error) {
	src, err := os.ReadFile(llFile)
	if err != nil {
		return nil, 0, err
	}
	u, err := SplitLibUnits(string(src))
	if err != nil {
		return nil, 0, err
	}
	for _, k := range sortedKeys(u.Modules) {
		obj, hit, err := cachedUnitObject(cacheDir, k, u.Modules[k], compileFlags)
		if err != nil {
			return nil, 0, err
		}
		if hit {
			hits++
		}
		objs = append(objs, obj)
	}
	return objs, hits, os.WriteFile(llFile, []byte(u.Program), 0o644)
}

// IslandObjects is LibObjects for a -dynamic-import=lazy program
// (SplitIslandUnits). place maps an island unit's key to the hash of the
// import() target whose shared library holds it, "" for the executable.
// It returns the executable's unit objects and each target's, the latter
// compiled with islandFlags (position-independent).
func IslandObjects(llFile, cacheDir string, compileFlags, islandFlags []string, place func(key string) string) (host []string, islands map[string][]string, err error) {
	src, err := os.ReadFile(llFile)
	if err != nil {
		return nil, nil, err
	}
	u, err := SplitIslandUnits(string(src), place)
	if err != nil {
		return nil, nil, err
	}
	islands = map[string][]string{}
	for _, k := range sortedKeys(u.Modules) {
		target := strings.TrimPrefix(k, IslandTargetKey(""))
		if target == k {
			target = ""
		}
		flags := compileFlags
		if target != "" {
			flags = islandFlags
		}
		obj, _, err := cachedUnitObject(cacheDir, k, u.Modules[k], flags)
		if err != nil {
			return nil, nil, err
		}
		if target == "" {
			host = append(host, obj)
		} else {
			islands[target] = append(islands[target], obj)
		}
	}
	return host, islands, os.WriteFile(llFile, []byte(u.Program), 0o644)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cachedUnitObject compiles unit text (key k) to an object in cacheDir,
// keyed by its text, the flags and the host clang arguments; hit reports
// it was already there.
func cachedUnitObject(cacheDir, k, text string, compileFlags []string) (obj string, hit bool, err error) {
	h := sha256.New()
	for _, s := range append(append([]string{text, runtime.GOOS}, compileFlags...), HostClangArgs()...) {
		fmt.Fprintf(h, "%d:%s", len(s), s)
	}
	obj = filepath.Join(cacheDir, "lib-"+k+"-"+hex.EncodeToString(h.Sum(nil)[:10])+".o")
	if st, serr := os.Stat(obj); serr == nil && st.Size() > 0 {
		return obj, true, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", false, err
	}
	stem := fmt.Sprintf("%s.%d", strings.TrimSuffix(obj, ".o"), os.Getpid())
	if err := os.WriteFile(stem+".ll", []byte(text), 0o644); err != nil {
		return "", false, err
	}
	args := append(append([]string{}, compileFlags...), "-Wno-override-module", "-c", stem+".ll", "-o", stem+".o.tmp")
	out, cerr := ClangCommand(args...).CombinedOutput()
	os.Remove(stem + ".ll")
	if cerr != nil {
		os.Remove(stem + ".o.tmp")
		return "", false, fmt.Errorf("compiling library module %s: %v\n%s", k, cerr, out)
	}
	if err := os.Rename(stem+".o.tmp", obj); err != nil {
		return "", false, err
	}
	return obj, false, nil
}
