// The namespace objects read as values (`const m = Math`, `use(console)`),
// and the global object (`globalThis` as a value, or a property of it the
// program adds):
// one object each, made on first use, holding the builtin functions and
// constants by value, as Node's do. Math, JSON, Reflect and Atomics keep
// their members non-enumerable and are tagged (`Object [Math] {}`);
// console's methods are enumerable. A member read straight off the name
// (`Math.max(…)`) never comes here: code generation compiles it directly.

import { inspect } from './internal_util_inspect';
import { _kmlCrypto } from './internal_crypto_global';

function hidden(o: any, key: string, value: any): void {
    Object.defineProperty(o, key, { value: value, writable: true, enumerable: false, configurable: true });
}

function tag(o: any, name: string): void {
    Object.defineProperty(o, Symbol.toStringTag, { value: name, writable: false, enumerable: false, configurable: true });
}

let math: any = null;
let json: any = null;
let reflect: any = null;
let atomics: any = null;
let con: any = null;

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

export function _kmlJSON(): any {
    if (json !== null) return json;
    const j: any = {};
    hidden(j, 'parse', JSON.parse);
    hidden(j, 'stringify', JSON.stringify);
    tag(j, 'JSON');
    json = j;
    return j;
}

export function _kmlReflect(): any {
    if (reflect !== null) return reflect;
    const r: any = {};
    hidden(r, 'defineProperty', Reflect.defineProperty);
    hidden(r, 'deleteProperty', Reflect.deleteProperty);
    hidden(r, 'apply', Reflect.apply);
    hidden(r, 'construct', Reflect.construct);
    hidden(r, 'get', Reflect.get);
    hidden(r, 'getOwnPropertyDescriptor', Reflect.getOwnPropertyDescriptor);
    hidden(r, 'getPrototypeOf', Reflect.getPrototypeOf);
    hidden(r, 'has', Reflect.has);
    hidden(r, 'isExtensible', Reflect.isExtensible);
    hidden(r, 'ownKeys', Reflect.ownKeys);
    hidden(r, 'preventExtensions', Reflect.preventExtensions);
    hidden(r, 'set', Reflect.set);
    hidden(r, 'setPrototypeOf', Reflect.setPrototypeOf);
    tag(r, 'Reflect');
    reflect = r;
    return r;
}

// Atomics' operations by value: the array's kind is known only at run
// time, so each dispatches to the operation compiled for that kind.
function atomics_load(ta: any, i: number): any {
    if (ta instanceof Int8Array) return Atomics.load(ta as Int8Array, i);
    if (ta instanceof Uint8Array) return Atomics.load(ta as Uint8Array, i);
    if (ta instanceof Int16Array) return Atomics.load(ta as Int16Array, i);
    if (ta instanceof Uint16Array) return Atomics.load(ta as Uint16Array, i);
    if (ta instanceof Int32Array) return Atomics.load(ta as Int32Array, i);
    if (ta instanceof Uint32Array) return Atomics.load(ta as Uint32Array, i);
    if (ta instanceof BigInt64Array) return Atomics.load(ta as BigInt64Array, i);
    if (ta instanceof BigUint64Array) return Atomics.load(ta as BigUint64Array, i);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an integer typed array.');
}

function atomics_store(ta: any, i: number, v: any): any {
    if (ta instanceof Int8Array) return Atomics.store(ta as Int8Array, i, v);
    if (ta instanceof Uint8Array) return Atomics.store(ta as Uint8Array, i, v);
    if (ta instanceof Int16Array) return Atomics.store(ta as Int16Array, i, v);
    if (ta instanceof Uint16Array) return Atomics.store(ta as Uint16Array, i, v);
    if (ta instanceof Int32Array) return Atomics.store(ta as Int32Array, i, v);
    if (ta instanceof Uint32Array) return Atomics.store(ta as Uint32Array, i, v);
    if (ta instanceof BigInt64Array) return Atomics.store(ta as BigInt64Array, i, v as bigint);
    if (ta instanceof BigUint64Array) return Atomics.store(ta as BigUint64Array, i, v as bigint);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an integer typed array.');
}

