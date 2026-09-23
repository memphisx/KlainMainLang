// A literal-only array literal — a lookup table of string/number/boolean
// literals, or a table of such rows — is compiled to one static constant plus
// a single copy into a fresh heap buffer (ADR-01064), so a generated
// thousands-of-rows table compiles in the same time as any other program.
// The array itself is an ordinary array: push/reverse/reassignment and
// aliasing behave exactly as for a literal built element by element.

const primes = [2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 67, 71];
const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
                "jan", "feb", "mar", "apr"];

primes.push(73);
console.log(primes.length, primes[20], primes.reduce((a, b) => a + b, 0)); // 21 73 712
console.log(months.slice(0, 12).join(" "));                                 // Jan ... Dec

// A table of literal rows: one flat constant + a row-length table; each row
// is its own mutable array with reference semantics.
const caseMap = [["a", "A"], ["b", "B"], ["ß", "SS"], ["ﬁ", "FI"]];
const row = caseMap[2];
row.push("ss");
caseMap[0][1] = "Z";
console.log(caseMap.length, caseMap[0][1], caseMap[2], caseMap[3][1].length); // 4 Z [ 'ß', 'SS', 'ss' ] 2

// Rows may differ in length, including empty rows.
const ragged = [[1, 2, 3], [4], [], [5, 6]];
console.log(ragged.map((r) => r.length).join(","), ragged[3][1]); // 3,1,0,2 6

// Anything non-constant in the literal (an identifier, a spread, a call)
// keeps the literal on the element-by-element path — same result.
const base = 100;
const mixed = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, base];
console.log(mixed[15], mixed.length); // 100 16
