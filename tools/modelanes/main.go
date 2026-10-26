// Command modelanes is `make mode-lanes` (TDD-00230 P0.3): every example is
// built and run in the default memory mode and again in each other mode, and
// its output must be the same. The restructure rewrites the code escape
// analysis and free insertion sit on, so a mode that diverges is a bug the
// default-mode suites cannot see.
//
//	go run ./tools/modelanes [-bin ./klainmain] [-out .modelanes-out/MODE-LANES.md] [-only regexp]
//
// An example whose two default-mode runs differ (timing, dates, randomness),
// or whose runs differ again when a mismatch is rerun, is reported as
// nondeterministic and not compared. A mode that rejects an
// example at compile time by design (Memory.free under -mm=auto) or cannot
// build at all on this machine (-mm=gc without libgc) is reported apart from
// a mismatch. Every build runs concurrently (-j; compiling is most of the
// time); the binaries then run one at a time. `make mode-lanes` starts the httpbin-lite fixture the fetch
// examples talk to, as `make examples` does.
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
	"time"
)

type mode struct {
	name  string
	flags []string
}

var modes = []mode{
	{"-mm=auto", []string{"-mm=auto"}},
	{"-mm=auto -optimize-memory", []string{"-mm=auto", "-optimize-memory"}},
	{"-mm=gc", []string{"-mm=gc"}},
}

// pinned examples keep the default memory mode in every lane (as in
// `make examples`): Memory.free is a compile error under -mm=auto by design.
var pinned = map[string]bool{
	"examples/memory/memory_free.ts":                 true,
	"examples/finalization/finalization_registry.ts": true,
}

// notUnderGC lists examples whose output depends on when a collector runs:
// FinalizationRegistry cleanups are mode-dependent by design (TDD-00163).
var notUnderGC = map[string]bool{
	"examples/finalization/finalization_registry.ts": true,
}

// runArgs gives an example the arguments its run takes: one that reads its
// own binary by default (which differs per mode) reads its source instead.
var runArgs = map[string][]string{
	"examples/readline/lines.ts": {"examples/readline/lines.ts"},
}

type run struct {
	out    string
	status string // "exit 0", "exit 3", "timeout", "build: …"
}

func main() {
	bin := flag.String("bin", "./klainmain", "compiler binary")
	out := flag.String("out", ".modelanes-out/MODE-LANES.md", "report path")
	only := flag.String("only", "", "only examples matching this regexp")
	timeout := flag.Duration("timeout", 300*time.Second, "per-run time limit")
	jobs := flag.Int("j", runtime.NumCPU(), "concurrent builds")
	flag.Parse()

	absBin, err := filepath.Abs(*bin)
	if err != nil {
		fatal(err)
	}
	var re *regexp.Regexp
	if *only != "" {
		re = regexp.MustCompile(*only)
	}
	work, err := filepath.Abs(filepath.Join(filepath.Dir(*out), "bin"))
	if err != nil {
		fatal(err)
	}
	os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o755); err != nil {
		fatal(err)
	}

	type result struct {
		example string
		mode    string
		kind    string // mismatch, rejected, unavailable
		detail  string
	}
	var results []result
	var nondeterministic []string
	compared := 0
	start := time.Now()
	var exs []string
	for _, ex := range examples() {
		if re == nil || re.MatchString(ex) {
			exs = append(exs, ex)
		}
	}
	// Build every example in every lane at once, each lane into its own
	// directory; lane 0 is the default mode.
	lanes := make([][]string, len(modes)+1)
	for k, m := range modes {
		lanes[k+1] = m.flags
	}
	built := make([][]run, len(exs))
	laneExe := func(k int, ex string) string {
		return filepath.Join(work, fmt.Sprint("lane", k), exeName(ex))
	}
	type job struct{ e, k int }
	jobCh := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < *jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for jb := range jobCh {
				built[jb.e][jb.k] = buildOnly(absBin, exs[jb.e], laneFlags(exs[jb.e], jb.k, lanes), laneExe(jb.k, exs[jb.e]))
			}
		}()
	}
	for k := range lanes {
		os.MkdirAll(filepath.Join(work, fmt.Sprint("lane", k)), 0o755)
	}
	for e := range exs {
		built[e] = make([]run, len(lanes))
	}
	for e, ex := range exs {
		for k := range lanes {
			if laneFlags(ex, k, lanes) != nil || k == 0 {
				jobCh <- job{e, k}
			}
		}
	}
	close(jobCh)
	wg.Wait()
	fmt.Printf("built %d examples in %d lanes (%s)\n", len(exs), len(lanes), time.Since(start).Round(time.Second))

	// Run them one at a time. Every run goes through the same path: an
	// example may print its own path (process.argv).
	for e, ex := range exs {
		exe := filepath.Join(work, exeName(ex))
		runLane := func(k int) run {
			if err := copyFile(laneExe(k, ex), exe); err != nil {
				return run{status: "build: " + err.Error()}
			}
			return runBinary(exe, runArgs[ex], *timeout)
		}
		if built[e][0].status != "" {
			results = append(results, result{ex, "default", "rejected", built[e][0].status})
			continue
		}
		base := runLane(0)
		again := runBinary(exe, runArgs[ex], *timeout)
		if again.out != base.out || again.status != base.status {
			nondeterministic = append(nondeterministic, ex)
			continue
		}
		unstable := false
		for k := 1; k < len(lanes) && !unstable; k++ {
			m := modes[k-1]
			if laneFlags(ex, k, lanes) == nil {
				continue
			}
			if st := built[e][k].status; st != "" {
				kind := "rejected"
				if m.name == "-mm=gc" && strings.Contains(strings.ToLower(st), "gc") {
					kind = "unavailable"
				}
				results = append(results, result{ex, m.name, kind, st})
				continue
			}
			r := runLane(k)
			if r.out != base.out || r.status != base.status {
				// Rerun both before calling it a mismatch: timings and the
				// order of concurrent output vary between runs of one build.
				r2 := runBinary(exe, runArgs[ex], *timeout)
				b2 := runLane(0)
				if r2.out != r.out || r2.status != r.status || b2.out != base.out || b2.status != base.status {
					unstable = true
					continue
				}
				results = append(results, result{ex, m.name, "mismatch", describe(base, r)})
			}
		}
		if unstable {
			nondeterministic = append(nondeterministic, ex)
			continue
		}
		compared++
		fmt.Printf("%s\n", ex)
	}

	var b strings.Builder
	b.WriteString("# Mode lanes\n\n")
	b.WriteString("Every example built and run in the default memory mode and in each other mode; the output must match.\n\n")
	counts := map[string]int{}
	for _, r := range results {
		counts[r.kind]++
	}
	fmt.Fprintf(&b, "- Examples compared: %d (%s)\n", compared, time.Since(start).Round(time.Second))
	fmt.Fprintf(&b, "- Mismatches: %d\n- Rejected by a mode: %d\n- Mode unavailable: %d\n- Nondeterministic (not compared): %d\n\n",
		counts["mismatch"], counts["rejected"], counts["unavailable"], len(nondeterministic))
	for _, kind := range []string{"mismatch", "rejected", "unavailable"} {
		var rows []result
		for _, r := range results {
			if r.kind == kind {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", map[string]string{"mismatch": "Mismatches", "rejected": "Rejected by a mode", "unavailable": "Mode unavailable"}[kind])
		for _, r := range rows {
			fmt.Fprintf(&b, "### `%s` under `%s`\n\n```\n%s\n```\n\n", r.example, r.mode, clip(r.detail, 1500))
		}
	}
	if len(nondeterministic) > 0 {
		sort.Strings(nondeterministic)
		b.WriteString("## Nondeterministic\n\n")
		for _, ex := range nondeterministic {
			fmt.Fprintf(&b, "- `%s`\n", ex)
		}
	}
	if err := os.WriteFile(*out, []byte(b.String()), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("%d mismatches, %d rejected, %d unavailable, %d nondeterministic over %d examples; report written to %s\n",
		counts["mismatch"], counts["rejected"], counts["unavailable"], len(nondeterministic), compared, *out)
	if counts["mismatch"] > 0 {
		os.Exit(1)
	}
}

// examples lists the example corpus exactly as `make examples` does.
func examples() []string {
	var out []string
	filepath.Walk("examples", func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".ts") {
			return nil
		}
		if strings.HasSuffix(p, "_worker.ts") || strings.HasPrefix(p, "examples/tls/") ||
			strings.HasPrefix(p, "examples/webview/") || filepath.Base(p) == "standard_decorators.ts" {
			return nil
		}
		out = append(out, filepath.ToSlash(p))
		return nil
	})
	sort.Strings(out)
	return out
}