function atomics_add(ta: any, i: number, v: any): any {
    if (ta instanceof Int8Array) return Atomics.add(ta as Int8Array, i, v);
    if (ta instanceof Uint8Array) return Atomics.add(ta as Uint8Array, i, v);
    if (ta instanceof Int16Array) return Atomics.add(ta as Int16Array, i, v);
    if (ta instanceof Uint16Array) return Atomics.add(ta as Uint16Array, i, v);
    if (ta instanceof Int32Array) return Atomics.add(ta as Int32Array, i, v);
    if (ta instanceof Uint32Array) return Atomics.add(ta as Uint32Array, i, v);
    if (ta instanceof BigInt64Array) return Atomics.add(ta as BigInt64Array, i, v as bigint);
    if (ta instanceof BigUint64Array) return Atomics.add(ta as BigUint64Array, i, v as bigint);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an integer typed array.');
}

function atomics_sub(ta: any, i: number, v: any): any {
    if (ta instanceof Int8Array) return Atomics.sub(ta as Int8Array, i, v);
    if (ta instanceof Uint8Array) return Atomics.sub(ta as Uint8Array, i, v);
    if (ta instanceof Int16Array) return Atomics.sub(ta as Int16Array, i, v);
    if (ta instanceof Uint16Array) return Atomics.sub(ta as Uint16Array, i, v);
    if (ta instanceof Int32Array) return Atomics.sub(ta as Int32Array, i, v);
    if (ta instanceof Uint32Array) return Atomics.sub(ta as Uint32Array, i, v);
    if (ta instanceof BigInt64Array) return Atomics.sub(ta as BigInt64Array, i, v as bigint);
    if (ta instanceof BigUint64Array) return Atomics.sub(ta as BigUint64Array, i, v as bigint);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an integer typed array.');
}

function atomics_and(ta: any, i: number, v: any): any {
    if (ta instanceof Int8Array) return Atomics.and(ta as Int8Array, i, v);
    if (ta instanceof Uint8Array) return Atomics.and(ta as Uint8Array, i, v);
    if (ta instanceof Int16Array) return Atomics.and(ta as Int16Array, i, v);
    if (ta instanceof Uint16Array) return Atomics.and(ta as Uint16Array, i, v);
    if (ta instanceof Int32Array) return Atomics.and(ta as Int32Array, i, v);
    if (ta instanceof Uint32Array) return Atomics.and(ta as Uint32Array, i, v);
    if (ta instanceof BigInt64Array) return Atomics.and(ta as BigInt64Array, i, v as bigint);
    if (ta instanceof BigUint64Array) return Atomics.and(ta as BigUint64Array, i, v as bigint);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an integer typed array.');
}

function atomics_or(ta: any, i: number, v: any): any {
    if (ta instanceof Int8Array) return Atomics.or(ta as Int8Array, i, v);
    if (ta instanceof Uint8Array) return Atomics.or(ta as Uint8Array, i, v);
    if (ta instanceof Int16Array) return Atomics.or(ta as Int16Array, i, v);
    if (ta instanceof Uint16Array) return Atomics.or(ta as Uint16Array, i, v);
    if (ta instanceof Int32Array) return Atomics.or(ta as Int32Array, i, v);
    if (ta instanceof Uint32Array) return Atomics.or(ta as Uint32Array, i, v);
    if (ta instanceof BigInt64Array) return Atomics.or(ta as BigInt64Array, i, v as bigint);
    if (ta instanceof BigUint64Array) return Atomics.or(ta as BigUint64Array, i, v as bigint);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an integer typed array.');
}

