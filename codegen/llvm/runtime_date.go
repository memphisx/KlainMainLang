// runtime_date.go — Date and performance clocks: the wall/monotonic reads,
// the UTC calendar decompose/compose, the ISO 8601 parser and the name
// tables. They live in datesrc/date.c (TDD-00240).
package llvm

import _ "embed"

//go:embed datesrc/date.c
var dateSource string

// DateSource is the Date/performance runtime's C source.
func DateSource() string { return dateSource }
