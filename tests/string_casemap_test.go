package tests

import "testing"

// ADR-01075: toUpperCase/toLowerCase are the Unicode Default Case Conversion —
// simple mappings, SpecialCasing expansions (ß → SS, İ → i̇, ﬁ → FI, ᾀ → ἈΙ),
// Final_Sigma — on every plane.
func TestE2EStringCaseMappingUnicode(t *testing.T) {
	const src = `
const s = ["ß", "İ", "ŉ", "ﬁ", "ǰ", "ᾀ", "ΟΔΥΣΣΕΥΣ", "ΣΑΣ Σ", "Σ.", "AΣ'", "straße", "ÀÉÎõü", "𐐀𐐨", "𝔘nicode", "ǅ", "Ⅷ", "ⓐ", "hello", "HELLO"];
for (const x of s) console.log(JSON.stringify(x.toUpperCase()), JSON.stringify(x.toLowerCase()));
console.log("straße".toUpperCase().length);
`
	assertOutput(t, src, `"SS" "ß"
"İ" "i̇"
"ʼN" "ŉ"
"FI" "ﬁ"
"J̌" "ǰ"
"ἈΙ" "ᾀ"
"ΟΔΥΣΣΕΥΣ" "οδυσσευς"
"ΣΑΣ Σ" "σας σ"
"Σ." "σ."
"AΣ'" "aς'"
"STRASSE" "straße"
"ÀÉÎÕÜ" "àéîõü"
"𐐀𐐀" "𐐨𐐨"
"𝔘NICODE" "𝔘nicode"
"Ǆ" "ǆ"
"Ⅷ" "ⅷ"
"Ⓐ" "ⓐ"
"HELLO" "hello"
"HELLO" "hello"
7`)
	assertSameAsNode(t, src)
}
