// Atomics read as a value: its operations, non-enumerable, tagged.

import { hidden, tag } from './internal_ns_object';

let atomics: any = null;

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
