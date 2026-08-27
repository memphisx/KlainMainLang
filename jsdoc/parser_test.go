package jsdoc

import "testing"

func TestParamTypeAndReturn(t *testing.T) {
	c := Parse(`
 * Adds two numbers.
 * @param {int32} a the first addend
 * @param {int32} b the second addend
 * @returns {int32} their sum
`)
	if got := c.ParamType("a"); got != "int32" {
		t.Errorf(`ParamType("a") = %q, want "int32"`, got)
	}
	if got := c.ParamType("b"); got != "int32" {
		t.Errorf(`ParamType("b") = %q, want "int32"`, got)
	}
	if got := c.ParamType("missing"); got != "" {
		t.Errorf(`ParamType("missing") = %q, want ""`, got)
	}
	if got := c.ReturnType(); got != "int32" {
		t.Errorf(`ReturnType() = %q, want "int32"`, got)
	}
}

func TestParamDecorationsStripped(t *testing.T) {
	c := Parse(`
 * @param {...number} rest a varargs list
 * @param {number=} opt an optional
 * @param {string} [name] a bracketed optional name
 * @return {boolean}
`)
	if got := c.ParamType("rest"); got != "number" {
		t.Errorf("varargs strip: got %q, want number", got)
	}
	if got := c.ParamType("opt"); got != "number" {
		t.Errorf("optional-= strip: got %q, want number", got)
	}
	if got := c.ParamType("name"); got != "string" {
		t.Errorf("bracketed name: got %q, want string", got)
	}
	if got := c.ReturnType(); got != "boolean" {
		t.Errorf("@return alias: got %q, want boolean", got)
	}
}

func TestTypedefObject(t *testing.T) {
	defs := Parse(`
 * @typedef {Object} Point
 * @property {number} x
 * @property {number} y
`).Typedefs()
	if len(defs) != 1 {
		t.Fatalf("got %d typedefs, want 1", len(defs))
	}
	d := defs[0]
	if d.Name != "Point" || d.Kind != "object" {
		t.Fatalf("got %+v, want Point/object", d)
	}
	if len(d.Fields) != 2 || d.Fields[0].Name != "x" || d.Fields[1].Type != "number" {
		t.Errorf("fields = %+v", d.Fields)
	}
}

func TestTypedefAliasAndCallback(t *testing.T) {
	defs := Parse(`
 * @typedef {int32} MyInt
 * @callback Cb
 * @param {number} a
 * @param {string} b
 * @returns {boolean}
`).Typedefs()
	if len(defs) != 2 {
		t.Fatalf("got %d typedefs, want 2", len(defs))
	}
	if defs[0].Kind != "alias" || defs[0].Base != "int32" {
		t.Errorf("alias = %+v, want alias/int32", defs[0])
	}
	cb := defs[1]
	if cb.Kind != "callback" || cb.Name != "Cb" || cb.Return != "boolean" {
		t.Errorf("callback = %+v", cb)
	}
	if len(cb.Fields) != 2 || cb.Fields[0].Type != "number" || cb.Fields[1].Type != "string" {
		t.Errorf("callback params = %+v", cb.Fields)
	}
}

func TestTemplates(t *testing.T) {
	tps := Parse(`
 * @template T
 * @template K, V
 * @template {number} N
`).Templates()
	got := make(map[string]string)
	for _, tp := range tps {
		got[tp.Name] = tp.Constraint
	}
	for _, name := range []string{"T", "K", "V", "N"} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing type param %q (got %+v)", name, tps)
		}
	}
	if got["N"] != "number" {
		t.Errorf("N constraint = %q, want number", got["N"])
	}
	if got["T"] != "" {
		t.Errorf("T constraint = %q, want empty", got["T"])
	}
}

func TestNestedBraceTypeBody(t *testing.T) {
	// An inline object type carries nested braces — the balanced-brace scanner
	// must keep the whole `{x: number, y: number}` body, not truncate at the
	// first `}` (TDD-00125 Stage 4).
	c := Parse(`* @param {{x: number, y: number}} p the point`)
	if got := c.ParamType("p"); got != "{x: number, y: number}" {
		t.Errorf("ParamType(p) = %q, want the full object body", got)
	}
}

func TestArgAliases(t *testing.T) {
	c := Parse(` * @arg {int32} x` + "\n" + ` * @argument {string} y`)
	if got := c.ParamType("x"); got != "int32" {
		t.Errorf(`@arg alias: got %q, want int32`, got)
	}
	if got := c.ParamType("y"); got != "string" {
		t.Errorf(`@argument alias: got %q, want string`, got)
	}
}
