// runexamples is `make examples`: it compiles every example concurrently
// (-j jobs; compiling is most of the time) and then runs the binaries one
// at a time in order, since examples share the httpbin fixture. Each run
// gets a free port in PORT, so a server example never depends on a fixed
// port being unused on the machine. Each example prints one OK/FAIL line; the exit status is
// non-zero when any failed.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// job is one example: its source, the compile flags beyond the mode
// flags, and the label suffix it prints with.
type job struct {
	src   string
	flags []string
	label string
	ok    bool
	why   string // the compiler's output when it failed
}

func main() {
	bin := flag.String("bin", "./klainmain", "the compiler")
	jobs := flag.Int("j", runtime.NumCPU(), "concurrent compiles")
	timeout := flag.Duration("timeout", 300*time.Second, "each example's run time limit")
	mode := flag.String("modeflags", "", "flags every example compiles with (-mm, -optimize-memory)")
	modeNoMM := flag.String("modeflags-nomm", "", "the mode flags for an example that picks its own -mm")
	listOnly := flag.Bool("list", false, "print the .ts examples, one per line, and exit")
	shard := flag.Int("shard", -1, "run only this shard of the examples, 0-based; -1 runs them all")
	shards := flag.Int("shards", 1, "how many shards the examples are dealt into")
	flag.Parse()

	list := examples(*mode, *modeNoMM)
	if *shard >= 0 && !*listOnly {
		var mine []*job
		for i, j := range list {
			if i%*shards == *shard {
				mine = append(mine, j)
			}
		}
		list = mine
	}
	if *listOnly {
		for _, j := range list {
			if j.label == "" {
				fmt.Println(j.src)
			}
		}
		return
	}
	compile(*bin, list, *jobs)
	ok, fail := 0, 0
	for _, j := range list {
		fmt.Printf("%-50s", "  "+j.src+j.label)
		why := j.why
		if j.ok {
			why = runOne(j.src, *timeout)
		}
		if why == "" {
			fmt.Println("OK")
			ok++
		} else {
			fmt.Println("FAIL")
			fmt.Println(indent(why))
			fail++
		}
	}
	fmt.Printf("\nResults: %d passed, %d failed\n", ok, fail)
	if fail > 0 {
		os.Exit(1)
	}
}

// examples lists every example in the order the gate runs them: the .ts
// examples, the -compat=js ones, the standard-decorator one.
func examples(mode, modeNoMM string) []*job {
	modeF, noMMF := strings.Fields(mode), strings.Fields(modeNoMM)
	ownMM := map[string]bool{"examples/memory/memory_free.ts": true, "examples/finalization/finalization_registry.ts": true}
	var ts, js []string
	filepath.WalkDir("examples", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		p = filepath.ToSlash(p)
		if d.IsDir() {
			if p == "examples/tls" || p == "examples/webview" {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(p, ".ts") && !strings.HasSuffix(p, "_worker.ts") && filepath.Base(p) != "standard_decorators.ts" && filepath.Base(p) != "unused_checks.ts":
			ts = append(ts, p)
		case strings.HasPrefix(p, "examples/jsmode/") && strings.HasSuffix(p, ".js") && !strings.Contains(strings.TrimPrefix(p, "examples/jsmode/"), "/"):
			js = append(js, p)
		}
		return nil
	})
	sort.Strings(ts)
	sort.Strings(js)
	var out []*job
	for _, p := range ts {
		f := modeF
		if ownMM[p] {
			f = noMMF
		}
		out = append(out, &job{src: p, flags: f})
	}
	for _, p := range js {
		out = append(out, &job{src: p, flags: append(append([]string{}, modeF...), "-compat=js"), label: " (-compat=js)"})
	}
	if _, err := os.Stat("examples/decorators/standard_decorators.ts"); err == nil {
		out = append(out, &job{src: "examples/decorators/standard_decorators.ts", flags: append(append([]string{}, modeF...), "-decorators=standard"), label: " (-decorators=standard)"})
	}
	if _, err := os.Stat("examples/types/unused_checks.ts"); err == nil {
		out = append(out, &job{src: "examples/types/unused_checks.ts", flags: append(append([]string{}, modeF...), "-no-unused-locals", "-no-unused-parameters"), label: " (-no-unused-locals -no-unused-parameters)"})
	}
	return out
}

// compile builds every job with n concurrent compiler processes.
func compile(bin string, list []*job, n int) {
	if n < 1 {
		n = 1
	}
	work := make(chan *job)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				cmd := exec.Command(bin, append(append([]string{}, j.flags...), j.src)...)
				out, err := cmd.CombinedOutput()
				j.ok = err == nil
				if err != nil {
					j.why = strings.TrimRight(fmt.Sprintf("did not compile: %v\n%s", err, out), "\n")
				}
			}
		}()
	}
	for _, j := range list {
		work <- j
	}
	close(work)
	wg.Wait()
}

// runOne runs src's binary with no stdin, killed past the time limit, and
// returns why it failed (its last output with it), "" when it passed.
func runOne(src string, limit time.Duration) string {
	out := strings.TrimSuffix(strings.TrimSuffix(src, ".ts"), ".js")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	var log tailBuffer
	cmd := exec.Command("./" + out)
	cmd.Env = append(os.Environ(), "PORT="+freePort())
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		return err.Error()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return ""
		}
		return err.Error() + "\n" + log.String()
	case <-time.After(limit):
		cmd.Process.Kill()
		<-done
		return fmt.Sprintf("timed out after %s\n%s", limit, log.String())
	}
}

// tailBuffer keeps the last 4 KiB written to it: what a failed example
// printed last.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if n := len(t.buf); n > 4096 {
		t.buf = append(t.buf[:0], t.buf[n-4096:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimRight(string(t.buf), "\n")
}

// indent prefixes each line of s for the gate's log.
func indent(s string) string {
	return "      | " + strings.ReplaceAll(s, "\n", "\n      | ")
}

// freePort asks the kernel for an unused TCP port and releases it for the
// example to bind.
func freePort() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "0"
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}
