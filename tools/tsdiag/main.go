// Command tsdiag measures the checker's diagnostics against TypeScript's own
// (TDD-00230 P2.7). It runs the checker over each single-file case of
// TypeScript's test corpus (tools/conformance/fetch.sh puts it in
// .ts-tests) and compares every diagnostic with the case's errors baseline
// by code and line. A diagnostic the baseline lacks is a false positive; the
// report lists each by code, so a checker error is surfaced to users only
// once its code's column is clean.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/diag"
	"KlainMainLang/lib"
	"KlainMainLang/lib/conform"
	"KlainMainLang/parser"
	"KlainMainLang/resolver"
)

var (
	reDirective = regexp.MustCompile(`(?mi)^//\s*@(\w+)\s*:\s*(\S+)`)
	reBaseline  = regexp.MustCompile(`^([^(\s]+)\((\d+),(\d+)\): error TS(\d+):`)
	reOption    = regexp.MustCompile(`^\s*//\s*@\w+\s*:`)
	reModule    = regexp.MustCompile(`(?m)^\s*(import|export)\b`)
)

// strictnessFree are the codes whose rules do not depend on strictNullChecks:
// a non-strict case is compared on them only.
var strictnessFree = map[int]bool{2339: true, 2362: true, 2363: true, 2365: true, 2367: true, 2554: true, 2555: true, 2353: true, 2588: true, 2628: true, 2629: true, 2630: true, 2739: true, 2740: true, 2741: true}

// baselineLines maps each source line to the line TypeScript's harness
// reports it at: the harness drops the `// @option` lines, then the blank
// lines left at the top. A dropped line maps to 0.
func baselineLines(src string) []int {
	lines := strings.Split(strings.TrimPrefix(src, "\ufeff"), "\n")
	out := make([]int, len(lines)+2)
	n, started := 0, false
	for i, l := range lines {
		if reOption.MatchString(l) || !started && strings.TrimSpace(l) == "" {
			continue
		}
		started = true
		n++
		out[i+1] = n
	}
	return out
}

type key struct {
	line, code int
}

type finding struct {
	file      string
	line, col int
	code      int
	text      string
	dirs      map[string]string // the case's directives
}

// checkOwn checks each .ts file under dirs as an entry, with its imports
// resolved, as the compiler does (a .js file is the -compat=js lane, left
// out: its checker mode reports nothing yet).
func checkOwn(dirs []string) []finding {
	var out []finding
	for _, dir := range dirs {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".d.ts") {
				return nil
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						out = append(out, finding{path, 0, 0, 0, fmt.Sprint("panic: ", r), nil})
					}
				}()
				// The strict lane's resolver checks the program itself: its
				// type errors come back as the error.
				_, err := resolver.ResolveProgram(path)
				for _, d := range diag.As(err) {
					if d.Message.Kind == diag.TypeScriptError {
						out = append(out, finding{d.File + " (from " + path + ")", d.Pos.Line, d.Pos.Col, d.Code(), d.Text, nil})
					}
				}
			}()
			return nil
		})
	}
	return out
}

// harnessOnly are the directives the test harness reads itself, not tsc.
var harnessOnly = map[string]bool{"filename": true, "noimplicitreferences": true, "currentdirectory": true, "includebuiltfile": true, "libfiles": true, "fullemitpaths": true, "reportdiagnostics": true, "capturesuggestions": true, "typescriptversion": true, "notypesandsymbols": true, "symlink": true, "link": true, "baselinefile": true, "noerrortruncation": true}

var reTscError = regexp.MustCompile(`^[^(]+\((\d+),\d+\): error TS(\d+):`)

