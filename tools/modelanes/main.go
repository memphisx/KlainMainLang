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
// a mismatch. Examples run one at a time: several start servers on fixed
// ports. `make mode-lanes` starts the httpbin-lite fixture the fetch
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
	"sort"
	"strings"
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

type run struct {
	out    string
	status string // "exit 0", "exit 3", "timeout", "build: …"
}

func main() {
	bin := flag.String("bin", "./klainmain", "compiler binary")
	out := flag.String("out", ".modelanes-out/MODE-LANES.md", "report path")
	only := flag.String("only", "", "only examples matching this regexp")
	timeout := flag.Duration("timeout", 300*time.Second, "per-run time limit")
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
	for _, ex := range examples() {
		if re != nil && !re.MatchString(ex) {
			continue
		}
		// Every build of an example goes to the same path: an example may
		// print its own path (process.argv).
		exe := filepath.Join(work, exeName(ex))
		base := buildAndRun(absBin, ex, nil, exe, *timeout)
		if strings.HasPrefix(base.status, "build:") {
			results = append(results, result{ex, "default", "rejected", base.status})
			continue
		}
		again := runBinary(exe, *timeout)
		if again.out != base.out || again.status != base.status {
			nondeterministic = append(nondeterministic, ex)
			continue
		}
		unstable := false
		for _, m := range modes {
			if unstable {
				break
			}
			if m.name == "-mm=gc" && notUnderGC[ex] {
				continue
			}
			flags := m.flags
			if pinned[ex] {
				flags = without(flags, "-mm=auto")
				if len(flags) == 0 {
					continue
				}
			}
			r := buildAndRun(absBin, ex, flags, exe, *timeout)
			switch {
			case strings.HasPrefix(r.status, "build:") && m.name == "-mm=gc" && strings.Contains(strings.ToLower(r.status), "gc"):
				results = append(results, result{ex, m.name, "unavailable", r.status})
			case strings.HasPrefix(r.status, "build:"):
				results = append(results, result{ex, m.name, "rejected", r.status})
			case r.out != base.out || r.status != base.status:
				// Rerun both before calling it a mismatch: timings and the
				// order of concurrent output vary between runs of one build.
				r2 := runBinary(exe, *timeout)
				rebuilt := buildAndRun(absBin, ex, nil, exe, *timeout)
				if r2.out != r.out || r2.status != r.status || rebuilt.out != base.out || rebuilt.status != base.status {
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

// buildAndRun builds ex with flags into exe and, with a nonzero timeout,
// runs it.
func buildAndRun(bin, ex string, flags []string, exe string, timeout time.Duration) run {
	args := append(append([]string{}, flags...), "-o", exe, ex)
	var stderr bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return run{status: "build: " + firstLine(stderr.String())}
	}
	if timeout == 0 {
		return run{}
	}
	return runBinary(exe, timeout)
}

func runBinary(exe string, timeout time.Duration) run {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, exe)
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
