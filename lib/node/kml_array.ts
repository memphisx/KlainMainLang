// kml:global
// Array.prototype's methods on a receiver that is not an array (an
// array-like object: `Array.prototype.pop.call(obj)`), written in TypeScript
// as ECMA-262 §23.1.3 states them: every element is read, written and
// deleted on the object itself through its properties. Each takes the
// receiver and the call's arguments as one array. The module links when
// the program names Array.prototype, the only way to call one of these
// methods on a receiver that is not an array.
//
// A walk over more indices than SPARSE_WALK visits only the indices the
// object has as its own properties, in the walk's order, as an engine
// keeping a sparse object's elements in a dictionary does: an object with a
// huge `length` costs only the elements it holds.

const MAX_LENGTH = 9007199254740991; // 2 ** 53 - 1
const MAX_ARRAY_LENGTH = 4294967295; // 2 ** 32 - 1
const SPARSE_WALK = 1048576;

// LengthOfArrayLike: ToLength(Get(O, "length")).
export function lengthOf(o: any): number { // kml:lower Array.prototype
    const n = Number(o.length);
    if (n !== n || n <= 0) return 0;
    if (n >= MAX_LENGTH) return MAX_LENGTH;
    return Math.floor(n);
}

// ToIntegerOrInfinity.
function toInteger(v: any): number {
    const n = Number(v);
    if (n !== n || n === 0) return 0;
    if (n === Infinity || n === -Infinity) return n;
    return n < 0 ? Math.ceil(n) : Math.floor(n);
}

// A relative index (`start`, `end`, `fromIndex`) clamped to [0, len].
function relativeIndex(v: any, len: number, dflt: number): number {
    if (v === undefined) return dflt;
    const rel = toInteger(v);
    if (rel === -Infinity) return 0;
    if (rel < 0) return Math.max(len + rel, 0);
    return Math.min(rel, len);
}

// DeletePropertyOrThrow.
function deleteOrThrow(o: any, key: string): void {
    if (!delete o[key]) {
        throw new TypeError("Cannot delete property '" + key + "' of " + Object.prototype.toString.call(o));
    }
}

function pastSafeLength(count: number, len: number): TypeError {
    return new TypeError('Pushing ' + count + ' elements on an array-like of length ' + len + ' is disallowed, as the total surpasses 2**53-1');
}

// ArraySpeciesCreate for a receiver that is not an array: a new Array.
function newArray(len: number): any[] {
    if (len > MAX_ARRAY_LENGTH) throw new RangeError('Invalid array length');
    const a: any[] = [];
    return a;
}

function callable(f: any): void {
    if (typeof f !== 'function') throw new TypeError(String(f) + ' is not a function');
}

// The indices in [lo, hi) a walk visits that the object has: every one
// HasProperty finds for a short walk, else its own integer keys in range.
// ascending gives the walk's order.
function present(o: any, lo: number, hi: number, ascending: boolean): number[] {
    const out: number[] = [];
    if (hi - lo <= SPARSE_WALK) {
        if (ascending) {
            for (let k = lo; k < hi; k++) if (String(k) in o) out.push(k);
        } else {
            for (let k = hi - 1; k >= lo; k--) if (String(k) in o) out.push(k);
        }
        return out;
    }
    const keys = Object.keys(o);
    for (let i = 0; i < keys.length; i++) {
        const key = keys[i];
        const k = Number(key);
        if (k >= lo && k < hi && String(k) === key) out.push(k);
    }
    out.sort((a: number, b: number) => (ascending ? a - b : b - a));
    return out;
}

// Every index in [lo, hi) for a short walk; the object's own integer keys
// in range for a long one (find, includes: holes read undefined).
function allIndices(o: any, lo: number, hi: number, ascending: boolean): number[] {
    if (hi - lo <= SPARSE_WALK) {
        const out: number[] = [];
        if (ascending) {
            for (let k = lo; k < hi; k++) out.push(k);
        } else {
            for (let k = hi - 1; k >= lo; k--) out.push(k);
        }
        return out;
    }
    return present(o, lo, hi, ascending);
}

export function __kml_Array_generic_pop(o: any, args: any[]): any { // kml:lower Array.prototype
    const len = lengthOf(o);
    if (len === 0) {
        o.length = 0;
        return undefined;
    }
    const key = String(len - 1);
    const element = o[key];
    deleteOrThrow(o, key);
    o.length = len - 1;
    return element;
}

