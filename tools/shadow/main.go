// Command shadow is `make shadow-report` (TDD-00230 P0.2): it compiles every
// corpus file with KML_SHADOW set, so the compiler compares each old codegen
// decider's answer with the new front end's, and ranks the disagreements of
// the whole run into a histogram, the way the conformance report ranks its
// blocked-by reasons.
//
//	go run ./tools/shadow [-bin ./klainmain] [-out .shadow-out/SHADOW-REPORT.md] [-limit N] [-manifest m1,m2]
//
// The corpus is the examples, tools and apps sources and Test262's
// language tests (a .js file in both emitter lanes); -roots narrows it.
// -manifest adds the Node and WPT conformance programs the conformance
// runner's -dump-manifest assembled, each in its own lane.
//
// Each run lists its units' outcome in units.tsv beside the report, and
// compares it with the previous run's (units.prev.tsv): a unit that was
// clean then and has a finding now (a disagreement, a codegen rejection,
// a failed compile) is a regression, listed in the report, and fails the
// run. A unit a run left no outcome for (one ranked again by -reuse from
// before outcomes were kept) is unknown, never clean. Until a new pass
// registers its oracle, every run is recorded with no disagreement.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"KlainMainLang/shadow"
)

func main() {
	bin := flag.String("bin", "./klainmain", "compiler binary")
	out := flag.String("out", ".shadow-out/SHADOW-REPORT.md", "report path")
	limit := flag.Int("limit", 0, "compile at most N corpus jobs (0: all)")
	timeout := flag.Duration("timeout", 60*time.Second, "per-file compile timeout")
	roots := flag.String("roots", "examples,tools,apps,.test262/test/language", "comma-separated corpus roots")
	reuse := flag.Bool("reuse", false, "rank the previous run's records again (its runs directory, same corpus) without compiling")
	manifests := flag.String("manifest", "", "comma-separated conformance -dump-manifest files whose programs join the corpus")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	absBin, err := filepath.Abs(*bin)
	if err != nil {
		fatal(err)
	}
	jobs := corpus(root, strings.Split(*roots, ","))
	if *manifests != "" {
		mj, err := manifestJobs(strings.Split(*manifests, ","))
		if err != nil {
			fatal(err)
		}
		jobs = append(jobs, mj...)
	}
	if *limit > 0 && len(jobs) > *limit {
		jobs = jobs[:*limit]
	}
	// Absolute: each compile runs in its source file's directory.
	work, err := filepath.Abs(filepath.Join(filepath.Dir(*out), "runs"))
	if err != nil {
		fatal(err)
	}
	if !*reuse {
		os.RemoveAll(work)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		fatal(err)
	}

	start := time.Now()
	var wg sync.WaitGroup
	ch := make(chan int)
	var mu sync.Mutex
	timedOut := 0
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				if !compile(absBin, jobs[i], filepath.Join(work, fmt.Sprintf("%06d", i)), *timeout) {
					mu.Lock()
					timedOut++
					fmt.Fprintf(os.Stderr, "timed out: %s\n", jobs[i].path)
					mu.Unlock()
				}
			}
		}()
	}
	for i := range jobs {
		if *reuse {
			break
		}
		ch <- i
	}
	close(ch)
	wg.Wait()

	// Concatenate the per-run records and rank them.
	var all bytes.Buffer
	files, _ := filepath.Glob(filepath.Join(work, "*.jsonl"))
	sort.Strings(files)
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			all.Write(b)
		}
	}
	tallies := shadow.Tallies(bytes.NewReader(all.Bytes()))
	buckets, total, err := shadow.Aggregate(&all)
	if err != nil {
		fatal(err)
	}
	failedRows, failedRuns := 0, 0
	failedFiles, _ := filepath.Glob(filepath.Join(work, "*.failed"))
	for _, f := range failedFiles {
		failedRuns++
		if b, err := os.ReadFile(f); err == nil {
			for _, l := range bytes.Split(b, []byte("\n")) {
				if bytes.Contains(l, []byte(`"old"`)) && !bytes.Contains(l, []byte(`"asked"`)) {
					failedRows++
				}
			}
		}
	}
	report := render(buckets, total, len(jobs), timedOut, time.Since(start))
	report = strings.Replace(report, "\n\n| Decider", fmt.Sprintf("\n- Not ranked: %d disagreements in the %d compile runs that failed\n\n| Decider", failedRows, failedRuns), 1)
	report += agreement(buckets, tallies)
	rejects, rejectSection := rejections(work)
	report = strings.Replace(report, "\n\n", fmt.Sprintf("\n- Codegen rejections of checker-accepted programs: %d\n\n", rejects), 1)
	report += rejectSection
	regressed, regressSection, err := unitDiff(filepath.Dir(*out), work, jobs)
	if err != nil {
		fatal(err)
	}
	report = strings.Replace(report, "\n\n", fmt.Sprintf("\n- Regressions since the previous run: %d\n\n", len(regressed)), 1)
	report += regressSection
	if err := os.WriteFile(*out, []byte(report), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("%d disagreements in %d buckets, %d codegen rejections, %d regressions, over %d compile runs; report written to %s\n", total, len(buckets), rejects, len(regressed), len(jobs), *out)
	if len(regressed) > 0 {
		os.Exit(1)
	}
}

