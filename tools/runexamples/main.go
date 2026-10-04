// runexamples is `make examples`: it compiles every example concurrently
// (-j jobs; compiling is most of the time) and then runs the binaries one
// at a time in order, since examples share fixed ports and the httpbin
// fixture. Each example prints one OK/FAIL line; the exit status is
// non-zero when any failed.
package main

import (
	"flag"
	"fmt"
	"io/fs"
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
}

func main() {
	bin := flag.String("bin", "./klainmain", "the compiler")
	jobs := flag.Int("j", runtime.NumCPU(), "concurrent compiles")
	timeout := flag.Duration("timeout", 300*time.Second, "each example's run time limit")
	mode := flag.String("modeflags", "", "flags every example compiles with (-mm, -optimize-memory)")
	modeNoMM := flag.String("modeflags-nomm", "", "the mode flags for an example that picks its own -mm")
	listOnly := flag.Bool("list", false, "print the .ts examples, one per line, and exit")
	flag.Parse()

	list := examples(*mode, *modeNoMM)
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
		if j.ok && runOne(j.src, *timeout) {
			fmt.Println("OK")
			ok++
		} else {
			fmt.Println("FAIL")
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
		case strings.HasSuffix(p, ".ts") && !strings.HasSuffix(p, "_worker.ts") && filepath.Base(p) != "standard_decorators.ts":
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
				j.ok = cmd.Run() == nil
			}
		}()
	}
	for _, j := range list {
		work <- j
	}
	close(work)
	wg.Wait()
}

// runOne runs src's binary with no stdin, its output discarded, killed past
// the time limit.
func runOne(src string, limit time.Duration) bool {
	out := strings.TrimSuffix(strings.TrimSuffix(src, ".ts"), ".js")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("./" + out)
	if err := cmd.Start(); err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(limit):
		cmd.Process.Kill()
		<-done
		return false
	}
}
