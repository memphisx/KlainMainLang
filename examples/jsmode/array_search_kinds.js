// indexOf, lastIndexOf and includes compare with strict equality: a search
// value of another kind than the elements is never converted, so it matches
// nothing. "1" is not 1, true is not 1, and an object is found only by
// identity.
// Run with:  klainmain -compat=js array_search_kinds.js

const stops = [1, 2, 3];
console.log(stops.indexOf("1"), stops.lastIndexOf("3"), stops.includes("2"));
console.log([1, 0].indexOf(true), [true].indexOf(1));

const station = { name: "Thessaloniki" };
const route = [0, station];
console.log(route.indexOf(station), [0, 1].indexOf(station), [{ name: "Thessaloniki" }].indexOf(station));
