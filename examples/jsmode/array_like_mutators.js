// Array.prototype's mutators borrowed by an array-like object: pop, push,
// shift, unshift, splice and reverse read and write the object's own
// indexed properties and its length, as in Node.
// Run with:  klainmain -compat=js array_like_mutators.js
var obj = {};
obj.pop = Array.prototype.pop;
obj[0] = "x";
obj[4294967296] = "y";
obj.length = 4294967297;
console.log(obj.pop(), obj.length, obj[0], obj[4294967296]);
var o2 = { 0: "a", 1: "b", length: 2 };
console.log(Array.prototype.push.call(o2, "c", "d"), JSON.stringify(o2));
console.log(Array.prototype.shift.call(o2), JSON.stringify(o2));
console.log(Array.prototype.unshift.call(o2, "z"), JSON.stringify(o2));
console.log(JSON.stringify(Array.prototype.splice.call(o2, 1, 2, "q")), JSON.stringify(o2));
console.log(JSON.stringify(Array.prototype.reverse.call(o2)));
var o3 = { length: 9007199254740991 };
try { Array.prototype.push.call(o3, 1); } catch (e) { console.log(e.constructor.name); }
var o4 = {};
console.log(Array.prototype.pop.call(o4), o4.length);
var arr = [1, 2, 3];
console.log(Array.prototype.pop.call(arr), arr);

// The read-only and copying methods work the same way.
var scores = { 0: 3, 1: 1, 3: 2, length: 5 };
console.log(Array.prototype.indexOf.call(scores, 2), Array.prototype.join.call(scores, "-"));
console.log(JSON.stringify(Array.prototype.map.call(scores, function (v) { return v * 10; })));
console.log(JSON.stringify(Array.prototype.toSorted.call(scores)));