func exeName(ex string) string {
	return strings.NewReplacer("/", "__", ".ts", "").Replace(ex)
}

// laneFlags is ex's flags in lane k (nil in lane 0, and for a lane ex
// skips): a pinned example drops -mm=auto, and some skip -mm=gc.
func laneFlags(ex string, k int, lanes [][]string) []string {
	if k == 0 {
		return nil
	}
	if modes[k-1].name == "-mm=gc" && notUnderGC[ex] {
		return nil
	}
	flags := lanes[k]
	if pinned[ex] {
		flags = without(flags, "-mm=auto")
	}
	if len(flags) == 0 {
		return nil
	}
	return flags
}

// buildOnly builds ex with flags into exe; a failed build's status is
// "build: <first stderr line>", a good one's empty.
func buildOnly(bin, ex string, flags []string, exe string) run {
	args := append(append([]string{}, flags...), "-o", exe, ex)
	var stderr bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return run{status: "build: " + firstLine(stderr.String())}
	}
	return run{}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	os.Remove(dst)
	return os.WriteFile(dst, data, 0o755)
}

func runBinary(exe string, args []string, timeout time.Duration) run {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdout = &stdout
	cmd.Stdin = nil
	err := cmd.Run()
	status := "exit 0"
	switch {
	case ctx.Err() != nil:
		status = "timeout"
	case err != nil:
		if ee, ok := err.(*exec.ExitError); ok {
			status = fmt.Sprintf("exit %d", ee.ExitCode())
		} else {
			status = err.Error()
		}
	}
	return run{out: stdout.String(), status: status}
}

// describe shows where a mode's run departs from the default's.
func describe(base, r run) string {
	if base.status != r.status {
		return fmt.Sprintf("status: default %s, this mode %s\n%s", base.status, r.status, firstDiff(base.out, r.out))
	}
	return firstDiff(base.out, r.out)
}

func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return fmt.Sprintf("first difference at line %d:\n- default: %s\n- mode:    %s", i+1, x, y)
		}
	}
	return "outputs equal"
}

func without(xs []string, drop string) []string {
	var out []string
	for _, x := range xs {
		if x != drop {
			out = append(out, x)
		}
	}
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "modelanes:", err)
	os.Exit(1)
}
