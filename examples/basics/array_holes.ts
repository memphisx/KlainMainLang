// Array holes: elements a gap write, `new Array(n)`, an elided literal
// element or `delete` leave unset. Node reads them as undefined, skips them
// in forEach/map/filter/reduce, prints them as empty items and serializes
// them as null.
const names: string[] = ["ann"];
names[3] = "dan";
console.log(names, names.length, names[1]);
console.log(JSON.stringify(names), names.join("|"));

let visited = 0;
names.forEach(() => visited++);
console.log("visited", visited, "keys", Object.keys(names));
console.log(names.map((n) => n.toUpperCase()));

const slots: string[] = new Array(3);
slots[1] = "mid";
console.log(slots, 0 in slots, 1 in slots);

const sparse = [1, , 3];
console.log(sparse, sparse.filter(() => true));

const tags = ["a", "b", "c"];
delete tags[1];
console.log(tags, tags.hasOwnProperty(1), tags.includes(undefined as any));
tags.sort();
console.log(tags);
