package tests

import (
	"strings"
	"testing"
	"time"
)

// Non-ASCII console output renders correctly rather than as OEM-code-page
// mojibake. console.log writes UTF-8 bytes; a Windows console reads them in
// its OEM code page unless told otherwise, so "café-日本語-Ω" would come out
// as "caf├®-µùÑ…". The shim sets the console to UTF-8 at startup and restores
// it at exit (ADR-00747), so the pseudo-console renders the original text.
func TestE2EConsoleUTF8Output(t *testing.T) {
	bin := buildBinary(t, "console.log('café-日本語-Ω')")
	res := runInConPTY(t, bin, 80, 24, 20*time.Second, nil)
	if !strings.Contains(res.Output, "café-日本語-Ω") {
		t.Fatalf("non-ASCII console output not rendered as UTF-8:\n%q", res.Output)
	}
}
