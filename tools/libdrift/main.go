// libdrift measures how far the builtin library is from compiling once
// (TDD-00238 Stage 4): it emits the IR of several programs and reports each
// library function whose body is not the same in all of them. A library
// object can be shared only once that list is empty.
//
//	go run ./tools/libdrift [-bin ./klainmain] [-l] [-v name] [-sym @sym] examples/a.ts ...
//	go run ./tools/libdrift -repeat 5 examples/*.ts   # same IR on every compile
//
// The summary counts the symbols the differing lines name, numbers folded, as a
// histogram of the causes.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
)

var (
	// A library function, a literal inside one (named by its module and
	// position), or a module's init.
	defineRe = regexp.MustCompile(`(?m)^define [^\n]*?@("?(?:[^\s(]*__kml_modL|[^\s(]*\.node_|__kml_lib_)[^\s(]*"?)\(`)
	symRe    = regexp.MustCompile(`@[\w.$"]+`)
	numRe    = regexp.MustCompile(`\d+`)
	// A program-numbered symbol (`@__kml_x_12`): fine in the program's own
	// code, a hazard in the library's, which must not depend on numbering.
	counterRe = regexp.MustCompile(`@"?[A-Za-z_][\w.]*?_\d+\b`)
	// A literal named by its library position (`@__closure.node_fs.12_7`).
	posRe = regexp.MustCompile(`\.node_\w+\.(\w+\.)?\d+_\d+|^@pcre2_`)
)

func main() {
	bin := flag.String("bin", "./klainmain", "the compiler")
	show := flag.String("v", "", "print the differing bodies of this function")
	top := flag.Int("top", 30, "histogram rows")
	jobs := flag.Int("j", runtime.NumCPU(), "concurrent compiles")
	fail := flag.Bool("fail", false, "exit 1 when a library function differs or library code names a program-numbered symbol")
	repeat := flag.Int("repeat", 0, "instead: compile each program this many times and list those whose IR is not the same every time")
	list := flag.Bool("l", false, "list the differing functions")
	rt := flag.Bool("runtime", false, "measure the runtime's routines (__kml_*, not content-named) instead of the library's")
	bySym := flag.String("sym", "", "list the differing functions whose differences name this symbol (numbers folded)")
	flag.Parse()
	// name → body → first program that emitted it
	bodies := map[string]map[string]string{}
	if *repeat > 1 {
		os.Exit(determinism(*bin, flag.Args(), *repeat, *jobs))
	}
	irs := make([]map[string]string, len(flag.Args()))
	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < *jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				src := flag.Args()[i]
				out, err := exec.Command(*bin, "-emit-llvm", src).Output()
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s: %v\n", src, err)
					os.Exit(1)
				}
				if *rt {
					irs[i] = runtimeFunctions(string(out))
				} else {
					irs[i] = libFunctions(string(out))
				}
			}
		}()
	}
	for i := range flag.Args() {
		work <- i
	}
	close(work)
	wg.Wait()
	for i, src := range flag.Args() {
		for name, body := range irs[i] {
			if bodies[name] == nil {
				bodies[name] = map[string]string{}
			}
			if _, ok := bodies[name][body]; !ok {
				bodies[name][body] = src
			}
		}
	}
	var differing []string
	hist := map[string]int{}
	for name, bs := range bodies {
		if len(bs) < 2 {
			continue
		}
		differing = append(differing, name)
		syms := diffSymbols(bs)
		for sym := range syms {
			hist[sym]++
		}
		if syms[*bySym] {
			fmt.Println("  " + name)
		}
	}
	sort.Strings(differing)
	fmt.Printf("library functions: %d, differing: %d\n", len(bodies), len(differing))
	if *list {
		for _, name := range differing {
			fmt.Println("  " + name)
		}
	}
	type row struct {
		sym string
		n   int
	}
	var rows []row
	for s, n := range hist {
		rows = append(rows, row{s, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].n > rows[j].n || rows[i].n == rows[j].n && rows[i].sym < rows[j].sym
	})
	for i, r := range rows {
		if i == *top {
			break
		}
		fmt.Printf("%6d  %s\n", r.n, r.sym)
	}
	// Counter-named symbols library code references, even where they agree
	// in this sample.
	counters := map[string]int{}
	for _, bs := range bodies {
		for body := range bs {
			for _, sym := range counterRe.FindAllString(body, -1) {
				if !posRe.MatchString(sym) {
					counters[numRe.ReplaceAllString(sym, "N")]++
				}
			}
		}
	}
	if len(counters) > 0 {
		fmt.Println("program-numbered symbols named by library code:")
		var keys []string
		for k := range counters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%6d  %s\n", counters[k], k)
		}
	}
	if *fail && (len(differing) > 0 || len(counters) > 0) {
		os.Exit(1)
	}
	if *show != "" {
		// Each version's lines that are not in every version.
		count := map[string]int{}
		for body := range bodies[*show] {
			for ln := range lineSet(body) {
				count[ln]++
			}
		}
		for body, src := range bodies[*show] {
			fmt.Printf("\n--- %s\n", src)
			for _, ln := range strings.Split(body, "\n") {
				if count[ln] < len(bodies[*show]) {
					fmt.Println(ln)
				}
			}
		}
	}
}

