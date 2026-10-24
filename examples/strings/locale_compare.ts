// localeCompare orders text as Node's ICU does: letters before accents
// before case, so accented and uppercase words sort next to their plain form.

const names = ["Zoë", "zoe", "Ångström", "angstrom", "Émile", "emile", "Straße", "strasse", "Øresund", "oresund"];
console.log([...names].sort((a, b) => a.localeCompare(b)).join(", "));

// Greek and Cyrillic follow their own alphabets.
console.log(["Θεσσαλονίκη", "Αθήνα", "Πάτρα", "Ηράκλειο"].sort((a, b) => a.localeCompare(b)).join(", "));

// numeric: true compares digit runs by value.
const files = ["track10.mp3", "track2.mp3", "Track1.mp3"];
console.log(files.sort((a, b) => a.localeCompare(b, undefined, { numeric: true })).join(" "));

// sensitivity picks the levels: "base" ignores accents and case.
console.log("resume".localeCompare("Résumé", "en", { sensitivity: "base" })); // 0
console.log("resume".localeCompare("Résumé", "en", { sensitivity: "case" })); // -1