// againstTsc splits fps into the ones tsc also reports, run on the case
// with its directives as options, and the rest.
func againstTsc(tsc string, fps []finding) (rest, stale []finding) {
	dir, err := os.MkdirTemp("", "tsdiag")
	if err != nil {
		return fps, nil
	}
	defer os.RemoveAll(dir)
	byFile := map[string]map[key]bool{}
	for _, f := range fps {
		if _, done := byFile[f.file]; !done {
			byFile[f.file] = tscErrors(tsc, dir, f.file, f.dirs)
		}
		if got := byFile[f.file]; got != nil && got[key{f.line, f.code}] {
			stale = append(stale, f)
		} else {
			rest = append(rest, f)
		}
	}
	return rest, stale
}

// tscErrors is the (line, code) of each error tsc reports for the case at
// path, or nil when it cannot be run.
func tscErrors(tsc, dir, path string, dirs map[string]string) map[key]bool {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	file := filepath.Join(dir, filepath.Base(path))
	if err := os.WriteFile(file, src, 0o644); err != nil {
		return nil
	}
	defer os.Remove(file)
	args := []string{"--noEmit", "--pretty", "false"}
	for k, v := range dirs {
		if !harnessOnly[k] {
			args = append(args, "--"+k, v)
		}
	}
	cmd := exec.Command(tsc, append(args, file)...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	got := map[key]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if m := reTscError.FindStringSubmatch(l); m != nil {
			line, _ := strconv.Atoi(m[1])
			code, _ := strconv.Atoi(m[2])
			got[key{line, code}] = true
		} else if strings.Contains(l, "error TS5") {
			return nil // an option tsc rejects: not comparable
		}
	}
	return got
}

var libCache = map[string]map[string]bool{}

// libGlobals is the global values a case's library declares: its lib
// directive's files, or its target's default library (TypeScript's
// getDefaultLibFileName); nil when that library cannot be read.
func libGlobals(dirs map[string]string) func(string) bool {
	libs := dirs["lib"]
	if libs == "" {
		switch t := dirs["target"]; t {
		case "", "latest":
			libs = "es2025.full"
		case "es3", "es5":
			libs = "es5.full"
		case "es6", "es2015":
			libs = "es2015.full"
		default:
			libs = t + ".full"
		}
	}
	names, ok := libCache[libs]
	if !ok {
		names, _ = conform.LibValueNames(".ts-tests/src/lib", strings.Split(libs, ","))
		libCache[libs] = names
	}
	if names == nil {
		return nil
	}
	return func(n string) bool { return names[n] }
}

