package llvm

import (
	_ "embed"
	"fmt"
)

// json_tree.go — the embedded JSON parse-tree runtime (TDD-00077 Track P). A
// self-contained recursive-descent JSON parser in C (no external library),
// compiled alongside the program (the bigint/gcshim pattern) only when a
// program uses JSON.parse. P1 uses it to *validate* JSON.parse input — a NULL
// return is malformed input, which the emitted IR turns into a catchable
// SyntaxError — and frees the tree; typed/dynamic projection off the same tree
// is P3/P4.

//go:embed jsonsrc/json_parse.c
var jsonParseTreeSource string

// JSONParseTreeSource returns the C source implementing the __kml_json_*
// parse-tree ABI. main.go writes it next to the .ll and compiles it when
// UsesJSONParse() is set — no external library to locate (libc only).
func JSONParseTreeSource() string { return jsonParseTreeSource }

// UsesJSONParse reports whether any JSON.parse (or Response.json()) reached
// codegen, so main.go knows to compile+link the parse-tree C file.
func (e *Emitter) UsesJSONParse() bool { return e.usesJSONParse }

// ensureJSONParseTree declares the parse-tree ABI (__kml_json_parse builds and
// validates the tree; __kml_json_free releases it) exactly once, and marks the
// program as needing the C file compiled in.
func (e *Emitter) ensureJSONParseTree() {
	e.usesJSONParse = true
	if e.declaredJSONParseTree {
		return
	}
	e.declaredJSONParseTree = true
	e.emitGlobal(`declare ptr @__kml_json_parse(ptr, ptr)`)
	e.emitGlobal(`declare void @__kml_json_free(ptr)`)
	e.emitGlobal(`declare i32 @__kml_json_kind(ptr)`)
	e.emitGlobal(`declare i32 @__kml_json_bool(ptr)`)
	e.emitGlobal(`declare ptr @__kml_json_num_lexeme(ptr)`)
	e.emitGlobal(`declare i64 @__kml_json_len(ptr)`)
	e.emitGlobal(`declare ptr @__kml_json_item(ptr, i64)`)
	e.emitGlobal(`declare ptr @__kml_json_string_dup(ptr)`)
	e.emitGlobal(`declare ptr @__kml_json_get(ptr, ptr)`)
}

// emitJSONParseTree parses jsonText with the real validating parser, throwing a
// catchable SyntaxError (matching JS) on malformed input, and returns the
// non-null tree-root register. The caller projects the tree onto the target
// type (emitJSONProject) and then releases it with __kml_json_free.
func (e *Emitter) emitJSONParseTree(jsonText Value) string {
	e.ensureJSONParseTree()
	e.ensureMalloc()
	e.ensureSprintf()

	errSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", errSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", errSlot))

	treeReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_json_parse(ptr %s, ptr %s)", treeReg, jsonText.Ref, errSlot))
	isBad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isBad, treeReg))

	badL := e.freshLabel("json.bad")
	okL := e.freshLabel("json.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBad, badL, okL))

	e.emitLabel(badL)
	offReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", offReg, errSlot))
	msgBuf := e.freshReg()
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 64)", msgBuf))
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)",
		msgBuf, e.internString("Unexpected token in JSON at position %lld"), offReg))
	e.emitInstr(fmt.Sprintf("call void @__kml_str_finalize(ptr %s)", msgBuf))
	errReg := e.buildErrorObj(errorKindIDs["SyntaxError"], msgBuf, e.internString("SyntaxError"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
	e.emitTerminator("unreachable")

	e.emitLabel(okL)
	return treeReg
}
