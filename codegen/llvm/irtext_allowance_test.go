package llvm

// irTextAllowance is TestIRTextOnlyShrinks's per-file ceiling: the IR
// definitions each file still writes as text, or wraps through funcDef
// (TDD-00240).
var irTextAllowance = map[string]int{
	"emit_finalization.go": 2,
	"emit_inspect.go":      2,
	"emit_record_view.go":  1,
	"funcstate.go":         3,
	"ir_sites.go":          1,
	"runtime_hooks.go":     1,
	"unitreg.go":           1,
}
