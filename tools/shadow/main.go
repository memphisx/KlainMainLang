// Command shadow is `make shadow-report` (TDD-00230 P0.2): it compiles every
// corpus file with KML_SHADOW set, so the compiler compares each old codegen
// decider's answer with the new front end's, and ranks the disagreements of
// the whole run into a histogram, the way the conformance report ranks its
// blocked-by reasons.
//
//	go run ./tools/shadow [-bin ./klainmain] [-out .shadow-out/SHADOW-REPORT.md] [-limit N]
//
// The corpus is the examples, tools and apps sources and Test262's
// language tests (a .js file in both emitter lanes); -roots narrows it. Until a new pass
// registers its oracle, every run is recorded with no disagreement.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	if *limit > 0 && len(jobs) > *limit {
		jobs = jobs[:*limit]
	}
	work := filepath.Join(filepath.Dir(*out), "runs")
	os.RemoveAll(work)
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
				if !compile(absBin, jobs[i], filepath.Join(work, fmt.Sprintf("%06d.jsonl", i)), *timeout) {
					mu.Lock()
					timedOut++
					mu.Unlock()
				}
			}
		}()
	}
	for i := range jobs {
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
	buckets, total, err := shadow.Aggregate(&all)
	if err != nil {
		fatal(err)
	}
	report := render(buckets, total, len(jobs), timedOut, time.Since(start))
	if err := os.WriteFile(*out, []byte(report), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("%d disagreements in %d buckets over %d compile runs; report written to %s\n", total, len(buckets), len(jobs), *out)
}

type job struct {
	path string // relative to the repo root
	lane string // "" (a .ts file's default), "js" or "strict"
}

func (j job) unit() string {
	if j.lane == "strict" {
		return j.path + "#strict"
	}
	return j.path
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
			jobs = append(jobs, job{rel, "js"}, job{rel, "strict"})
		} else {
			jobs = append(jobs, job{rel, ""})
		}
	}
	return jobs
}

// compile runs the compiler on one job with recording on; it reports false
// when the compile timed out.
func compile(bin string, j job, record string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := []string{"-emit-llvm"}
	if j.lane != "" {
		args = append(args, "-compat="+j.lane)
	}
	abs, _ := filepath.Abs(j.path)
	cmd := exec.CommandContext(ctx, bin, append(args, abs)...)
	cmd.Dir = filepath.Dir(abs)
	cmd.Env = append(os.Environ(), shadow.EnvVar+"="+record, shadow.EnvUnit+"="+j.unit())
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.Run()
	return ctx.Err() == nil
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
		fmt.Fprintf(&b, "| %d | `%s` | %s | %s | %d | `%s:%d:%d` |\n", i+1, bk.Decider, bk.Kind, bk.Fields, bk.Count, ex.Unit, ex.Line, ex.Col)
	}
	b.WriteString("\n## Examples\n")
	for i, bk := range buckets {
		if i == 30 {
			break
		}
		fmt.Fprintf(&b, "\n### %d. `%s` on %s (%s)\n\n", i+1, bk.Decider, bk.Kind, bk.Fields)
		for _, ex := range bk.Examples {
			fmt.Fprintf(&b, "- `%s:%d:%d`\n  - old: `%s`\n  - new: `%s`\n", ex.Unit, ex.Line, ex.Col, clip(ex.Old), clip(ex.New))
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
