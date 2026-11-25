package llvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A MinGW object gives every extern variable it reads a .refptr.<name>
// stub in a COMDAT section, and so does every other object reading the same
// variable. The stub is a weak definition: read as a strong one, the second
// runtime file holding it clashed with the first and was left out of the
// link (stackguard.c, hostbox.c, ... on Windows).
func TestCOFFComdatDefinitionIsWeak(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not found")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "refptr.c")
	obj := filepath.Join(dir, "refptr.o")
	code := "extern long long __kml_shared_var;\n" +
		"long long __kml_reads_var(void) { return __kml_shared_var; }\n" +
		"void __kml_defined_here(void) {}\n"
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(clang, "--target=x86_64-w64-windows-gnu", "-O2", "-w", "-c", src, "-o", obj).CombinedOutput(); err != nil {
		t.Skipf("clang cannot target x86_64-w64-windows-gnu here: %v\n%s", err, out)
	}
	s, err := readObjSyms(obj)
	if err != nil {
		t.Fatal(err)
	}
	if !s.strong["__kml_reads_var"] || !s.strong["__kml_defined_here"] {
		t.Fatalf("functions defined here should be strong: %v", s.strong)
	}
	if !s.undef["__kml_shared_var"] {
		t.Fatalf("__kml_shared_var should be undefined: %v", s.undef)
	}
	if s.strong[".refptr.__kml_shared_var"] {
		t.Fatalf(".refptr stub read as a strong definition: %v", s.strong)
	}
	if !s.weak[".refptr.__kml_shared_var"] {
		t.Fatalf(".refptr stub should be a weak definition: weak=%v", s.weak)
	}
}
