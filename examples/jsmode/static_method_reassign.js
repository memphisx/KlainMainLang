// A class's static method is a writable property: replacing it changes what
// later reads and calls reach, and a primitive's property write goes through
// its wrapper's prototype chain (a Proxy there sees it).

class Greeter {
  static greet(name) {
    return "Hello, " + name;
  }
}

const original = Greeter.greet;
Greeter.greet = function (name) {
  return original.call(this, name) + "!";
};
console.log(Greeter.greet("Thessaloniki"));
console.log(Greeter.greet === original);

let writes = 0;
Object.setPrototypeOf(Number.prototype, new Proxy({}, {
  set() {
    writes += 1;
    return true;
  },
}));
(42).label = "answer";
console.log("writes seen:", writes, "label:", (42).label);
