package diag

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestListOnePerPosition(t *testing.T) {
	var l List
	a := New(ExpectedSemicolon, Span{Pos: Pos{Line: 1, Col: 5}, Start: 4, End: 5}, "IDENT")
	b := New(UnexpectedTokenInExpr, Span{Pos: Pos{Line: 1, Col: 5}, Start: 4, End: 5}, ";")
	c := New(UnexpectedTokenInExpr, Span{Pos: Pos{Line: 2, Col: 1}, Start: 10, End: 11}, "}")
	if !l.Add(a) || l.Add(b) || !l.Add(c) || l.Len() != 2 {
		t.Fatalf("list %v", l.Items())
	}
	if got := l.Err().Error(); got != "1:5: expected ';', got IDENT\n2:1: unexpected token } in expression" {
		t.Errorf("Error() = %q", got)
	}
}

func TestInFileAndAs(t *testing.T) {
	var l List
	l.Add(New(UnterminatedString, Span{Pos: Pos{Line: 3, Col: 2}}))
	err := InFile(l.Err(), "a.ts")
	if got := err.Error(); got != "a.ts: 3:2: unterminated string literal" {
		t.Errorf("got %q", got)
	}
	if ds := As(fmt.Errorf("wrapped: %w", err)); len(ds) != 1 || ds[0].File != "a.ts" {
		t.Errorf("As through a wrap: %v", ds)
	}
	if As(errors.New("plain")) != nil {
		t.Error("a plain error has no diagnostics")
	}
	if got := InFile(errors.New("plain"), "b.ts").Error(); got != "b.ts: plain" {
		t.Errorf("plain error in file: %q", got)
	}
}

func TestWriteJSON(t *testing.T) {
	d := New(IndexSignatureKey, Span{File: "x.ts", Pos: Pos{Line: 1, Col: 14}, Start: 13, End: 20}, "boolean")
	var b bytes.Buffer
	if err := WriteJSON(&b, []*Diagnostic{d}); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"code": 1268.0, "severity": "error", "file": "x.ts", "line": 1.0, "col": 14.0, "start": 13.0, "end": 20.0, "kind": "SyntaxError", "phase": "parse"}
	for k, v := range want {
		if got[0][k] != v {
			t.Errorf("%s = %v, want %v", k, got[0][k], v)
		}
	}
}

// Every code below FirstOwnCode is TypeScript's; the own codes are unique per
// condition family, and every message has a phase.
func TestMessageTable(t *testing.T) {
	for _, m := range []*Message{ExpectedToken, UnterminatedString, RestTupleElement, PureNotFunction} {
		if m.Phase == "" || m.Text == "" {
			t.Errorf("%+v", m)
		}
	}
	if RestTupleElement.Code < FirstOwnCode || ExpectedToken.Code >= FirstOwnCode {
		t.Error("code ranges")
	}
}