type job struct {
	path string // relative to the repo root (absolute for a manifest's program)
	lane string // "" (a .ts file's default), "js" or "strict"
	name string // the test a manifest's program assembles ("": path)
}

func (j job) unit() string {
	n := j.path
	if j.name != "" {
		n = j.name
	}
	if j.lane == "strict" {
		return n + "#strict"
	}
	return n
}

// manifestJobs reads conformance -dump-manifest files: one assembled
// program per line, `program\tlane\ttest`.
func manifestJobs(paths []string) ([]job, error) {
	var jobs []job
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			f := strings.Split(l, "\t")
			if len(f) != 3 {
				continue
			}
			lane := f[1]
			if lane != "js" {
				lane = "strict"
			}
			jobs = append(jobs, job{path: f[0], lane: lane, name: f[2]})
		}
	}
	return jobs, nil
}

// unitDiff writes units.tsv (each unit's outcome: ok, or what it found)
// into dir, keeping the previous one as units.prev.tsv, and lists the units
// clean in the previous run that have a finding now.
func unitDiff(dir, work string, jobs []job) ([]string, string, error) {
	cur := filepath.Join(dir, "units.tsv")
	prev := filepath.Join(dir, "units.prev.tsv")
	old := map[string]string{}
	if b, err := os.ReadFile(cur); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if u, o, ok := strings.Cut(l, "\t"); ok {
				old[u] = o
			}
		}
		if err := os.WriteFile(prev, b, 0o644); err != nil {
			return nil, "", err
		}
	}
	var lines, regressed []string
	for i, j := range jobs {
		base := filepath.Join(work, fmt.Sprintf("%06d", i))
		outcome := "ok"
		st, stErr := os.ReadFile(base + ".status")
		if b, err := os.ReadFile(base + ".reject"); err == nil {
			_, msg, _ := strings.Cut(strings.TrimSpace(string(b)), "\t")
			outcome = "reject: " + reShape.ReplaceAllString(msg, "_")
		} else if _, err := os.Stat(base + ".failed"); err == nil || string(st) == "failed" {
			outcome = "failed"
		} else if b, err := os.ReadFile(base + ".jsonl"); err == nil {
			n := 0
			for _, l := range bytes.Split(b, []byte("\n")) {
				if bytes.Contains(l, []byte(`"old"`)) && !bytes.Contains(l, []byte(`"asked"`)) {
					n++
				}
			}
			if n > 0 {
				outcome = fmt.Sprintf("disagree: %d", n)
			}
		} else if stErr != nil {
			outcome = "unknown" // a run from before outcomes were recorded
		}
		u := j.unit()
		lines = append(lines, u+"\t"+outcome)
		if was, seen := old[u]; seen && was == "ok" && outcome != "ok" {
			regressed = append(regressed, u+" — "+outcome)
		}
	}
	sort.Strings(lines)
	if err := os.WriteFile(cur, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return nil, "", err
	}
	if len(regressed) == 0 {
		return nil, "", nil
	}
	sort.Strings(regressed)
	var b strings.Builder
	b.WriteString("\n## Regressions\n\nUnits clean in the previous run (units.prev.tsv) with a finding now.\n\n")
	for _, r := range regressed {
		fmt.Fprintf(&b, "- `%s`\n", r)
	}
	return regressed, b.String(), nil
}

