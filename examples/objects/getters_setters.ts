// --- Object-literal getters and setters (TDD-00153) ---
// A `get x()` / `set x(v)` property runs a function on read / write. The
// accessor body has a real `this` bound to the object — so it reads and
// writes the object's own data fields. Under the hood the literal is lowered
// to a synthetic anonymous class, reusing the same accessor machinery classes
// use. Runs identically under Node.js.

const temperature = {
    _celsius: 20,

    // A getter derives a value from stored state.
    get celsius(): number {
        return this._celsius;
    },
    // A setter validates/normalizes on write.
    set celsius(v: number) {
        this._celsius = v;
    },
    // A computed, read-only view over the same backing field.
    get fahrenheit(): number {
        return this._celsius * 9 / 5 + 32;
    },
};

console.log(temperature.celsius);      // 20
console.log(temperature.fahrenheit);   // 68

temperature.celsius = 100;             // goes through the setter
console.log(temperature.celsius);      // 100
console.log(temperature.fahrenheit);   // 212

// Compound assignment reads through the getter and writes through the setter.
temperature.celsius += 5;
console.log(temperature.celsius);      // 105

// A data initializer may reference an enclosing local.
const startAt = 3;
const counter = {
    _n: startAt,
    get value(): number { return this._n; },
    set value(v: number) { this._n = v; },
};
console.log(counter.value);            // 3
counter.value = counter.value * 2;
console.log(counter.value);            // 6
