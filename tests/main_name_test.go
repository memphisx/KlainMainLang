package tests

import "testing"

// A program may name a function main, as under the CLI.
func TestE2ETopLevelFunctionNamedMain(t *testing.T) {
	assertSameAsNode(t, `
async function main() { console.log("hi", await Promise.resolve(1)); }
main();
`)
}
