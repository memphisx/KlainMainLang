// TS namespaces with function merging: one name that is both callable and a
// member carrier — plus the built-in conversion functions.

function distance(km: number): string {
  return km + "km";
}
namespace distance {
  export function miles(km: number): string {
    return String(km * 621371 / 1000000) + "mi";
  }
  export const unit = "km";
}

console.log(distance(504)); // Thessaloniki -> Athens
console.log(distance.miles(504));
console.log(distance.unit);

namespace parse {
  export function strictNumber(s: string): number {
    const n = Number(s);
    if (Number.isNaN(n)) {
      throw new TypeError("not a number: " + s);
    }
    return n;
  }
}

console.log(parse.strictNumber("41.5"));
try {
  parse.strictNumber("nope");
} catch (e) {
  console.log(e instanceof TypeError, Boolean(e));
}
