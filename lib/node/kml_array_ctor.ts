// kml:global
// Array.from, Array.of and Array.fromAsync as ECMA-262 §23.1.2.1–3 state
// them where the compiled forms do not apply: Array.fromAsync, and each
// called with another constructor as `this`. A module of their own, so a
// program using them links only these, not every array-like algorithm.

// LengthOfArrayLike: ToLength(Get(O, "length")).
function lengthOf(o: any): number {
    const n = Number(o.length);
    if (n !== n || n <= 0) return 0;
    return Math.min(Math.floor(n), 9007199254740991);
}

// Array.fromAsync (ECMA-262 §23.1.2.2): the array an async iterable, an
// iterable (each value awaited) or an array-like (each element awaited)
// gives, each value mapped through mapfn and awaited, as a promise.
export async function __kml_Array_fromAsync(items: any, mapfn: any = undefined, thisArg: any = undefined): Promise<any[]> { // kml:lower Array.fromAsync
    if (mapfn !== undefined && typeof mapfn !== 'function') {
        throw new TypeError(String(mapfn) + ' is not a function');
    }
    const out: any[] = [];
    if (items[Symbol.asyncIterator] != null || items[Symbol.iterator] != null) {
        let k = 0;
        for await (const v of items) {
            out.push(mapfn === undefined ? v : await mapfn.call(thisArg, v, k));
            k++;
        }
        return out;
    }
    const o = Object(items);
    const len = lengthOf(o);
    if (len > 4294967295) {
        throw new RangeError('Invalid array length'); // ArrayCreate(len)
    }
    for (let k = 0; k < len; k++) {
        const v = await o[k];
        out.push(mapfn === undefined ? v : await mapfn.call(thisArg, v, k));
    }
    return out;
}

// Array.from, Array.of and Array.fromAsync called with a constructor other
// than Array as `this` (`Array.from.call(C, …)`, ECMA-262 §23.1.2.1–3): the
// result is `new C()` (`new C(len)` for an array-like or Array.of), each
// value defined on it as its own data property (CreateDataPropertyOrThrow,
// a TypeError when C's object refuses it), then its length set. A `this`
// that is not a constructor gives a plain array, as Array itself does.
function isCtor(C: any): boolean {
    return typeof C === 'function' && C !== Array && C.prototype !== undefined;
}

function defineElement(a: any, k: number, v: any): void {
    Object.defineProperty(a, String(k), { value: v, writable: true, enumerable: true, configurable: true });
}

function setLength(a: any, n: number): void {
    a.length = n;
    if (a.length !== n) {
        throw new TypeError("Cannot assign to read only property 'length' of object");
    }
}

export function __kml_Array_fromCtor(C: any, args: any[]): any { // kml:lower Array.from.call
    const items: any = args[0], mapfn: any = args[1], thisArg: any = args[2];
    if (!isCtor(C)) {
        return mapfn === undefined ? Array.from(items) : Array.from(items, (v: any, i: number) => mapfn.call(thisArg, v, i));
    }
    if (mapfn !== undefined && typeof mapfn !== 'function') {
        throw new TypeError(String(mapfn) + ' is not a function');
    }
    if (items === null || items === undefined) {
        throw new TypeError(String(items) + ' is not iterable');
    }
    if (items[Symbol.iterator] != null) {
        const out: any = new C();
        let k = 0;
        for (const v of items) {
            defineElement(out, k, mapfn === undefined ? v : mapfn.call(thisArg, v, k));
            k++;
        }
        setLength(out, k);
        return out;
    }
    const o = Object(items);
    const len = lengthOf(o);
    const out: any = new C(len);
    for (let k = 0; k < len; k++) {
        const v = o[k];
        defineElement(out, k, mapfn === undefined ? v : mapfn.call(thisArg, v, k));
    }
    setLength(out, len);
    return out;
}

export function __kml_Array_ofCtor(C: any, items: any[]): any { // kml:lower Array.of.call
    if (!isCtor(C)) {
        return items.slice();
    }
    const len = items.length;
    const out: any = new C(len);
    for (let k = 0; k < len; k++) {
        defineElement(out, k, items[k]);
    }
    setLength(out, len);
    return out;
}

export async function __kml_Array_fromAsyncCtor(C: any, args: any[]): Promise<any> { // kml:lower Array.fromAsync.call
    const items: any = args[0], mapfn: any = args[1], thisArg: any = args[2];
    if (!isCtor(C)) {
        return mapfn === undefined ? __kml_Array_fromAsync(items) : __kml_Array_fromAsync(items, mapfn, thisArg);
    }
    if (mapfn !== undefined && typeof mapfn !== 'function') {
        throw new TypeError(String(mapfn) + ' is not a function');
    }
    if (items[Symbol.asyncIterator] != null || items[Symbol.iterator] != null) {
        const out: any = new C();
        let k = 0;
        for await (const v of items) {
            defineElement(out, k, mapfn === undefined ? v : await mapfn.call(thisArg, v, k));
            k++;
        }
        setLength(out, k);
        return out;
    }
    const o = Object(items);
    const len = lengthOf(o);
    const out: any = new C(len);
    for (let k = 0; k < len; k++) {
        const v = await o[k];
        defineElement(out, k, mapfn === undefined ? v : await mapfn.call(thisArg, v, k));
    }
    setLength(out, len);
    return out;
}
