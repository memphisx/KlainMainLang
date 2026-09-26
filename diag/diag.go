// Package diag holds the compiler's diagnostics: every user-facing error a
// phase reports is a Diagnostic built from a Message in the table
// (messages.go), collected per file in a List.
//
// A message carries a stable numeric code. Where the condition is one
// TypeScript also reports, the code is TypeScript's (TS1005 "';' expected"),
// so the TypeScript oracle compares by code rather than wording; codes from
// FirstOwnCode up are this compiler's own. A message also records the
// JavaScript error kind the condition corresponds to (a SyntaxError for the
// grammar) and the Test262 phase that reports it, for -compat=js and
// Test262's negative tests.
package diag

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// FirstOwnCode is the first code that is not TypeScript's.
const FirstOwnCode = 90000

// Severity is how serious a diagnostic is.
type Severity int

const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// ErrorKind is the JavaScript error a diagnostic corresponds to.
type ErrorKind string

const (
	SyntaxError    ErrorKind = "SyntaxError"
	ReferenceError ErrorKind = "ReferenceError"
	TypeError      ErrorKind = "TypeError"
	RangeError     ErrorKind = "RangeError"
	// Unsupported marks a construct valid in JavaScript/TypeScript that this
	// compiler does not implement: no JavaScript error corresponds to it.
	Unsupported ErrorKind = ""
	// TypeScriptError marks a program TypeScript's type rules reject that
	// JavaScript would run: the strict lane's checker errors (TDD-00230
	// P2.7), which -compat=js compiles.
	TypeScriptError ErrorKind = "TypeScript"
)

// Phase is the Test262 phase a diagnostic belongs to.
type Phase string

const (
	PhaseParse      Phase = "parse"
	PhaseResolution Phase = "resolution"
	PhaseCheck      Phase = "check"
)

// Message is one entry of the message table. Text is a fmt format.
type Message struct {
	Code     int
	Severity Severity
	Kind     ErrorKind
	Phase    Phase
	Text     string
}

// Pos is a 1-based line and column.
type Pos struct{ Line, Col int }

// Span is a source location: the file, the line and column of its start, and
// its byte range [Start, End).
type Span struct {
	File       string
	Pos        Pos
	Start, End int
}

// Related is a secondary location ("declared here").
type Related struct {
	Span
	Text string
}

// Diagnostic is one reported condition.
type Diagnostic struct {
	Message *Message
	Text    string // Message.Text formatted with the arguments
	Span
	Related []Related
	Hint    string
	// Payload is structured detail for tools (for example the missing
	// symbol's qualified name, which the conformance blocked-by histogram
	// groups by).
	Payload map[string]string
}

// New builds a diagnostic for m at sp.
func New(m *Message, sp Span, args ...any) *Diagnostic {
	return &Diagnostic{Message: m, Text: fmt.Sprintf(m.Text, args...), Span: sp}
}

// Code returns the diagnostic's code.
func (d *Diagnostic) Code() int { return d.Message.Code }

// Error renders the diagnostic as `line:col: text`, or the text alone when
// the position is unknown.
func (d *Diagnostic) Error() string {
	if d.Pos.Line == 0 {
		return d.Text
	}
	return fmt.Sprintf("%d:%d: %s", d.Pos.Line, d.Pos.Col, d.Text)
}

// List collects the diagnostics of one file, in report order.
type List struct {
	items []*Diagnostic
}

// Add appends d, unless the last diagnostic is at the same position: one
// error per position, so a recovery that stops twice at one token reports
// once. It reports whether d was added.
func (l *List) Add(d *Diagnostic) bool {
	if n := len(l.items); n > 0 && l.items[n-1].Pos == d.Pos && l.items[n-1].File == d.File {
		return false
	}
	l.items = append(l.items, d)
	return true
}

// Len is the number of diagnostics collected.
func (l *List) Len() int { return len(l.items) }

// Truncate drops every diagnostic after the first n (a speculative parse
// that is rewound takes its diagnostics with it).
func (l *List) Truncate(n int) { l.items = l.items[:n] }

// Items returns the diagnostics in report order.
func (l *List) Items() []*Diagnostic { return l.items }

// Err returns the collected diagnostics as an error, or nil when there are
// none.
func (l *List) Err() error {
	if len(l.items) == 0 {
		return nil
	}
	return Errors(append([]*Diagnostic(nil), l.items...))
}

// Located renders the diagnostic as `file: line:col: text`, or as Error does
// when the file is not known.
func (d *Diagnostic) Located() string {
	if d.File == "" {
		return d.Error()
	}
	return d.File + ": " + d.Error()
}

// Errors is a non-empty set of diagnostics used as an error. Its message is
// one line per diagnostic, each naming its file when known.
type Errors []*Diagnostic

func (e Errors) Error() string {
	lines := make([]string, len(e))
	for i, d := range e {
		lines[i] = d.Located()
	}
	return strings.Join(lines, "\n")
}

// As returns the diagnostics carried by err (or an error it wraps): every
// diagnostic of an Errors, a single *Diagnostic, or nil for any other error.
func As(err error) []*Diagnostic {
	var es Errors
	if errors.As(err, &es) {
		return es
	}
	var d *Diagnostic
	if errors.As(err, &d) {
		return []*Diagnostic{d}
	}
	return nil
}

// InFile returns err with every diagnostic it carries attributed to file, as
// an Errors; any other error comes back as `file: err`.
func InFile(err error, file string) error {
	ds := As(err)
	if ds == nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	for _, d := range ds {
		d.File = file
	}
	return Errors(ds)
}

type jsonRelated struct {
	File    string `json:"file,omitempty"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Message string `json:"message"`
}

type jsonDiagnostic struct {
	Code     int               `json:"code"`
	Severity string            `json:"severity"`
	File     string            `json:"file,omitempty"`
	Line     int               `json:"line"`
	Col      int               `json:"col"`
	Start    int               `json:"start"`
	End      int               `json:"end"`
	Message  string            `json:"message"`
	Kind     string            `json:"kind,omitempty"`
	Phase    string            `json:"phase,omitempty"`
	Hint     string            `json:"hint,omitempty"`
	Related  []jsonRelated     `json:"related,omitempty"`
	Payload  map[string]string `json:"payload,omitempty"`
}

// WriteJSON writes ds as a JSON array, one object per diagnostic.
func WriteJSON(w io.Writer, ds []*Diagnostic) error {
	out := make([]jsonDiagnostic, 0, len(ds))
	for _, d := range ds {
		j := jsonDiagnostic{
			Code: d.Message.Code, Severity: d.Message.Severity.String(),
			File: d.File, Line: d.Pos.Line, Col: d.Pos.Col, Start: d.Start, End: d.End,
			Message: d.Text, Kind: string(d.Message.Kind), Phase: string(d.Message.Phase),
			Hint: d.Hint, Payload: d.Payload,
		}
		for _, r := range d.Related {
			j.Related = append(j.Related, jsonRelated{File: r.File, Line: r.Pos.Line, Col: r.Pos.Col, Start: r.Start, End: r.End, Message: r.Text})
		}
		out = append(out, j)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// Offsets returns a function giving the byte offset in src of a 1-based line
// and column, where a column counts characters and a line starts after '\n'
// (the positions the scanner reports).
func Offsets(src string) func(line, col int) int {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return func(line, col int) int {
		if line < 1 || line > len(starts) || col < 1 {
			return -1
		}
		off := starts[line-1]
		for c := 1; c < col && off < len(src); c++ {
			_, size := utf8.DecodeRuneInString(src[off:])
			off += size
		}
		return off
	}
}
