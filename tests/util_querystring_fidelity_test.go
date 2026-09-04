package tests

import "testing"

// Node-fidelity fixes for util.format (ADR-00696):
//   - %d formats a number as-is (no truncation); only %i truncates.
//   - %c consumes its argument and emits nothing (CSS directive).

func TestE2EUtilFormatPercentDDoesNotTruncateFloat(t *testing.T) {
	// Node: format('%d', 3.7) === '3.7'; format('%i', 3.7) === '3'.
	assertOutputImports(t, `
import util from 'util'
console.log(util.format("%d", 3.7))
console.log(util.format("%i", 3.7))
console.log(util.format("%d", 42))
`, "3.7\n3\n42")
}

func TestE2EUtilFormatPercentCConsumesArgEmitsNothing(t *testing.T) {
	// Node: format('%c', 'red') === '' and the arg is consumed.
	assertOutputImports(t, `
import util from 'util'
console.log(util.format("%c", "red") + "|")
console.log(util.format("a %c b", "styled") + "|")
`, "|\na  b|")
}
