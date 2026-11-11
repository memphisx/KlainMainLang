package llvm

// runtime_resolve.go — which runtime C files a program links, read from
// symbols (TDD-00230 Phase 4, TDD-00240 design 3): the program's IR names
// what it calls, each runtime file's object names what it defines (strongly
// or as a weak default) and what it calls in turn, and the link takes the
// closure. A file is linked because something the program reaches names
// it, not because codegen remembered to set a flag.

import (
	"bufio"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// objSyms is one object's external symbol table.
type objSyms struct {
	strong, weak, undef map[string]bool
}

func newObjSyms() objSyms {
	return objSyms{strong: map[string]bool{}, weak: map[string]bool{}, undef: map[string]bool{}}
}

// readObjSyms reads an object's external symbols: Mach-O, ELF or COFF, as
// the target's clang wrote it. A Mach-O name loses its leading underscore.
func readObjSyms(path string) (objSyms, error) {
	s := newObjSyms()
	if f, err := macho.Open(path); err == nil {
		defer f.Close()
		if f.Symtab == nil {
			return s, nil
		}
		for _, sym := range f.Symtab.Syms {
			const nExt, nType, nSect, nWeakDef = 0x01, 0x0e, 0x0e, 0x0080
			if sym.Type&nExt == 0 || sym.Type&0xe0 != 0 { // local, or a debug (stab) entry
				continue
			}
			name := strings.TrimPrefix(sym.Name, "_")
			switch {
			case sym.Type&nType == 0 && sym.Value == 0:
				s.undef[name] = true
			case sym.Type&nType == nSect && sym.Desc&nWeakDef != 0:
				s.weak[name] = true
			case sym.Type&nType == nSect || sym.Type&nType == 0: // a section symbol, or a common one
				s.strong[name] = true
			}
		}
		return s, nil
	}
	if f, err := elf.Open(path); err == nil {
		defer f.Close()
		syms, err := f.Symbols()
		if err != nil {
			return s, nil // no symbol table: nothing to resolve
		}
		for _, sym := range syms {
			bind := elf.ST_BIND(sym.Info)
			if bind != elf.STB_GLOBAL && bind != elf.STB_WEAK {
				continue
			}
			switch {
			case sym.Section == elf.SHN_UNDEF:
				s.undef[sym.Name] = true
			case bind == elf.STB_WEAK:
				s.weak[sym.Name] = true
			default:
				s.strong[sym.Name] = true
			}
		}
		return s, nil
	}
	f, err := pe.Open(path)
	if err != nil {
		return s, fmt.Errorf("%s: not a Mach-O, ELF or COFF object", path)
	}
	defer f.Close()
	for _, sym := range f.Symbols {
		const classExternal, classWeakExternal = 2, 105
		switch sym.StorageClass {
		case classExternal:
			if sym.SectionNumber == 0 && sym.Value == 0 {
				s.undef[sym.Name] = true
			} else if sym.SectionNumber != 0 || sym.Value != 0 {
				s.strong[sym.Name] = true
			}
		case classWeakExternal:
			s.weak[sym.Name] = true
		}
	}
	return s, nil
}

var (
	symScanMu    sync.Mutex
	symScanMemo  = map[string]objSyms{}
	symScanLocks sync.Map
)

// symScanDir holds the runtime files' symbol tables, one small text file
// per source and toolchain, so a later build reads a list instead of
// compiling the file.
func symScanDir() string {
	if d := os.Getenv("KML_SYMSCAN_DIR"); d != "" {
		return d
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "klainmain", "csyms")
	}
	return filepath.Join(os.TempDir(), "klainmain-csyms")
}

// scanSyms is cs's symbol table for tc's target: compiled once, its table
// kept on disk. The object is -O2, as the runtime links: an inline helper's
// references are those of the code that links.
func (c CSource) scanSyms(tc Toolchain) (objSyms, error) {
	return c.scanSymsWith(tc, nil)
}

