package tests

import (
	"strings"
	"testing"
	"time"
)

// ANSI colour written by a plain console.log — no raw mode — reaches the
// console as a real VT sequence, not a mangled or doubled escape: the shim
// enables VT output processing at startup as Node does (ADR-00744). Under the
// pseudo-console (ADR-00727) the red sequence survives around the text. On a
// legacy conhost this is what makes coloured output render at all rather than
// printing the escape literally; the pseudo-console cannot show that
// difference (it is VT-native) but does confirm the sequence is emitted and
// processed end-to-end without the program entering raw mode.
func TestE2ETtyConsoleStartupVTOutput(t *testing.T) {
	bin := buildBinary(t, "console.log('\\x1b[31mRED\\x1b[0m done')")
	res := runInConPTY(t, bin, 80, 24, 20*time.Second, nil)
	if !strings.Contains(res.Output, "\x1b[31m") || !strings.Contains(res.Output, "RED") {
		t.Fatalf("VT colour sequence not present/processed in console output:\n%q", res.Output)
	}
}
