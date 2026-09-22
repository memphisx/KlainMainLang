package tests

import (
	"strings"
	"testing"
	"time"
)

// Type inference asks "what is the receiver?" many times per member call; each
// ask used to re-infer the whole receiver, so a method chain cost
// probes^depth — a five-link `.then` chain compiled in ~52 s (the Test262
// top-level-await "ticks" files timed out in codegen), a sixth link would not
// finish. Compile time must stay flat in the chain's depth.
func TestE2EDeepMethodChainCompilesInLinearTime(t *testing.T) {
	var src strings.Builder
	src.WriteString("const log: string[] = [];\nPromise.resolve(0)\n")
	for i := 0; i < 24; i++ {
		src.WriteString("  .then(() => log.push('t'))\n")
	}
	src.WriteString("  .then(() => { console.log(log.length); });\n")
	src.WriteString("const s = ' a '.trim().toUpperCase().toLowerCase().trim().padStart(3, 'x').padEnd(5, 'y').slice(1).repeat(2).trim().toUpperCase();\nconsole.log(s);\n")
	src.WriteString("const n = [1, 2, 3, 4].map((x) => x + 1).filter((x) => x > 2).map((x) => x * 2).filter((x) => x > 0).map((x) => x - 1).reduce((a, b) => a + b, 0);\nconsole.log(n);\n")
	start := time.Now()
	assertOutput(t, src.String(), "XAYYXAYY\n21\n24\n")
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("a 24-link chain took %v to compile and run — inference is super-linear in chain depth again", d)
	}
}
