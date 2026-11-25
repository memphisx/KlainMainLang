package llvm

import "testing"

// A library unit whose headers are missing still answers for the
// functions it defines, read off its text: a program that names none of
// them compiles without it.
func TestLibUnitMissingHeaderAnswersByText(t *testing.T) {
	u := libUnit{
		src: CSource{"nohdr", "#include <kml_no_such_header.h>\n" +
			"static int helper(void) { return 1; }\n" +
			"double __kml_native_nohdr_one(double x) { return x; }\n" +
			"char *__kml_native_nohdr_two(void) {\n    return 0;\n}\n" +
			"extern void __kml_native_nohdr_decl(void);\n", nil, nil, ""},
		locate: func() ([]string, []string, error) { return nil, nil, nil },
	}
	s, err := u.scanSyms(Toolchain{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, n := range []string{"__kml_native_nohdr_one", "__kml_native_nohdr_two"} {
		if !s.strong[n] {
			t.Errorf("%s not answered", n)
		}
	}
	if s.strong["__kml_native_nohdr_decl"] || s.strong["helper"] {
		t.Errorf("declaration or static answered: %v", s.strong)
	}
}
