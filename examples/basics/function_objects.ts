// Functions are objects: console.log shows their kind and name (inferred from
// the binding they're assigned to, as JS does), and `name`/`length` read the
// same metadata — also through `any`.
const greet = (who: string, punctuation = '!') => `Hello, ${who}${punctuation}`;
async function fetchCity(): Promise<string> { return 'Thessaloniki'; }
function* countdown(from: number) { while (from > 0) yield from--; }
const handlers = { onOpen: () => 1, onClose: (code: number) => code };

console.log(greet, greet.name, greet.length);
console.log(fetchCity, countdown);
console.log(handlers);
const dynamic: any = greet;
console.log(dynamic.name, dynamic('Kalamaria'));

// Null-prototype objects render the way Node shows them.
const dict: any = Object.create(null);
dict.city = 'Thessaloniki';
console.log(dict, Object.getPrototypeOf({}) === Object.prototype);

// A bigint keeps its identity inside `any`.
const big: any = 2n ** 64n;
console.log(typeof big, big, big === 18446744073709551616n);