// scanSymsWith is scanSyms, with prepare run on a copy of c before it is
// compiled (when its table is not on disk yet).
func (c CSource) scanSymsWith(tc Toolchain, prepare func(*CSource) error) (objSyms, error) {
	h := sha256.New()
	for _, part := range [][]string{{c.Name, c.Content, runtime.GOOS, win32CompatHeader, clangIdentity()}, c.CFlags, tc.Args()} {
		for _, s := range part {
			fmt.Fprintf(h, "%d:%s", len(s), s)
		}
		h.Write([]byte{0})
	}
	key := c.Name + "-" + hex.EncodeToString(h.Sum(nil)[:10])
	symScanMu.Lock()
	if s, ok := symScanMemo[key]; ok {
		symScanMu.Unlock()
		return s, nil
	}
	symScanMu.Unlock()
	mu, _ := symScanLocks.LoadOrStore(key, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	dir := symScanDir()
	table := filepath.Join(dir, key+".syms")
	s, err := readSymTable(table)
	if err != nil {
		cc := c
		if prepare != nil {
			if err := prepare(&cc); err != nil {
				return s, err
			}
		}
		if s, err = cc.compileForSyms(tc, dir, key); err != nil {
			return s, err
		}
		writeSymTable(table, s)
	}
	symScanMu.Lock()
	symScanMemo[key] = s
	symScanMu.Unlock()
	return s, nil
}

// libUnit is a runtime file that brings a library of its own. Finding the
// library (pkg-config, a package prefix) is left until it is needed: its
// symbol table is keyed by the source alone, and locate runs only to
// compile it for that table or to link it.
type libUnit struct {
	src    CSource
	locate func() (cflags, libs []string, err error)
	links  []string // the LinkLibs names it brings
}

// scanSyms is u's symbol table; a library that is not installed leaves it
// empty, so nothing pulls the unit in.
func (u libUnit) scanSyms(tc Toolchain) (objSyms, error) {
	located := false
	c := u.src
	c.Name = "lib-" + c.Name
	s, err := c.scanSymsWith(tc, func(c *CSource) error {
		if u.locate == nil {
			return nil
		}
		cflags, _, err := u.locate()
		if err != nil {
			return err
		}
		located = true
		c.CFlags = append(append([]string(nil), c.CFlags...), cflags...)
		return nil
	})
	if err != nil && !located {
		return newObjSyms(), nil
	}
	return s, err
}

// linked is u as the link takes it, its library located.
func (u libUnit) linked() (CSource, error) {
	c := u.src
	if u.locate != nil {
		cflags, libs, err := u.locate()
		if err != nil {
			return CSource{}, err
		}
		c.CFlags = append(append([]string(nil), c.CFlags...), cflags...)
		c.Libs = append(append([]string(nil), c.Libs...), libs...)
	}
	return c, nil
}

// compileForSyms compiles cs to a scratch object and reads its symbols.
func (c CSource) compileForSyms(tc Toolchain, dir, key string) (objSyms, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return objSyms{}, err
	}
	stem := filepath.Join(dir, fmt.Sprintf("%s.%d", key, os.Getpid()))
	src, obj := stem+"."+c.SrcExt(), stem+".o"
	if err := os.WriteFile(src, []byte(c.Content), 0o644); err != nil {
		return objSyms{}, err
	}
	defer os.Remove(src)
	defer os.Remove(obj)
	args := []string{"-O2", "-w", "-c", src, "-o", obj}
	for _, f := range c.CFlags {
		if strings.HasPrefix(f, "-l") || strings.HasPrefix(f, "-L") || strings.HasPrefix(f, "-Wl,") {
			continue
		}
		args = append(args, f)
	}
	if out, err := tc.Command(args...).CombinedOutput(); err != nil {
		return objSyms{}, fmt.Errorf("compiling embedded %s: %v\n%s", c.Name, err, out)
	}
	return readObjSyms(obj)
}

// A symbol table file is one symbol per line, prefixed s (strong), w (weak)
// or u (undefined).
func readSymTable(path string) (objSyms, error) {
	f, err := os.Open(path)
	if err != nil {
		return objSyms{}, err
	}
	defer f.Close()
	s := newObjSyms()
	sc := bufio.NewScanner(f)
	ok := false
	for sc.Scan() {
		line := sc.Text()
		if line == "end" {
			ok = true
			break
		}
		if len(line) < 3 {
			continue
		}
		switch line[0] {
		case 's':
			s.strong[line[2:]] = true
		case 'w':
			s.weak[line[2:]] = true
		case 'u':
			s.undef[line[2:]] = true
		}
	}
	if !ok {
		return objSyms{}, fmt.Errorf("%s: truncated", path)
	}
	return s, nil
}

