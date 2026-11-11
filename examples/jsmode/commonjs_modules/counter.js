// A CommonJS module growing `exports`: every require of it sees the same
// module instance, so the count is shared.
let count = 0;
exports.inc = () => ++count;
exports.get = () => count;
