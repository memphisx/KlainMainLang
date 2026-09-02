// Vanilla, untyped JavaScript compiled natively under -compat=js: class
// fields come into being via constructor assignments (no declarations), and
// unannotated constructor parameters take their types from the `new` call
// sites. Run with:  klainmain -compat=js vanilla_class.js

class City {
  constructor(name, population) {
    this.name = name;
    this.population = population;
    this.visited = false;
  }
  describe() {
    return this.name + " has " + this.population + " residents";
  }
  visit() { this.visited = true; }
}

class Capital extends City {
  constructor(name, population, country) {
    super(name, population);
    this.country = country;
  }
  describe() {
    return this.name + " is the capital of " + this.country;
  }
}

const home = new City("Thessaloniki", 1030338);
console.log(home.describe());
home.visit();
console.log(home.name, home.visited);

const cap = new Capital("Athens", 3153000, "Greece");
console.log(cap.describe());
console.log(cap.population > home.population);

// Plain-JS definite assignment: TS would reject this read pattern, JS mode
// accepts it (the branches cover both cases at runtime).
let rank;
if (cap.population > home.population) { rank = 1; }
if (cap.population <= home.population) { rank = 2; }
console.log(rank);

// Unannotated function parameters are typed from their call sites too —
// this is idiomatic untyped JS, compiled to concrete native code.
// Real JS operator semantics on dynamic values (TDD-00076, NaN-boxed):
// polymorphic functions, coercion, NaN, truthiness — all at runtime.
function idn(v) { return v; }              // called with numbers AND strings → implicit any
console.log(idn(2) + idn(3));              // 5
console.log(idn("con") + idn("cat"));      // concat
console.log(idn("6") * 7);                 // 42 — ToNumber on a numeric string
console.log(null + 1, true + 1);           // 1 2
let undef;
console.log(undef + 1);                    // NaN
console.log(idn("10") < 9, idn("a") < "b"); // false true — numeric vs lexicographic
if (!idn("")) { console.log("empty string is falsy"); }

function Point(x, y) { this.x = x; this.y = y; }
Point.prototype.dist = function() {
  return Math.sqrt((this.x * this.x) + (this.y * this.y));
};
console.log(new Point(3, 4).dist());       // 5 — arithmetic on boxed fields

// Property descriptors (D1 Stage 5): accessors, enumerability, freeze —
// and untyped literals are real dynamic objects (ad-hoc properties work).
const account = {
  _balance: 100,
  get balance() { return this._balance; },
  set balance(v) { if (v >= 0) this._balance = v; }
};
console.log(account.balance);          // 100
account.balance = 250;
account.balance = -5;                   // ignored by the setter
console.log(account.balance);          // 250
Object.defineProperty(account, "iban", { value: "GR16-0110", enumerable: false });
console.log(account.iban, Object.keys(account));      // hidden from keys
console.log(JSON.stringify(account));                 // getters serialize
const settings = { theme: "dark" };
settings.locale = "el-GR";              // dynamic add on an untyped literal
Object.freeze(settings);
try { settings.theme = "light"; } catch (e) { console.log("frozen:", e.name); }
console.log(settings.theme, Object.isFrozen(settings));

// Pre-ES6 prototype classes work too (D1 Stage 4): function constructors,
// prototype methods with a real `this`, and the classic inheritance chain.
function Shape(kind) {
  this.kind = kind;
}
Shape.prototype.label = function() {
  return "a " + this.kind;
};
function Square(size) {
  Shape.call(this, "square");
  this.size = size;
}
Square.prototype = Object.create(Shape.prototype);
Square.prototype.label = function() {
  return "a " + this.kind + " of size " + this.size;
};
const sq = new Square(4);
console.log(sq.label());                          // a square of size 4
console.log(new Shape("circle").label());         // a circle
console.log(sq.__proto__ === Square.prototype);   // true

function describe(city, times) {
  let out = city.name;
  for (let i = 1; i < times; i++) { out = out + ", " + city.name; }
  return out;
}
console.log(describe(home, 2));
console.log(greet("from Thessaloniki"));
function greet(suffix) { return "hello " + suffix; }
