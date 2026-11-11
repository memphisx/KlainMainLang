package llvm

import _ "embed"

//go:embed stringsrc/strings.c
var stringsCSource string

// StringsCSource is the string trim/case/replace/split runtime's C source.
func StringsCSource() string { return stringsCSource }
