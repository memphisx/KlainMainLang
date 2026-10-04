// util.inspect shows a Symbol.toStringTag that differs from the class name
// as `Name [Tag]`, and a class instance past the depth cap as `[Name]`.
class Temperature {
    celsius = 21;
    get [Symbol.toStringTag]() { return "Thessaloniki"; }
}
class Reading { value = 3; }

console.log(new Temperature());
console.log({ city: { today: { morning: new Temperature(), noon: new Reading() } } });
console.log(Object.prototype.toString.call(new Temperature()));