export function __kml_Array_generic_push(o: any, args: any[]): number { // kml:lower Array.prototype
    let len = lengthOf(o);
    if (len + args.length > MAX_LENGTH) throw pastSafeLength(args.length, len);
    for (let i = 0; i < args.length; i++) {
        o[String(len)] = args[i];
        len++;
    }
    o.length = len;
    return len;
}

// Moves the elements of [from, from + count) to [to, to + count), in the
// order that never overwrites one not yet moved, deleting a destination
// whose source is missing.
function moveElements(o: any, from: number, to: number, count: number): void {
    if (count <= 0 || from === to) return;
    if (count > SPARSE_WALK) {
        const ks = present(o, from, from + count, from > to);
        const have: any = {};
        for (let i = 0; i < ks.length; i++) have[String(ks[i])] = true;
        // The destinations no source fills.
        const dest = present(o, to, to + count, from > to);
        for (let i = 0; i < ks.length; i++) {
            const k = ks[i];
            o[String(k - from + to)] = o[String(k)];
        }
        for (let i = 0; i < dest.length; i++) {
            const d = dest[i];
            if (!have[String(d - to + from)]) deleteOrThrow(o, String(d));
        }
        return;
    }
    if (from > to) {
        for (let k = 0; k < count; k++) {
            const f = String(from + k);
            const t = String(to + k);
            if (f in o) o[t] = o[f];
            else deleteOrThrow(o, t);
        }
    } else {
        for (let k = count - 1; k >= 0; k--) {
            const f = String(from + k);
            const t = String(to + k);
            if (f in o) o[t] = o[f];
            else deleteOrThrow(o, t);
        }
    }
}

export function __kml_Array_generic_shift(o: any, args: any[]): any { // kml:lower Array.prototype
    const len = lengthOf(o);
    if (len === 0) {
        o.length = 0;
        return undefined;
    }
    const first = o['0'];
    moveElements(o, 1, 0, len - 1);
    deleteOrThrow(o, String(len - 1));
    o.length = len - 1;
    return first;
}

export function __kml_Array_generic_unshift(o: any, args: any[]): number { // kml:lower Array.prototype
    const len = lengthOf(o);
    const count = args.length;
    if (count > 0) {
        if (len + count > MAX_LENGTH) throw pastSafeLength(count, len);
        moveElements(o, 0, count, len);
        for (let j = 0; j < count; j++) o[String(j)] = args[j];
    }
    o.length = len + count;
    return len + count;
}

export function __kml_Array_generic_reverse(o: any, args: any[]): any { // kml:lower Array.prototype
    const len = lengthOf(o);
    const middle = Math.floor(len / 2);
    let lowers: number[];
    if (len > SPARSE_WALK) {
        // Each pair once, from its lower index.
        const seen: any = {};
        lowers = [];
        const ks = present(o, 0, len, true);
        for (let i = 0; i < ks.length; i++) {
            const lo = ks[i] < middle ? ks[i] : len - ks[i] - 1;
            if (lo < middle && !seen[String(lo)]) {
                seen[String(lo)] = true;
                lowers.push(lo);
            }
        }
        lowers.sort((a: number, b: number) => a - b);
    } else {
        lowers = [];
        for (let lower = 0; lower < middle; lower++) lowers.push(lower);
    }
    for (let i = 0; i < lowers.length; i++) {
        const lower = lowers[i];
        const lowerKey = String(lower);
        const upperKey = String(len - lower - 1);
        const lowerExists = lowerKey in o;
        const lowerValue = lowerExists ? o[lowerKey] : undefined;
        const upperExists = upperKey in o;
        const upperValue = upperExists ? o[upperKey] : undefined;
        if (lowerExists && upperExists) {
            o[lowerKey] = upperValue;
            o[upperKey] = lowerValue;
        } else if (upperExists) {
            o[lowerKey] = upperValue;
            deleteOrThrow(o, upperKey);
        } else if (lowerExists) {
            deleteOrThrow(o, lowerKey);
            o[upperKey] = lowerValue;
        }
    }
    return o;
}