// corpus lists the compile runs: the same corpus the IR equivalence tool
// uses, a .js file in both lanes.
func corpus(root string, roots []string) []job {
	var files []string
	for _, base := range roots {
		filepath.Walk(filepath.Join(root, base), func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() && info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if !info.IsDir() && (strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".js")) && !strings.Contains(p, "_FIXTURE") {
				files = append(files, p)
			}
			return nil
		})
	}
	sort.Strings(files)
	var jobs []job
	for _, f := range files {
		rel, _ := filepath.Rel(root, f)
		if strings.HasSuffix(f, ".js") {
			jobs = append(jobs, job{path: rel, lane: "js"}, job{path: rel, lane: "strict"})
		} else {
			jobs = append(jobs, job{path: rel})
		}
	}
	return jobs
}

// compile runs the compiler on one job with recording on (into
// base+".jsonl"); it reports false when the compile timed out. A Test262
// unit compiles with its harness, as the conformance runner assembles it,
// so its program is the one Test262 runs; the rows are moved back onto
// the test file's own lines, and the harness's are dropped.
func compile(bin string, j job, base string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := []string{"-emit-llvm"}
	if j.lane != "" {
		args = append(args, "-compat="+j.lane)
	}
	// The examples runner compiles this one example in the TC39 dialect.
	if filepath.Base(j.path) == "standard_decorators.ts" {
		args = append(args, "-decorators=standard")
	}
	abs, _ := filepath.Abs(j.path)
	src, offset := abs, 0
	if full, prefix, ok := withTest262Harness(abs); ok {
		src = base + ".js"
		if os.WriteFile(src, []byte(full), 0o644) == nil {
			offset = prefix
		} else {
			src = abs
		}
	}
	record := base + ".jsonl"
	cmd := exec.CommandContext(ctx, bin, append(args, src)...)
	cmd.Dir = filepath.Dir(abs)
	cmd.Env = append(os.Environ(), shadow.EnvVar+"="+record, shadow.EnvUnit+"="+j.unit())
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	failed := cmd.Run() != nil
	status := "ok"
	if failed {
		status = "failed"
	}
	os.WriteFile(base+".status", []byte(status), 0o644)
	if m := reCodegenErr.FindSubmatch(stderr.Bytes()); m != nil {
		// The checker accepted the program and codegen refused it (P2.7).
		os.WriteFile(base+".reject", []byte(j.unit()+"\t"+string(m[1])+"\n"), 0o644)
	}
	if offset > 0 {
		shiftRecord(record, offset)
		os.Remove(src)
	}
	if failed {
		// A program that does not compile has no decider to switch over;
		// its rows are counted apart.
		os.Rename(record, base+".failed")
	}
	return ctx.Err() == nil
}

var (
	// A codegen rejection, after its position: `codegen error: L:C: msg`.
	reCodegenErr = regexp.MustCompile(`codegen error: (?:\d+:\d+: )?([^\n]*)`)
	reIncludes   = regexp.MustCompile(`(?m)^includes:\s*\[([^\]]*)\]`)
	reFlags      = regexp.MustCompile(`(?m)^flags:\s*\[([^\]]*)\]`)
)