func writeSymTable(path string, s objSyms) {
	var b strings.Builder
	for _, set := range []struct {
		tag string
		m   map[string]bool
	}{{"s", s.strong}, {"w", s.weak}, {"u", s.undef}} {
		names := make([]string, 0, len(set.m))
		for n := range set.m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			b.WriteString(set.tag + " " + n + "\n")
		}
	}
	b.WriteString("end\n")
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if os.WriteFile(tmp, []byte(b.String()), 0o644) == nil {
		os.Rename(tmp, path)
	}
}

// irSymbolUse is what a program's IR defines and names: every `@name` its
// definitions and globals write, and the ones it defines (a `define` line,
// or a global with an initializer), weakly (weak, linkonce) or not; an
// internal or private one is the program's own.
func irSymbolUse(ir string) (defined, weakDefined, named map[string]bool) {
	defined, weakDefined, named = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := 0; i < len(ir); {
		end := strings.IndexByte(ir[i:], '\n')
		if end < 0 {
			end = len(ir)
		} else {
			end += i
		}
		line := ir[i:end]
		i = end + 1
		// A declare names nothing on its own: only a call or a reference
		// in a definition uses the symbol.
		if strings.HasPrefix(line, ";") || strings.HasPrefix(line, "declare") {
			continue
		}
		isDefine := strings.HasPrefix(line, "define")
		isGlobal := strings.HasPrefix(line, "@") && !strings.Contains(line, "= external")
		first := true
		for j := 0; j < len(line); j++ {
			if line[j] != '@' {
				continue
			}
			k := j + 1
			var name string
			if k < len(line) && line[k] == '"' {
				q := strings.IndexByte(line[k+1:], '"')
				if q < 0 {
					break
				}
				name = line[k+1 : k+1+q]
				k += q + 2
			} else {
				for k < len(line) && isIRNameByte(line[k]) {
					k++
				}
				name = line[j+1 : k]
			}
			if name == "" {
				continue
			}
			if first && (isDefine || isGlobal && j == 0) {
				head := line
				if isGlobal {
					head = line[k:]
				}
				if strings.Contains(head, " internal ") || strings.Contains(head, " private ") {
					// local to the program: defines nothing the runtime sees
				} else if strings.Contains(head, " weak") || strings.Contains(head, " linkonce") {
					weakDefined[name] = true
				} else {
					defined[name] = true
				}
			} else {
				named[name] = true
			}
			first = false
			j = k - 1
		}
	}
	return defined, weakDefined, named
}