// libFunctions is every library function's body in ir.
func libFunctions(ir string) map[string]string {
	out := map[string]string{}
	locs := defineRe.FindAllStringSubmatchIndex(ir, -1)
	for _, l := range locs {
		end := strings.Index(ir[l[0]:], "\n}\n")
		if end < 0 {
			continue
		}
		out[ir[l[2]:l[3]]] = ir[l[3]+1 : l[0]+end]
	}
	return out
}

// diffSymbols is the set of symbols (numbers folded) on lines that are not
// in every body.
func diffSymbols(bs map[string]string) map[string]bool {
	count := map[string]int{}
	for body := range bs {
		for ln := range lineSet(body) {
			count[ln]++
		}
	}
	syms := map[string]bool{}
	for ln, n := range count {
		if n == len(bs) {
			continue
		}
		for _, s := range symRe.FindAllString(ln, -1) {
			syms[numRe.ReplaceAllString(s, "N")] = true
		}
	}
	return syms
}

// lineSet is body's distinct lines.
func lineSet(body string) map[string]bool {
	set := map[string]bool{}
	for _, ln := range strings.Split(body, "\n") {
		set[ln] = true
	}
	return set
}

// runtimeRe is a runtime routine: a __kml_ name that is neither the
// library's nor content-named (a hex suffix).
var (
	anyDefineRe = regexp.MustCompile(`(?m)^define [^\n]*?@("?[^\s(]+"?)\(`)
	contentRe   = regexp.MustCompile(`\.[0-9a-f]{16}"?$`)
)

// runtimeFunctions is every runtime routine's body in ir.
func runtimeFunctions(ir string) map[string]string {
	out := map[string]string{}
	for _, l := range anyDefineRe.FindAllStringSubmatchIndex(ir, -1) {
		name := ir[l[2]:l[3]]
		if !strings.HasPrefix(strings.Trim(name, `"`), "__kml_") || defineRe.MatchString(ir[l[0]:l[1]]) || contentRe.MatchString(name) {
			continue
		}
		end := strings.Index(ir[l[0]:], "\n}\n")
		if end < 0 {
			continue
		}
		out[name] = ir[l[0] : l[0]+end]
	}
	return out
}

// determinism compiles each program n times and reports those whose IR (or
// whose success) differs between compiles; its result is the exit code.
func determinism(bin string, srcs []string, n, jobs int) int {
	bad := make([]bool, len(srcs))
	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				first := ""
				for k := 0; k < n; k++ {
					out, err := exec.Command(bin, "-emit-llvm", srcs[i]).Output()
					got := string(out)
					if err != nil {
						got = "error: " + err.Error()
					}
					if k == 0 {
						first = got
					} else if got != first {
						bad[i] = true
						break
					}
				}
			}
		}()
	}
	for i := range srcs {
		work <- i
	}
	close(work)
	wg.Wait()
	code := 0
	for i, b := range bad {
		if b {
			fmt.Println("nondeterministic:", srcs[i])
			code = 1
		}
	}
	return code
}
