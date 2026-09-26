// ir_sites.go — name the Go line that emitted a rejected IR line.
//
// With KML_IR_SITES set, every line the emitter writes carries a trailing
// `; @site file.go:N < file.go:M` comment: the first two frames outside the
// IR-writing primitives. When clang rejects the module, AnnotateClangOutput
// reads the offending .ll line back and names those frames, so an invalid-IR
// failure points at the codegen site that produced it instead of a line
// number in a generated file.
package llvm

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var irSites = os.Getenv("KML_IR_SITES") != ""

// irWriters are the primitives that append text to the module; a site is the
// first frame outside them.
var irWriters = map[string]bool{
	"emitInstr":      true,
	"emitTerminator": true,
	"emitAlloca":     true,
	"emitGlobal":     true,
	"emitLabel":      true,
	"withSite":       true,
	"irSite":         true,
}

// irSite returns "file.go:N < file.go:M" for the emitting code, or "".
func irSite() string {
	var pcs [16]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	var out []string
	for len(out) < 2 {
		f, more := frames.Next()
		name := f.Function
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			name = name[i+1:]
		}
		if !irWriters[name] {
			out = append(out, filepath.Base(f.File)+":"+strconv.Itoa(f.Line))
		}
		if !more {
			break
		}
	}
	return strings.Join(out, " < ")
}

// withSite appends the site comment to a single emitted line, or prefixes a
// comment line to a multi-line chunk (a runtime template's closing `}` cannot
// carry a comment that means anything for the lines above it).
func withSite(text string) string {
	if !irSites {
		return text
	}
	site := irSite()
	if strings.Contains(text, "\n") {
		return "; @site " + site + "\n" + text
	}
	return text + "  ; @site " + site
}

var clangIRLoc = regexp.MustCompile(`(?m)^(.*?\.ll):(\d+):\d+: error: .*$`)

// AnnotateClangOutput appends, under each `x.ll:N:C: error:` line in clang's
// output, the emitting Go site recorded on line N (or on the nearest `; @site`
// comment above it, for a multi-line chunk). Output without such errors, or
// IR built without KML_IR_SITES, comes back unchanged.
func AnnotateClangOutput(out []byte) string {
	text := string(out)
	cache := map[string][]string{}
	return clangIRLoc.ReplaceAllStringFunc(text, func(match string) string {
		m := clangIRLoc.FindStringSubmatch(match)
		lines, ok := cache[m[1]]
		if !ok {
			data, err := os.ReadFile(m[1])
			if err == nil {
				lines = strings.Split(string(data), "\n")
			}
			cache[m[1]] = lines
		}
		n, _ := strconv.Atoi(m[2])
		if site := siteAt(lines, n-1); site != "" {
			return match + "\n  emitted by " + site
		}
		return match
	})
}

func siteAt(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	if j := strings.Index(lines[i], "; @site "); j >= 0 {
		return strings.TrimSpace(lines[i][j+len("; @site "):])
	}
	for k := i - 1; k >= 0; k-- {
		if strings.HasPrefix(lines[k], "; @site ") {
			return strings.TrimSpace(lines[k][len("; @site "):])
		}
		if strings.HasPrefix(lines[k], "define ") {
			// a multi-line chunk's comment sits directly above its define
			if k > 0 && strings.HasPrefix(lines[k-1], "; @site ") {
				return strings.TrimSpace(lines[k-1][len("; @site "):])
			}
			break
		}
		if lines[k] == "}" {
			break
		}
	}
	return ""
}