export function __kml_Array_generic_splice(o: any, args: any[]): any[] { // kml:lower Array.prototype
    const len = lengthOf(o);
    const start = relativeIndex(args[0], len, 0);
    let itemCount = 0;
    let deleteCount = 0;
    if (args.length === 1) {
        deleteCount = len - start;
    } else if (args.length > 1) {
        itemCount = args.length - 2;
        deleteCount = Math.min(Math.max(toInteger(args[1]), 0), len - start);
    }
    if (len + itemCount - deleteCount > MAX_LENGTH) throw pastSafeLength(itemCount, len);
    const removed = newArray(deleteCount);
    const ks = present(o, start, start + deleteCount, true);
    for (let i = 0; i < ks.length; i++) removed[ks[i] - start] = o[String(ks[i])];
    removed.length = deleteCount;
    if (itemCount !== deleteCount) {
        moveElements(o, start + deleteCount, start + itemCount, len - start - deleteCount);
        if (itemCount < deleteCount) {
            const tail = present(o, len - deleteCount + itemCount, len, false);
            for (let i = 0; i < tail.length; i++) deleteOrThrow(o, String(tail[i]));
        }
    }
    for (let i = 0; i < itemCount; i++) o[String(start + i)] = args[i + 2];
    o.length = len - deleteCount + itemCount;
    return removed;
}

export function __kml_Array_generic_slice(o: any, args: any[]): any[] { // kml:lower Array.prototype
    const len = lengthOf(o);
    const start = relativeIndex(args[0], len, 0);
    const end = relativeIndex(args[1], len, len);
    const count = Math.max(end - start, 0);
    const a = newArray(count);
    const ks = present(o, start, start + count, true);
    for (let i = 0; i < ks.length; i++) a[ks[i] - start] = o[String(ks[i])];
    a.length = count;
    return a;
}

function searchFrom(o: any, args: any[], len: number): number {
    const n = toInteger(args[1]);
    if (n === Infinity) return len;
    if (n === -Infinity) return 0;
    return n >= 0 ? n : Math.max(len + n, 0);
}

export function __kml_Array_generic_indexOf(o: any, args: any[]): number { // kml:lower Array.prototype
    const len = lengthOf(o);
    if (len === 0) return -1;
    const from = searchFrom(o, args, len);
    const ks = present(o, from, len, true);
    for (let i = 0; i < ks.length; i++) if (o[String(ks[i])] === args[0]) return ks[i];
    return -1;
}

export function __kml_Array_generic_lastIndexOf(o: any, args: any[]): number { // kml:lower Array.prototype
    const len = lengthOf(o);
    if (len === 0) return -1;
    let from = len - 1;
    if (args.length > 1) {
        const n = toInteger(args[1]);
        if (n === -Infinity) return -1;
        from = n >= 0 ? Math.min(n, len - 1) : len + n;
    }
    if (from < 0) return -1;
    const ks = present(o, 0, from + 1, false);
    for (let i = 0; i < ks.length; i++) if (o[String(ks[i])] === args[0]) return ks[i];
    return -1;
}

function sameValueZero(a: any, b: any): boolean {
    return a === b || (a !== a && b !== b);
}

export function __kml_Array_generic_includes(o: any, args: any[]): boolean { // kml:lower Array.prototype
    const len = lengthOf(o);
    if (len === 0) return false;
    const from = searchFrom(o, args, len);
    if (len - from > SPARSE_WALK && args[0] === undefined) return true; // a hole reads undefined
    const ks = allIndices(o, from, len, true);
    for (let i = 0; i < ks.length; i++) if (sameValueZero(o[String(ks[i])], args[0])) return true;
    return false;
}

export function __kml_Array_generic_join(o: any, args: any[]): string { // kml:lower Array.prototype
    const len = lengthOf(o);
    const sep = args[0] === undefined ? ',' : String(args[0]);
    let out = '';
    for (let k = 0; k < len; k++) {
        if (k > 0) out += sep;
        const v = o[String(k)];
        if (v !== undefined && v !== null) out += String(v);
    }
    return out;
}

// Array.prototype.toString: this.join() when join is callable, else
// Object.prototype.toString's tag.
export function __kml_Array_generic_toString(o: any, args: any[]): string { // kml:lower Array.prototype
    const j = o.join;
    if (typeof j === 'function') return j.call(o);
    return Object.prototype.toString.call(o);
}

