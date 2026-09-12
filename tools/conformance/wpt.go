// Web Platform Tests conformance track (TDD-00082 Track 2, TDD-00204). Runs
// the ENTIRE headless multi-global corpus — every `.any.js`/`.window.js`/
// `.worker.js` source in a pinned web-platform-tests/wpt checkout, no
// directory allowlist — through this compiler's real pipeline, structurally
// parallel to the Test262 and Node-core tracks.
//
// Design notes, matching how wpt-runner/Node/Bun run WPT headless:
//   - `// META: script=` helper includes are RESOLVED and INLINED from the
//     checkout (absolute against the WPT root, relative against the file), rather
//     than pre-skipped — the single largest unblock.
//   - A typed `testharness.js`-shaped shim (wptHarness) supplies test/
//     promise_test/async_test + the ~30-function assert vocabulary, compiling
//     clean under BOTH compat lanes so a strict-lane failure is the test body,
//     never the harness.
//   - Results are counted at the SUBTEST level (each test/promise_test/async_test
//     is one subtest — WPT's own metric), parsed from a `__WPT_RESULT__` summary
//     line, alongside the coarser file-level pass/fail.
//   - The genuine limit vs a JS-engine runner: no DOM and no dynamic global
//     object, so a file that drives a real DOM tree (document/createElement) is
//     reported out of scope — a project scope choice, not silently dropped.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The denominator is the ENTIRE headless multi-global corpus — every
// `.any.js`/`.window.js`/`.worker.js` source in the pinned checkout, found by
// walking the whole tree (TDD-00204). There is deliberately no directory
// allowlist: scope classification (DOM/browser-hardware globals, WebIDL
// reflection harnesses) happens per file at run time, where it lands in the
// report's counted skip histogram, never at fetch time where an omission is
// invisible. fetch.sh's sparse patterns fetch all .js/.json plus fixture dirs.
const wptCorpus = ".wpt-tests"

// wptCorpusStats holds the full-tree class counts fetch.sh derives from git
// tree metadata (`.git/kml-corpus-stats`) — the whole pinned WPT repo, most of
// which is never checked out. They anchor the report's capability ladder: what
// a rendering engine, a DOM-without-rendering (jsdom-tier) runner, and this
// no-DOM runner can each reach.
type wptCorpusStats struct {
	Total, Headless, CSSHTML, OtherHTML, Manual, Crashtests, Wdspec int
}

func readWPTCorpusStats() (s wptCorpusStats) {
	b, err := os.ReadFile(filepath.Join(wptCorpus, ".git", "kml-corpus-stats"))
	if err != nil {
		return s // ladder lines degrade to zeros; the run itself is unaffected
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		n, _ := strconv.Atoi(v)
		switch k {
		case "total":
			s.Total = n
		case "headless":
			s.Headless = n
		case "css_html":
			s.CSSHTML = n
		case "other_html":
			s.OtherHTML = n
		case "manual":
			s.Manual = n
		case "crashtests":
			s.Crashtests = n
		case "wdspec":
			s.Wdspec = n
		}
	}
	return s
}

// reWptGlobalMeta captures the `// META: global=` scope list of an `.any.js`
// file, which decides how many test documents WPT's own infrastructure
// generates from it (`.any.html`, `.any.worker.html`, …) — the unit wpt.fyi
// and browser vendors count. Parsed for the report's comparability number.
var reWptGlobalMeta = regexp.MustCompile(`(?m)^//\s*META:\s*global=(.*)$`)

// wptVariantCount returns how many test documents WPT generates for one source
// file. `.window.js`/`.worker.js` are single-scope. For `.any.js` each entry of
// `META: global=` is one document (`worker` is shorthand for the three worker
// scopes); the default without the directive is window + dedicated worker.
// Each source still RUNS here once — this runtime has no distinct per-scope
// globals to vary — so the expansion is a denominator-comparability figure,
// never claimed as extra coverage.
func wptVariantCount(path, src string) int {
	if !strings.HasSuffix(path, ".any.js") {
		return 1
	}
	m := reWptGlobalMeta.FindStringSubmatch(src)
	if m == nil {
		return 2 // default: window, dedicatedworker
	}
	n := 0
	for _, tok := range strings.Split(m[1], ",") {
		switch strings.TrimSpace(tok) {
		case "":
			// ignore
		case "worker":
			n += 3 // dedicatedworker, sharedworker, serviceworker
		default:
			n++
		}
	}
	if n == 0 {
		n = 2
	}
	return n
}