function atomics_xor(ta: any, i: number, v: any): any {
    if (ta instanceof Int8Array) return Atomics.xor(ta as Int8Array, i, v);
    if (ta instanceof Uint8Array) return Atomics.xor(ta as Uint8Array, i, v);
    if (ta instanceof Int16Array) return Atomics.xor(ta as Int16Array, i, v);
    if (ta instanceof Uint16Array) return Atomics.xor(ta as Uint16Array, i, v);
    if (ta instanceof Int32Array) return Atomics.xor(ta as Int32Array, i, v);
    if (ta instanceof Uint32Array) return Atomics.xor(ta as Uint32Array, i, v);
    if (ta instanceof BigInt64Array) return Atomics.xor(ta as BigInt64Array, i, v as bigint);
    if (ta instanceof BigUint64Array) return Atomics.xor(ta as BigUint64Array, i, v as bigint);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an integer typed array.');
}

function atomics_exchange(ta: any, i: number, v: any): any {
    if (ta instanceof Int8Array) return Atomics.exchange(ta as Int8Array, i, v);
    if (ta instanceof Uint8Array) return Atomics.exchange(ta as Uint8Array, i, v);
    if (ta instanceof Int16Array) return Atomics.exchange(ta as Int16Array, i, v);
    if (ta instanceof Uint16Array) return Atomics.exchange(ta as Uint16Array, i, v);
    if (ta instanceof Int32Array) return Atomics.exchange(ta as Int32Array, i, v);
    if (ta instanceof Uint32Array) return Atomics.exchange(ta as Uint32Array, i, v);
    if (ta instanceof BigInt64Array) return Atomics.exchange(ta as BigInt64Array, i, v as bigint);
    if (ta instanceof BigUint64Array) return Atomics.exchange(ta as BigUint64Array, i, v as bigint);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an integer typed array.');
}

function atomics_compareExchange(ta: any, i: number, e: any, r: any): any {
    if (ta instanceof Int8Array) return Atomics.compareExchange(ta as Int8Array, i, e, r);
    if (ta instanceof Uint8Array) return Atomics.compareExchange(ta as Uint8Array, i, e, r);
    if (ta instanceof Int16Array) return Atomics.compareExchange(ta as Int16Array, i, e, r);
    if (ta instanceof Uint16Array) return Atomics.compareExchange(ta as Uint16Array, i, e, r);
    if (ta instanceof Int32Array) return Atomics.compareExchange(ta as Int32Array, i, e, r);
    if (ta instanceof Uint32Array) return Atomics.compareExchange(ta as Uint32Array, i, e, r);
    if (ta instanceof BigInt64Array) return Atomics.compareExchange(ta as BigInt64Array, i, e as bigint, r as bigint);
    if (ta instanceof BigUint64Array) return Atomics.compareExchange(ta as BigUint64Array, i, e as bigint, r as bigint);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an integer typed array.');
}

function atomics_wait(ta: any, i: number, v: any, t?: number): any {
    if (ta instanceof Int32Array) return Atomics.wait(ta as Int32Array, i, v, t);
    if (ta instanceof BigInt64Array) return Atomics.wait(ta as BigInt64Array, i, v as bigint, t);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an int32 or BigInt64 typed array.');
}

function atomics_notify(ta: any, i: number, n?: number): any {
    if (ta instanceof Int32Array) return Atomics.notify(ta as Int32Array, i, n);
    if (ta instanceof BigInt64Array) return Atomics.notify(ta as BigInt64Array, i, n);
    throw new TypeError(Object.prototype.toString.call(ta) + ' is not an int32 or BigInt64 typed array.');
}

export function _kmlAtomics(): any {
    if (atomics !== null) return atomics;
    const a: any = {};
    hidden(a, 'load', atomics_load);
    hidden(a, 'store', atomics_store);
    hidden(a, 'add', atomics_add);
    hidden(a, 'sub', atomics_sub);
    hidden(a, 'and', atomics_and);
    hidden(a, 'or', atomics_or);
    hidden(a, 'xor', atomics_xor);
    hidden(a, 'exchange', atomics_exchange);
    hidden(a, 'compareExchange', atomics_compareExchange);
    hidden(a, 'isLockFree', Atomics.isLockFree);
    hidden(a, 'wait', atomics_wait);
    hidden(a, 'notify', atomics_notify);
    tag(a, 'Atomics');
    atomics = a;
    return a;
}

