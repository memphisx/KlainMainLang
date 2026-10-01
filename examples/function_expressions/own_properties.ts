// A function is an object: it carries its own properties.
import { promisify } from "util";

// A TypeScript expando declaration: `greet.count` is part of greet's type.
function greet(name: string): string {
  greet.count++;
  return "hello " + name;
}
greet.count = 0;

console.log(greet("Thessaloniki"), greet("Kalamaria"), greet.count);
console.log(greet);

// util.promisify.custom: a function names its own promisified form.
function lookup(city: string, done: (err: Error | null, zip: string) => void): void {
  done(null, city === "Thessaloniki" ? "54" : "?");
}
lookup[promisify.custom] = (city: string) => Promise.resolve({ city, zip: "546" });

promisify(lookup)("Thessaloniki").then((r: any) => console.log(r));

// Called through `any`, a function still fills an omitted argument from its
// default.
function welcome(name: string = "Thessaloniki") { console.log("hello", name) }
const dyn: any = welcome
dyn()
