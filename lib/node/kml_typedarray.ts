// kml:global
// The typed arrays' statics written in TypeScript (ECMA-262 §23.2.2),
// reached through their `@lower` declarations in lib/es.d.ts.

// TypedArray.from's list: the source's values (an iterable's, else an
// array-like's), each through mapfn when there is one.
function typedValues(source: any, mapfn: any, thisArg: any): any[] {
    if (mapfn !== undefined && typeof mapfn !== 'function') {
        throw new TypeError(String(mapfn) + ' is not a function');
    }
    if (source === null || source === undefined) {
        throw new TypeError('Cannot convert undefined or null to object');
    }
    const values: any[] = Array.from(source);
    if (mapfn === undefined) return values;
    const f: any = mapfn;
    const out: any[] = [];
    for (let k = 0; k < values.length; k++) out.push(f.call(thisArg, values[k], k));
    return out;
}

export function __kml_Int8Array_from(source: any, mapfn?: any, thisArg?: any): Int8Array { // kml:lower Int8Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Int8Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Int8Array_of(...items: any[]): Int8Array { // kml:lower Int8Array.of
    const target = new Int8Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Uint8Array_from(source: any, mapfn?: any, thisArg?: any): Uint8Array { // kml:lower Uint8Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Uint8Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Uint8Array_of(...items: any[]): Uint8Array { // kml:lower Uint8Array.of
    const target = new Uint8Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Uint8ClampedArray_from(source: any, mapfn?: any, thisArg?: any): Uint8ClampedArray { // kml:lower Uint8ClampedArray.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Uint8ClampedArray(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Uint8ClampedArray_of(...items: any[]): Uint8ClampedArray { // kml:lower Uint8ClampedArray.of
    const target = new Uint8ClampedArray(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Int16Array_from(source: any, mapfn?: any, thisArg?: any): Int16Array { // kml:lower Int16Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Int16Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Int16Array_of(...items: any[]): Int16Array { // kml:lower Int16Array.of
    const target = new Int16Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Uint16Array_from(source: any, mapfn?: any, thisArg?: any): Uint16Array { // kml:lower Uint16Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Uint16Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Uint16Array_of(...items: any[]): Uint16Array { // kml:lower Uint16Array.of
    const target = new Uint16Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Int32Array_from(source: any, mapfn?: any, thisArg?: any): Int32Array { // kml:lower Int32Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Int32Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Int32Array_of(...items: any[]): Int32Array { // kml:lower Int32Array.of
    const target = new Int32Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Uint32Array_from(source: any, mapfn?: any, thisArg?: any): Uint32Array { // kml:lower Uint32Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Uint32Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Uint32Array_of(...items: any[]): Uint32Array { // kml:lower Uint32Array.of
    const target = new Uint32Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Float32Array_from(source: any, mapfn?: any, thisArg?: any): Float32Array { // kml:lower Float32Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Float32Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Float32Array_of(...items: any[]): Float32Array { // kml:lower Float32Array.of
    const target = new Float32Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

export function __kml_Float64Array_from(source: any, mapfn?: any, thisArg?: any): Float64Array { // kml:lower Float64Array.from
    const values = typedValues(source, mapfn, thisArg);
    const target = new Float64Array(values.length);
    for (let i = 0; i < values.length; i++) target[i] = Number(values[i]);
    return target;
}

export function __kml_Float64Array_of(...items: any[]): Float64Array { // kml:lower Float64Array.of
    const target = new Float64Array(items.length);
    for (let i = 0; i < items.length; i++) target[i] = Number(items[i]);
    return target;
}

// BigInt64Array/BigUint64Array.prototype's methods that call back or search
// (ECMA-262 §23.2.3), reached through their `@lower` declarations: an
// element is a bigint, and a value stored into one goes through ToBigInt.
// A result array is the receiver's own kind.

function toBigInt(v: any): bigint {
    if (typeof v === 'bigint') return v;
    if (typeof v === 'boolean') return v ? 1n : 0n;
    if (typeof v === 'string') return BigInt(v);
    throw new TypeError('Cannot convert ' + String(v) + ' to a BigInt');
}

function requireCallable(f: any): void {
    if (typeof f !== 'function') throw new TypeError(String(f) + ' is not a function');
}

// ToIntegerOrInfinity of a relative index, clamped into [0, len].
function relIndex(v: any, len: number, dflt: number): number {
    if (v === undefined) return dflt;
    let n = Math.trunc(Number(v));
    if (n !== n) n = 0;
    if (n < 0) return Math.max(len + n, 0);
    return Math.min(n, len);
}

// The default comparison of two bigints, and a comparator's verdict.
function bigCompare(cmp: any, a: bigint, b: bigint): number {
    if (cmp === undefined) return a < b ? -1 : a > b ? 1 : 0;
    const r = Number(cmp(a, b));
    return r !== r ? 0 : r;
}

export function __kml_BigInt64Array_forEach(o: BigInt64Array, f: any, thisArg?: any): void { // kml:lower forEach
    requireCallable(f);
    for (let i = 0; i < o.length; i++) f.call(thisArg, o[i], i, o);
}

export function __kml_BigInt64Array_map(o: BigInt64Array, f: any, thisArg?: any): BigInt64Array { // kml:lower map
    requireCallable(f);
    const r = new BigInt64Array(o.length);
    for (let i = 0; i < o.length; i++) r[i] = toBigInt(f.call(thisArg, o[i], i, o));
    return r;
}

export function __kml_BigInt64Array_filter(o: BigInt64Array, f: any, thisArg?: any): BigInt64Array { // kml:lower filter
    requireCallable(f);
    const kept: bigint[] = [];
    for (let i = 0; i < o.length; i++) {
        const v: bigint = o[i];
        if (f.call(thisArg, v, i, o)) kept.push(v);
    }
    const r = new BigInt64Array(kept.length);
    for (let i = 0; i < kept.length; i++) r[i] = kept[i];
    return r;
}

export function __kml_BigInt64Array_some(o: BigInt64Array, f: any, thisArg?: any): boolean { // kml:lower some
    requireCallable(f);
    for (let i = 0; i < o.length; i++) if (f.call(thisArg, o[i], i, o)) return true;
    return false;
}

export function __kml_BigInt64Array_every(o: BigInt64Array, f: any, thisArg?: any): boolean { // kml:lower every
    requireCallable(f);
    for (let i = 0; i < o.length; i++) if (!f.call(thisArg, o[i], i, o)) return false;
    return true;
}

export function __kml_BigInt64Array_find(o: BigInt64Array, f: any, thisArg?: any): any { // kml:lower find
    requireCallable(f);
    for (let i = 0; i < o.length; i++) {
        const v: bigint = o[i];
        if (f.call(thisArg, v, i, o)) return v;
    }
    return undefined;
}

export function __kml_BigInt64Array_findIndex(o: BigInt64Array, f: any, thisArg?: any): number { // kml:lower findIndex
    requireCallable(f);
    for (let i = 0; i < o.length; i++) if (f.call(thisArg, o[i], i, o)) return i;
    return -1;
}

export function __kml_BigInt64Array_findLast(o: BigInt64Array, f: any, thisArg?: any): any { // kml:lower findLast
    requireCallable(f);
    for (let i = o.length - 1; i >= 0; i--) {
        const v: bigint = o[i];
        if (f.call(thisArg, v, i, o)) return v;
    }
    return undefined;
}

export function __kml_BigInt64Array_findLastIndex(o: BigInt64Array, f: any, thisArg?: any): number { // kml:lower findLastIndex
    requireCallable(f);
    for (let i = o.length - 1; i >= 0; i--) if (f.call(thisArg, o[i], i, o)) return i;
    return -1;
}

export function __kml_BigInt64Array_reduce(o: BigInt64Array, f: any, ...init: any[]): any { // kml:lower reduce
    requireCallable(f);
    let k = 0;
    let acc: any;
    if (init.length > 0) acc = init[0];
    else {
        if (o.length === 0) throw new TypeError('Reduce of empty array with no initial value');
        acc = o[0];
        k = 1;
    }
    for (; k < o.length; k++) acc = f(acc, o[k], k, o);
    return acc;
}

export function __kml_BigInt64Array_reduceRight(o: BigInt64Array, f: any, ...init: any[]): any { // kml:lower reduceRight
    requireCallable(f);
    let k = o.length - 1;
    let acc: any;
    if (init.length > 0) acc = init[0];
    else {
        if (o.length === 0) throw new TypeError('Reduce of empty array with no initial value');
        acc = o[k];
        k--;
    }
    for (; k >= 0; k--) acc = f(acc, o[k], k, o);
    return acc;
}

export function __kml_BigInt64Array_indexOf(o: BigInt64Array, x: any, fromIndex?: any): number { // kml:lower indexOf
    if (typeof x !== 'bigint') return -1;
    for (let i = relIndex(fromIndex, o.length, 0); i < o.length; i++) if (o[i] === x) return i;
    return -1;
}

export function __kml_BigInt64Array_lastIndexOf(o: BigInt64Array, x: any, ...from: any[]): number { // kml:lower lastIndexOf
    const len = o.length;
    if (len === 0 || typeof x !== 'bigint') return -1;
    let n = len - 1;
    if (from.length > 0) {
        let f = Math.trunc(Number(from[0]));
        if (f !== f) f = 0;
        n = f >= 0 ? Math.min(f, len - 1) : len + f;
    }
    for (let i = n; i >= 0; i--) if (o[i] === x) return i;
    return -1;
}

export function __kml_BigInt64Array_includes(o: BigInt64Array, x: any, fromIndex?: any): boolean { // kml:lower includes
    if (typeof x !== 'bigint') return false;
    for (let i = relIndex(fromIndex, o.length, 0); i < o.length; i++) if (o[i] === x) return true;
    return false;
}

export function __kml_BigInt64Array_join(o: BigInt64Array, separator?: any): string { // kml:lower join
    const sep = separator === undefined ? ',' : String(separator);
    let s = '';
    for (let i = 0; i < o.length; i++) {
        if (i > 0) s += sep;
        s += String(o[i]);
    }
    return s;
}

export function __kml_BigInt64Array_keys(o: BigInt64Array): any { // kml:lower keys
    const ks: number[] = [];
    for (let i = 0; i < o.length; i++) ks.push(i);
    return ks[Symbol.iterator]();
}

export function __kml_BigInt64Array_values(o: BigInt64Array): any { // kml:lower values
    const vs: bigint[] = [];
    for (let i = 0; i < o.length; i++) vs.push(o[i]);
    return vs[Symbol.iterator]();
}

export function __kml_BigInt64Array_entries(o: BigInt64Array): any { // kml:lower entries
    const es: any[] = [];
    for (let i = 0; i < o.length; i++) es.push([i, o[i]]);
    return es[Symbol.iterator]();
}

export function __kml_BigInt64Array_copyWithin(o: BigInt64Array, target: any, start: any, end?: any): BigInt64Array { // kml:lower copyWithin
    const len = o.length;
    const to = relIndex(target, len, 0);
    const from = relIndex(start, len, 0);
    const fin = relIndex(end, len, len);
    const count = Math.min(fin - from, len - to);
    if (count <= 0) return o;
    const tmp: bigint[] = [];
    for (let i = 0; i < count; i++) tmp.push(o[from + i]);
    for (let i = 0; i < count; i++) o[to + i] = tmp[i];
    return o;
}

export function __kml_BigInt64Array_with(o: BigInt64Array, index: any, value: any): BigInt64Array { // kml:lower with
    const len = o.length;
    let rel = Math.trunc(Number(index));
    if (rel !== rel) rel = 0;
    const at = rel >= 0 ? rel : len + rel;
    const v = toBigInt(value);
    if (at < 0 || at >= len) throw new RangeError('Invalid typed array index');
    const r = new BigInt64Array(len);
    for (let i = 0; i < len; i++) r[i] = i === at ? v : o[i];
    return r;
}

export function __kml_BigInt64Array_sort(o: BigInt64Array, cmp?: any): BigInt64Array { // kml:lower sort
    if (cmp !== undefined) requireCallable(cmp);
    // A stable insertion sort over a copy, written back.
    const vs: bigint[] = [];
    for (let i = 0; i < o.length; i++) vs.push(o[i]);
    for (let i = 1; i < vs.length; i++) {
        const v: bigint = vs[i];
        let j = i - 1;
        while (j >= 0 && bigCompare(cmp, vs[j], v) > 0) {
            vs[j + 1] = vs[j];
            j--;
        }
        vs[j + 1] = v;
    }
    for (let i = 0; i < vs.length && i < o.length; i++) o[i] = vs[i];
    return o;
}

export function __kml_BigInt64Array_toSorted(o: BigInt64Array, cmp?: any): BigInt64Array { // kml:lower toSorted
    if (cmp !== undefined) requireCallable(cmp);
    const r = new BigInt64Array(o.length);
    for (let i = 0; i < o.length; i++) r[i] = o[i];
    return __kml_BigInt64Array_sort(r, cmp);
}

export function __kml_BigUint64Array_forEach(o: BigUint64Array, f: any, thisArg?: any): void { // kml:lower forEach
    requireCallable(f);
    for (let i = 0; i < o.length; i++) f.call(thisArg, o[i], i, o);
}

export function __kml_BigUint64Array_map(o: BigUint64Array, f: any, thisArg?: any): BigUint64Array { // kml:lower map
    requireCallable(f);
    const r = new BigUint64Array(o.length);
    for (let i = 0; i < o.length; i++) r[i] = toBigInt(f.call(thisArg, o[i], i, o));
    return r;
}

export function __kml_BigUint64Array_filter(o: BigUint64Array, f: any, thisArg?: any): BigUint64Array { // kml:lower filter
    requireCallable(f);
    const kept: bigint[] = [];
    for (let i = 0; i < o.length; i++) {
        const v: bigint = o[i];
        if (f.call(thisArg, v, i, o)) kept.push(v);
    }
    const r = new BigUint64Array(kept.length);
    for (let i = 0; i < kept.length; i++) r[i] = kept[i];
    return r;
}

export function __kml_BigUint64Array_some(o: BigUint64Array, f: any, thisArg?: any): boolean { // kml:lower some
    requireCallable(f);
    for (let i = 0; i < o.length; i++) if (f.call(thisArg, o[i], i, o)) return true;
    return false;
}

export function __kml_BigUint64Array_every(o: BigUint64Array, f: any, thisArg?: any): boolean { // kml:lower every
    requireCallable(f);
    for (let i = 0; i < o.length; i++) if (!f.call(thisArg, o[i], i, o)) return false;
    return true;
}

export function __kml_BigUint64Array_find(o: BigUint64Array, f: any, thisArg?: any): any { // kml:lower find
    requireCallable(f);
    for (let i = 0; i < o.length; i++) {
        const v: bigint = o[i];
        if (f.call(thisArg, v, i, o)) return v;
    }
    return undefined;
}

export function __kml_BigUint64Array_findIndex(o: BigUint64Array, f: any, thisArg?: any): number { // kml:lower findIndex
    requireCallable(f);
    for (let i = 0; i < o.length; i++) if (f.call(thisArg, o[i], i, o)) return i;
    return -1;
}

export function __kml_BigUint64Array_findLast(o: BigUint64Array, f: any, thisArg?: any): any { // kml:lower findLast
    requireCallable(f);
    for (let i = o.length - 1; i >= 0; i--) {
        const v: bigint = o[i];
        if (f.call(thisArg, v, i, o)) return v;
    }
    return undefined;
}

export function __kml_BigUint64Array_findLastIndex(o: BigUint64Array, f: any, thisArg?: any): number { // kml:lower findLastIndex
    requireCallable(f);
    for (let i = o.length - 1; i >= 0; i--) if (f.call(thisArg, o[i], i, o)) return i;
    return -1;
}

export function __kml_BigUint64Array_reduce(o: BigUint64Array, f: any, ...init: any[]): any { // kml:lower reduce
    requireCallable(f);
    let k = 0;
    let acc: any;
    if (init.length > 0) acc = init[0];
    else {
        if (o.length === 0) throw new TypeError('Reduce of empty array with no initial value');
        acc = o[0];
        k = 1;
    }
    for (; k < o.length; k++) acc = f(acc, o[k], k, o);
    return acc;
}

export function __kml_BigUint64Array_reduceRight(o: BigUint64Array, f: any, ...init: any[]): any { // kml:lower reduceRight
    requireCallable(f);
    let k = o.length - 1;
    let acc: any;
    if (init.length > 0) acc = init[0];
    else {
        if (o.length === 0) throw new TypeError('Reduce of empty array with no initial value');
        acc = o[k];
        k--;
    }
    for (; k >= 0; k--) acc = f(acc, o[k], k, o);
    return acc;
}

export function __kml_BigUint64Array_indexOf(o: BigUint64Array, x: any, fromIndex?: any): number { // kml:lower indexOf
    if (typeof x !== 'bigint') return -1;
    for (let i = relIndex(fromIndex, o.length, 0); i < o.length; i++) if (o[i] === x) return i;
    return -1;
}

export function __kml_BigUint64Array_lastIndexOf(o: BigUint64Array, x: any, ...from: any[]): number { // kml:lower lastIndexOf
    const len = o.length;
    if (len === 0 || typeof x !== 'bigint') return -1;
    let n = len - 1;
    if (from.length > 0) {
        let f = Math.trunc(Number(from[0]));
        if (f !== f) f = 0;
        n = f >= 0 ? Math.min(f, len - 1) : len + f;
    }
    for (let i = n; i >= 0; i--) if (o[i] === x) return i;
    return -1;
}

export function __kml_BigUint64Array_includes(o: BigUint64Array, x: any, fromIndex?: any): boolean { // kml:lower includes
    if (typeof x !== 'bigint') return false;
    for (let i = relIndex(fromIndex, o.length, 0); i < o.length; i++) if (o[i] === x) return true;
    return false;
}

export function __kml_BigUint64Array_join(o: BigUint64Array, separator?: any): string { // kml:lower join
    const sep = separator === undefined ? ',' : String(separator);
    let s = '';
    for (let i = 0; i < o.length; i++) {
        if (i > 0) s += sep;
        s += String(o[i]);
    }
    return s;
}

export function __kml_BigUint64Array_keys(o: BigUint64Array): any { // kml:lower keys
    const ks: number[] = [];
    for (let i = 0; i < o.length; i++) ks.push(i);
    return ks[Symbol.iterator]();
}

export function __kml_BigUint64Array_values(o: BigUint64Array): any { // kml:lower values
    const vs: bigint[] = [];
    for (let i = 0; i < o.length; i++) vs.push(o[i]);
    return vs[Symbol.iterator]();
}

export function __kml_BigUint64Array_entries(o: BigUint64Array): any { // kml:lower entries
    const es: any[] = [];
    for (let i = 0; i < o.length; i++) es.push([i, o[i]]);
    return es[Symbol.iterator]();
}

export function __kml_BigUint64Array_copyWithin(o: BigUint64Array, target: any, start: any, end?: any): BigUint64Array { // kml:lower copyWithin
    const len = o.length;
    const to = relIndex(target, len, 0);
    const from = relIndex(start, len, 0);
    const fin = relIndex(end, len, len);
    const count = Math.min(fin - from, len - to);
    if (count <= 0) return o;
    const tmp: bigint[] = [];
    for (let i = 0; i < count; i++) tmp.push(o[from + i]);
    for (let i = 0; i < count; i++) o[to + i] = tmp[i];
    return o;
}

export function __kml_BigUint64Array_with(o: BigUint64Array, index: any, value: any): BigUint64Array { // kml:lower with
    const len = o.length;
    let rel = Math.trunc(Number(index));
    if (rel !== rel) rel = 0;
    const at = rel >= 0 ? rel : len + rel;
    const v = toBigInt(value);
    if (at < 0 || at >= len) throw new RangeError('Invalid typed array index');
    const r = new BigUint64Array(len);
    for (let i = 0; i < len; i++) r[i] = i === at ? v : o[i];
    return r;
}

export function __kml_BigUint64Array_sort(o: BigUint64Array, cmp?: any): BigUint64Array { // kml:lower sort
    if (cmp !== undefined) requireCallable(cmp);
    // A stable insertion sort over a copy, written back.
    const vs: bigint[] = [];
    for (let i = 0; i < o.length; i++) vs.push(o[i]);
    for (let i = 1; i < vs.length; i++) {
        const v: bigint = vs[i];
        let j = i - 1;
        while (j >= 0 && bigCompare(cmp, vs[j], v) > 0) {
            vs[j + 1] = vs[j];
            j--;
        }
        vs[j + 1] = v;
    }
    for (let i = 0; i < vs.length && i < o.length; i++) o[i] = vs[i];
    return o;
}

export function __kml_BigUint64Array_toSorted(o: BigUint64Array, cmp?: any): BigUint64Array { // kml:lower toSorted
    if (cmp !== undefined) requireCallable(cmp);
    const r = new BigUint64Array(o.length);
    for (let i = 0; i < o.length; i++) r[i] = o[i];
    return __kml_BigUint64Array_sort(r, cmp);
}
