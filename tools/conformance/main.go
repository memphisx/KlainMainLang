// Command conformance runs the full, unfiltered tc39/test262 corpus against
// this compiler and reports real pass/fail numbers — TDD-00008 Design V2.
// Fetch the corpus first with tools/conformance/fetch.sh.
//
// This intentionally does NOT curate which files to run (see the TDD): it
// walks every *.js file under <corpus>/test, classifies each one, and
// reports the real numbers, including a low pass rate for reasons that have
// nothing to do with per-feature correctness (this compiler doesn't target
// vanilla untyped JS compatibility, and most Test262 files use eval() as
// their own assertion mechanism, which this compiler doesn't support at
// all) — see the report's own methodology note.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
)

// frontmatter holds the subset of a Test262 file's /*--- ... ---*/ YAML
// block this runner actually needs. Hand-written extraction, not a general
// YAML parser — this project has zero external Go dependencies by design,
// and Test262's own frontmatter is regular enough (confirmed by direct
// sampling: includes/flags are always single-line flow lists, negative is
// always a fixed two-key indented block) not to need one.
type frontmatter struct {
	Includes      []string
	NegativePhase string // "parse", "resolution", or "runtime" — empty if not a negative test
}

var (
	reIncludes = regexp.MustCompile(`(?m)^includes:\s*\[([^\]]*)\]`)
	reNegPhase = regexp.MustCompile(`(?m)^negative:\s*\n\s*phase:\s*(\S+)`)
	rePos      = regexp.MustCompile(`^\d+:\d+:\s*`)
	reQuoted   = regexp.MustCompile(`'[^']*'`)
)

// regexModeFlag mirrors klainmain's -regex flag (TDD-00067) for the compiled
// test binaries, so a conformance run can measure a specific RegExp dialect
// (e.g. -regex=pcre to compare against the pre-alignment baseline). Empty ==
// the compiler's default (ecmascript, TDD-00067 Option C). Set once in main()
// before the workers start, read-only thereafter.
var regexModeFlag string

func parseFrontmatter(src string) frontmatter {
	start := strings.Index(src, "/*---")
	end := strings.Index(src, "---*/")
	var fm frontmatter
	if start == -1 || end == -1 || end < start {
		return fm
	}
	block := src[start:end]
	if m := reIncludes.FindStringSubmatch(block); m != nil {
		for _, inc := range strings.Split(m[1], ",") {
			inc = strings.TrimSpace(inc)
			if inc != "" {
				fm.Includes = append(fm.Includes, inc)
			}
		}
	}
	if m := reNegPhase.FindStringSubmatch(block); m != nil {
		fm.NegativePhase = m[1]
	}
	return fm
}

// normalizeReason buckets a failure so the report can surface the single
// most common blocker instead of 53,000 individually-unique messages:
// strips this compiler's "line:col: " position prefix, then collapses any
// single-quoted identifier/token to a placeholder so e.g. "undefined
// variable 'x'" and "undefined variable 'y'" fall into one bucket.
func normalizeReason(kind, msg string) string {
	msg = rePos.ReplaceAllString(msg, "")
	msg = reQuoted.ReplaceAllString(msg, "'%s'")
	if len(msg) > 120 {
		msg = msg[:120] + "…"
	}
	return kind + ": " + msg
}

type result struct {
	Path     string // relative to <corpus>/test
	Category string // first path segment: language, built-ins, intl402, staging, annexB
	Pass     bool
	Reason   string // empty when Pass
}

