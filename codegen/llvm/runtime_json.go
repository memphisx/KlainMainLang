package llvm

import _ "embed"

//go:embed coresrc/json.c
var jsonSource string

// JSONRuntimeSource is the JSON.stringify fragment runtime's C source
// (TDD-00240).
func JSONRuntimeSource() string { return layoutHeader() + jsonSource }
