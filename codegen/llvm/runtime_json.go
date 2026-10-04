package llvm

import _ "embed"

//go:embed coresrc/json.c
var jsonSource string

// JSONRuntimeSource is the JSON.stringify fragment runtime's C source
// (TDD-00240).
func JSONRuntimeSource() string { return layoutHeader() + jsonSource }

// UsesJSONRuntime reports whether the program links coresrc/json.c.
func (e *Emitter) UsesJSONRuntime() bool {
	return e.usedJSONStringifyNum || e.usedJSONConcat2 || e.usedJSONStringifyStr
}

func (e *Emitter) ensureJSONStringifyNum() {
	if e.usedJSONStringifyNum {
		return
	}
	e.usedJSONStringifyNum = true
	e.ensureMalloc()
	e.ensureSprintf()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_json_str_num(i64)")
}

// ensureJSONConcat2 declares __kml_json_concat2: concatenate two header
// strings into a fresh one, conditionally freeing each input. The stringify
// accumulator loops use it so intermediate accumulators and element
// fragments are reclaimed as they go, in every memory mode.
func (e *Emitter) ensureJSONConcat2() {
	if e.usedJSONConcat2 {
		return
	}
	e.usedJSONConcat2 = true
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_json_concat2(ptr, ptr, i1, i1)")
}

// ensureJSONStringifyStr declares __kml_json_str_str: a string (null pointer
// = the value null) as a quoted JSON string.
func (e *Emitter) ensureJSONStringifyStr() {
	if e.usedJSONStringifyStr {
		return
	}
	e.usedJSONStringifyStr = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_json_str_str(ptr)")
}
