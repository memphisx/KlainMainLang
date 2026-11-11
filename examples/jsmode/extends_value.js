// A class may extend any constructor a variable holds, not only a declared
// class or function: a function expression, a bound function, a function an
// object hands back. The heritage is evaluated where the class is defined,
// as JavaScript does — a value that is no constructor (an arrow, a
// generator) throws a TypeError there, and so does a `prototype` that is
// neither an object nor null. Run with:  klainmain -compat=js extends_value.js

var Point = function (x, y) {
  this.x = x;
  this.y = y;
};
Point.prototype.norm = function () {
  return Math.sqrt(this.x * this.x + this.y * this.y);
};

class Labeled extends Point {
  constructor(label, x, y) {
    super(x, y);
    this.label = label;
  }
  toString() {
    return this.label + " (" + this.norm() + ")";
  }
}

var p = new Labeled("Thessaloniki", 3, 4);
console.log(String(p));                     // Thessaloniki (5)
console.log(p instanceof Point);            // true

var arrow = () => {};
try {
  (function () {
    class Nope extends arrow {}
  })();
} catch (e) {
  console.log(e instanceof TypeError);      // true
}
