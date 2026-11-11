package tests

import (
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestE2EDebugBuild: `-g` compiles the program and its runtime with -g -O0.
// The binary behaves as the optimized one does, and carries DWARF for the
// runtime's C units, whose sources stay beside their cached objects (on
// macOS the DWARF is gathered into <bin>.dSYM).
func TestE2EDebugBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("DWARF in a PE binary is not checked here")
	}
	cli := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "dbg.ts")
	if err := os.WriteFile(src, []byte(`class P { x = 1; }
const p: any = new P();
p.y = 2;
console.log(p, JSON.stringify(p));
const pr: any = Promise.resolve(5);
console.log(pr);
try { JSON.parse("{"); } catch (e: any) { console.log(e.name); }
(async () => { console.log(await pr); })();
`), 0644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "dbg")
	cache := filepath.Join(dir, "cobj")
	if out, err := exec.Command(cli, "-g", "-cache-dir", cache, "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile -g: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := "P { x: 1, y: 2 } {\"x\":1,\"y\":2}\nPromise { 5 }\nSyntaxError\n5\n"
	if string(out) != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}

	// The runtime's DWARF names sources that exist.
	var dwarfFile string
	switch runtime.GOOS {
	case "darwin":
		dwarfFile = filepath.Join(bin+".dSYM", "Contents", "Resources", "DWARF", "dbg")
	default:
		dwarfFile = bin
	}
	var names []string
	switch runtime.GOOS {
	case "darwin":
		f, err := macho.Open(dwarfFile)
		if err != nil {
			t.Fatalf("open %s: %v", dwarfFile, err)
		}
		defer f.Close()
		d, err := f.DWARF()
		if err != nil {
			t.Fatalf("DWARF: %v", err)
		}
		names = compileUnitNames(t, d.Reader())
	default:
		f, err := elf.Open(dwarfFile)
		if err != nil {
			t.Fatalf("open %s: %v", dwarfFile, err)
		}
		defer f.Close()
		d, err := f.DWARF()
		if err != nil {
			t.Fatalf("DWARF: %v", err)
		}
		names = compileUnitNames(t, d.Reader())
	}
	var runtimeUnits int
	for _, n := range names {
		if !strings.HasPrefix(n, cache) {
			continue
		}
		runtimeUnits++
		if _, err := os.Stat(n); err != nil {
			t.Errorf("compile unit %s names a missing source: %v", n, err)
		}
	}
	if runtimeUnits == 0 {
		t.Fatalf("no runtime compile unit under %s in %v", cache, names)
	}
}

// compileUnitNames lists the DW_AT_name of every compile unit r reads.
func compileUnitNames(t *testing.T, r *dwarf.Reader) []string {
	t.Helper()
	var out []string
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatalf("DWARF read: %v", err)
		}
		if e == nil {
			return out
		}
		if e.Tag == dwarf.TagCompileUnit {
			if n, ok := e.Val(dwarf.AttrName).(string); ok {
				out = append(out, n)
			}
			r.SkipChildren()
		}
	}
}

// TestE2ELTOBuild: `-lto` links the program, its library and its runtime as
// bitcode (ThinLTO); the binary behaves as the plain build does.
func TestE2ELTOBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ThinLTO through mingw's linker is not set up")
	}
	cli := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "lto.ts")
	if err := os.WriteFile(src, []byte(`const m = new Map<string, number>();
for (let i = 0; i < 1000; i++) m.set("k" + (i % 10), i);
const a = [5, 3, 9, 1].sort((x, y) => x - y);
const o: any = { a: 1 };
o.b = [2, 3];
console.log(m.size, a, JSON.stringify(o), o);
(async () => { console.log(await Promise.resolve("done")); })();
`), 0644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "lto")
	if out, err := exec.Command(cli, "-lto", "-cache-dir", filepath.Join(dir, "cobj"), "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile -lto: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := "10 [ 1, 3, 5, 9 ] {\"a\":1,\"b\":[2,3]} { a: 1, b: [ 2, 3 ] }\ndone\n"
	if string(out) != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}