type wptResult struct {
	File     string // slice-relative path (url/urlsearchparams-append.any.js)
	Area     string // first path segment: url | dom
	Status   string // PASS | FAIL | SKIP_OUT_OF_SCOPE
	Reason   string
	SubPass  int  // subtests that passed (parsed from the __WPT_RESULT__ line)
	SubFail  int  // subtests that failed; 0/0 when the file never ran
	Ran      bool // the binary produced a __WPT_RESULT__ summary (compiled + ran)
	Variants int  // test documents WPT generates from this source (META: global=)
}

var reWPTResult = regexp.MustCompile(`(?m)^__WPT_RESULT__ (\d+) (\d+)$`)

// parseWPTSubtests extracts the subtest pass/fail counts the harness prints
// (`__WPT_RESULT__ <pass> <fail>`); the last such line wins.
func parseWPTSubtests(stdout string) (pass, fail int, found bool) {
	ms := reWPTResult.FindAllStringSubmatch(stdout, -1)
	if len(ms) == 0 {
		return 0, 0, false
	}
	m := ms[len(ms)-1]
	pass, _ = strconv.Atoi(m[1])
	fail, _ = strconv.Atoi(m[2])
	return pass, fail, true
}

var (
	// A `// META: key=value` directive line (WPT frontmatter). Only `script=`
	// (helper includes) is acted on; the rest (global/title/timeout/variant) are
	// informational and dropped.
	reWptMeta       = regexp.MustCompile(`^//\s*META:\s*([a-z]+)=(.*)$`)
	reWptScriptMeta = regexp.MustCompile(`^//\s*META:\s*script=(.*)$`)
)

// wptDOMNeedle names identifiers whose presence means the file drives a real
// DOM/document tree or a browser-only global this compiler has no surface for —
// it is then out of scope (reported, not a failure), the same "stricter,
// deliberately different runtime" filter the Test262 DOM categories hit. Kept as
// word-boundary identifier matches so `document`-in-a-comment or a substring
// (`documentation`) doesn't trip it. AbortSignal/EventTarget are NOT here: they
// are implemented ambient globals, so a `dom/abort` file using them is in scope.
var wptDOMNeedle = []string{
	"document", "navigator", "location", "XMLHttpRequest", "MessageChannel",
	"MessagePort", "createElement", "HTMLElement", "customElements",
	"requestAnimationFrame", "getComputedStyle", "IntersectionObserver",
	"window.", "self.postMessage", "GLOBAL.isWindow",
}

// wptOutOfScopeInclude names `META: script=` helper includes that pull in a
// harness this compiler can't run — chiefly WebIDL's `idlharness.js`/
// `WebIDLParser.js`, which reflect over the live object model. A file requiring
// one is out of scope (a whole-file structural skip), not a per-feature failure.
var wptOutOfScopeInclude = map[string]string{
	"/resources/idlharness.js":         "requires the WebIDL idlharness reflection harness",
	"/resources/WebIDLParser.js":       "requires the WebIDL parser harness",
	"/resources/testdriver.js":         "requires testdriver (WebDriver browser automation)",
	"/resources/testdriver-vendor.js":  "requires testdriver (WebDriver browser automation)",
	"/resources/testdriver-actions.js": "requires testdriver (WebDriver browser automation)",
}

