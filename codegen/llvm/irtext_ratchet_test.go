package llvm

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// irDefineRe is an IR function definition written as Go text: at a line
// start inside a raw string, or after a "\n" or a quote.
var irDefineRe = regexp.MustCompile("(?m)(^|\\\\n|[`\"])define ")

// irTextDefines counts the IR function definitions each codegen file
// writes as text.
func irTextDefines(t *testing.T) map[string]int {
	files, _ := filepath.Glob("*.go")
	out := map[string]int{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(irDefineRe.FindAllIndex(src, -1)); n > 0 {
			out[f] = n
		}
	}
	return out
}

// The runtime moves out of IR text into C (TDD-00240): a file may lose IR
// definitions, never gain them, and a file without any may not start. A
// port lowers its file's allowance in irTextAllowance (to zero: delete the
// entry); KML_IRTEXT_BASELINE=1 prints the current counts.
func TestIRTextOnlyShrinks(t *testing.T) {
	got := irTextDefines(t)
	if os.Getenv("KML_IRTEXT_BASELINE") != "" {
		var keys []string
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("\t%q: %d,\n", k, got[k])
		}
		return
	}
	for f, n := range got {
		if n > irTextAllowance[f] {
			t.Errorf("%s: %d IR definitions as text, allowance %d — new runtime code goes in C (TDD-00240)", f, n, irTextAllowance[f])
		}
	}
	for f, n := range irTextAllowance {
		if got[f] < n {
			t.Errorf("%s: %d IR definitions as text, allowance %d — lower the allowance", f, got[f], n)
		}
	}
}
