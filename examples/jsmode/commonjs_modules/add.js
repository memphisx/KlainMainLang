// A CommonJS module replacing module.exports with a function, then hanging
// a helper off it.
function add(a, b) {
  return a + b;
}
module.exports = add;
module.exports.twice = (x) => add(x, x);
