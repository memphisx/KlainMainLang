package llvm

import "KlainMainLang/ast"

// pureDisplayName strips the resolver's per-file mangling suffix so a
// diagnostic shows the source-level name.
func pureDisplayName(s string) string { return ast.UnmangleText(s) }
