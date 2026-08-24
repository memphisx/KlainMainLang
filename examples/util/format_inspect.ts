import util from 'util';

console.log(util.format("%s = %d", "count", 42));
console.log(util.format("pi is %f", 3.14));
console.log(util.format("json: %j", { a: 1, b: [2, 3] }));
console.log(util.format("100%% done", "extra", 7));
console.log(util.inspect("a string"));
console.log(util.inspect([1, 2, 3]));
console.log(util.inspect({ name: "kml", tags: ["ts", "native"] }));
