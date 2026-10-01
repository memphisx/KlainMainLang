// Methods of whatever an `any` holds at run time.
interface Stop { name: string; line: number }

const stops: Stop[] = [{ name: "Aristotelous", line: 1 }, { name: "Vardaris", line: 3 }];
const loose: any = stops;

// Array methods run on the array itself; a push is seen by `stops` too.
loose.push({ name: "Kamara", line: 2 });
console.log(loose.map((s: any) => s.name).join(" → "), stops.length);
console.log(loose.filter((s: any) => s.line > 1));

// String and number methods of a string or number held in `any`.
const title: any = "  thessaloniki metro ";
const fare: any = 0.6;
console.log(title.trim().toUpperCase(), fare.toFixed(2));

// A promise held in `any` is still a promise.
const later: any = Promise.resolve(stops.length);
const count: Promise<number> = later;
count.then((n) => console.log("stops:", n));
console.log(later);
