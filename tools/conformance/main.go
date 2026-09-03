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
	Features      []string // the `features: [...]` tag list — drives the in-scope subset filter
	Flags         []string // the `flags: [...]` list (onlyStrict/noStrict/module/raw/async/…)
	NegativePhase string   // "parse", "resolution", or "runtime" — empty if not a negative test
}

var (
	reIncludes = regexp.MustCompile(`(?m)^includes:\s*\[([^\]]*)\]`)
	reFeatures = regexp.MustCompile(`(?m)^features:\s*\[([^\]]*)\]`)
	reFlags    = regexp.MustCompile(`(?m)^flags:\s*\[([^\]]*)\]`)
	reNegPhase = regexp.MustCompile(`(?m)^negative:\s*\n\s*phase:\s*(\S+)`)
	rePos      = regexp.MustCompile(`^\d+:\d+:\s*`)
	reQuoted   = regexp.MustCompile(`'[^']*'`)
	// reFilePosPrefix strips a parse error's leading `<abspath>: line:col: `
	// file-position prefix (the per-worker temp scratch file the front-end
	// read) so the reason buckets by message and doesn't leak the local
	// machine's directory layout into the report.
	reFilePosPrefix = regexp.MustCompile(`^/[\w.\-/]+:\s*\d+:\d+:\s*`)
	// reScratchPrefix strips a clang error's leading `…/w<id>.ll:line:col: `
	// prefix — the per-worker scratch module path (now under a per-process
	// `run-<pid>/` subdirectory). Bucketing by the clang *message* keeps the
	// reason histogram stable across runs; without this the pid in the path
	// would fragment every CLANG_ERROR into its own bucket.
	reScratchPrefix = regexp.MustCompile(`^\S*w\d+\.ll:\d+:\d+:\s*`)
	// reAbsPath collapses any remaining absolute filesystem path to a stable
	// placeholder (a path that appears mid-message rather than as the prefix).
	reAbsPath = regexp.MustCompile(`/[\w.\-]+(?:/[\w.\-]+)+`)
)

// outOfScopeFeature lists Test262 `features` tags that name capabilities this
// compiler deliberately does not target, so a file tagged with one is excluded
// from the in-scope subset (see inScope). This is the honest denominator: the
// raw full-corpus number counts these; the in-scope number does not, because
// failing them is a scope decision, not a per-feature bug. Kept deliberately
// conservative — when in doubt a feature is left IN scope (which can only lower
// the in-scope number, never inflate it) — and grouped by the reason it's out.
// Intl.* and Temporal.* are matched by prefix in inScope, not enumerated here.
var outOfScopeFeature = map[string]bool{
	// Dynamic code / dynamic module loading — eval and dynamic import() are an
	// opt-in embedded-engine path (TDD-00046), not started.
	"dynamic-import":    true,
	"import-assertions": true,
	"import-attributes": true,
	"IsHTMLDDA":         true, // the [[IsHTMLDDA]] document.all sentinel — legacy web-compat
	// Dynamic object model — no Proxy / Reflect / prototype mutation
	// (fixed-shape struct object model; TDD-00068 deferred).
	"Proxy":                  true,
	"proxy-missing-checks":   true,
	"Reflect":                true,
	"Reflect.construct":      true,
	"Reflect.set":            true,
	"Reflect.setPrototypeOf": true,
	"__proto__":              true,
	"__getter__":             true,
	"__setter__":             true,
	// Realms / cross-realm evaluation — no ShadowRealm.
	"ShadowRealm": true,
	// Resource management (`using` / `await using`) — not targeted.
	"explicit-resource-management": true,
	// Engine-internal optimizations with no observable typed-subset surface.
	"tail-call-optimization": true,
	// Runtime decorators / decorator metadata — not targeted at runtime.
	"decorators": true,
}

