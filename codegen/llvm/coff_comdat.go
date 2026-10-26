package llvm

import (
	"regexp"
	"strings"
)

// coffWeakFnRe matches a weak or linkonce function definition's header line,
// coffWeakVarRe such a global's line; group 1 is the symbol.
var (
	coffWeakFnRe  = regexp.MustCompile(`^define (?:weak_odr|weak|linkonce_odr|linkonce) [^@]*@([A-Za-z0-9_.$]+)\(.*\{$`)
	coffWeakVarRe = regexp.MustCompile(`^@([A-Za-z0-9_.$]+) = (?:weak_odr|weak|linkonce_odr|linkonce) `)
	// coffAlignTailRe is a global's trailing alignment (a string constant's
	// own text may contain ", align ").
	coffAlignTailRe = regexp.MustCompile(`, align \d+$`)
)

// coffWeakComdats gives every weak or linkonce definition in a Windows module
// its own comdat. Without one, LLVM lowers a mingw weak/linkonce definition to
// a COFF weak external, which GNU ld handles badly: another object's
// reference to it stays unresolved (the C runtime objects name helpers such as
// __kml_nb_tag and __kml_str_alloc), and the copies libsplit puts into each
// unit collide as multiple definitions of their `.weak.` aliases. With a
// comdat the definition is an ordinary external symbol in a select-any
// section, so the copies fold to one, as these linkages do elsewhere.
//
// It acts only on a module whose target triple is Windows and is idempotent,
// so every producer of module text can run it.
func coffWeakComdats(ll string) string {
	// Only a Windows target needs it: look for the triple before splitting.
	if i := strings.Index(ll, "\ntarget triple"); i < 0 && !strings.HasPrefix(ll, "target triple") ||
		!strings.Contains(firstLineAt(ll, i+1), "windows") {
		return ll
	}
	lines := strings.Split(ll, "\n")
	triple := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "target triple") {
			triple = i
			break
		}
	}
	if triple < 0 || !strings.Contains(lines[triple], "windows") {
		return ll
	}
	declared := map[string]bool{}
	for _, ln := range lines {
		if name, ok := strings.CutSuffix(ln, " = comdat any"); ok && strings.HasPrefix(name, "$") {
			declared[name[1:]] = true
		}
	}
	// A definition libsplit copied into a unit may carry its comdat without
	// the declaration, which stayed in the program's unit.
	var decls []string
	for i, ln := range lines {
		var name string
		if m := coffWeakFnRe.FindStringSubmatch(ln); m != nil {
			name = m[1]
			if !strings.HasSuffix(ln, " comdat {") {
				lines[i] = strings.TrimSuffix(ln, "{") + "comdat {"
			}
		} else if m := coffWeakVarRe.FindStringSubmatch(ln); m != nil {
			if strings.Contains(ln, " = weak global ptr null") {
				// A hook (a weak null pointer a C runtime file may define as
				// its routine): in a comdat it would be a strong definition
				// colliding with that file's.
				continue
			}
			name = m[1]
			if !strings.Contains(ln, ", comdat") {
				lines[i] = withVarComdat(ln)
			}
		} else {
			continue
		}
		if !declared[name] {
			declared[name] = true
			decls = append(decls, "$"+name+" = comdat any")
		}
	}
	if len(decls) == 0 {
		return ll
	}
	// A comdat is declared after the module's target lines.
	out := append([]string{}, lines[:triple+1]...)
	out = append(out, decls...)
	return strings.Join(append(out, lines[triple+1:]...), "\n")
}

// withVarComdat adds `, comdat` to a global's line: before its alignment,
// which the grammar puts last.
func withVarComdat(ln string) string {
	if loc := coffAlignTailRe.FindStringIndex(ln); loc != nil {
		return ln[:loc[0]] + ", comdat" + ln[loc[0]:]
	}
	return ln + ", comdat"
}

// firstLineAt is the line of s starting at i (all of s from 0 when i is 0).
func firstLineAt(s string, i int) string {
	s = s[i:]
	if j := strings.IndexByte(s, '\n'); j >= 0 {
		return s[:j]
	}
	return s
}