func main() {
	corpus := flag.String("corpus", ".test262", "path to a test262 checkout (see fetch.sh)")
	out := flag.String("out", "docs/testing/CONFORMANCE-RESULTS.md", "report output path")
	category := flag.String("category", "", "only run files under test/<category> (default: everything, unfiltered)")
	workers := flag.Int("workers", runtime.NumCPU(), "parallel workers")
	limit := flag.Int("limit", 0, "stop after N files (0 = no limit) — for smoke-testing the harness itself")
	perFileTimeout := flag.Duration("timeout", 5*time.Second, "timeout for clang and for running each compiled test binary")
	workDir := flag.String("workdir", ".conformance-out", "scratch directory for generated .ll/binaries")
	passList := flag.String("passlist", "", "optional path: write the sorted list of passing file paths (one per line) for regression diffing")
	failList := flag.String("faillist", "", "optional path: write the sorted list of failing files as `path\\treason` (one per line) — for finding near-miss clusters (e.g. RUNTIME_NONZERO_EXIT, which already compiled and ran)")
	regexMode := flag.String("regex", "", "RegExp dialect for compiled tests (TDD-00067): ecmascript (default), es-unicode, es-utf16, es-ascii, or pcre — for measuring a specific dialect's conformance")
	flag.Parse()
	regexModeFlag = *regexMode

	testDir := filepath.Join(*corpus, "test")
	harnessDir := filepath.Join(*corpus, "harness")
	shimDir := "tools/conformance/harness-shim"

	// sta.js/assert.js (the default, universal harness pair every file gets)
	// come from this repo's own compiler-compatible shim, not the upstream
	// files — the real ones don't compile at all (prototype-based pseudo-
	// classes, dynamic property assignment onto a function). Every other
	// includes: file (compareArray.js, propertyHelper.js, ...) still comes
	// from the real, unmodified upstream harness — see harness-shim's own
	// comments and TDD-00008 Design V2 for why only these two are shimmed.
	defaultHarness, err := loadHarness(shimDir, []string{"sta.js", "assert.js"})
	if err != nil {
		fatal("loading harness shim: %v", err)
	}

	if err := os.MkdirAll(*workDir, 0755); err != nil {
		fatal("creating workdir: %v", err)
	}

	var files []string
	walkRoot := testDir
	if *category != "" {
		walkRoot = filepath.Join(testDir, *category)
	}
	if err := filepath.Walk(walkRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, "_FIXTURE.js") || !strings.HasSuffix(p, ".js") {
			return nil // Test262's own convention: _FIXTURE.js files are includes for other tests, not tests themselves
		}
		files = append(files, p)
		return nil
	}); err != nil {
		fatal("walking %s: %v", walkRoot, err)
	}
	if *limit > 0 && len(files) > *limit {
		files = files[:*limit]
	}

	fmt.Fprintf(os.Stderr, "running %d files with %d workers...\n", len(files), *workers)

	jobs := make(chan string, len(files))
	results := make(chan result, len(files))
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for path := range jobs {
				results <- runOne(path, testDir, harnessDir, defaultHarness, *workDir, id, *perFileTimeout)
			}
		}(w)
	}
	for _, f := range files {
		jobs <- f
	}
	close(jobs)
	go func() { wg.Wait(); close(results) }()

	var all []result
	done := 0
	start := time.Now()
	for r := range results {
		all = append(all, r)
		done++
		// Report often (every 250) with elapsed time and throughput, so a stall
		// (e.g. a codegen infinite loop, which runs in-process and so isn't
		// caught by the per-file subprocess timeout) shows up as the count
		// freezing rather than a silent multi-minute hang.
		if done%250 == 0 {
			el := time.Since(start).Seconds()
			rate := float64(done) / el
			eta := time.Duration(float64(len(files)-done)/rate) * time.Second
			fmt.Fprintf(os.Stderr, "%d/%d done — %.0fs elapsed, %.0f/s, ETA %s\n",
				done, len(files), el, rate, eta.Round(time.Second))
		}
	}

	if err := writeReport(*out, all); err != nil {
		fatal("writing report: %v", err)
	}
	if *passList != "" {
		var passing []string
		for _, r := range all {
			if r.Pass {
				passing = append(passing, r.Path)
			}
		}
		sort.Strings(passing)
		if err := os.WriteFile(*passList, []byte(strings.Join(passing, "\n")+"\n"), 0644); err != nil {
			fatal("writing passlist: %v", err)
		}
	}
	if *failList != "" {
		var failing []string
		for _, r := range all {
			if !r.Pass {
				failing = append(failing, r.Path+"\t"+r.Reason)
			}
		}
		sort.Strings(failing)
		if err := os.WriteFile(*failList, []byte(strings.Join(failing, "\n")+"\n"), 0644); err != nil {
			fatal("writing faillist: %v", err)
		}
	}
	fmt.Fprintf(os.Stderr, "done. report written to %s\n", *out)
}

