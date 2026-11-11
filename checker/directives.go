package checker

import (
	"regexp"
	"sort"
	"strings"

	"KlainMainLang/diag"
)

// UnusedExpectError is tsc's report of a `// @ts-expect-error` no error
// follows.
var UnusedExpectError = &diag.Message{Code: 2578, Kind: diag.TypeScriptError, Phase: diag.PhaseCheck, Text: "Unused '@ts-expect-error' directive."}

var (
	lineDirective  = regexp.MustCompile(`^///?\s*@(ts-expect-error|ts-ignore)`)
	blockDirective = regexp.MustCompile(`^/\*[/*]*\s*@(ts-expect-error|ts-ignore)`)
)

type commentDirective struct {
	expect bool
	used   bool
	col    int
}

// ApplyCommentDirectives drops the type errors a `// @ts-ignore` or `//
// @ts-expect-error` comment covers, as tsc does: the directive covers the
// next line that is neither blank nor a `//` comment. An expect-error that
// covers no error is itself one (TS2578). srcOf is a file's source text
// ("" for one whose comments are not the program's own); paths are the
// program's files.
func ApplyCommentDirectives(ds []*diag.Diagnostic, paths []string, srcOf func(string) string) []*diag.Diagnostic {
	type fileDirs struct {
		lines []string
		dirs  map[int]*commentDirective // by 0-based line
	}
	files := map[string]*fileDirs{}
	get := func(path string) *fileDirs {
		if f, ok := files[path]; ok {
			return f
		}
		f := &fileDirs{dirs: map[int]*commentDirective{}}
		if src := srcOf(path); src != "" {
			f.lines = strings.Split(src, "\n")
			for i, l := range f.lines {
				t := strings.TrimSpace(l)
				m := lineDirective.FindStringSubmatch(t)
				if m == nil {
					if bm := blockDirective.FindStringSubmatch(t); bm != nil && strings.Contains(t, "*/") {
						m = bm
					}
				}
				if m != nil {
					f.dirs[i] = &commentDirective{expect: m[1] == "ts-expect-error", col: len(l) - len(strings.TrimLeft(l, " \t")) + 1}
				}
			}
		}
		files[path] = f
		return f
	}
	for _, p := range paths {
		get(p)
	}
	var out []*diag.Diagnostic
	for _, d := range ds {
		f := get(d.File)
		covered := false
		for line := d.Pos.Line - 2; line >= 0 && line < len(f.lines); line-- {
			if dir := f.dirs[line]; dir != nil {
				dir.used, covered = true, true
				break
			}
			if t := strings.TrimSpace(f.lines[line]); t != "" && !strings.HasPrefix(t, "//") {
				break
			}
		}
		if !covered {
			out = append(out, d)
		}
	}
	all := make([]string, 0, len(files))
	for p := range files {
		all = append(all, p)
	}
	sort.Strings(all)
	for _, p := range all {
		f := files[p]
		lines := make([]int, 0, len(f.dirs))
		for l := range f.dirs {
			lines = append(lines, l)
		}
		sort.Ints(lines)
		for _, l := range lines {
			if dir := f.dirs[l]; dir.expect && !dir.used {
				d := diag.New(UnusedExpectError, diag.Span{File: p, Pos: diag.Pos{Line: l + 1, Col: dir.col}})
				d.Hint = strictHint
				out = append(out, d)
			}
		}
	}
	return out
}
