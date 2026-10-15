// testshard runs the tests/ suite in shards: it compiles the test binary
// once, deals the tests round-robin into -shards shards and runs one shard
// (-shard i, a CI matrix job, its tests dealt again over -j concurrent
// processes so the job uses every core) or all of them concurrently
// (-shard -1, `make test-par`). A test that fails is re-run alone, serially: one that
// only failed under concurrency (a fixed port, signal timing) does not fail
// the run, one that fails again does.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

func main() {
	shard := flag.Int("shard", -1, "the shard to run, 0-based; -1 runs every shard concurrently")
	shards := flag.Int("shards", 4, "how many shards the suite is dealt into")
	timeout := flag.String("timeout", "50m", "each shard's go test timeout")
	pkg := flag.String("pkg", "./tests/", "the package whose tests are sharded")
	jobs := flag.Int("j", runtime.NumCPU(), "with -shard i: concurrent test processes the shard is run in")
	flag.Parse()
	if *shards < 1 || *shard >= *shards {
		fatal("need 0 <= -shard < -shards, or -shard -1")
	}
	boundRun()
	os.Exit(run(*pkg, *shard, *shards, *jobs, *timeout))
}

func run(pkg string, shard, shards, jobs int, timeout string) int {
	dir, err := os.MkdirTemp("", "kml-testshard")
	if err != nil {
		fatal(err.Error())
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "kml_tests.test")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := command("go", "test", pkg, "-c", "-o", bin).Run(); err != nil {
		fatal("building the test binary: " + err.Error())
	}
	names, err := listTests(bin)
	if err != nil {
		fatal("listing the tests: " + err.Error())
	}
	dealt := make([][]string, shards)
	for i, n := range names {
		dealt[i%shards] = append(dealt[i%shards], n)
	}
	total := len(names)
	if shard >= 0 {
		// One shard: its tests, dealt over jobs processes.
		total = len(dealt[shard])
		if jobs < 1 {
			jobs = 1
		}
		mine := dealt[shard]
		dealt = make([][]string, jobs)
		for i, n := range mine {
			dealt[i%jobs] = append(dealt[i%jobs], n)
		}
	}
	// A group whose pattern would not fit one command line runs as several
	// processes in turn (runBatches).
	var todo []int
	for i := range dealt {
		if len(dealt[i]) > 0 {
			todo = append(todo, i)
		}
	}

	type result struct {
		out    []byte
		failed []string
		err    error
	}
	results := make([]result, len(dealt))
	var wg sync.WaitGroup
	for _, i := range todo {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var all []byte
			var firstErr error
			for _, part := range runBatches(dealt[i]) {
				c := exec.Command(bin, "-test.run", anchored(part), "-test.timeout", timeout)
				c.Dir = pkgDir(pkg)
				out, err := c.CombinedOutput()
				all = append(all, out...)
				if err != nil && firstErr == nil {
					firstErr = err
				}
			}
			results[i] = result{all, failedTests(all), firstErr}
		}(i)
	}
	wg.Wait()

	var failed []string
	for _, i := range todo {
		r := results[i]
		if r.err == nil {
			continue
		}
		if len(r.failed) == 0 {
			// A panic or a timeout: no test-level FAIL line to re-run.
			fmt.Printf("group %d failed with no test-level FAIL (panic/timeout) — full output:\n", i)
			os.Stdout.Write(r.out)
			return 1
		}
		failed = append(failed, r.failed...)
	}
	if len(failed) == 0 {
		if shard >= 0 {
			fmt.Printf("ok  tests (shard %d of %d, %d tests in %d processes)\n", shard, shards, total, len(todo))
		} else {
			fmt.Printf("ok  tests (%d of %d shards, %d tests)\n", len(todo), shards, total)
		}
		return 0
	}
	fmt.Printf("parallel run had failures; re-running serially to rule out concurrency flakes: %s\n", strings.Join(failed, " "))
	rc := 0
	for _, part := range runBatches(failed) {
		c := command(bin, "-test.run", anchored(part), "-test.v", "-test.timeout", timeout)
		c.Dir = pkgDir(pkg)
		if err := c.Run(); err != nil {
			rc = 1
		}
	}
	return rc
}

// maxRunArg bounds one -test.run pattern: Windows limits a whole command line
// to 32767 characters.
const maxRunArg = 24000

// runBatches splits names into lists whose anchored pattern fits maxRunArg.
func runBatches(names []string) [][]string {
	var out [][]string
	var cur []string
	size := 4
	for _, n := range names {
		if len(cur) > 0 && size+len(n)+1 > maxRunArg {
			out = append(out, cur)
			cur, size = nil, 4
		}
		cur = append(cur, n)
		size += len(n) + 1
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// listTests is every top-level test of the binary, in its own order.
func listTests(bin string) ([]string, error) {
	out, err := exec.Command(bin, "-test.list", ".*").Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "Test") {
			names = append(names, strings.TrimSpace(l))
		}
	}
	return names, nil
}

var failLine = regexp.MustCompile(`(?m)^--- FAIL: (\S+)`)

// failedTests is the top-level tests output reports as failed.
func failedTests(out []byte) []string {
	var names []string
	for _, m := range failLine.FindAllSubmatch(out, -1) {
		if name := string(m[1]); !strings.Contains(name, "/") {
			names = append(names, name)
		}
	}
	return names
}

// pkgDir is the directory a test binary runs in: its package's, as go test
// runs it, so a test's relative paths (testdata/, ../apps) resolve.
func pkgDir(pkg string) string {
	d, err := filepath.Abs(filepath.FromSlash(pkg))
	if err != nil {
		return ""
	}
	return d
}

func anchored(names []string) string {
	return "^(" + strings.Join(names, "|") + ")$"
}

func command(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "testshard: "+msg)
	os.Exit(2)
}
