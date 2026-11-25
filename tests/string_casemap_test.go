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

// toLocaleUpperCase/toLocaleLowerCase: Turkish/Azeri and Lithuanian
// mappings, the locale list's checks, and NUL-holding strings.
func TestE2EStringLocaleCase(t *testing.T) {
	assertSameAsNode(t, `
const t = ["istanbul İI ı", "IİJ̇", "i̇", "ÌÍĨĮ́", "é\u0000É"];
const show = (x: string) => [...x].map(c => c.codePointAt(0)!.toString(16)).join(".");
for (const l of [undefined, "tr", "az", "TR", "tr-TR", ["en", "tr"], ["tr", "en"], "lt", "en", []] as any[])
  console.log(JSON.stringify(l), t.map(s => show(s.toLocaleUpperCase(l)) + "|" + show(s.toLocaleLowerCase(l))).join(" ; "));
for (const v of [[5], ["en", 5], "und", "i-klingon", "en-", "EN-us", "root", "x-private", "en-US-u-ca-gregory", "en--US", "-en", "", "en_US", "tr-", 5, null] as any[]) {
  try { console.log(JSON.stringify(v), show("i".toLocaleUpperCase(v))); } catch (e) { console.log(JSON.stringify(v), (e as Error).name, (e as Error).message); }
}
console.log("é\u0000é".toUpperCase().length, "É\u0000".toLowerCase() === "é\u0000", ("é\u0000é").normalize().length);
`)
}
