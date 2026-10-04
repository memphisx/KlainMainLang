// Unit registration (TDD-00238 Stage 3). A dispatcher that used to switch
// over every class of the program looks a type id up in a table instead.
// Each compiled unit contributes its rows as a constant array, registered by
// a constructor before main runs (unitregsrc/unitreg.c), so a separately
// compiled library object can add its own rows to the same tables.
package llvm

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed unitregsrc/unitreg.c
var unitRegSource string

// UnitRegSource returns the C runtime holding the registered tables.
func UnitRegSource() string { return unitRegSource }

// UsesUnitReg reports whether the program registered any table rows (the
// registry runtime is then linked in).
func (e *Emitter) UsesUnitReg() bool { return e.usedUnitReg }

// Table kinds; each must stay below KML_UNIT_KINDS in unitreg.c.
const (
	unitKindToStringTag   = 0 // { i64 id, ptr fn(ptr) -> ptr }
	unitKindInspectCustom = 1 // { i64 id, ptr fn(ptr, i64) -> ptr }
	unitKindHost          = 2 // hostRowTy (emit_hostbox.go), keyed by the whole header word
	unitKindShape         = 3 // KmlShape (shapesrc/shape.c)
	unitKindFnMeta        = 4 // KmlFnMeta (fnmetasrc/fnmeta.c), keyed by the code pointer
	unitKindCtor          = 5 // ctorRowTy (emit_classref_rows.go), keyed by the reference payload
	unitKindCtorOf        = 6 // { i64 id, ptr payload }: obj.constructor by type id
	unitKindClassParent   = 7 // { i64 id, i64 parent id }: __kml_class_is walks it
)

// unitRow is one table row: its constant, `{ i64 <id>, ... }`.
type unitRow struct {
	lit string
}

// unitTable is one kind's rows and the IR struct type of a row.
type unitTable struct {
	rowTy string // e.g. "{ i64, ptr }"
	rows  []unitRow
}

// ensureUnitReg declares the registry's lookup.
func (e *Emitter) ensureUnitReg() {
	if e.usedUnitReg {
		return
	}
	e.usedUnitReg = true
	e.emitGlobal("declare void @__kml_unit_register(i64, ptr, i64, i64)")
	e.emitGlobal("declare ptr @__kml_unit_find(i64, i64)")
}

// addUnitRow adds a row to kind's table. rowTy is the row's IR struct type,
// starting with the i64 id; fields is the IR of the fields after the id.
func (e *Emitter) addUnitRow(kind int, rowTy string, id int64, fields string) {
	e.addUnitRowLit(kind, rowTy, fmt.Sprintf("{ i64 %d, %s }", id, fields))
}

// addUnitRowLit adds a row given as its whole constant, `{ i64 <id>, ... }`.
func (e *Emitter) addUnitRowLit(kind int, rowTy, lit string) {
	e.ensureUnitReg()
	if e.unitTables == nil {
		e.unitTables = map[int]*unitTable{}
	}
	t := e.unitTables[kind]
	if t == nil {
		t = &unitTable{rowTy: rowTy}
		e.unitTables[kind] = t
	}
	t.rows = append(t.rows, unitRow{lit: lit})
}

// addCtor runs fn (a `void ()` function) before main.
func (e *Emitter) addCtor(fn string) { e.ctors = append(e.ctors, fn) }

