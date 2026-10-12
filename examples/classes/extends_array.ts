// A class extending Array: the instance is the array itself.

class Readings extends Array<number> {
  city = "Thessaloniki";

  get mean(): number {
    return this.length === 0 ? 0 : this.reduce((a, b) => a + b, 0) / this.length;
  }

  // An override is what every caller reaches, even through a plain number[].
  push(...xs: number[]): number {
    return super.push(...xs.map(x => Math.round(x * 10) / 10));
  }
}

function record(into: number[], value: number): void {
  into.push(value);
}

const temps = new Readings(18.24, 21.06);
record(temps, 23.47);
console.log(temps, temps.length, temps.mean.toFixed(2));

// Array's own methods make Readings too (species), sorted in place.
const warm = temps.filter(t => t > 20);
console.log(warm instanceof Readings, warm);
console.log(temps.sort((a, b) => b - a) === temps, temps.join(" > "));

// The same object through `any` and as a plain array.
const seen: any = temps;
console.log(Array.isArray(seen), seen instanceof Readings, seen.length);
const [hottest, ...rest] = temps;
console.log(hottest, rest, Object.keys(temps));

// A class extending a typed array.
class Bytes extends Uint8Array {
  hex(): string {
    return Array.from(this, b => b.toString(16).padStart(2, "0")).join("");
  }
}
const bytes = new Bytes([0xca, 0xfe, 0x01]);
console.log(bytes, bytes.hex(), bytes.subarray(1) instanceof Bytes, Bytes.from([7]));