export function __kml_Array_generic_forEach(o: any, args: any[]): void { // kml:lower Array.prototype
    const len = lengthOf(o);
    const f = args[0];
    callable(f);
    const ks = present(o, 0, len, true);
    for (let i = 0; i < ks.length; i++) {
        const key = String(ks[i]);
        if (key in o) f.call(args[1], o[key], ks[i], o);
    }
}

export function __kml_Array_generic_some(o: any, args: any[]): boolean { // kml:lower Array.prototype
    const len = lengthOf(o);
    const f = args[0];
    callable(f);
    const ks = present(o, 0, len, true);
    for (let i = 0; i < ks.length; i++) {
        const key = String(ks[i]);
        if (key in o && f.call(args[1], o[key], ks[i], o)) return true;
    }
    return false;
}

export function __kml_Array_generic_every(o: any, args: any[]): boolean { // kml:lower Array.prototype
    const len = lengthOf(o);
    const f = args[0];
    callable(f);
    const ks = present(o, 0, len, true);
    for (let i = 0; i < ks.length; i++) {
        const key = String(ks[i]);
        if (key in o && !f.call(args[1], o[key], ks[i], o)) return false;
    }
    return true;
}

export function __kml_Array_generic_map(o: any, args: any[]): any[] { // kml:lower Array.prototype
    const len = lengthOf(o);
    const f = args[0];
    callable(f);
    const a = newArray(len);
    const ks = present(o, 0, len, true);
    for (let i = 0; i < ks.length; i++) {
        const key = String(ks[i]);
        if (key in o) a[ks[i]] = f.call(args[1], o[key], ks[i], o);
    }
    a.length = len;
    return a;
}

export function __kml_Array_generic_filter(o: any, args: any[]): any[] { // kml:lower Array.prototype
    const len = lengthOf(o);
    const f = args[0];
    callable(f);
    const a: any[] = [];
    const ks = present(o, 0, len, true);
    for (let i = 0; i < ks.length; i++) {
        const key = String(ks[i]);
        if (key in o) {
            const v = o[key];
            if (f.call(args[1], v, ks[i], o)) a.push(v);
        }
    }
    return a;
}

function reduceWith(o: any, args: any[], ascending: boolean): any {
    const len = lengthOf(o);
    const f = args[0];
    callable(f);
    const ks = present(o, 0, len, ascending);
    let i = 0;
    let acc: any;
    if (args.length > 1) {
        acc = args[1];
    } else {
        while (i < ks.length && !(String(ks[i]) in o)) i++;
        if (i >= ks.length) throw new TypeError('Reduce of empty array with no initial value');
        acc = o[String(ks[i])];
        i++;
    }
    for (; i < ks.length; i++) {
        const key = String(ks[i]);
        if (key in o) acc = f.call(undefined, acc, o[key], ks[i], o);
    }
    return acc;
}

export function __kml_Array_generic_reduce(o: any, args: any[]): any { // kml:lower Array.prototype
    return reduceWith(o, args, true);
}

export function __kml_Array_generic_reduceRight(o: any, args: any[]): any { // kml:lower Array.prototype
    return reduceWith(o, args, false);
}

function findWith(o: any, args: any[], ascending: boolean, wantIndex: boolean): any {
    const len = lengthOf(o);
    const f = args[0];
    callable(f);
    const ks = allIndices(o, 0, len, ascending);
    for (let i = 0; i < ks.length; i++) {
        const v = o[String(ks[i])];
        if (f.call(args[1], v, ks[i], o)) return wantIndex ? ks[i] : v;
    }
    return wantIndex ? -1 : undefined;
}

export function __kml_Array_generic_find(o: any, args: any[]): any { // kml:lower Array.prototype
    return findWith(o, args, true, false);
}

export function __kml_Array_generic_findIndex(o: any, args: any[]): number { // kml:lower Array.prototype
    return findWith(o, args, true, true);
}

export function __kml_Array_generic_findLast(o: any, args: any[]): any { // kml:lower Array.prototype
    return findWith(o, args, false, false);
}

export function __kml_Array_generic_findLastIndex(o: any, args: any[]): number { // kml:lower Array.prototype
    return findWith(o, args, false, true);
}

export function __kml_Array_generic_fill(o: any, args: any[]): any { // kml:lower Array.prototype
    const len = lengthOf(o);
    const start = relativeIndex(args[1], len, 0);
    const end = relativeIndex(args[2], len, len);
    for (let k = start; k < end; k++) o[String(k)] = args[0];
    return o;
}