func loadHarness(dir string, names []string) (string, error) {
	var b strings.Builder
	for _, n := range names {
		content, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return "", err
		}
		b.Write(content)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// killableCommand builds an exec.Cmd whose whole process *group* is killed
// when ctx expires, and which never blocks Wait for more than a moment once
// killed. Both matter for the pathological inputs in this corpus: a handful
// of files generate tens of MB of LLVM IR (39MB seen), on which `clang -O2`
// runs for many minutes. Plain exec.CommandContext + CombinedOutput would
// (a) kill only the clang *driver*, leaving its forked `clang -cc1` child
// orphaned and still running, and (b) block Wait indefinitely reading the
// output pipe that orphaned child still holds open — so one bad file could
// hang the whole run far past its per-file timeout. Setpgid + a group-wide
// SIGKILL on cancel reaps the child too; WaitDelay bounds any residual
// pipe wait. Unix-only attrs, fine on this project's Linux+macOS targets.
func killableCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			// Negative pid ⇒ signal the entire process group.
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// runOne compiles and (if applicable) runs a single test262 file, returning
// its classification. Recovers from a panic in this compiler's own
// parser/codegen so one crashing input can't take down the whole batch —
// a real, if rare, possibility running 53,872 files this project's own
// parser was never exercised against before.
func runOne(path, testDir, harnessDir, defaultHarness, workDir string, workerID int, timeout time.Duration) (res result) {
	rel, _ := filepath.Rel(testDir, path)
	res.Path = rel
	res.Category = strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]

	defer func() {
		if r := recover(); r != nil {
			res.Pass = false
			res.Reason = normalizeReason("CRASH", fmt.Sprintf("%v", r))
		}
	}()

	src, err := os.ReadFile(path)
	if err != nil {
		res.Reason = normalizeReason("READ_ERROR", err.Error())
		return res
	}
	fm := parseFrontmatter(string(src))

	full := defaultHarness
	for _, inc := range fm.Includes {
		content, err := os.ReadFile(filepath.Join(harnessDir, inc))
		if err != nil {
			res.Reason = normalizeReason("MISSING_HARNESS_INCLUDE", inc)
			return res
		}
		full += string(content) + "\n"
	}
	full += string(src)

	prog, perr := parser.Parse(full)
	var ir string
	var cerr error
	var linkLibs []string
	if perr == nil {
		em := llvm.NewEmitter()
		em.SetRegexMode(regexModeFlag)
		ir, cerr = em.EmitProgram(prog)
		linkLibs = em.LinkLibs()
	}
	compileErr := perr
	if compileErr == nil {
		compileErr = cerr
	}

	if fm.NegativePhase == "parse" {
		res.Pass = compileErr != nil
		if !res.Pass {
			res.Reason = "expected a parse-phase rejection but this compiled"
		}
		return res
	}

	if compileErr != nil {
		res.Reason = normalizeReason("COMPILE_ERROR", compileErr.Error())
		return res
	}

	llFile := filepath.Join(workDir, fmt.Sprintf("w%d.ll", workerID))
	binFile := filepath.Join(workDir, fmt.Sprintf("w%d.bin", workerID))
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		res.Reason = normalizeReason("WRITE_ERROR", err.Error())
		return res
	}

	clangArgs := []string{"-O2", llFile, "-o", binFile}
	for _, lib := range linkLibs {
		clangArgs = append(clangArgs, "-l"+lib)
	}

	cctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var clangOut bytes.Buffer
	clangCmd := killableCommand(cctx, "clang", clangArgs...)
	clangCmd.Stdout = &clangOut
	clangCmd.Stderr = &clangOut
	if err := clangCmd.Run(); err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			res.Reason = "CLANG_TIMEOUT"
		} else {
			res.Reason = normalizeReason("CLANG_ERROR", firstLine(clangOut.String()))
		}
		return res
	}
	defer os.Remove(binFile)

	rctx, rcancel := context.WithTimeout(context.Background(), timeout)
	defer rcancel()
	var stdout, stderr bytes.Buffer
	runCmd := killableCommand(rctx, binFile)
	runCmd.Stdout = &stdout
	runCmd.Stderr = &stderr
	runErr := runCmd.Run()

	negativeRuntime := fm.NegativePhase == "runtime" || fm.NegativePhase == "resolution"
	exitedZero := runErr == nil
	if rctx.Err() == context.DeadlineExceeded {
		res.Reason = "RUN_TIMEOUT"
		return res
	}

	if negativeRuntime {
		res.Pass = !exitedZero
		if !res.Pass {
			res.Reason = "expected a runtime rejection but the binary exited 0"
		}
		return res
	}
	res.Pass = exitedZero
	if !res.Pass {
		res.Reason = normalizeReason("RUNTIME_NONZERO_EXIT", firstLine(stderr.String()))
	}
	return res
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "conformance: "+format+"\n", args...)
	os.Exit(1)
}