// emitUnitRegFinalize emits each table as a constant array and the
// constructor registering them, then the program's constructor list.
func (e *Emitter) emitUnitRegFinalize() {
	e.emitConstructorOfOnce() // a constructor read a late finalize step generated
	e.emitClassParentRows()
	if len(e.unitTables) > 0 {
		kinds := make([]int, 0, len(e.unitTables))
		for k := range e.unitTables {
			kinds = append(kinds, k)
		}
		sort.Ints(kinds)
		var body strings.Builder
		for _, k := range kinds {
			t := e.unitTables[k]
			elems := make([]string, len(t.rows))
			for i, r := range t.rows {
				elems[i] = t.rowTy + " " + r.lit
			}
			name := fmt.Sprintf("@__kml_unit_rows_%d", k)
			arr := fmt.Sprintf("[%d x %s]", len(t.rows), t.rowTy)
			e.emitGlobal(fmt.Sprintf("%s = private constant %s [%s]", name, arr, strings.Join(elems, ", ")))
			stride := fmt.Sprintf("ptrtoint (ptr getelementptr (%s, ptr null, i32 1) to i64)", t.rowTy)
			fmt.Fprintf(&body, "  call void @__kml_unit_register(i64 %d, ptr %s, i64 %d, i64 %s)\n", k, name, len(t.rows), stride)
		}
		e.emitGlobal(fmt.Sprintf("define internal void @__kml_unit_init() {\nentry:\n%s  ret void\n}", body.String()))
		e.addCtor("@__kml_unit_init")
	}
	if len(e.ctors) == 0 {
		return
	}
	elems := make([]string, len(e.ctors))
	for i, fn := range e.ctors {
		elems[i] = fmt.Sprintf("{ i32, ptr, ptr } { i32 65535, ptr %s, ptr null }", fn)
	}
	e.emitGlobal(fmt.Sprintf("@llvm.global_ctors = appending global [%d x { i32, ptr, ptr }] [%s]", len(e.ctors), strings.Join(elems, ", ")))
}

// emitUnitDispatch declares a hook `ptr @name(ptr %o<extra params>)` that
// unitreg.c defines: it finds the row for o's header type id in kind's table
// and calls its function with (o<extra args>), or returns null when no row
// matches. Only the two hooks C defines (kinds 0 and 1) are supported.
func (e *Emitter) emitUnitDispatch(name string, kind int, params, args string) {
	e.ensureUnitReg()
	e.emitGlobal(fmt.Sprintf("declare ptr @%s(ptr%s)", name, strings.ReplaceAll(params, " %depth", "")))
}

// unitFieldIR looks key up in kind's table and binds `%<out>` to field idx
// (of IR type ty) of the row found, or to def when there is none. Its
// blocks are named after out, so one function can hold several.
func unitFieldIR(kind int, rowTy, key string, idx int, ty, def, out string) string {
	return fmt.Sprintf(`  %%%[1]s.row = call ptr @__kml_unit_find(i64 %[2]d, i64 %[3]s)
  %%%[1]s.none = icmp eq ptr %%%[1]s.row, null
  br i1 %%%[1]s.none, label %%%[1]s.miss, label %%%[1]s.hit
%[1]s.hit:
  %%%[1]s.p = getelementptr %[4]s, ptr %%%[1]s.row, i32 0, i32 %[5]d
  %%%[1]s.v = load %[6]s, ptr %%%[1]s.p, align 8
  br label %%%[1]s.join
%[1]s.miss:
  br label %%%[1]s.join
%[1]s.join:
  %%%[1]s = phi %[6]s [ %[7]s, %%%[1]s.miss ], [ %%%[1]s.v, %%%[1]s.hit ]
`, out, kind, key, rowTy, idx, ty, def)
}

// emitClassIs is `instanceof` on a stored type id (a header word, or an
// Error's flagged kind slot): whether its class is target's or extends it.
func (e *Emitter) emitClassIs(stored string, target int64) string {
	e.ensureUnitReg()
	if !e.usedClassIs {
		e.usedClassIs = true
		e.emitGlobal("declare i32 @__kml_class_is(i64, i64)")
	}
	r, b := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_class_is(i64 %s, i64 %d)", r, stored, target))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", b, r))
	return b
}

// emitClassParentRows registers each class's parent: its base class's id,
// or for an Error subclass rooted at a builtin kind, that kind's.
func (e *Emitter) emitClassParentRows() {
	if !e.usedClassIs {
		return
	}
	var names []string
	for name, info := range e.classes {
		if info.TagID != 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		info := e.classes[name]
		parent := int64(-1)
		if base, ok := e.classes[info.BaseClass]; ok && base.TagID != 0 {
			parent = base.TagID & kmlHdrIDMask
		} else if kind, ok := errorKindIDs[info.BaseClass]; ok && info.IsErrorSubclass {
			parent = kind
		}
		if parent >= 0 {
			e.addUnitRow(unitKindClassParent, "{ i64, i64 }", info.TagID&kmlHdrIDMask, fmt.Sprintf("i64 %d", parent))
		}
	}
}