// transformWPTSource rewrites one WPT headless (`.any.js`/`.window.js`/`.worker.js`) file into the form
// this compiler compiles: strip the `// META:` frontmatter (inlining any local
// `script=./resources/*.js` helper, dropping the shim-provided
// `/common/subset-tests-by-key.js`), prepend the typed testharness shim (with the
// file-backed `fetch` shim only when the body uses `fetch`), and append the
// event-loop drain that turns an internal failure into a nonzero exit. Returns
// (source, "", "") on success, ("", skipReason, "") when the file is out of scope
// for a structural reason, or ("", "", failReason) when its first blocker is an
// in-scope gap. dir is the file's directory, used to resolve local script includes.
func transformWPTSource(src, dir string) (out, skip, fail string) {
	var body []string
	var localIncludes []string // inlined `script=./resources/*.js` helper sources
	for _, line := range strings.Split(src, "\n") {
		if m := reWptScriptMeta.FindStringSubmatch(line); m != nil {
			inc := strings.TrimSpace(m[1])
			// A `?feature=…` query on an include (testdriver.js?feature=bidi)
			// is a load-time option, not part of the file path.
			if q := strings.IndexByte(inc, '?'); q >= 0 {
				inc = inc[:q]
			}
			if why, bad := wptOutOfScopeInclude[inc]; bad {
				return "", why, ""
			}
			if inc == "/common/subset-tests-by-key.js" {
				continue // subsetTestByKey is provided by the shim
			}
			// Resolve and INLINE the helper script, the way wpt-runner/Node/Bun
			// load `// META: script=` includes: an absolute `/common/utils.js`
			// resolves against the WPT root, a relative `../util/helpers.js` /
			// `./resources/x.js` against the file's own dir. This is the single
			// largest unblock (the former "requires shared helper include" skip was
			// the dominant bucket). A helper that isn't in the fetched slice, or is
			// itself untyped-JS this compiler can't compile, then surfaces honestly
			// (a skip for the missing file, a COMPILE_ERROR for an unsupported one)
			// rather than the whole area being pre-skipped.
			var p string
			if strings.HasPrefix(inc, "/") {
				p = filepath.Join(wptCorpus, strings.TrimPrefix(inc, "/"))
			} else {
				p = filepath.Join(dir, inc)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return "", "shared helper not in fetched slice: " + inc, ""
			}
			localIncludes = append(localIncludes, string(data))
			continue
		}
		if reWptMeta.MatchString(line) {
			continue // global/title/timeout/variant — informational, drop
		}
		body = append(body, line)
	}
	joined := strings.Join(localIncludes, "\n") + "\n" + strings.Join(body, "\n")

	// DOM / browser-global surface this compiler doesn't target — out of scope,
	// reported (not a failure). Checked on a quote-stripped copy so a needle
	// inside a string literal ('document' in a test name) doesn't trip it.
	stripped := stripQuoted(joined)
	for _, needle := range wptDOMNeedle {
		if wordPresent(stripped, needle) {
			return "", "uses DOM/browser global (" + needle + ")", ""
		}
	}

	usesFetch := wordPresent(stripped, "fetch")
	var b strings.Builder
	if usesFetch {
		b.WriteString("import fs from 'fs';\n")
	}
	b.WriteString(wptHarness)
	if usesFetch {
		b.WriteString(wptFetchShim)
	}
	b.WriteString("\n")
	b.WriteString(joined)
	b.WriteString("\n")
	b.WriteString(wptDrain)
	return b.String(), "", ""
}