// categoryDesc describes what each Test262 top-level category covers, so the
// per-category table reads without cross-referencing the corpus layout. Keyed
// by the first path segment under <corpus>/test.
var categoryDesc = map[string]string{
	"language":  "Core language: statements, expressions, literals, classes, modules, ASI, scoping — the in-scope engine.",
	"built-ins": "Standard library objects (Array, Object, String, Math, RegExp, Promise, TypedArrays, …) — many probe the dynamic object model.",
	"intl402":   "ECMA-402 Internationalization (Intl.*) — out of scope for this compiler.",
	"annexB":    "Annex B legacy/web-compat features (legacy octal, __proto__, escape/unescape, …) — mostly out of scope.",
	"staging":   "Not-yet-standard proposals staged upstream — moving target, largely out of scope.",
	"harness":   "Self-tests of Test262's own assertion harness (sta.js/assert.js/propertyHelper) — needs this repo's harness-shim.",
}

// phaseOf maps a normalized failure reason to the pipeline phase it died in,
// so the report can separate genuine near-misses (compiled + ran, wrong
// result) from front-end parse gaps, bad-IR codegen bugs, wrongly-accepted
// negative tests, and unsupported-harness skips.
func phaseOf(reason string) string {
	switch {
	case strings.HasPrefix(reason, "RUNTIME_NONZERO_EXIT") || strings.HasPrefix(reason, "CRASH"):
		return "runtime (ran, wrong result — near-miss)"
	case strings.HasPrefix(reason, "RUN_TIMEOUT"):
		return "runtime (timeout)"
	case strings.HasPrefix(reason, "CLANG_ERROR") || strings.HasPrefix(reason, "CLANG_TIMEOUT"):
		return "clang (invalid IR — codegen bug)"
	case strings.HasPrefix(reason, "COMPILE_ERROR"):
		return "compile (front-end parse/resolve/codegen)"
	case strings.HasPrefix(reason, "MISSING_HARNESS_INCLUDE"):
		return "skipped (unsupported harness include)"
	case strings.HasPrefix(reason, "expected a parse-phase rejection") || strings.HasPrefix(reason, "expected a runtime rejection"):
		return "wrongly-accepted (negative test compiled/ran)"
	case strings.HasPrefix(reason, "READ_ERROR") || strings.HasPrefix(reason, "WRITE_ERROR"):
		return "infra (I/O)"
	default:
		return "other"
	}
}

// phaseShort is the compact phase label used in the per-reason table's own
// Phase column (the full phaseOf strings are too wide for a table cell).
func phaseShort(phase string) string {
	switch {
	case strings.HasPrefix(phase, "runtime (ran"):
		return "runtime"
	case strings.HasPrefix(phase, "runtime (timeout"):
		return "run-timeout"
	case strings.HasPrefix(phase, "clang"):
		return "clang"
	case strings.HasPrefix(phase, "compile"):
		return "compile"
	case strings.HasPrefix(phase, "wrongly-accepted"):
		return "neg-accepted"
	case strings.HasPrefix(phase, "skipped"):
		return "skipped"
	case strings.HasPrefix(phase, "infra"):
		return "infra"
	default:
		return "other"
	}
}

// phaseOrder fixes the display order of the pipeline-phase summary, most
// actionable first (near-misses and codegen bugs before out-of-scope skips).
var phaseOrder = []string{
	"runtime (ran, wrong result — near-miss)",
	"clang (invalid IR — codegen bug)",
	"wrongly-accepted (negative test compiled/ran)",
	"compile (front-end parse/resolve/codegen)",
	"runtime (timeout)",
	"skipped (unsupported harness include)",
	"infra (I/O)",
	"other",
}