// inScope decides whether a Test262 file belongs to this compiler's target
// (the typed, no-eval, no-dynamic-object, no-Intl subset). It is a purely
// mechanical filter over the file's own frontmatter — category, flags, and
// features — so the in-scope denominator is reproducible and auditable, never
// hand-curated per file.
func inScope(fm frontmatter, category string) bool {
	switch category {
	case "intl402", "annexB", "staging":
		return false // internationalization / legacy web-compat / not-yet-standard proposals
	}
	for _, fl := range fm.Flags {
		switch fl {
		case "raw", // no harness at all — usually an engine/parse detail
			"async",  // async harness path deferred until measured (not that it can't run)
			"module": // module-graph negative/semantics tests beyond the single-entry model
			return false
		}
	}
	for _, ft := range fm.Features {
		if strings.HasPrefix(ft, "Intl") || strings.HasPrefix(ft, "Temporal") {
			return false
		}
		if outOfScopeFeature[ft] {
			return false
		}
	}
	return true
}

// codegenTimeout bounds the in-process parse+emit for a single file. Most
// files compile in well under a second; anything past this is a spin (a
// parser/emitter infinite loop), recorded as CODEGEN_TIMEOUT so one bad file
// can't wedge the whole run. Generous on purpose — it is a hang backstop, not a
// performance gate.
const codegenTimeout = 30 * time.Second

// laneCompat is the emitter compat mode for the lane currently running
// ("" == strict, "js" == -compat=js) — a package global, like regexModeFlag,
// because the per-file runners are deep in worker goroutines. Lanes never
// run concurrently (a -compat=both node run is strictly sequential), so a
// plain global is safe. Note the resolver-side permissive bool is `true` in
// BOTH lanes (ADR-00472: both oracles' referents allow global shadowing) —
// the lane axis is the emitter's -compat semantics.
var laneCompat string

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
	fm.Includes = parseFlowList(reIncludes, block)
	fm.Features = parseFlowList(reFeatures, block)
	fm.Flags = parseFlowList(reFlags, block)
	if m := reNegPhase.FindStringSubmatch(block); m != nil {
		fm.NegativePhase = m[1]
	}
	return fm
}

