// Strings index by UTF-16 code units, as in Node: an accented letter is one
// unit, an emoji outside the Basic Multilingual Plane two (a surrogate pair).

const city = "Θεσσαλονίκη";
console.log(city.length, city[0], city.slice(0, 5), city.indexOf("λ")); // 11 Θ Θεσσα 5

const tag = "café ☕ 😀";
console.log(tag.length);                         // 9: the emoji is two units
console.log(tag.charCodeAt(7).toString(16));     // d83d, its high surrogate
console.log(tag.codePointAt(7)!.toString(16));   // 1f600, the whole character
console.log([...tag].length);                    // 8: iteration walks code points

// Each half on its own is a lone surrogate; joined they are the emoji again.
const hi = tag.slice(7, 8), lo = tag.slice(8);
console.log(JSON.stringify(hi), hi + lo === "😀"); // "\ud83d" true

// Padding and searching count units too.
console.log("ü".padStart(3, "·"), "naïve".lastIndexOf("v"), "aé😀".split("").length); // ··ü 3 4
