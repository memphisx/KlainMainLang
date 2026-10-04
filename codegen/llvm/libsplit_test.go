package llvm

import (
	"strings"
	"testing"
)

func TestLibOwner(t *testing.T) {
	for name, want := range map[string]string{
		"normalizeString__kml_modL9node_path":                        "node_path",
		"__kml_global_sep__kml_modL9node_path":                       "node_path",
		"__kml_lib_node_path_init":                                   "node_path",
		"__kml_lib_node_path_done":                                   "node_path",
		"EventEmitter__kml_modL11node_events_static_x":               "node_events",
		"__kml_cr_nstatic.NodeError__kml_modL20node_internal_errors": "",
		"__fnval_exit__kml_modL25node_internal_process_env":          "",
		"main": "",
	} {
		if got := libOwner(name); got != want {
			t.Errorf("libOwner(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDeclOf(t *testing.T) {
	f := irEntity{name: "f__kml_modL1a", kind: 'f', text: "define hidden { ptr, i64 } @f__kml_modL1a(ptr noundef %s, i64 %n, { i1, double } %o) {\nentry:\n  ret { ptr, i64 } zeroinitializer\n}"}
	if d, _ := declOf(f); d != "declare { ptr, i64 } @f__kml_modL1a(ptr noundef, i64, { i1, double })" {
		t.Errorf("function: %s", d)
	}
	g := irEntity{name: "x", kind: 'g', text: "@x = thread_local global ptr null, align 8"}
	if d, _ := declOf(g); d != "@x = external hidden thread_local global ptr" {
		t.Errorf("global: %s", d)
	}
}

// A module's unit is the same text whatever the program names, and a
// definition reached across units is hidden.
func TestSplitLibUnitsStable(t *testing.T) {
	lib := `@__kml_global_v__kml_modL1m = internal global i64 0, align 8
define internal i64 @g__kml_modL1m() {
entry:
  %v = load i64, ptr @__kml_global_v__kml_modL1m, align 8
  ret i64 %v
}
define internal i64 @h__kml_modL1m() {
entry:
  ret i64 1
}`
	a := lib + "\ndefine i32 @main() {\nentry:\n  %r = call i64 @g__kml_modL1m()\n  ret i32 0\n}\n"
	b := "define i32 @main() {\nentry:\n  %r = call i64 @h__kml_modL1m()\n  ret i32 0\n}\n" + lib
	ua, err := SplitLibUnits(a)
	if err != nil {
		t.Fatal(err)
	}
	ub, err := SplitLibUnits(b)
	if err != nil {
		t.Fatal(err)
	}
	if ua.Modules["m"] != ub.Modules["m"] {
		t.Fatalf("module unit differs:\n%s\n---\n%s", ua.Modules["m"], ub.Modules["m"])
	}
	if strings.Contains(ua.Modules["m"], "internal") {
		t.Errorf("library definitions stay internal:\n%s", ua.Modules["m"])
	}
	if !strings.Contains(ua.Program, "declare i64 @g__kml_modL1m()") {
		t.Errorf("program does not declare its library call:\n%s", ua.Program)
	}
}
