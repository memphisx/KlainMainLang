package main

// Schema for a per-area status source (docs/status/data/<id>.json).
//
// A page is a title plus an ordered list of segments, rendered joined by
// blank lines. Everything numeric (coverage fractions, percentages) is
// derived from the table rows by the generator and never stored; free-form
// prose is stored as verbatim Markdown blocks so the round-trip is byte-exact.

type StatusArea struct {
	ID       string    `json:"id"`   // slug, e.g. "string-methods"
	File     string    `json:"file"` // "STRING-METHODS.md"
	Title    string    `json:"title"`
	Segments []Segment `json:"segments"`
}

// Segment kinds:
//   - "block": Text is a verbatim Markdown block (paragraph, blockquote,
//     heading, list, footer — anything), possibly multi-line.
//   - "coverage": the derived Coverage/Strict line(s); Style selects the
//     rendering. A page with bespoke coverage wording has no coverage
//     segment — its wording is an ordinary block.
//   - "table": a feature table, with an optional attached "## …" heading.
type Segment struct {
	Kind  string `json:"kind"`
	Text  string `json:"text,omitempty"`  // block only
	Style string `json:"style,omitempty"` // coverage only: "standard" | "multiCategory"
	Table *Table `json:"table,omitempty"` // table only
}

type Table struct {
	// Heading names the "## …" line directly above the table (blank line
	// between). On multi-category pages it is also the category name in the
	// derived coverage line. Empty when the table has no attached heading.
	Heading       string `json:"heading,omitempty"`
	FeatureColumn string `json:"featureColumn"` // "Feature", "Tag", "API", …
	HasCaveats    bool   `json:"hasCaveats"`
	HasNotes      bool   `json:"hasNotes"`
	// False for tables excluded from the coverage figures (this project's
	// own extensions on JSDOC).
	CountsTowardParity bool  `json:"countsTowardParity"`
	Rows               []Row `json:"rows"`
}

type Row struct {
	// Verbatim cell text, backticks and escaped \| included.
	Feature string `json:"feature"`
	Status  string `json:"status"` // "implemented" (✅) | "missing" (❌)
	// Bullet fragments without the leading "• "; joined with "<br>• " on
	// render. A cell holding plain prose instead of a bullet list is a
	// single fragment with the matching *Plain flag set.
	Caveats      []string `json:"caveats,omitempty"`
	Notes        []string `json:"notes,omitempty"`
	CaveatsPlain bool     `json:"caveatsPlain,omitempty"`
	NotesPlain   bool     `json:"notesPlain,omitempty"`
}
