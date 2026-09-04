// Vanilla JavaScript compiled natively under -compat=js: an array bracket
// index given as a *string* addresses the same slot as the equivalent number,
// exactly as JS specifies (`a["2"]` is `a[2]`). The same string-to-index
// conversion applies to the bounds arguments of the slice-style array methods
// (copyWithin/fill/slice/at/…). Run with:
//   klainmain -compat=js array_string_index.js

const a = [10, 20, 30, 40, 50];

// A string index reads and writes the canonical slot.
const i = "2";
console.log(a[i]);            // 30
console.log(a["4"]);          // 50

a["1"] = 99;
console.log(a[1]);            // 99

// Slice-style method bounds accept a string just as well.
const b = [0, 1, 2, 3];
console.log(b.copyWithin("0", 2).join(","));   // 2,3,2,3

const c = [1, 2, 3, 4, 5];
console.log(c.fill(9, "1", "3").join(","));     // 1,9,9,4,5

const d = [10, 20, 30, 40, 50];
console.log(d.slice("1", "4").join(","));       // 20,30,40
console.log(d.at("0"), d.at("2"));              // 10 30
