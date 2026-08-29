// `declare` ambient declarations describe types the environment provides. They
// carry no implementation, so this compiler parses and erases them (ADR-00388).
// The value is letting .d.ts-style code — ambient globals, module augmentation —
// compile instead of failing at parse.

declare const BUILD_ID: string;
declare function nativeHook(x: number): number;

declare global {
  interface Window { title: string; }
}

declare module "vendor-lib" {
  export function doThing(): void;
}

// Real code alongside the ambient declarations compiles and runs normally.
function double(n: number): number {
  return n * 2;
}
console.log(double(21)); // 42

// Ambient values are real bindings: a `declare var` reads its zero value,
// a `declare function` is a stub that throws only if actually called.
declare var ambientFlag: boolean;
declare function notLinked(): void;
console.log(ambientFlag);
try { notLinked(); } catch (e) { console.log(e.message); }