export function __kml_Array_generic_copyWithin(o: any, args: any[]): any { // kml:lower Array.prototype
    const len = lengthOf(o);
    const to = relativeIndex(args[0], len, 0);
    const from = relativeIndex(args[1], len, 0);
    const end = relativeIndex(args[2], len, len);
    const count = Math.min(end - from, len - to);
    moveElements(o, from, to, count);
    return o;
}

export function __kml_Array_generic_at(o: any, args: any[]): any { // kml:lower Array.prototype
    const len = lengthOf(o);
    const rel = toInteger(args[0]);
    const k = rel >= 0 ? rel : len + rel;
    if (k < 0 || k >= len) return undefined;
    return o[String(k)];
}

// SortCompare with comparefn, or the default string order (undefined last).
function sortCompare(cmp: any): (x: any, y: any) => number {
    return (x: any, y: any): number => {
        if (x === undefined && y === undefined) return 0;
        if (x === undefined) return 1;
        if (y === undefined) return -1;
        if (cmp !== undefined) {
            const v = Number(cmp(x, y));
            return v !== v ? 0 : v;
        }
        const xs = String(x);
        const ys = String(y);
        return xs < ys ? -1 : xs > ys ? 1 : 0;
    };
}

// The present values of the object in [0, len), sorted.
function sortedValues(o: any, cmp: any, holesRead: boolean): any[] {
    const len = lengthOf(o);
    const vals: any[] = [];
    const ks = holesRead ? allIndices(o, 0, len, true) : present(o, 0, len, true);
    for (let i = 0; i < ks.length; i++) vals.push(o[String(ks[i])]);
    vals.sort(sortCompare(cmp));
    return vals;
}

export function __kml_Array_generic_sort(o: any, args: any[]): any { // kml:lower Array.prototype
    const cmp = args[0];
    if (cmp !== undefined) callable(cmp);
    const len = lengthOf(o);
    const vals = sortedValues(o, cmp, false);
    for (let i = 0; i < vals.length; i++) o[String(i)] = vals[i];
    const holes = present(o, vals.length, len, false);
    for (let i = 0; i < holes.length; i++) deleteOrThrow(o, String(holes[i]));
    return o;
}

export function __kml_Array_generic_toSorted(o: any, args: any[]): any[] { // kml:lower Array.prototype
    const cmp = args[0];
    if (cmp !== undefined) callable(cmp);
    const len = lengthOf(o);
    const a = newArray(len);
    const vals = sortedValues(o, cmp, true);
    for (let i = 0; i < vals.length; i++) a[i] = vals[i];
    a.length = len;
    return a;
}

export function __kml_Array_generic_toReversed(o: any, args: any[]): any[] { // kml:lower Array.prototype
    const len = lengthOf(o);
    const a = newArray(len);
    for (let k = 0; k < len; k++) a[k] = o[String(len - k - 1)];
    return a;
}

export function __kml_Array_generic_with(o: any, args: any[]): any[] { // kml:lower Array.prototype
    const len = lengthOf(o);
    const rel = toInteger(args[0]);
    const index = rel >= 0 ? rel : len + rel;
    if (index >= len || index < 0) throw new RangeError('Invalid index : ' + String(args[0]));
    const a = newArray(len);
    for (let k = 0; k < len; k++) a[k] = k === index ? args[1] : o[String(k)];
    return a;
}

export function __kml_Array_generic_toSpliced(o: any, args: any[]): any[] { // kml:lower Array.prototype
    const len = lengthOf(o);
    const start = relativeIndex(args[0], len, 0);
    let insert = 0;
    let skip = 0;
    if (args.length === 1) {
        skip = len - start;
    } else if (args.length > 1) {
        insert = args.length - 2;
        skip = Math.min(Math.max(toInteger(args[1]), 0), len - start);
    }
    const newLen = len + insert - skip;
    if (newLen > MAX_LENGTH) throw new TypeError('Invalid array length');
    const a = newArray(newLen);
    let i = 0;
    for (let k = 0; k < start; k++) a[i++] = o[String(k)];
    for (let j = 0; j < insert; j++) a[i++] = args[j + 2];
    for (let k = start + skip; k < len; k++) a[i++] = o[String(k)];
    return a;
}