// writeReport renders the generated docs/testing/CONFORMANCE-RESULTS.md —
// overall totals, per-top-level-category breakdown, a pipeline-phase summary,
// and the most common failure-reason buckets (each with the phase it died in
// and a representative example file). Regenerated wholesale each run, never
// hand-edited.
func writeReport(path string, all []result) error {
	var b strings.Builder
	b.WriteString("# Test262 conformance results\n\n")
	b.WriteString("Generated by `tools/conformance` (TDD-00008 Design V2) against the full, unfiltered `tc39/test262` corpus — not a hand-picked subset. Regenerate with `make conformance` (see the README's \"Test262 conformance\" section). Do not hand-edit; re-run instead.\n\n")
	b.WriteString("**Read this number carefully**: a low pass rate here is expected right now for two reasons unrelated to per-feature correctness, not just missing features — this compiler doesn't target vanilla untyped-JS compatibility (TDD-00022, not started), and most Test262 files use `eval()` as their own assertion mechanism (this compiler's `eval` is a not-started opt-in, TDD-00046). See the failure-reason breakdown below to tell those apart from a genuine, in-scope feature gap.\n\n")

	total := len(all)
	passed := 0
	byCat := map[string]*struct{ total, pass int }{}
	byReason := map[string]int{}
	byPhase := map[string]int{}
	reasonExample := map[string]string{} // lexicographically-smallest failing path per reason
	for _, r := range all {
		c, ok := byCat[r.Category]
		if !ok {
			c = &struct{ total, pass int }{}
			byCat[r.Category] = c
		}
		c.total++
		if r.Pass {
			passed++
			c.pass++
		} else {
			byReason[r.Reason]++
			byPhase[phaseOf(r.Reason)]++
			if ex, seen := reasonExample[r.Reason]; !seen || r.Path < ex {
				reasonExample[r.Reason] = r.Path
			}
		}
	}

	pct := 0.0
	if total > 0 {
		pct = 100 * float64(passed) / float64(total)
	}
	fmt.Fprintf(&b, "## Overall\n\n%d / %d passed (%.1f%%)\n\n", passed, total, pct)

	b.WriteString("## By top-level category\n\n| Category | Passed | Total | % | What it covers |\n|---|---|---|---|---|\n")
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		s := byCat[c]
		p := 0.0
		if s.total > 0 {
			p = 100 * float64(s.pass) / float64(s.total)
		}
		desc := categoryDesc[c]
		if desc == "" {
			desc = "—"
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %.1f%% | %s |\n", c, s.pass, s.total, p, desc)
	}

	// Failures grouped by the pipeline phase they died in — the single most
	// useful cut for deciding what to work on: "runtime … near-miss" files
	// already compiled and ran (a correctness fix flips them directly),
	// "clang … codegen bug" files are our own invalid IR, and
	// "wrongly-accepted" are negative tests we should have rejected.
	b.WriteString("\n## Failures by pipeline phase\n\nWhere each failing file died in the pipeline. Near-misses (already compiled and ran) and codegen bugs (invalid IR we emitted) are the most actionable; the rest are largely out-of-scope parse gaps or unsupported harness features.\n\n| Failing files | Phase |\n|---|---|\n")
	for _, ph := range phaseOrder {
		if n := byPhase[ph]; n > 0 {
			fmt.Fprintf(&b, "| %d | %s |\n", n, ph)
		}
	}

	b.WriteString("\n## Most common failure reasons\n\nThe `Reason` is normalized (position stripped, quoted identifiers collapsed to `'%s'`). `Phase` is the pipeline stage it died in; `Example` is one representative file (the lexicographically first) — pass it to the compiler directly to reproduce.\n\n| Count | Phase | Reason | Example |\n|---|---|---|---|\n")
	type reasonCount struct {
		reason string
		count  int
	}
	var reasons []reasonCount
	for r, n := range byReason {
		reasons = append(reasons, reasonCount{r, n})
	}
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].count != reasons[j].count {
			return reasons[i].count > reasons[j].count
		}
		return reasons[i].reason < reasons[j].reason
	})
	limit := 50
	if len(reasons) < limit {
		limit = len(reasons)
	}
	for _, rc := range reasons[:limit] {
		fmt.Fprintf(&b, "| %d | %s | %s | `%s` |\n", rc.count, phaseShort(phaseOf(rc.reason)), rc.reason, reasonExample[rc.reason])
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}