// withTest262Harness is a Test262 test file's source as the conformance
// runner compiles it (tools/conformance runOne): the harness shim, the
// async reporter, the includes, then the test; prefix is the number of
// lines before the test's first line.
func withTest262Harness(abs string) (full string, prefix int, ok bool) {
	slash := filepath.ToSlash(abs)
	i := strings.Index(slash, "/.test262/test/")
	if i < 0 {
		return "", 0, false
	}
	root := abs[:i]
	srcB, err := os.ReadFile(abs)
	if err != nil {
		return "", 0, false
	}
	src := string(srcB)
	var fm string
	if s, e := strings.Index(src, "/*---"), strings.Index(src, "---*/"); s >= 0 && e > s {
		fm = src[s:e]
	}
	list := func(re *regexp.Regexp) []string {
		m := re.FindStringSubmatch(fm)
		if m == nil {
			return nil
		}
		var out []string
		for _, it := range strings.Split(m[1], ",") {
			if it = strings.TrimSpace(it); it != "" {
				out = append(out, it)
			}
		}
		return out
	}
	flags := map[string]bool{}
	for _, f := range list(reFlags) {
		flags[f] = true
	}
	read := func(p string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(root, p))
		return string(b) + "\n", err == nil
	}
	var b strings.Builder
	if flags["onlyStrict"] {
		b.WriteString("\"use strict\";\n")
	}
	shim := filepath.Join("tools", "conformance", "harness-shim")
	if !flags["raw"] {
		for _, n := range []string{"sta.js", "assert.js"} {
			h, ok := read(filepath.Join(shim, n))
			if !ok {
				return "", 0, false
			}
			b.WriteString(h)
		}
	}
	if flags["async"] {
		h, ok := read(filepath.Join(shim, "doneprintHandle.js"))
		if !ok {
			return "", 0, false
		}
		b.WriteString(h)
	}
	for _, inc := range list(reIncludes) {
		h, ok := read(filepath.Join(".test262", "harness", inc))
		if !ok {
			return "", 0, false
		}
		b.WriteString(h)
	}
	prefix = strings.Count(b.String(), "\n")
	b.WriteString(src)
	return b.String(), prefix, true
}

// shiftRecord moves a harnessed run's rows of the program's own source back
// by the harness's lines, dropping the rows in the harness.
func shiftRecord(path string, offset int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var out bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var d shadow.Disagreement
		if json.Unmarshal(line, &d) != nil {
			out.Write(line)
			out.WriteByte('\n')
			continue
		}
		if d.Module == "" && d.Line > 0 {
			if d.Line <= offset {
				continue // the harness's own
			}
			d.Line -= offset
			if nb, err := json.Marshal(d); err == nil {
				line = nb
			}
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	os.WriteFile(path, out.Bytes(), 0o644)
}

func render(buckets []shadow.Bucket, total, runs, timedOut int, took time.Duration) string {
	var b strings.Builder
	b.WriteString("# Shadow report\n\n")
	b.WriteString("Where the new front end disagrees with the codegen decider it replaces. A decider switches over when its rows are gone.\n\n")
	fmt.Fprintf(&b, "- Compile runs: %d (%d timed out), %s\n", runs, timedOut, took.Round(time.Second))
	fmt.Fprintf(&b, "- Disagreements: %d in %d buckets\n\n", total, len(buckets))
	if total == 0 {
		b.WriteString("No disagreement was recorded.\n")
		return b.String()
	}
	perDecider := map[string]int{}
	for _, bk := range buckets {
		perDecider[bk.Decider] += bk.Count
	}
	var deciders []string
	for d := range perDecider {
		deciders = append(deciders, d)
	}
	sort.Slice(deciders, func(i, j int) bool { return perDecider[deciders[i]] > perDecider[deciders[j]] })
	b.WriteString("| Decider | Disagreements |\n|---|---|\n")
	for _, d := range deciders {
		fmt.Fprintf(&b, "| `%s` | %d |\n", d, perDecider[d])
	}
	b.WriteString("\n## Buckets\n\n| # | Decider | Node | Differing fields | Count | Example |\n|---|---|---|---|---|---|\n")
	for i, bk := range buckets {
		ex := bk.Examples[0]
		fmt.Fprintf(&b, "| %d | `%s` | %s | %s | %d | `%s:%d:%d` |\n", i+1, bk.Decider, bk.Kind, bk.Fields, bk.Count, where(ex), ex.Line, ex.Col)
	}
	b.WriteString("\n## Examples\n")
	for i, bk := range buckets {
		if i == 30 {
			break
		}
		fmt.Fprintf(&b, "\n### %d. `%s` on %s (%s)\n\n", i+1, bk.Decider, bk.Kind, bk.Fields)
		for _, ex := range bk.Examples {
			fmt.Fprintf(&b, "- `%s:%d:%d`\n  - old: `%s`\n  - new: `%s`\n", where(ex), ex.Line, ex.Col, clip(ex.Old), clip(ex.New))
		}
	}
	return b.String()
}

func clip(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "shadow:", err)
	os.Exit(1)
}

