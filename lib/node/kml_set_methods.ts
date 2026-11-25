// kml:global
// Set.prototype's set-algebra methods (ES2025, §24.2.4): union,
// intersection, difference, symmetricDifference, isSubsetOf,
// isSupersetOf and isDisjointFrom, as ECMA-262 states them. Each takes its
// receiver first. The other argument is any set-like object: GetSetRecord
// reads its `size`, `has` and `keys` once, in that order, and validates
// them; the algorithms then call them as the spec does, so a Map or a
// user object with those members works, and a callback that changes the
// receiver mid-walk sees the spec's order of reads.

interface SetRecord {
    set: any;
    size: number;
    has: any;
    keys: any;
}

// The receiver stays `any`: this module is compiled once for every
// program, so a typed Set view here would not know the program's own Set
// layouts (a Set<number> holds raw doubles); reads through `any` dispatch
// on the object's own layout.
function thisSet(recv: any, method: string): any {
    if (!(recv instanceof Set)) {
        throw new TypeError('Method Set.prototype.' + method + ' called on incompatible receiver ' + receiverText(recv));
    }
    return recv;
}

function receiverText(v: any): string {
    if (v === null) return 'null';
    if (v === undefined) return 'undefined';
    if (typeof v === 'string') return v;
    if (typeof v === 'object' || typeof v === 'function') return '#<' + (v.constructor && v.constructor.name ? v.constructor.name : 'Object') + '>';
    return String(v);
}

// GetSetRecord (§24.2.1.2).
function getSetRecord(obj: any): SetRecord {
    if (obj === null || (typeof obj !== 'object' && typeof obj !== 'function')) {
        throw new TypeError('The "other" argument must be an object');
    }
    const rawSize = obj.size;
    const numSize = Number(rawSize);
    if (numSize !== numSize) {
        throw new TypeError("The 'size' property must be a number");
    }
    const intSize = numSize === Infinity || numSize === -Infinity ? numSize : Math.trunc(numSize);
    if (intSize < 0) {
        throw new RangeError("'" + String(rawSize) + "' is an invalid size");
    }
    const has = obj.has;
    if (typeof has !== 'function') {
        throw new TypeError("The 'has' property must be a function");
    }
    const keys = obj.keys;
    if (typeof keys !== 'function') {
        throw new TypeError("The 'keys' property must be a function");
    }
    return { set: obj, size: intSize, has: has, keys: keys };
}

// GetIteratorFromMethod over the record's keys (GetKeysIterator).
function keysIterator(rec: SetRecord): any {
    const it = rec.keys.call(rec.set);
    if (it === null || (typeof it !== 'object' && typeof it !== 'function')) {
        throw new TypeError('keys() result is not an object');
    }
    const next = it.next;
    if (typeof next !== 'function') {
        throw new TypeError('keys() result has no next method');
    }
    return { it: it, next: next };
}

function step(iter: any): any {
    const r = iter.next.call(iter.it);
    if (r === null || (typeof r !== 'object' && typeof r !== 'function')) {
        throw new TypeError('Iterator result ' + String(r) + ' is not an object');
    }
    return r;
}

// CanonicalizeKeyedCollectionKey: -0 is +0.
function canon(v: any): any {
    return v === 0 ? 0 : v;
}

function hasIn(rec: SetRecord, v: any): boolean {
    return !!rec.has.call(rec.set, v);
}

function closeIterator(iter: any): void {
    const ret = iter.it.return;
    if (ret !== undefined && ret !== null) {
        ret.call(iter.it);
    }
}

export function __kml_Set_union(recv: any, other: any): Set<any> { // kml:lower union
    const o = thisSet(recv, 'union');
    const rec = getSetRecord(other);
    const iter = keysIterator(rec);
    const result: any = new Set<any>(o);
    for (;;) {
        const r = step(iter);
        if (r.done) break;
        result.add(canon(r.value));
    }
    return result;
}

export function __kml_Set_intersection(recv: any, other: any): Set<any> { // kml:lower intersection
    const o = thisSet(recv, 'intersection');
    const rec = getSetRecord(other);
    const result: any = new Set<any>();
    if (o.size <= rec.size) {
        // Walk this set; each element still present is tested against other.
        const elems = Array.from(o);
        for (let i = 0; i < elems.length; i++) {
            const e = elems[i];
            if (!o.has(e)) continue;
            if (hasIn(rec, e)) result.add(e);
        }
    } else {
        const iter = keysIterator(rec);
        for (;;) {
            const r = step(iter);
            if (r.done) break;
            const v = canon(r.value);
            if (o.has(v)) result.add(v);
        }
    }
    return result;
}

export function __kml_Set_difference(recv: any, other: any): Set<any> { // kml:lower difference
    const o = thisSet(recv, 'difference');
    const rec = getSetRecord(other);
    const result: any = new Set<any>(o);
    if (o.size <= rec.size) {
        const elems = Array.from(o);
        for (let i = 0; i < elems.length; i++) {
            const e = elems[i];
            if (!o.has(e)) continue;
            if (hasIn(rec, e)) result.delete(e);
        }
    } else {
        const iter = keysIterator(rec);
        for (;;) {
            const r = step(iter);
            if (r.done) break;
            result.delete(canon(r.value));
        }
    }
    return result;
}

export function __kml_Set_symmetricDifference(recv: any, other: any): Set<any> { // kml:lower symmetricDifference
    const o = thisSet(recv, 'symmetricDifference');
    const rec = getSetRecord(other);
    const iter = keysIterator(rec);
    const result: any = new Set<any>(o);
    for (;;) {
        const r = step(iter);
        if (r.done) break;
        const v = canon(r.value);
        if (o.has(v)) result.delete(v);
        else result.add(v);
    }
    return result;
}

export function __kml_Set_isSubsetOf(recv: any, other: any): boolean { // kml:lower isSubsetOf
    const o = thisSet(recv, 'isSubsetOf');
    const rec = getSetRecord(other);
    if (o.size > rec.size) return false;
    const elems = Array.from(o);
    for (let i = 0; i < elems.length; i++) {
        const e = elems[i];
        if (!o.has(e)) continue;
        if (!hasIn(rec, e)) return false;
    }
    return true;
}

export function __kml_Set_isSupersetOf(recv: any, other: any): boolean { // kml:lower isSupersetOf
    const o = thisSet(recv, 'isSupersetOf');
    const rec = getSetRecord(other);
    if (o.size < rec.size) return false;
    const iter = keysIterator(rec);
    for (;;) {
        const r = step(iter);
        if (r.done) return true;
        if (!o.has(r.value)) {
            closeIterator(iter);
            return false;
        }
    }
}

export function __kml_Set_isDisjointFrom(recv: any, other: any): boolean { // kml:lower isDisjointFrom
    const o = thisSet(recv, 'isDisjointFrom');
    const rec = getSetRecord(other);
    if (o.size <= rec.size) {
        const elems = Array.from(o);
        for (let i = 0; i < elems.length; i++) {
            const e = elems[i];
            if (!o.has(e)) continue;
            if (hasIn(rec, e)) return false;
        }
    } else {
        const iter = keysIterator(rec);
        for (;;) {
            const r = step(iter);
            if (r.done) break;
            if (o.has(r.value)) {
                closeIterator(iter);
                return false;
            }
        }
    }
    return true;
}
