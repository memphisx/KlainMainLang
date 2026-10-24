// A value held in `any` keeps its own kind wherever it goes, as in
// JavaScript: a type annotation converts nothing at run time.

function describe(label: string, n: number): string {
  return `${label}: ${typeof n} ${n}`;
}

function checkName(name: string): string {
  if (typeof name !== "string") throw new TypeError(`name must be a string, got ${typeof name}`);
  return name.toUpperCase();
}

const parsed: any = JSON.parse('{"count": "12", "name": 7, "bytes": [1, 2]}');

console.log(describe("count", parsed.count)); // count: string 12
try {
  checkName(parsed.name);
} catch (e: any) {
  console.log(e.message); // name must be a string, got number
}

const total: number = parsed.count;
console.log(typeof total, total + 1); // string 121

const view: any = new Uint16Array([300, 65535]);
const bytes: Uint8Array = view;
console.log(bytes.constructor.name, bytes[0], bytes[1]); // Uint16Array 300 65535
