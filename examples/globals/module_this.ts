// A module's own `this` — at its top level, or read by an arrow there — is
// undefined in an ES module (this file has an export).
export {};

const self = () => this;
console.log(self() === undefined);

// `declare class` types a global the environment provides: constructing it
// throws ReferenceError until something defines it.
declare class Widget {
  constructor(size: number);
  size: number;
}

try {
  new Widget(1);
} catch (e) {
  console.log((e as Error).name);
}
