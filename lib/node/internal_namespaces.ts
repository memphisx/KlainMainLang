// Math read as a value (`const m = Math`): one object, made on first use,
// holding the builtin functions and constants by value, non-enumerable and
// tagged (`Object [Math] {}`), as Node's is. A member read straight off the
// name (`Math.max(…)`) never comes here: code generation compiles it
// directly. JSON, Reflect, Atomics, console and the global object have a
// module each (internal_ns_*.ts, internal_global_object.ts), so a program
// compiles only the ones it reads.

import { hidden, tag } from './internal_ns_object';

let math: any = null;

export function _kmlMath(): any {
    if (math !== null) return math;
    const m: any = {};
    hidden(m, 'abs', Math.abs);
    hidden(m, 'acos', Math.acos);
    hidden(m, 'acosh', Math.acosh);
    hidden(m, 'asin', Math.asin);
    hidden(m, 'asinh', Math.asinh);
    hidden(m, 'atan', Math.atan);
    hidden(m, 'atanh', Math.atanh);
    hidden(m, 'atan2', Math.atan2);
    hidden(m, 'ceil', Math.ceil);
    hidden(m, 'cbrt', Math.cbrt);
    hidden(m, 'expm1', Math.expm1);
    hidden(m, 'clz32', Math.clz32);
    hidden(m, 'cos', Math.cos);
    hidden(m, 'cosh', Math.cosh);
    hidden(m, 'exp', Math.exp);
    hidden(m, 'floor', Math.floor);
    hidden(m, 'fround', Math.fround);
    hidden(m, 'hypot', Math.hypot);
    hidden(m, 'imul', Math.imul);
    hidden(m, 'log', Math.log);
    hidden(m, 'log1p', Math.log1p);
    hidden(m, 'log2', Math.log2);
    hidden(m, 'log10', Math.log10);
    hidden(m, 'max', Math.max);
    hidden(m, 'min', Math.min);
    hidden(m, 'pow', Math.pow);
    hidden(m, 'random', Math.random);
    hidden(m, 'round', Math.round);
    hidden(m, 'sign', Math.sign);
    hidden(m, 'sin', Math.sin);
    hidden(m, 'sinh', Math.sinh);
    hidden(m, 'sqrt', Math.sqrt);
    hidden(m, 'tan', Math.tan);
    hidden(m, 'tanh', Math.tanh);
    hidden(m, 'trunc', Math.trunc);
    Object.defineProperty(m, 'E', { value: Math.E, writable: false, enumerable: false, configurable: false });
    Object.defineProperty(m, 'LN10', { value: Math.LN10, writable: false, enumerable: false, configurable: false });
    Object.defineProperty(m, 'LN2', { value: Math.LN2, writable: false, enumerable: false, configurable: false });
    Object.defineProperty(m, 'LOG10E', { value: Math.LOG10E, writable: false, enumerable: false, configurable: false });
    Object.defineProperty(m, 'LOG2E', { value: Math.LOG2E, writable: false, enumerable: false, configurable: false });
    Object.defineProperty(m, 'PI', { value: Math.PI, writable: false, enumerable: false, configurable: false });
    Object.defineProperty(m, 'SQRT1_2', { value: Math.SQRT1_2, writable: false, enumerable: false, configurable: false });
    Object.defineProperty(m, 'SQRT2', { value: Math.SQRT2, writable: false, enumerable: false, configurable: false });
    tag(m, 'Math');
    math = m;
    return m;
}