// wordPresent reports whether needle appears in s bounded by non-identifier
// characters on both sides (so `fetch` matches `fetch(` but not `prefetch`). For
// a needle ending in `.` or `(` the trailing boundary is the needle's own char.
func wordPresent(s, needle string) bool {
	from := 0
	for {
		i := strings.Index(s[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		leftOK := i == 0 || !isIdentChar(s[i-1])
		end := i + len(needle)
		lastCh := needle[len(needle)-1]
		rightOK := !isIdentChar(lastCh) || end >= len(s) || !isIdentChar(s[end])
		if leftOK && rightOK {
			return true
		}
		from = i + 1
	}
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func runWPTSuite(workDir string, timeout time.Duration, workers int, compat string) {
	var files []string
	err := filepath.Walk(wptCorpus, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".any.js") || strings.HasSuffix(p, ".window.js") || strings.HasSuffix(p, ".worker.js") {
			// The multi-global source files a non-browser host runs (a
			// `.https`/`.sub` variant ends with one of these too). The whole
			// checkout is walked — no allowlist (TDD-00204); out-of-scope files
			// classify themselves at run time into the counted skip buckets.
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		fatal("walking WPT corpus %s (run tools/conformance/fetch.sh first): %v", wptCorpus, err)
	}
	if len(files) == 0 {
		fatal("no WPT .any.js/.window.js/.worker.js files under %s (run tools/conformance/fetch.sh first)", wptCorpus)
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		fatal("creating workdir: %v", err)
	}

	var lanes []string
	if compat == "strict" || compat == "both" {
		lanes = append(lanes, "")
	}
	if compat == "js" || compat == "both" {
		lanes = append(lanes, "js")
	}
	stats := readWPTCorpusStats()
	for _, lane := range lanes {
		laneCompat = lane
		results := runWPTLane(files, workDir, timeout, workers)
		out := reportPath("CONFORMANCE-RESULTS-WPT.md")
		if err := writeWPTReport(out, results, stats); err != nil {
			fatal("writing WPT report: %v", err)
		}
		fmt.Fprintf(os.Stderr, "WPT suite (lane=%s): %d files, report written to %s\n", laneLabel(), len(results), out)
	}

	if fl := os.Getenv("WPT_FAILLIST"); fl != "" {
		var lines []string
		for _, r := range results0(lanes, files, workDir, timeout, workers) {
			if r.Status == "FAIL" {
				lines = append(lines, r.File+"\t"+strings.ReplaceAll(r.Reason, "\n", " "))
			}
		}
		sort.Strings(lines)
		_ = os.WriteFile(fl, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	}
}

// results0 re-runs the last lane only to service the optional WPT_FAILLIST dump
// without holding every lane's results in memory — the corpus is small, so the
// extra pass is cheap and keeps runWPTSuite's control flow flat.
func results0(lanes []string, files []string, workDir string, timeout time.Duration, workers int) []wptResult {
	if len(lanes) == 0 {
		return nil
	}
	laneCompat = lanes[len(lanes)-1]
	return runWPTLane(files, workDir, timeout, workers)
}

func runWPTLane(files []string, workDir string, timeout time.Duration, workers int) []wptResult {
	fmt.Fprintf(os.Stderr, "WPT suite (-compat=%s): running %d files ...\n", laneLabel(), len(files))
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan string, len(files))
	out := make(chan wptResult, len(files))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for p := range jobs {
				out <- runOneWPT(p, workDir, id, timeout)
			}
		}(w)
	}
	for _, f := range files {
		jobs <- f
	}
	close(jobs)
	go func() { wg.Wait(); close(out) }()

	var results []wptResult
	prog := newProgressTracker("wpt", laneLabel(), len(files), workDir)
	for r := range out {
		results = append(results, r)
		prog.tick(r.Status, r.Reason)
	}
	return results
}

func runOneWPT(path, workDir string, workerID int, timeout time.Duration) (res wptResult) {
	rel := path
	if r, err := filepath.Rel(wptCorpus, path); err == nil {
		rel = filepath.ToSlash(r)
	}
	res.File = rel
	res.Area = strings.SplitN(rel, "/", 2)[0]
	defer func() {
		if r := recover(); r != nil {
			res.Status = "FAIL"
			res.Reason = normalizeReason("CRASH", fmt.Sprintf("%v", r))
		}
	}()

	raw, err := os.ReadFile(path)
	if err != nil {
		res.Status = "FAIL"
		res.Reason = "READ_ERROR: " + err.Error()
		return res
	}
	res.Variants = wptVariantCount(path, string(raw))
	src, skip, fail := transformWPTSource(string(raw), filepath.Dir(path))
	if skip != "" {
		res.Status = "SKIP_OUT_OF_SCOPE"
		res.Reason = skip
		return res
	}
	if fail != "" {
		res.Status = "FAIL"
		res.Reason = fail
		return res
	}

	// The compiled binary runs with its cwd set to the test file's directory so a
	// `fetch("resources/x.json")` — rewritten by wptFetchShim to a relative
	// fs.readFileSync — resolves against the file's own resources/ dir.
	ok, reason, stdout, _ := compileAndRunInDir(src, workDir, fmt.Sprintf("wpt%d", workerID), timeout, filepath.Dir(path), "parallel")
	if sp, sf, found := parseWPTSubtests(stdout); found {
		res.SubPass, res.SubFail, res.Ran = sp, sf, true
	}
	if ok {
		res.Status = "PASS"
	} else {
		res.Status = "FAIL"
		res.Reason = reason
	}
	return res
}

func writeWPTReport(path string, all []wptResult, stats wptCorpusStats) error {
	var b strings.Builder
	b.WriteString("# Web Platform Tests conformance results\n\n")
	b.WriteString(reportLaneHeader())
	b.WriteString("Generated by `tools/conformance -suite=wpt` (TDD-00082 Track 2, TDD-00204 full-corpus denominator) against **every** headless multi-global source file (`.any.js`/`.window.js`/`.worker.js`) in a pinned `web-platform-tests/wpt` checkout — the whole repo, no directory allowlist; regenerate with `make conformance-wpt`. Do not hand-edit; re-run instead.\n\n")

	totalDocs := stats.CSSHTML + stats.OtherHTML + stats.Headless // upper bound; wpt.fyi counts generated variants on top
	b.WriteString("## Where this runner sits in the WPT capability ladder\n\n")
	fmt.Fprintf(&b, "The pinned tree holds %d files. Full browser engines (Servo, Chrome, Firefox) report ~52–56K test documents because they render pixels; each capability rung below unlocks a class of the corpus. This report's denominator is rung 3 — every exclusion above it is a *capability gap named here*, not a curated omission:\n\n", stats.Total)
	b.WriteString("| Rung | Capability | Corpus class | Files |\n|---|---|---|---|\n")
	fmt.Fprintf(&b, "| 1 | Rendering engine (layout/paint/screenshot) | `css/` + visual reftests | %d |\n", stats.CSSHTML)
	fmt.Fprintf(&b, "| 2 | DOM without rendering (jsdom tier — planned, own TDD) | testharness `.html` documents | %d (minus support/manual within them) |\n", stats.OtherHTML)
	fmt.Fprintf(&b, "| **3** | **No DOM (this runner, today)** | **headless multi-global sources** | **%d** |\n", stats.Headless)
	fmt.Fprintf(&b, "| — | Never headless-runnable by anyone | `-manual` %d · `crashtests/` %d · `webdriver/` wdspec %d | %d |\n", stats.Manual, stats.Crashtests, stats.Wdspec, stats.Manual+stats.Crashtests+stats.Wdspec)
	fmt.Fprintf(&b, "\n(%d total test-ish documents across rungs; wpt.fyi's ~56K additionally counts each `META: global=` variant of an `.any.js` file as a separate test — the equivalent expansion of this denominator is given under Overall.)\n\n", totalDocs)

	b.WriteString("Each file has its `// META:` frontmatter stripped, a typed `testharness.js`-shaped shim prepended (the same shim compiles under both compat lanes, so a strict-lane failure is the test body, not the harness), then is compiled and run. **PASS** = compiled, ran, and every `test`/`promise_test`/`async_test` assertion held (exit 0); **FAIL** = a compile error, invalid IR, or an assertion/`assert_unreached` failure (nonzero exit); **SKIP** = out of scope — a file driving a real DOM tree/browser-hardware global (Bluetooth, WebNN, sensors, …) or a WebIDL `idlharness` reflection harness this compiler has no surface for. Every skip is counted in the histogram below.\n\n")

	type agg struct{ pass, fail, skip, subPass, subFail int }
	byArea := map[string]*agg{}
	byFail := map[string]int{}
	bySkip := map[string]int{}
	var oPass, oFail, oSkip, oSubPass, oSubFail, oDocs int
	var passing []wptResult
	for _, r := range all {
		oDocs += r.Variants
		a, ok := byArea[r.Area]
		if !ok {
			a = &agg{}
			byArea[r.Area] = a
		}
		a.subPass += r.SubPass
		a.subFail += r.SubFail
		oSubPass += r.SubPass
		oSubFail += r.SubFail
		switch r.Status {
		case "PASS":
			a.pass++
			oPass++
			passing = append(passing, r)
		case "FAIL":
			a.fail++
			oFail++
			byFail[nodeReasonBucket(r.Reason)]++
		default:
			a.skip++
			oSkip++
			bySkip[nodeReasonBucket(r.Reason)]++
		}
	}

	ran := oPass + oFail
	pct := 0.0
	if ran > 0 {
		pct = 100 * float64(oPass) / float64(ran)
	}
	subTotal := oSubPass + oSubFail
	subPct := 0.0
	if subTotal > 0 {
		subPct = 100 * float64(oSubPass) / float64(subTotal)
	}
	fmt.Fprintf(&b, "## Overall\n\n%d headless source files — the full corpus class, every one attempted: **%d passed**, %d failed, %d skipped (counted, reasons below). In wpt.fyi's unit these %d sources correspond to **%d generated test documents** (`META: global=` variant expansion; each source runs once here — no distinct per-scope globals exist in this runtime — so the expansion is denominator comparability, not extra coverage).\n\nOf the %d files that were in scope and compiled far enough to run, **%d passed (%.1f%%)** at the file level (a file passes only if *every* subtest in it passes).\n\n", len(all), oPass, oFail, oSkip, len(all), oDocs, ran, oPass, pct)
	fmt.Fprintf(&b, "**Subtest level (the metric WPT itself reports): %d / %d subtests passed (%.1f%%)** across the files that compiled and ran — each `test`/`promise_test`/`async_test` is one subtest. This is the finer, fairer number: a file with 30 passing subtests and 1 failing counts 30/31 here but 0 at the file level above.\n\n", oSubPass, subTotal, subPct)

	b.WriteString("## By area\n\n| Area | Files pass | Files fail | Files skip | Subtests pass/total |\n|---|---|---|---|---|\n")
	areas := make([]string, 0, len(byArea))
	for a := range byArea {
		areas = append(areas, a)
	}
	sort.Strings(areas)
	for _, a := range areas {
		s := byArea[a]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d/%d |\n", a, s.pass, s.fail, s.skip, s.subPass, s.subPass+s.subFail)
	}

	b.WriteString("\n## Top failure reasons\n\nBucketed first line of each FAIL — the leverage map for what to implement/fix next (a missing API compile-fails a whole file, masking every feature it co-exercises).\n\n| Count | Reason |\n|---|---|\n")
	writeReasonHist(&b, byFail, 40)

	b.WriteString("\n## Top skip reasons\n\nWhy out-of-scope files can't be attempted — a real DOM/browser global (no DOM here), a WebIDL reflection harness, or a shared helper script not in the fetched slice.\n\n| Count | Skip reason |\n|---|---|\n")
	writeReasonHist(&b, bySkip, 25)

	fmt.Fprintf(&b, "\n## Passing files (%d)\n\n| File | Area |\n|---|---|\n", len(passing))
	sort.Slice(passing, func(i, j int) bool { return passing[i].File < passing[j].File })
	for _, r := range passing {
		fmt.Fprintf(&b, "| `%s` | %s |\n", r.File, r.Area)
	}

	if err := updateConformanceSummary("wpt", laneLabel(), wptSummaryLane{
		Pass:         oPass,
		Fail:         oFail,
		Skip:         oSkip,
		Runnable:     ran,
		Total:        len(all),
		Documents:    oDocs,
		HTML:         stats.CSSHTML + stats.OtherHTML,
		SubtestPass:  oSubPass,
		SubtestTotal: subTotal,
	}, ""); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeReasonHist renders a count-sorted reason histogram into b, capped at n rows.
func writeReasonHist(b *strings.Builder, hist map[string]int, n int) {
	type rc struct {
		reason string
		n      int
	}
	var rows []rc
	for r, c := range hist {
		rows = append(rows, rc{r, c})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].reason < rows[j].reason
	})
	for i, r := range rows {
		if i >= n {
			break
		}
		fmt.Fprintf(b, "| %d | %s |\n", r.n, r.reason)
	}
}