// resolveRuntime picks, from pool, the runtime files the program and the
// already linked files reach. linked are files linked regardless (their
// symbols take part). A symbol only a weak default answers takes the
// pool's strong definition when that file's closure links: the weak
// default stands in for a file the program cannot link (one needing a
// library unit it does not link), so the feature gets its real
// implementation wherever it can have one.
//
// libPool are files that bring a library of their own (unitLinks): one is
// linked only when a symbol it defines has no other definition, never in
// place of a weak default, so a program pays for a library only when it
// calls into it.
func resolveRuntime(tc Toolchain, ir string, linked, pool []CSource, libPool []libUnit) ([]CSource, []libUnit, error) {
	nPlain := len(pool)
	pool = pool[:nPlain:nPlain]
	for _, u := range libPool {
		pool = append(pool, u.src)
	}
	isLib := func(i int) bool { return i >= nPlain }
	syms := func(n int, scan func(i int) (objSyms, error)) ([]objSyms, error) {
		out := make([]objSyms, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		sem := make(chan struct{}, max(2, runtime.NumCPU()))
		for i := range n {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				out[i], errs[i] = scan(i)
				<-sem
			}(i)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	linkedSyms, err := syms(len(linked), func(i int) (objSyms, error) { return linked[i].scanSyms(tc) })
	if err != nil {
		return nil, nil, err
	}
	poolSyms, err := syms(len(pool), func(i int) (objSyms, error) {
		if isLib(i) {
			return libPool[i-nPlain].scanSyms(tc)
		}
		return pool[i].scanSyms(tc)
	})
	if err != nil {
		return nil, nil, err
	}
	strongIn, weakIn := map[string]int{}, map[string]int{}
	for i, s := range poolSyms {
		for n := range s.strong {
			if _, dup := strongIn[n]; !dup {
				strongIn[n] = i
			}
		}
		for n := range s.weak {
			if _, dup := weakIn[n]; !dup {
				weakIn[n] = i
			}
		}
	}
	irDefined, irWeak, irNamed := irSymbolUse(ir)
	strong, weak := map[string]bool{}, map[string]bool{}
	for n := range irDefined {
		strong[n] = true
	}
	for n := range irWeak {
		weak[n] = true
	}
	var want []string
	for n := range irWeak {
		want = append(want, n)
	}
	take := map[int]bool{}
	add := func(s objSyms) {
		for n := range s.strong {
			strong[n] = true
		}
		for n := range s.weak {
			weak[n] = true
			want = append(want, n) // a strong definition may replace it
		}
		for n := range s.undef {
			want = append(want, n)
		}
	}
	for _, s := range linkedSyms {
		add(s)
	}
	trace := os.Getenv("KML_RESOLVE_TRACE") != ""
	// clashes reports a pool file defining what the program or a linked
	// file already defines: the program's own version stands.
	clashes := func(i int) bool {
		for n := range poolSyms[i].strong {
			if strong[n] {
				if trace {
					fmt.Fprintf(os.Stderr, "runtime: %s does not link: %s is defined\n", pool[i].Name, n)
				}
				return true
			}
		}
		return false
	}
	pull := func(i int, why string) {
		if !take[i] && clashes(i) {
			return
		}
		if trace && !take[i] {
			fmt.Fprintf(os.Stderr, "runtime: %s for %s\n", pool[i].Name, why)
		}
		if !take[i] {
			take[i] = true
			add(poolSyms[i])
		}
	}
	// closes reports whether linking pool file i leaves every runtime symbol
	// its closure names defined somewhere: a file that needs a library unit
	// the program does not link (bigintbox.c without the BigInt backend)
	// does not close, and the weak default stays.
	closes := func(i int) bool {
		seen := map[int]bool{i: true}
		queue := []int{i}
		for len(queue) > 0 {
			j := queue[0]
			queue = queue[1:]
			for n := range poolSyms[j].undef {
				if strong[n] || weak[n] || irDefined[n] {
					continue
				}
				k, ok := strongIn[n]
				if !ok {
					k, ok = weakIn[n]
				}
				if !ok {
					if strings.HasPrefix(n, "__kml_") {
						if trace {
							fmt.Fprintf(os.Stderr, "runtime: %s does not link: %s names %s\n", pool[i].Name, pool[j].Name, n)
						}
						return false
					}
					continue
				}
				if !take[k] && !seen[k] {
					if clashes(k) || isLib(k) {
						if trace && isLib(k) {
							fmt.Fprintf(os.Stderr, "runtime: %s does not link: %s needs %s\n", pool[i].Name, pool[j].Name, pool[k].Name)
						}
						return false
					}
					seen[k] = true
					queue = append(queue, k)
				}
			}
		}
		return true
	}
	// What the program names: a strong definition wins over a weak default
	// when it links.
	for n := range irNamed {
		if strong[n] {
			continue
		}
		i, ok := strongIn[n]
		if !ok {
			if i, ok = weakIn[n]; !ok || weak[n] {
				continue
			}
		}
		if !weak[n] || !isLib(i) && closes(i) {
			pull(i, n)
		}
	}
	// What the runtime names, by the same rule: a weak default stands in
	// for a file that cannot link here, not for one that can.
	for len(want) > 0 {
		n := want[len(want)-1]
		want = want[:len(want)-1]
		if strong[n] {
			continue
		}
		i, ok := strongIn[n]
		if !ok {
			if i, ok = weakIn[n]; !ok || weak[n] {
				continue
			}
		}
		if take[i] {
			continue
		}
		if !weak[n] || !isLib(i) && closes(i) {
			pull(i, n)
		}
	}
	var out []CSource
	var libs []libUnit
	for i, cs := range pool {
		if !take[i] {
			continue
		}
		if !isLib(i) {
			out = append(out, cs)
			continue
		}
		u := libPool[i-nPlain]
		c, err := u.linked()
		if err != nil {
			return nil, nil, err
		}
		out = append(out, c)
		libs = append(libs, u)
	}
	return out, libs, nil
}