func codesOf(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func main() {
	root := flag.String("root", ".ts-tests/tests", "the TypeScript corpus's tests directory")
	out := flag.String("out", "", "write the report here (default stdout)")
	examples := flag.Int("examples", 8, "false positives listed per code")
	roots := flag.String("roots", "", "also check every .ts entry under these comma-separated directories (this project's own programs, which compile: every diagnostic there is listed)")
	tscPath := flag.String("tsc", "", "re-check each false positive's case with this tsc (the oracle's TypeScript 7): one it reports too is a stale baseline, listed apart")
	matchesOut := flag.String("matches", "", "write every matched diagnostic here, one `file:line code` per line (to diff two runs)")
	only := flag.String("only", "", "check only the cases whose path contains this")
	trace := flag.String("trace", "", "write each case's path here before checking it (finds a case that does not end)")
	flag.Parse()

	libProgs, lerr := lib.Programs()
	if lerr != nil {
		fmt.Fprintln(os.Stderr, "tsdiag: builtin declarations:", lerr)
		os.Exit(1)
	}
	cases := filepath.Join(*root, "cases")
	baselines := filepath.Join(*root, "baselines", "reference")
	variants := map[string]bool{}
	entries, err := os.ReadDir(baselines)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsdiag:", err)
		os.Exit(1)
	}
	for _, e := range entries {
		if i := strings.Index(e.Name(), "("); i > 0 {
			variants[e.Name()[:i]] = true // a case run under several configurations
		}
	}

	var ran, skipped, nonStrict, parseFailed, panicked int
	reported := map[int]int{}
	matched := map[int]int{}
	expected := map[int]int{}
	var fps []finding
	var matchList []string
	filepath.WalkDir(cases, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".d.ts") {
			return nil
		}
		if !strings.Contains(path, *only) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(path), ".ts")
		dirs := map[string]string{}
		// A leading byte-order mark would hide the first line's directive.
		for _, m := range reDirective.FindAllStringSubmatch(strings.TrimPrefix(string(src), "\ufeff"), -1) {
			dirs[strings.ToLower(m[1])] = strings.ToLower(m[2])
		}
		multiConfig := false
		for _, v := range dirs {
			multiConfig = multiConfig || strings.Contains(v, ",")
		}
		if _, multi := dirs["filename"]; multi || variants[name] || multiConfig {
			skipped++
			return nil
		}
		// This compiler is strict-only: a case TypeScript checks without
		// strictNullChecks types `a && b`, null and undefined differently.
		// A case that suppresses or reformats its errors has no comparable
		// baseline.
		// TypeScript 6 made `strict` the default: each strictness option
		// follows it unless a directive sets that option itself.
		strictFamily := func(opt string) bool {
			if v, ok := dirs[opt]; ok {
				return v == "true"
			}
			return dirs["strict"] != "false"
		}
		strict := strictFamily("strictnullchecks")
		if dirs["nocheck"] == "true" || dirs["pretty"] == "true" || strings.Contains(string(src), "@ts-nocheck") {
			skipped++
			return nil
		}

		want := map[key]bool{}
		suggestions := 0
		if f, err := os.Open(filepath.Join(baselines, name+".errors.txt")); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1<<20), 1<<24)
			for sc.Scan() {
				m := reBaseline.FindStringSubmatch(sc.Text())
				if m == nil || m[1] != name+".ts" {
					continue
				}
				line, _ := strconv.Atoi(m[2])
				code, _ := strconv.Atoi(m[4])
				switch code {
				case 2820:
					code = 2322 // "…; did you mean …?" is TS2322 with a suggestion
				case 2551, 2576:
					code = 2339 // a missing property, with a suggestion
				case 2561:
					code = 2353 // an excess property, with a suggestion
				}
				if code == 2552 {
					suggestions++
				}
				if !want[key{line, code}] {
					want[key{line, code}] = true
					expected[code]++
				}
			}
			f.Close()
		}
		if *trace != "" {
			os.WriteFile(*trace, []byte(path), 0644)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked++
					fmt.Fprintf(os.Stderr, "panic: %s: %v\n", path, r)
				}
			}()
			prog, err := parser.Parse(string(src))
			if err != nil || prog == nil {
				parseFailed++
				return
			}
			ran++
			if !strict {
				nonStrict++
			}
			// A script without strict mode is sloppy JavaScript: its block
			// functions follow Annex B, as the -compat=js lane binds them.
			sloppy := !strictFamily("alwaysstrict") && !reModule.MatchString(string(src))
			module := reModule.MatchString(string(src))
			c := checker.New(binder.BindWith(prog, binder.Options{AnnexB: sloppy, Script: !module, Lib: libProgs}))
			c.ImplicitAnyVariables = !strictFamily("noimplicitany")
			c.CatchVariablesAny = !strictFamily("useunknownincatchvariables")
			c.LibGlobal = libGlobals(dirs)
			switch dirs["target"] {
			case "es3", "es5", "es6", "es2015", "es2016", "es2017", "es2018", "es2019", "es2020", "es2021":
				c.LegacyClassFields = true
			}
			if dirs["usedefineforclassfields"] == "false" {
				c.LegacyClassFields = true
			}
			lineMap := baselineLines(string(src))
			seen := map[key]bool{}
			// `// @ts-ignore` and `// @ts-expect-error` silence the next line.
			silenced := map[int]bool{}
			for i, l := range strings.Split(string(src), "\n") {
				if strings.Contains(l, "@ts-ignore") || strings.Contains(l, "@ts-expect-error") {
					silenced[i+2] = true
				}
			}
			for _, d := range c.Check() {
				if silenced[d.Pos.Line] || !strict && !strictnessFree[d.Code()] {
					continue
				}
				line := 0
				if d.Pos.Line < len(lineMap) {
					line = lineMap[d.Pos.Line]
				}
				k := key{line, d.Code()}
				if k.code == 2552 && suggestions >= 10 && want[key{line, 2304}] && !want[k] {
					// The baselines' compiler gave at most 10 suggestions a
					// file (tsc 7 has no cap): past them, TS2304.
					k.code = 2304
				}
				if seen[k] {
					continue
				}
				seen[k] = true
				reported[k.code]++
				if want[k] {
					matched[k.code]++
					matchList = append(matchList, fmt.Sprintf("%s:%d TS%d", path, line, k.code))
				} else {
					fps = append(fps, finding{path, d.Pos.Line, d.Pos.Col, d.Code(), d.Text, dirs})
				}
			}
		}()
		return nil
	})

	var b strings.Builder
	fmt.Fprintf(&b, "# Checker diagnostics against TypeScript's baselines\n\n")
	fmt.Fprintf(&b, "- Cases checked: %d single-file cases, %d of them without strictNullChecks and compared on TS%v only (skipped %d multi-file, multi-configuration or suppressed; %d parse failures, %d panics)\n", ran, nonStrict, codesOf(strictnessFree), skipped, parseFailed, panicked)
	if *matchesOut != "" {
		sort.Strings(matchList)
		os.WriteFile(*matchesOut, []byte(strings.Join(matchList, "\n")+"\n"), 0o644)
	}
	var stale []finding
	if *tscPath != "" {
		fps, stale = againstTsc(*tscPath, fps)
	}
	total, fpTotal := 0, len(fps)
	for _, n := range reported {
		total += n
	}
	fmt.Fprintf(&b, "- Diagnostics: %d, of which %d match a baseline error and %d do not\n\n", total, total-fpTotal, fpTotal)
	fmt.Fprintf(&b, "| Code | Reported | Match | False positive | Baseline errors (all cases) |\n|---|---|---|---|---|\n")
	var codes []int
	for code := range reported {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	for _, code := range codes {
		fmt.Fprintf(&b, "| TS%d | %d | %d | %d | %d |\n", code, reported[code], matched[code], reported[code]-matched[code], expected[code])
	}
	for _, code := range codes {
		var list []finding
		for _, f := range fps {
			if f.code == code {
				list = append(list, f)
			}
		}
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## TS%d false positives (%d)\n\n", code, len(list))
		for i, f := range list {
			if i == *examples {
				break
			}
			fmt.Fprintf(&b, "- `%s:%d:%d` %s\n", f.file, f.line, f.col, f.text)
		}
	}
	if len(stale) > 0 {
		fmt.Fprintf(&b, "\n## Stale baselines (%d)\n\nThe checker's diagnostic is one TypeScript 7 reports too; the baseline is an older compiler's.\n\n", len(stale))
		for _, f := range stale {
			fmt.Fprintf(&b, "- `%s:%d:%d` TS%d %s\n", f.file, f.line, f.col, f.code, f.text)
		}
	}
	if *roots != "" {
		own := checkOwn(strings.Split(*roots, ","))
		fmt.Fprintf(&b, "\n## This project's programs (%d diagnostics)\n\n", len(own))
		for _, f := range own {
			fmt.Fprintf(&b, "- `%s:%d:%d` TS%d %s\n", f.file, f.line, f.col, f.code, f.text)
		}
	}
	if *out == "" {
		fmt.Print(b.String())
		return
	}
	if err := os.WriteFile(*out, []byte(b.String()), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "tsdiag:", err)
		os.Exit(1)
	}
	fmt.Printf("%d diagnostics, %d false positives over %d cases; report written to %s\n", total, fpTotal, ran, *out)
}
