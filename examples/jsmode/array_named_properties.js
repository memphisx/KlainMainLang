// An array is an object: besides its elements it holds named properties,
// property attributes and accessors, as in any JavaScript engine.
// Run with:  klainmain -compat=js array_named_properties.js

const temps = [21, 24, 19];
temps.city = "Thessaloniki";
temps.unit = "C";
console.log(temps);                              // [ 21, 24, 19, city: 'Thessaloniki', unit: 'C' ]
console.log(Object.keys(temps).join());          // 0,1,2,city,unit
console.log(JSON.stringify(temps));              // [21,24,19] (named keys are not elements)

// A method stored on the array sees it as `this`.
temps.average = function () {
  return this.reduce((a, b) => a + b, 0) / this.length;
};
console.log(temps.average().toFixed(1));         // 21.3

// Attributes: a non-enumerable property stays out of keys and inspect.
Object.defineProperty(temps, "source", { value: "sensor-7" });
console.log(temps.source, Object.keys(temps).includes("source")); // sensor-7 false

// An accessor element: reads run the getter, writes the setter.
let latest = 0;
Object.defineProperty(temps, "0", {
  get() { return latest; },
  set(v) { latest = v; },
  enumerable: true,
  configurable: true,
});
temps[0] = 30;
console.log(temps[0], temps.map((t) => t + 1).join()); // 30 31,25,20
console.log(temps);                              // [Getter/Setter] in place of the element, its getter not run

// An element of another kind widens the array, as JavaScript allows.
const mixed = [1, 2];
mixed[2] = "three";
console.log(mixed);                              // [ 1, 2, 'three' ]