export function _kmlConsole(): any {
    if (con !== null) return con;
    const c: any = {};
    c.log = console.log;
    c.info = console.info;
    c.debug = console.debug;
    c.warn = console.warn;
    c.error = console.error;
    c.dir = function dir(obj: any, options?: any): void {
        console.log(inspect(obj, { customInspect: false, ...options }));
    };
    c.time = console.time;
    c.timeEnd = console.timeEnd;
    c.trace = console.trace;
    c.assert = console.assert;
    c.count = console.count;
    c.countReset = console.countReset;
    c.group = console.group;
    c.groupEnd = console.groupEnd;
    c.table = console.table;
    c.groupCollapsed = console.groupCollapsed;
    tag(c, 'console');
    con = c;
    return c;
}

let glob: any = null;

function globalThisClone(value: any): any {
    return structuredClone(value);
}

// The global object: Node's enumerable globals, the builtins as its other
// (non-enumerable) properties, and whatever the program sets on it. A
// module's top-level bindings are not on it, as in an ES module.
export function _kmlGlobalThis(): any {
    if (glob !== null) return glob;
    const g: any = {};
    glob = g;
    g.global = g;
    g.clearImmediate = clearImmediate;
    g.setImmediate = setImmediate;
    g.clearInterval = clearInterval;
    g.clearTimeout = clearTimeout;
    g.setInterval = setInterval;
    g.setTimeout = setTimeout;
    g.queueMicrotask = queueMicrotask;
    g.structuredClone = function structuredClone(value: any, options?: any): any {
        if (options !== undefined && options !== null && options.transfer !== undefined && options.transfer.length > 0) {
            throw new TypeError("structuredClone's transfer option is not supported");
        }
        return globalThisClone(value);
    };
    g.atob = atob;
    g.btoa = btoa;
    g.fetch = fetch;
    hidden(g, 'globalThis', g);
    hidden(g, 'Math', _kmlMath());
    hidden(g, 'JSON', _kmlJSON());
    hidden(g, 'Reflect', _kmlReflect());
    hidden(g, 'Atomics', _kmlAtomics());
    hidden(g, 'console', _kmlConsole());
    hidden(g, 'crypto', _kmlCrypto());
    hidden(g, 'Object', Object);
    hidden(g, 'Function', Function);
    hidden(g, 'Array', Array);
    hidden(g, 'Number', Number);
    hidden(g, 'String', String);
    hidden(g, 'Boolean', Boolean);
    hidden(g, 'Symbol', Symbol);
    hidden(g, 'BigInt', BigInt);
    hidden(g, 'Date', Date);
    hidden(g, 'RegExp', RegExp);
    hidden(g, 'Error', Error);
    hidden(g, 'TypeError', TypeError);
    hidden(g, 'RangeError', RangeError);
    hidden(g, 'SyntaxError', SyntaxError);
    hidden(g, 'ReferenceError', ReferenceError);
    hidden(g, 'Promise', Promise);
    hidden(g, 'Map', Map);
    hidden(g, 'Set', Set);
    hidden(g, 'WeakMap', WeakMap);
    hidden(g, 'WeakSet', WeakSet);
    hidden(g, 'ArrayBuffer', ArrayBuffer);
    hidden(g, 'Uint8Array', Uint8Array);
    hidden(g, 'parseInt', parseInt);
    hidden(g, 'parseFloat', parseFloat);
    hidden(g, 'isNaN', isNaN);
    hidden(g, 'isFinite', isFinite);
    hidden(g, 'encodeURIComponent', encodeURIComponent);
    hidden(g, 'decodeURIComponent', decodeURIComponent);
    hidden(g, 'encodeURI', encodeURI);
    hidden(g, 'decodeURI', decodeURI);
    hidden(g, 'NaN', NaN);
    hidden(g, 'Infinity', Infinity);
    hidden(g, 'undefined', undefined);
    Object.defineProperty(g, Symbol.toStringTag, { value: 'global', writable: false, enumerable: false, configurable: true });
    return g;
}
