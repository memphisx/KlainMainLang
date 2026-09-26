// A closure may call a `const` closure declared after it: the call runs
// later, once the later binding is initialized, as in JavaScript. Mutually
// recursive helpers and callbacks wired before their handlers both rely on it.
const isEven = (n: number): boolean => n === 0 ? true : isOdd(n - 1);
const isOdd = (n: number): boolean => n === 0 ? false : isEven(n - 1);
console.log(isEven(10), isOdd(7));

function tokenize(src: string): string[] {
  const out: string[] = [];
  let i = 0;
  const next = (): void => {
    if (i >= src.length) return;
    if (src[i] === ' ') { i++; next(); return; }
    word();
  };
  const word = (): void => {
    let w = '';
    while (i < src.length && src[i] !== ' ') { w += src[i]; i++; }
    out.push(w);
    next();
  };
  next();
  return out;
}
console.log(tokenize("kalimera apo  thessaloniki"));
