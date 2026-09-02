// Proxy traps (get/set/has/deleteProperty) and the Reflect surface
// on dynamic objects.

const settings: any = { theme: "dark" };

const guarded = new Proxy(settings, {
  get(t: any, key: any): any {
    return key in t ? t[key] : "<unset>";
  },
  set(t: any, key: any, value: any): boolean {
    console.log("setting", key);
    t[key] = value;
    return true;
  },
});

console.log(guarded.theme); // dark
console.log(guarded.font); // <unset>
guarded.city = "Thessaloniki"; // setting city
console.log(settings.city); // Thessaloniki

console.log(Reflect.get(settings, "theme")); // dark
console.log(Reflect.has(settings, "city")); // true
console.log(Reflect.ownKeys(settings)); // [ 'theme', 'city' ]
console.log(Reflect.deleteProperty(settings, "theme")); // true
console.log(Reflect.has(settings, "theme")); // false
Reflect.defineProperty(settings, "version", { value: 2, enumerable: true });
console.log(settings.version); // 2
Reflect.preventExtensions(settings);
console.log(Reflect.isExtensible(settings)); // false