// parseFlowList extracts a single-line `key: [a, b, c]` flow list from the
// frontmatter block — the shape Test262 uses for includes/features/flags.
func parseFlowList(re *regexp.Regexp, block string) []string {
	m := re.FindStringSubmatch(block)
	if m == nil {
		return nil
	}
	var out []string
	for _, item := range strings.Split(m[1], ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// normalizeReason buckets a failure so the report can surface the single
// most common blocker instead of 53,000 individually-unique messages:
// strips this compiler's "line:col: " position prefix, then collapses any
// single-quoted identifier/token to a placeholder so e.g. "undefined
// variable 'x'" and "undefined variable 'y'" fall into one bucket.
func normalizeReason(kind, msg string) string {
	msg = reScratchPrefix.ReplaceAllString(msg, "")
	msg = reFilePosPrefix.ReplaceAllString(msg, "")
	msg = rePos.ReplaceAllString(msg, "")
	msg = reAbsPath.ReplaceAllString(msg, "<path>")
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
	InScope  bool   // this file is in this compiler's target subset (see inScope) — drives the in-scope counter
	Reason   string // empty when Pass
	Blocker  string // compile-phase failures only: the concrete identifier/API the file died on (see blockerOf)
}

// blockerOf extracts the concrete missing identifier/API from a raw (still
// position-prefixed, still identifier-carrying) compile error — the exact
// information normalizeReason deliberately collapses away. This feeds the
// blocked-by histogram: a missing API compile-fails a *whole* test file, so
// one absent built-in can mask bugs in every in-scope feature the file
// co-exercises. Ranking blockers by how many files they gate is how
// low-priority leaf APIs get elevated to their real leverage.
//
// Heuristic, not a parser: the first single-quoted token in the message is
// the blocker for the overwhelmingly common shapes ("undefined variable
// 'Reflect'", "unknown function 'isConstructor'", "'Symbol.species' is not
// supported"); a "Math.xyz is not supported"-style unquoted message falls
// back to its leading token. Empty when nothing identifier-like is found —
// those files still show up in the reason buckets, just not the histogram.
func blockerOf(msg string) string {
	msg = rePos.ReplaceAllString(msg, "")
	if m := reQuotedCapture.FindStringSubmatch(msg); m != nil && m[1] != "" {
		return m[1]
	}
	if i := strings.Index(msg, " is not supported"); i > 0 {
		head := msg[:i]
		if j := strings.LastIndexByte(head, ' '); j >= 0 {
			head = head[j+1:]
		}
		return head
	}
	return ""
}

var reQuotedCapture = regexp.MustCompile(`'([^']+)'`)

func main() {
	corpus := flag.String("corpus", ".test262", "path to a test262 checkout (see fetch.sh)")
	out := flag.String("out", "", "report output path override for a single-lane run (default: the per-lane folder docs/testing/<lane>/CONFORMANCE-RESULTS.md); ignored under -compat=both")
	category := flag.String("category", "", "only run files under test/<category> (default: everything, unfiltered)")
	// Leave two cores free by default: with every worker running a CPU-bound
	// `clang -O2`, a fully-saturated machine starves the *run* phase of a
	// trivial test binary of CPU, so its wall-clock timeout can expire even
	// though it needs milliseconds of actual CPU — the sole cause of run-to-run
	// result flapping ("flaky" conformance). Headroom keeps the run phase fair.
	defaultWorkers := runtime.NumCPU() - 2
	if defaultWorkers < 1 {
		defaultWorkers = 1
	}
	workers := flag.Int("workers", defaultWorkers, "parallel workers")
	limit := flag.Int("limit", 0, "stop after N files (0 = no limit) — for smoke-testing the harness itself")
	// The flakiness is fixed by the worker headroom above, not by the timeout —
	// so this stays 5s, the value the whole Test262 baseline was measured at (a
	// much larger budget peak-OOMs 53k concurrent `clang -O2` runs, and an
	// intermediate value like 10s lands right on a couple of pathological
	// 10–30s-runtime Node tests and re-introduces flapping). A genuine hang is
	// still caught at 5s; the rare slow-but-terminating test that wants more can
	// pass `-timeout`.
	perFileTimeout := flag.Duration("timeout", 5*time.Second, "timeout for clang and for running each compiled test binary")
	workDir := flag.String("workdir", ".conformance-out", "scratch directory for generated .ll/binaries")
	passList := flag.String("passlist", "", "optional path: write the sorted list of passing file paths (one per line) for regression diffing")
	failList := flag.String("faillist", "", "optional path: write the sorted list of failing files as `path\\treason` (one per line) — for finding near-miss clusters (e.g. RUNTIME_NONZERO_EXIT, which already compiled and ran)")
	regexMode := flag.String("regex", "", "RegExp dialect for compiled tests (TDD-00067): ecmascript (default), es-unicode, es-utf16, es-ascii, or pcre — for measuring a specific dialect's conformance")
	compatFlag := flag.String("compat", "strict", "emitter compat lane: strict (default), js, or both (node suite only) — TDD-00022; the corpora are untyped JS, so the js lane measures the vanilla-JS-compat surface")
	suite := flag.String("suite", "test262", "which conformance suite to run: test262 (default), node (Node-core pure modules, TDD-00121 Track B), or ts (TypeScript acceptance oracle, Track C)")
	flag.Parse()
	regexModeFlag = *regexMode

	// Isolate the per-worker scratch files (w{id}.ll / .bin / embedded C) into a
	// per-process subdirectory when the default workdir is in effect, so a
	// second runner invocation (an overlapping run, or a stale one that outlived
	// its parent) can't overwrite this run's w{id}.ll mid-compile and hand clang
	// a corrupted module — which surfaces as a spurious, non-reproducible
	// CLANG_ERROR and makes two runs' pass sets diverge for reasons that have
	// nothing to do with the compiler. An explicit -workdir is left exactly as
	// given (the caller owns isolation then). Kept under the default root, not
	// auto-deleted, so the .ll files stay inspectable after a run.
	if *workDir == ".conformance-out" {
		*workDir = filepath.Join(*workDir, fmt.Sprintf("run-%d", os.Getpid()))
	}

	// TDD-00121 Tracks B/C run entirely different corpora with different oracles
	// (Node behavioral run; TS front-end accept/reject) — dispatch to their own
	// runners, which reuse the shared helpers (killableCommand/firstLine/…) but
	// not the Test262 file walk below.
	switch *compatFlag {
	case "strict", "js", "both":
	default:
		fatal("unknown -compat %q (want strict, js, or both)", *compatFlag)
	}
	switch *suite {
	case "test262":
		// fall through to the Test262 runner below; -compat=both runs the
		// corpus once per lane (strict then js) and the report carries both
		// scores plus the delta (TDD-00022).
	case "node":
		runNodeSuite(*workDir, *perFileTimeout, *workers, *compatFlag)
		return
	case "ts":
		runTSSuite(*workDir, *perFileTimeout, *compatFlag)
		return
	default:
		fatal("unknown -suite %q (want test262, node, or ts)", *suite)
	}

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

	// Lanes run strictly sequentially (laneCompat is a shared global). Under
	// -compat=both the strict lane runs first (the historical correctness
	// baseline), then the js lane; the report carries both scores plus the
	// delta — the direct measurement of what the vanilla-JS-compat surface
	// buys, and the honest home for files that strict deliberately rejects but
	// js accepts (TDD-00022, TDD-00162).
	var lanes []string
	if *compatFlag == "strict" || *compatFlag == "both" {
		lanes = append(lanes, "")
	}
	if *compatFlag == "js" || *compatFlag == "both" {
		lanes = append(lanes, "js")
	}
	// Each lane runs the full corpus and writes its own report into its own
	// folder (docs/testing/<lane>/…). `primary` (the last lane run) feeds the
	// optional pass/fail dumps used for triage.
	var primary []result
	for _, lane := range lanes {
		laneCompat = lane
		all := runTest262Lane(files, testDir, harnessDir, defaultHarness, *workDir, *workers, *perFileTimeout)
		primary = all
		// An explicit -out overrides the folder layout only for a single-lane
		// run (a scratch/triage report); -compat=both always uses the per-lane
		// folders so the two never clobber one file.
		outPath := reportPath("CONFORMANCE-RESULTS.md")
		if *out != "" && *compatFlag != "both" {
			outPath = *out
		}
		if err := writeReport(outPath, all); err != nil {
			fatal("writing report: %v", err)
		}
		fmt.Fprintf(os.Stderr, "done (lane=%s). report written to %s\n", laneLabel(), outPath)
	}
	if *passList != "" {
		var passing []string
		for _, r := range primary {
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
		for _, r := range primary {
			if !r.Pass {
				failing = append(failing, r.Path+"\t"+r.Reason)
			}
		}
		sort.Strings(failing)
		if err := os.WriteFile(*failList, []byte(strings.Join(failing, "\n")+"\n"), 0644); err != nil {
			fatal("writing faillist: %v", err)
		}
	}
}

// runTest262Lane runs the whole file set once under the current laneCompat and
// returns every per-file result. Called once per compat lane (strict, js, or
// both in sequence).
func runTest262Lane(files []string, testDir, harnessDir, defaultHarness, workDir string, workers int, perFileTimeout time.Duration) []result {
	fmt.Fprintf(os.Stderr, "running %d files with %d workers (lane=%s)...\n", len(files), workers, laneLabel())

	jobs := make(chan string, len(files))
	results := make(chan result, len(files))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for path := range jobs {
				results <- runOne(path, testDir, harnessDir, defaultHarness, workDir, id, perFileTimeout)
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
	return all
}

// laneLabel names the lane currently running for progress output and for the
// per-lane report folder ("strict" or "js").
func laneLabel() string {
	if laneCompat == "js" {
		return "js"
	}
	return "strict"
}

// reportPath is the per-compat-lane report location: docs/testing/<lane>/<file>.
// Each lane writes an *identically structured* report into its own folder,
// rather than both lanes sharing one file — so each flag's history is a clean,
// independently diffable git delta (spotting a regression, or an error that
// only appears in one lane, is a per-file diff), and the layout extends to a
// future flag by adding a folder, not a column.
func reportPath(filename string) string {
	return filepath.Join("docs", "testing", laneLabel(), filename)
}

// reportLaneHeader is the one-line banner every per-lane report opens with,
// naming its compat lane and the sibling-folder layout so a reader knows the
// number is one flag's, and where the others live.
func reportLaneHeader() string {
	return fmt.Sprintf("> **Compat lane: `-compat=%s`.** This report covers the `%s` lane only. Each compat flag is generated into its own `docs/testing/<flag>/` folder — identical structure per flag — so its history is independently diffable and an error that surfaces in only one lane is obvious. Sibling lanes live in the neighbouring folders.\n\n", laneLabel(), laneLabel())
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
	res.InScope = inScope(fm, res.Category)

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

	// Parse + emit run *in-process*, so a pathological input that makes the
	// parser or emitter spin forever would wedge this worker indefinitely — the
	// per-file clang/run subprocess timeouts below cannot catch an in-process
	// loop, and with every worker stuck on such a file the whole run freezes at
	// 0% CPU and never writes a report. Run codegen under a wall-clock guard: on
	// timeout, record CODEGEN_TIMEOUT and move on, leaking the stuck goroutine
	// (it dies with the process at exit) rather than stalling the run — one
	// hanging file must not block the whole corpus. Observed under -compat=js,
	// where a handful of files spin the emitter. The panic-recover is duplicated
	// into the goroutine because the outer deferred recover only covers this
	// function's own goroutine, not the one spawned here.
	type cgOut struct {
		ir         string
		linkLibs   []string
		cSources   []llvm.CSource
		embedBlobs []llvm.EmbeddedBlob
		err        error
	}
	cgCh := make(chan cgOut, 1)
	go func() {
		var out cgOut
		defer func() {
			if r := recover(); r != nil {
				out = cgOut{err: fmt.Errorf("CRASH: %v", r)}
			}
			cgCh <- out
		}()
		prog, perr := parser.Parse(full)
		if perr != nil {
			out.err = perr
			return
		}
		em := llvm.NewEmitter()
		em.SetRegexMode(regexModeFlag)
		em.SetCompatMode(laneCompat)
		out.ir, out.err = em.EmitProgram(prog)
		if out.err == nil {
			out.linkLibs = em.LinkLibs()
			out.cSources, out.err = em.EmbeddedCSources()
			out.embedBlobs, _ = em.EmbeddedBlobs()
		}
	}()
	var ir string
	var compileErr error
	var linkLibs []string
	var cSources []llvm.CSource
	var embedBlobs []llvm.EmbeddedBlob
	select {
	case cg := <-cgCh:
		ir, compileErr = cg.ir, cg.err
		linkLibs, cSources, embedBlobs = cg.linkLibs, cg.cSources, cg.embedBlobs
	case <-time.After(codegenTimeout):
		res.Reason = "CODEGEN_TIMEOUT"
		return res
	}

	if fm.NegativePhase == "parse" {
		res.Pass = compileErr != nil
		if !res.Pass {
			res.Reason = "expected a parse-phase rejection but this compiled"
		}
		return res
	}

	if compileErr != nil {
		// Deliberate strict-lane typed rejections (TDD-00162) carry a stable
		// "-compat=js" hint in their message: the two-tier model refuses an
		// untyped-JS pattern (e.g. a cross-type reassignment) under strict and
		// points at the -compat=js lane that accepts it. Bucket these apart
		// from genuine front-end gaps and invalid-IR codegen bugs, so the
		// strict/js delta reads as a designed tightening rather than a
		// regression. Detected on the raw (untruncated) message, since the hint
		// clause sits past normalizeReason's 120-char cut.
		kind := "COMPILE_ERROR"
		if strings.Contains(compileErr.Error(), "-compat=js") {
			kind = "STRICT_REJECT"
		}
		res.Reason = normalizeReason(kind, compileErr.Error())
		res.Blocker = blockerOf(compileErr.Error())
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
	// Compile+link the embedded C runtime files the program's IR depends on
	// (dtoa float formatter, bigint/crypto/JSON/… backends) — the same set the
	// CLI driver links. Without these, any test needing one fails to *link*
	// (e.g. every number-to-string via dtoa) even though the IR is valid.
	for _, cs := range cSources {
		cPath := filepath.Join(workDir, fmt.Sprintf("w%d.%s.%s", workerID, cs.Name, cs.SrcExt()))
		if err := os.WriteFile(cPath, []byte(cs.Content), 0644); err != nil {
			res.Reason = normalizeReason("WRITE_ERROR", err.Error())
			return res
		}
		clangArgs = append(clangArgs, cPath)
		clangArgs = append(clangArgs, cs.CFlags...)
		clangArgs = append(clangArgs, cs.Libs...)
	}
	// Embedded asset blobs (TDD-00142 Stage 7) — parallel to the CLI; the
	// conformance corpus never embeds, but this keeps the two drivers drift-free.
	for _, b := range embedBlobs {
		binPath := filepath.Join(workDir, fmt.Sprintf("w%d.%s.bin", workerID, b.Symbol))
		asmPath := filepath.Join(workDir, fmt.Sprintf("w%d.%s.s", workerID, b.Symbol))
		_ = os.WriteFile(binPath, b.Blob, 0644)
		_ = os.WriteFile(asmPath, []byte(llvm.EmbedBlobAsm(b.Symbol, binPath, runtime.GOOS)), 0644)
		clangArgs = append(clangArgs, asmPath)
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
	case strings.HasPrefix(reason, "CODEGEN_TIMEOUT"):
		return "codegen (in-process hang — emitter/parser spin)"
	case strings.HasPrefix(reason, "CLANG_ERROR") || strings.HasPrefix(reason, "CLANG_TIMEOUT"):
		return "clang (invalid IR — codegen bug)"
	case strings.HasPrefix(reason, "STRICT_REJECT"):
		return "strict typed-rejection (recoverable under -compat=js)"
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
	case strings.HasPrefix(phase, "codegen (in-process hang"):
		return "codegen-hang"
	case strings.HasPrefix(phase, "clang"):
		return "clang"
	case strings.HasPrefix(phase, "strict typed-rejection"):
		return "strict-reject"
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
	"codegen (in-process hang — emitter/parser spin)",
	"wrongly-accepted (negative test compiled/ran)",
	"strict typed-rejection (recoverable under -compat=js)",
	"compile (front-end parse/resolve/codegen)",
	"runtime (timeout)",
	"skipped (unsupported harness include)",
	"infra (I/O)",
	"other",
}

// writeReport renders one lane's generated docs/testing/<lane>/CONFORMANCE-RESULTS.md
// — overall totals, per-top-level-category breakdown, a pipeline-phase summary,
// and the most common failure-reason buckets (each with the phase it died in
// and a representative example file). Regenerated wholesale each run, never
// hand-edited.
func writeReport(path string, all []result) error {
	var b strings.Builder
	b.WriteString("# Test262 conformance results\n\n")
	b.WriteString(reportLaneHeader())
	b.WriteString("Generated by `tools/conformance` (TDD-00008 Design V2) against the full, unfiltered `tc39/test262` corpus — not a hand-picked subset. Regenerate with `make conformance` (see the README's \"Test262 conformance\" section). Do not hand-edit; re-run instead.\n\n")
	b.WriteString("**Read this number carefully**: a low pass rate here is expected right now for two reasons unrelated to per-feature correctness, not just missing features — this compiler doesn't target vanilla untyped-JS compatibility (TDD-00022), and most Test262 files use `eval()` as their own assertion mechanism (this compiler's `eval` is a not-started opt-in, TDD-00046). See the failure-reason breakdown below to tell those apart from a genuine, in-scope feature gap.\n\n")

	total := len(all)
	passed := 0
	inTotal, inPassed := 0, 0
	byCat := map[string]*struct{ total, pass, inTotal, inPass int }{}
	byReason := map[string]int{}
	byPhase := map[string]int{}
	byBlocker := map[string]int{}
	reasonExample := map[string]string{}  // lexicographically-smallest failing path per reason
	blockerExample := map[string]string{} // same, per blocker
	for _, r := range all {
		c, ok := byCat[r.Category]
		if !ok {
			c = &struct{ total, pass, inTotal, inPass int }{}
			byCat[r.Category] = c
		}
		c.total++
		if r.InScope {
			inTotal++
			c.inTotal++
			if r.Pass {
				inPassed++
				c.inPass++
			}
		}
		if r.Pass {
			passed++
			c.pass++
		} else {
			byReason[r.Reason]++
			byPhase[phaseOf(r.Reason)]++
			if ex, seen := reasonExample[r.Reason]; !seen || r.Path < ex {
				reasonExample[r.Reason] = r.Path
			}
			if r.Blocker != "" {
				byBlocker[r.Blocker]++
				if ex, seen := blockerExample[r.Blocker]; !seen || r.Path < ex {
					blockerExample[r.Blocker] = r.Path
				}
			}
		}
	}

	pct := 0.0
	if total > 0 {
		pct = 100 * float64(passed) / float64(total)
	}
	fmt.Fprintf(&b, "## Overall\n\n%d / %d passed (%.1f%%)\n\n", passed, total, pct)

	// In-scope subset — the honest apples-to-apples number. Sits *beside* the
	// raw overall above, never replacing it: the raw number is the full corpus
	// (most of which is out of this compiler's target by construction), while
	// this is the pass rate over only the files the compiler actually targets,
	// filtered mechanically from each file's own frontmatter (see inScope). The
	// two bound the same thing from opposite ends — the raw number is the floor,
	// this is what "in-scope correctness" actually measures.
	inPct := 0.0
	if inTotal > 0 {
		inPct = 100 * float64(inPassed) / float64(inTotal)
	}
	outTotal := total - inTotal
	fmt.Fprintf(&b, "## In-scope subset\n\n**%d / %d passed (%.1f%%)** over the in-scope subset — the files this compiler targets, selected mechanically by frontmatter (excluding `intl402`/`annexB`/`staging`, the `raw`/`async`/`module` flags, and out-of-scope `features` like `Temporal`/`Intl.*`/`dynamic-import`/`Proxy`/`Reflect`/`explicit-resource-management`). The remaining %d files are out of scope by design and are excluded here but still counted in the raw **Overall** number above. This is not a curated pass-list: it is a reproducible filter over the corpus's own tags.\n\n", inPassed, inTotal, inPct, outTotal)

	b.WriteString("## By top-level category\n\n| Category | Passed | Total | % | In-scope pass | In-scope total | In-scope % | What it covers |\n|---|---|---|---|---|---|---|---|\n")
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
		ip := 0.0
		if s.inTotal > 0 {
			ip = 100 * float64(s.inPass) / float64(s.inTotal)
		}
		desc := categoryDesc[c]
		if desc == "" {
			desc = "—"
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %.1f%% | %d | %d | %.1f%% | %s |\n", c, s.pass, s.total, p, s.inPass, s.inTotal, ip, desc)
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

	// Blocked-by histogram — the leverage-ranking cut: each row is one
	// concrete identifier/API whose absence compile-failed that many whole
	// files. Because a compiler reports only the FIRST error per file, a file
	// gated by several missing APIs counts toward the first one hit — the
	// histogram is therefore iterative by design: fix the top blocker, re-run,
	// and the files it was masking redistribute to whatever blocks them next.
	b.WriteString("\n## Blocked-by histogram (compile-phase)\n\nEach row is one concrete identifier/API whose absence was the *first* compile error in that many files — a missing API fails the whole file, masking everything else it exercises, so high-count rows are high-leverage regardless of the API's own face value. First-error-only, so the ranking is iterative: fix the top row, re-run, and its files redistribute to their next blocker. (Low-count rows can be program-local variable names caught up in the same quoted-token extraction — the ranking sinks them; no lexical filter is applied because real blockers like `assert` are lowercase too.)\n\n| Files blocked | Blocker | Example |\n|---|---|---|\n")
	type blockerCount struct {
		blocker string
		count   int
	}
	var blockers []blockerCount
	for bl, n := range byBlocker {
		blockers = append(blockers, blockerCount{bl, n})
	}
	sort.Slice(blockers, func(i, j int) bool {
		if blockers[i].count != blockers[j].count {
			return blockers[i].count > blockers[j].count
		}
		return blockers[i].blocker < blockers[j].blocker
	})
	blimit := 60
	if len(blockers) < blimit {
		blimit = len(blockers)
	}
	for _, bc := range blockers[:blimit] {
		fmt.Fprintf(&b, "| %d | `%s` | `%s` |\n", bc.count, bc.blocker, blockerExample[bc.blocker])
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

	// Machine-readable projection for downstream consumers (the website),
	// merged into the shared summary under this lane's key.
	phaseCounts := map[string]int{}
	for ph, n := range byPhase {
		phaseCounts[phaseShort(ph)] = n
	}
	if err := updateConformanceSummary("test262", laneLabel(), test262SummaryLane{
		Overall: passTotal{Pass: passed, Total: total},
		InScope: passTotal{Pass: inPassed, Total: inTotal},
		ByPhase: phaseCounts,
	}, ""); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}
