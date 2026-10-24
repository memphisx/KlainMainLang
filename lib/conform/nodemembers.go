package conform

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	nodeTypeStart = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:declare\s+)?(?:abstract\s+)?(interface|class)\s+([A-Za-z_$][\w$]*)`)
	nodeMember    = regexp.MustCompile(`^\s*(?:(?:public|protected|private|readonly|abstract|declare|override|get|set|async)\s+)*([A-Za-z_$][\w$]*)\s*\??\s*[(<:]`)
	nodeStatic    = regexp.MustCompile(`^\s*(?:(?:public|protected|private|declare)\s+)*static\b`)
	nodeIndex     = regexp.MustCompile(`^\s*(?:readonly\s+)?\[\s*[A-Za-z_$][\w$]*\s*:`)
)

// nodeDecls reads every interface and class @types/node (typesNode)
// declares, by name: each declaration's instance member names and its
// `extends`/`implements` names (a qualified `NodeJS.EventEmitter` by its
// last part). Every declaration of a name is merged, whatever module or
// namespace holds it, so a member any of them has counts. The files do not
// all parse (ADR-01144), so the declarations are read from the text: a
// body's top-level lines (topLevelText), and a header's names. An index
// signature is recorded as indexMember.
func nodeDecls(typesNode string) (members, bases map[string]map[string]bool, err error) {
	members, bases = map[string]map[string]bool{}, map[string]map[string]bool{}
	err = filepath.Walk(typesNode, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".d.ts") {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		src := string(data)
		for _, m := range nodeTypeStart.FindAllStringSubmatchIndex(src, -1) {
			name := src[m[4]:m[5]]
			open := headerEnd(src, m[1])
			if open < 0 {
				continue
			}
			end := matchingBrace(src, open)
			if end < 0 {
				continue
			}
			if members[name] == nil {
				members[name] = map[string]bool{}
				bases[name] = map[string]bool{}
			}
			for _, b := range headerBases(src[m[1]:open]) {
				bases[name][b] = true
			}
			for _, line := range strings.Split(topLevelText(src[open+1:end]), "\n") {
				if nodeStatic.MatchString(line) {
					continue
				}
				if nodeIndex.MatchString(line) {
					members[name][indexMember] = true
				} else if mm := nodeMember.FindStringSubmatch(line); mm != nil {
					members[name][mm[1]] = true
				}
			}
		}
		return nil
	})
	return members, bases, err
}

// indexMember marks a declaration with an index signature: any name is
// one of its members.
const indexMember = "[index]"

// headerEnd is the offset of the `{` opening the body of the declaration
// whose header starts at i: the first one outside its type parameters
// (`<T = {}>`), or -1 at a `;` or the end.
func headerEnd(src string, i int) int {
	depth := 0
	for ; i < len(src); i++ {
		switch src[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 && src[i-1] != '=' {
				depth--
			}
		case '{':
			if depth == 0 {
				return i
			}
			if end := matchingBrace(src, i); end > 0 {
				i = end
			}
		case ';':
			if depth == 0 {
				return -1
			}
		}
	}
	return -1
}

// headerBases is the names a declaration header's `extends` and
// `implements` clauses list, type arguments dropped and a qualified name
// by its last part.
func headerBases(h string) []string {
	var b strings.Builder
	depth := 0
	for _, r := range h {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	text := b.String()
	k := strings.Index(text, " extends ")
	if k < 0 {
		k = strings.Index(text, " implements ")
		if k < 0 {
			return nil
		}
	}
	var out []string
	for _, f := range strings.FieldsFunc(text[k:], func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '\r' }) {
		if f == "extends" || f == "implements" {
			continue
		}
		if i := strings.LastIndex(f, "."); i >= 0 {
			f = f[i+1:]
		}
		out = append(out, f)
	}
	return out
}