// where is the file a row's position is in: its library module, named
// with the run that compiled it, or the run's own file.
func where(d shadow.Disagreement) string {
	if d.Module != "" {
		return d.Module + " (via " + d.Unit + ")"
	}
	return d.Unit
}

// agreement is the per-decider, per-node-kind table of how often the two
// answers agree: a kind that agrees everywhere can switch to the new answer.
func agreement(buckets []shadow.Bucket, tallies map[string]int) string {
	if len(tallies) == 0 {
		return ""
	}
	dis := map[string]int{}
	for _, bk := range buckets {
		dis[bk.Decider+"/"+bk.Kind] += bk.Count
	}
	keys := make([]string, 0, len(tallies))
	for k := range tallies {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if dis[keys[i]] != dis[keys[j]] {
			return dis[keys[i]] < dis[keys[j]]
		}
		return tallies[keys[i]] > tallies[keys[j]]
	})
	var b strings.Builder
	b.WriteString("\n## Agreement by node kind\n\nNodes asked about (each once per compile run) and how many of them disagree.\n\n| Decider / node | Asked | Disagree | Agree |\n|---|---|---|---|\n")
	for _, k := range keys {
		a := tallies[k]
		pct := 100 * float64(a-dis[k]) / float64(a)
		fmt.Fprintf(&b, "| `%s` | %d | %d | %.1f%% |\n", k, a, dis[k], pct)
	}
	return b.String()
}

// reShape folds a rejection message's specifics (quoted names, numbers)
// so one cause ranks as one row.
var reShape = regexp.MustCompile(`'[^']*'|"[^"]*"|\b\d+\b`)

// rejections ranks the codegen rejections the runs recorded: programs
// the checker accepted and codegen refused, which P2.7 drives to none.
func rejections(work string) (int, string) {
	files, _ := filepath.Glob(filepath.Join(work, "*.reject"))
	type row struct {
		count   int
		example string
		msg     string
	}
	rows := map[string]*row{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		unit, msg, _ := strings.Cut(strings.TrimSpace(string(b)), "\t")
		key := reShape.ReplaceAllString(msg, "_")
		if r := rows[key]; r != nil {
			r.count++
		} else {
			rows[key] = &row{1, unit, msg}
		}
	}
	var keys []string
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if rows[keys[i]].count != rows[keys[j]].count {
			return rows[keys[i]].count > rows[keys[j]].count
		}
		return keys[i] < keys[j]
	})
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Codegen rejections\n\nPrograms the checker accepts and codegen refuses: %d runs in %d shapes.\n", len(files), len(keys))
	if len(keys) > 0 {
		b.WriteString("\n| # | Count | Message | Example |\n|---|---|---|---|\n")
		for i, k := range keys {
			r := rows[k]
			fmt.Fprintf(&b, "| %d | %d | %s | `%s` |\n", i+1, r.count, strings.ReplaceAll(clip(r.msg), "|", "\\|"), r.example)
		}
	}
	return len(files), b.String()
}
