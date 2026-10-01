// Node's lib/internal/util/comparisons.js: isDeepEqual, isDeepStrictEqual
// and isPartialStrictEqual.
//
// Node compares prototypes (ObjectGetPrototypeOf, constructors); here a
// value's prototype is named by __kml_native.protoKey, which is equal for
// two objects exactly when their prototypes are. A URL is a plain object of
// its components here, so comparing them is comparing its href, as Node
// does. Symbol-keyed properties are not compared.

const kStrict = 2;
const kStrictWithoutPrototypes = 3;
const kLoose = 0;
const kPartial = 1;

const kNoIterator = 0;
const kIsArray = 1;
const kIsSet = 2;
const kIsMap = 3;

class Memos {
    set: Set<any> | undefined = undefined;
    a: any = undefined;
    b: any = undefined;
    c: any = undefined;
    d: any = undefined;
    deep = false;
}

function hasOwn(o: any, key: any): boolean {
    return Object.prototype.hasOwnProperty.call(o, key);
}

function hasEnumerable(o: any, key: any): boolean {
    return Object.prototype.propertyIsEnumerable.call(o, key);
}

function isError(v: any): boolean {
    return v instanceof Error;
}

function prototypeKey(v: any): string {
    return __kml_native.protoKey(v);
}

function isArrayBufferView(v: any): boolean {
    return ArrayBuffer.isView(v);
}

function isAnyArrayBuffer(v: any): boolean {
    return v instanceof ArrayBuffer || v instanceof SharedArrayBuffer;
}

function isFloatArray(v: any): boolean {
    return v instanceof Float32Array || v instanceof Float64Array;
}

// Check if they have the same source and flags
function areSimilarRegExps(a: any, b: any): boolean {
    return a.source === b.source &&
        a.flags === b.flags &&
        a.lastIndex === b.lastIndex;
}

// Two views of one kind (their prototype keys are equal) compare element
// by element.
function isPartialView(a: any, b: any): boolean {
    const lenA = a.length;
    const lenB = b.length;
    if (lenA < lenB) {
        return false;
    }
    let offsetA = 0;
    for (let offsetB = 0; offsetB < lenB; offsetB++) {
        while (!Object.is(a[offsetA], b[offsetB])) {
            offsetA++;
            if (offsetA > lenA - lenB + offsetB) {
                return false;
            }
        }
        offsetA++;
    }
    return true;
}

function areSimilarFloatArrays(a: any, b: any): boolean {
    const len = a.length;
    if (len !== b.length) {
        return false;
    }
    for (let offset = 0; offset < len; offset++) {
        if (a[offset] !== b[offset]) {
            return false;
        }
    }
    return true;
}

// Byte equality of two views of one kind: equal elements, NaN payloads
// and -0 included.
function areSimilarTypedArrays(a: any, b: any): boolean {
    const len = a.length;
    if (len !== b.length) {
        return false;
    }
    for (let i = 0; i < len; i++) {
        if (!Object.is(a[i], b[i])) {
            return false;
        }
    }
    return true;
}

function areEqualArrayBuffers(buf1: any, buf2: any): boolean {
    return buf1.byteLength === buf2.byteLength && areSimilarTypedArrays(new Uint8Array(buf1), new Uint8Array(buf2));
}

function isEnumerableOrIdentical(val1: any, val2: any, prop: string, mode: number, memos: Memos | undefined): boolean {
    return hasEnumerable(val2, prop) ||
        (mode === kPartial && (val2[prop] === undefined || (prop === 'message' && val2[prop] === ''))) ||
        innerDeepEqual(val1[prop], val2[prop], mode, memos);
}

function innerDeepEqual(val1: any, val2: any, mode: number, memos: Memos | undefined): boolean {
    // All identical values are equivalent, as determined by ===.
    if (val1 === val2) {
        return val1 !== 0 || Object.is(val1, val2) || mode === kLoose;
    }

    // Check more closely if val1 and val2 are equal.
    if (mode !== kLoose) {
        if (typeof val1 === 'number') {
            // Check for NaN
            return val1 !== val1 && val2 !== val2;
        }
        if (typeof val2 !== 'object' || typeof val1 !== 'object' || val1 === null || val2 === null) {
            return false;
        }
    } else {
        if (val1 === null || typeof val1 !== 'object') {
            return (val2 === null || typeof val2 !== 'object') &&
                (val1 == val2 || (val1 !== val1 && val2 !== val2));
        }
        if (val2 === null || typeof val2 !== 'object') {
            return false;
        }
    }
    return objectComparisonStart(val1, val2, mode, memos);
}

function isPlainKind(key: string): boolean {
    return key === 'Object' || key === 'null' || key.startsWith('C:') || key.startsWith('P:');
}

function objectComparisonStart(val1: any, val2: any, mode: number, memos: Memos | undefined): boolean {
    const key1 = prototypeKey(val1);
    const key2 = prototypeKey(val2);
    if (mode === kStrict && key1 !== key2) {
        return false;
    }

    if (Array.isArray(val1)) {
        if (!Array.isArray(val2) ||
            (val1.length !== val2.length && (mode !== kPartial || val1.length < val2.length))) {
            return false;
        }
        return keyCheck(val1, val2, mode, memos, kIsArray, []);
    }

    if (isPlainKind(key1)) {
        if (!isPlainKind(key2)) {
            return false;
        }
        return keyCheck(val1, val2, mode, memos, kNoIterator, undefined);
    } else if (val1 instanceof Set) {
        if (!(val2 instanceof Set) ||
            (val1.size !== val2.size && (mode !== kPartial || val1.size < val2.size))) {
            return false;
        }
        return keyCheck(val1, val2, mode, memos, kIsSet, undefined);
    } else if (val1 instanceof Map) {
        if (!(val2 instanceof Map) ||
            (val1.size !== val2.size && (mode !== kPartial || val1.size < val2.size))) {
            return false;
        }
        return keyCheck(val1, val2, mode, memos, kIsMap, undefined);
    } else if (isArrayBufferView(val1)) {
        if (key1 !== key2) {
            return false;
        }
        if (mode === kPartial && val1.length !== val2.length) {
            if (!isPartialView(val1, val2)) {
                return false;
            }
        } else if (mode === kLoose && isFloatArray(val1)) {
            if (!areSimilarFloatArrays(val1, val2)) {
                return false;
            }
        } else if (!areSimilarTypedArrays(val1, val2)) {
            return false;
        }
        return true;
    } else if (val1 instanceof Date) {
        if (!(val2 instanceof Date)) {
            return false;
        }
        const time1 = (val1 as Date).getTime();
        const time2 = (val2 as Date).getTime();
        if (time1 !== time2 && (time1 === time1 || time2 === time2)) {
            return false;
        }
    } else if (val1 instanceof RegExp) {
        if (!(val2 instanceof RegExp) || !areSimilarRegExps(val1, val2)) {
            return false;
        }
    } else if (isAnyArrayBuffer(val1)) {
        if (!isAnyArrayBuffer(val2)) {
            return false;
        }
        if (mode !== kPartial || val1.byteLength === val2.byteLength) {
            if (!areEqualArrayBuffers(val1, val2)) {
                return false;
            }
        } else if (!isPartialView(new Uint8Array(val1), new Uint8Array(val2))) {
            return false;
        }
    } else if (key1 !== key2 && !(isError(val1) && isError(val2)) ||
        Array.isArray(val2) ||
        isArrayBufferView(val2) ||
        val2 instanceof Set ||
        val2 instanceof Map ||
        val2 instanceof Date ||
        val2 instanceof RegExp ||
        isAnyArrayBuffer(val2)) {
        return false;
    } else if (isError(val1)) {
        // Do not compare the stack as it might differ even though the error itself
        // is otherwise identical.
        if (!isError(val2) ||
            !isEnumerableOrIdentical(val1, val2, 'message', mode, memos) ||
            !isEnumerableOrIdentical(val1, val2, 'name', mode, memos) ||
            !isEnumerableOrIdentical(val1, val2, 'cause', mode, memos) ||
            !isEnumerableOrIdentical(val1, val2, 'errors', mode, memos)) {
            return false;
        }
        const hasOwnVal2Cause = hasOwn(val2, 'cause');
        if ((hasOwnVal2Cause !== hasOwn(val1, 'cause') && (mode !== kPartial || hasOwnVal2Cause))) {
            return false;
        }
    } else if (isError(val2) ||
        val1 instanceof WeakMap ||
        val1 instanceof WeakSet ||
        val1 instanceof Promise) {
        return false;
    }

    return keyCheck(val1, val2, mode, memos, kNoIterator, undefined);
}

function keyCheck(val1: any, val2: any, mode: number, memos: Memos | undefined, iterationType: number, keysArg: string[] | undefined): boolean {
    // For all remaining Object pairs, including Array, objects and Maps,
    // equivalence is determined by having:
    // a) The same number of owned enumerable properties
    // b) The same set of keys/indexes (although not necessarily the same order)
    // c) Equivalent values for every corresponding key/index
    // d) For Sets and Maps, equal contents
    // Note: this accounts for both named and indexed properties on Arrays.
    const isArrayLikeObject = keysArg !== undefined;
    const keys2: string[] = keysArg !== undefined ? keysArg : Object.keys(val2);
    let keys1: string[] | undefined = undefined;

    if (!isArrayLikeObject) {
        // The pair must have the same number of owned properties.
        if (mode !== kPartial) {
            keys1 = Object.keys(val1);
            if (keys2.length !== keys1.length) {
                return false;
            }
        }
    }

    if (keys2.length === 0 &&
        (iterationType === kNoIterator ||
            (iterationType === kIsArray && val2.length === 0) ||
            val2.size === 0)) {
        return true;
    }

    return handleCycles(val1, val2, mode, keys1, keys2, memos, iterationType);
}

function handleCycles(val1: any, val2: any, mode: number, keys1: string[] | undefined, keys2: string[], memosArg: Memos | undefined, iterationType: number): boolean {
    // Use memos to handle cycles.
    if (memosArg === undefined) {
        const fresh = new Memos();
        fresh.a = val1;
        fresh.b = val2;
        return objEquiv(val1, val2, mode, keys1, keys2, fresh, iterationType);
    }
    const memos: Memos = memosArg;

    if (memos.set === undefined) {
        if (memos.deep === false) {
            if (memos.a === val1) {
                return memos.b === val2;
            }
            if (memos.b === val2) {
                return false;
            }
            memos.c = val1;
            memos.d = val2;
            memos.deep = true;
            const result = objEquiv(val1, val2, mode, keys1, keys2, memos, iterationType);
            memos.deep = false;
            // objEquiv may have created the set; the narrowing above does
            // not see that.
            const set = memos.set as Set<any> | undefined;
            if (set !== undefined) {
                set.delete(memos.c);
                set.delete(memos.d);
            }
            return result;
        }
        const s = new Set<any>();
        s.add(memos.a);
        s.add(memos.b);
        s.add(memos.c);
        s.add(memos.d);
        memos.set = s;
    }

    const set: Set<any> = memos.set;

    const originalSize = set.size;
    set.add(val1);
    set.add(val2);
    if (originalSize !== set.size - 2) {
        return originalSize === set.size;
    }

    const areEq = objEquiv(val1, val2, mode, keys1, keys2, memos, iterationType);

    set.delete(val1);
    set.delete(val2);

    return areEq;
}

// See https://developer.mozilla.org/en-US/docs/Web/JavaScript/Equality_comparisons_and_sameness#Loose_equality_using
// Sadly it is not possible to detect corresponding values properly in case the
// type is a string, number, bigint or boolean. The reason is that those values
// can match lots of different string values (e.g., 1n == '+00001').
// The answer is null or undefined (the loose partner to look up), or a
// boolean (the result).
function findLooseMatchingPrimitives(primArg: any): any {
    let prim: any = primArg;
    switch (typeof prim) {
        case 'undefined':
            return null;
        case 'object': // Only pass in null as object!
            return undefined;
        case 'symbol':
            return false;
        case 'string':
            prim = +prim;
            // Loose equal entries exist only if the string is possible to convert to
            // a regular number and not NaN.
            if (prim !== prim) {
                return false;
            }
            return true;
        case 'number':
            // Check for NaN
            if (prim !== prim) {
                return false;
            }
    }
    return true;
}

function setMightHaveLoosePrim(a: any, b: any, prim: any): boolean {
    const altValue = findLooseMatchingPrimitives(prim);
    if (altValue !== null && altValue !== undefined) {
        return altValue;
    }
    return !b.has(altValue) && a.has(altValue);
}

function mapMightHaveLoosePrim(a: any, b: any, prim: any, item2: any, memo: Memos | undefined): boolean {
    const altValue = findLooseMatchingPrimitives(prim);
    if (altValue !== null && altValue !== undefined) {
        return altValue;
    }
    const item1 = a.get(altValue);
    if ((item1 === undefined && !a.has(altValue)) || !innerDeepEqual(item1, item2, kLoose, memo)) {
        return false;
    }
    return !b.has(altValue) && innerDeepEqual(item1, item2, kLoose, memo);
}

function arrayHasEqualElement(array: any[], val1: any, mode: number, memo: Memos | undefined, strictObjects: boolean, start: number, end: number): boolean {
    for (let i = end - 1; i >= start; i--) {
        const eq = strictObjects ? objectComparisonStart(val1, array[i], mode, memo) : innerDeepEqual(val1, array[i], mode, memo);
        if (eq) {
            // Move the matching element to make sure we do not check that again.
            array[i] = array[end];
            return true;
        }
    }
    return false;
}

function partialObjectSetEquiv(array: any[], a: any, b: any, mode: number, memo: Memos | undefined): boolean {
    let aPos = 0;
    let direction = 1;
    let start = 0;
    let end = array.length - 1;
    for (const val1 of a) {
        aPos++;
        if (!b.has(val1)) {
            let innerStart = start;
            if (direction === 1) {
                if (innerDeepEqual(val1, array[start], mode, memo)) {
                    if (start === end) {
                        return true;
                    }
                    start += 1;
                    continue;
                }
                if (start === end) {
                    // The last element of set b might match a later element in set a.
                    continue;
                }
                direction = -1;
                innerStart += 1;
            }
            let matched = true;
            if (!innerDeepEqual(val1, array[end], mode, memo)) {
                direction = 1;
                matched = arrayHasEqualElement(array, val1, mode, memo, false, innerStart, end);
            }
            if (matched) {
                if (start === end) {
                    return true;
                }
                end -= 1;
            }
        }
        if (a.size - aPos <= end - start) {
            return false;
        }
    }
    return false;
}

function setObjectEquiv(array: any[], a: any, b: any, mode: number, memo: Memos | undefined): boolean {
    let direction = 1;
    let start = 0;
    let end = array.length - 1;
    const strictObjects = mode !== kLoose;
    const compare = (x: any, y: any): boolean => strictObjects ? objectComparisonStart(x, y, mode, memo) : innerDeepEqual(x, y, mode, memo);
    const extraChecks = mode === kLoose || array.length !== a.size;
    for (const val1 of a) {
        if (extraChecks) {
            if (typeof val1 === 'object') {
                if (b.has(val1)) {
                    continue;
                }
            } else if (b.has(val1)) {
                continue;
            } else if (mode !== kLoose) {
                return false;
            }
        }
        let innerStart = start;
        if (direction === 1) {
            if (compare(val1, array[start])) {
                start += 1;
                continue;
            }
            if (start === end) {
                return false;
            }
            direction = -1;
            innerStart += 1;
        }
        if (!compare(val1, array[end])) {
            direction = 1;
            if (!arrayHasEqualElement(array, val1, mode, memo, strictObjects, innerStart, end)) {
                return false;
            }
        }
        end -= 1;
    }
    return true;
}

function compareSmallSets(a: any, b: any, val: any, restB: any[], mode: number, memo: Memos | undefined): boolean {
    const valuesA: any[] = [...a];
    const firstA = valuesA[0];
    const first = innerDeepEqual(firstA, val, mode, memo);
    if (first) {
        if (b.size === 1) { // Partial mode && a.size === 1 || b.size === 1
            return true;
        }
        const secondA = valuesA[1];
        return b.has(secondA) || innerDeepEqual(secondA, restB[0], mode, memo);
    }
    return a.size !== 1 && innerDeepEqual(valuesA[1], val, mode, memo) && (
        b.size === 1 || // Partial mode
        b.has(firstA) || // Primitive or reference equal
        innerDeepEqual(firstA, restB[0], mode, memo)
    );
}

function setEquiv(a: any, b: any, mode: number, memo: Memos | undefined): boolean {
    // This is a lazily initiated Set of entries which have to be compared
    // pairwise.
    let array: any[] | undefined = undefined;

    const valuesB: any[] = [...b];
    for (let i = 0; i < valuesB.length; i++) {
        const val = valuesB[i];
        if (!a.has(val)) {
            if ((typeof val !== 'object' || val === null) &&
                (mode !== kLoose || !setMightHaveLoosePrim(a, b, val))) {
                return false;
            }

            if (array === undefined) {
                if (a.size < 3) {
                    return compareSmallSets(a, b, val, valuesB.slice(i + 1), mode, memo);
                }
                array = [];
            }
            // If the specified value doesn't exist in the second set it's a object
            // (or in loose mode: a non-matching primitive). Find the
            // deep-(mode-)equal element in a set copy to reduce duplicate checks.
            array.push(val);
        }
    }

    if (array === undefined) {
        return true;
    }
    if (mode === kPartial) {
        return partialObjectSetEquiv(array, a, b, mode, memo);
    }
    return setObjectEquiv(array, a, b, mode, memo);
}

function arrayHasEqualMapElement(array: any[], key1: any, item1: any, b: any, mode: number, memo: Memos | undefined, strictObjects: boolean, start: number, end: number): boolean {
    for (let i = end - 1; i >= start; i--) {
        const key2 = array[i];
        const keysEq = strictObjects ? objectComparisonStart(key1, key2, mode, memo) : innerDeepEqual(key1, key2, mode, memo);
        if (keysEq && innerDeepEqual(item1, b.get(key2), mode, memo)) {
            // Move the matching element to make sure we do not check that again.
            array[i] = array[end];
            return true;
        }
    }
    return false;
}

function partialObjectMapEquiv(array: any[], a: any, b: any, mode: number, memo: Memos | undefined): boolean {
    let aPos = 0;
    let direction = 1;
    let start = 0;
    let end = array.length - 1;
    for (const [key1, item1] of a) {
        aPos++;
        if (typeof key1 === 'object' && key1 !== null) {
            let innerStart = start;
            if (direction === 1) {
                const key2 = array[start];
                if (objectComparisonStart(key1, key2, mode, memo) && innerDeepEqual(item1, b.get(key2), mode, memo)) {
                    if (start === end) {
                        return true;
                    }
                    start += 1;
                    continue;
                }
                if (start === end) {
                    // The last element of map b might match a later element in map a.
                    continue;
                }
                direction = -1;
                innerStart += 1;
            }
            let matched = true;
            const key2 = array[end];
            if (!objectComparisonStart(key1, key2, mode, memo) || !innerDeepEqual(item1, b.get(key2), mode, memo)) {
                direction = 1;
                matched = arrayHasEqualMapElement(array, key1, item1, b, mode, memo, true, innerStart, end);
            }
            if (matched) {
                if (start === end) {
                    return true;
                }
                end -= 1;
            }
        }
        if (a.size - aPos <= end - start) {
            return false;
        }
    }
    return false;
}

function mapObjectEquiv(array: any[], a: any, b: any, mode: number, memo: Memos | undefined): boolean {
    let direction = 1;
    let start = 0;
    let end = array.length - 1;
    const strictObjects = mode !== kLoose;
    const compare = (x: any, y: any): boolean => strictObjects ? objectComparisonStart(x, y, mode, memo) : innerDeepEqual(x, y, mode, memo);
    const extraChecks = mode === kLoose || array.length !== a.size;

    for (const [key1, item1] of a) {
        if (extraChecks && (typeof key1 !== 'object' || key1 === null)) {
            if (b.has(key1)) {
                if (mode !== kLoose || innerDeepEqual(item1, b.get(key1), mode, memo)) {
                    continue;
                }
            } else if (mode !== kLoose) {
                return false;
            }
        }

        let innerStart = start;
        if (direction === 1) {
            const key2 = array[start];
            if (compare(key1, key2) && innerDeepEqual(item1, b.get(key2), mode, memo)) {
                start += 1;
                continue;
            }
            if (start === end) {
                return false;
            }
            direction = -1;
            innerStart += 1;
        }
        const key2 = array[end];
        if (!compare(key1, key2) || !innerDeepEqual(item1, b.get(key2), mode, memo)) {
            direction = 1;
            if (!arrayHasEqualMapElement(array, key1, item1, b, mode, memo, strictObjects, innerStart, end)) {
                return false;
            }
        }
        end -= 1;
    }
    return true;
}

function mapEquiv(a: any, b: any, mode: number, memo: Memos | undefined): boolean {
    let array: any[] | undefined = undefined;

    for (const [key2, item2] of b) {
        if (typeof key2 === 'object' && key2 !== null) {
            if (array === undefined) {
                if (a.size === 1) {
                    const entries: any[] = [...a];
                    const first: any = entries[0];
                    return innerDeepEqual(first[0], key2, mode, memo) &&
                        innerDeepEqual(first[1], item2, mode, memo);
                }
                array = [];
            }
            array.push(key2);
        } else {
            // By directly retrieving the value we prevent another b.has(key2) check in
            // almost all possible cases.
            const item1 = a.get(key2);
            if (((item1 === undefined && !a.has(key2)) || !innerDeepEqual(item1, item2, mode, memo))) {
                if (mode !== kLoose) {
                    return false;
                }
                // Fast path to detect missing string, symbol, undefined and null
                // keys.
                if (!mapMightHaveLoosePrim(a, b, key2, item2, memo)) {
                    return false;
                }
                if (array === undefined) {
                    array = [];
                }
                array.push(key2);
            }
        }
    }

    if (array === undefined) {
        return true;
    }

    if (mode === kPartial) {
        return partialObjectMapEquiv(array, a, b, mode, memo);
    }

    return mapObjectEquiv(array, a, b, mode, memo);
}

function partialSparseArrayEquiv(a: any, b: any, mode: number, memos: Memos | undefined, startA: number, startB: number): boolean {
    let aPos = startA;
    const keysA = Object.keys(a);
    const keysB = Object.keys(b);
    const lenA = keysA.length - startA;
    const lenB = keysB.length - startB;
    if (lenA < lenB) {
        return false;
    }
    for (let i = 0; i < lenB; i++) {
        const keyB = keysB[startB + i];
        while (!innerDeepEqual(a[keysA[aPos]], b[keyB], mode, memos)) {
            aPos++;
            if (aPos > keysA.length - lenB + i) {
                return false;
            }
        }
        aPos++;
    }
    return true;
}

function partialArrayEquiv(a: any, b: any, mode: number, memos: Memos | undefined): boolean {
    let aPos = 0;
    for (let i = 0; i < b.length; i++) {
        let isSparse = b[i] === undefined && !hasOwn(b, i);
        if (isSparse) {
            return partialSparseArrayEquiv(a, b, mode, memos, aPos, i);
        }
        while (!(isSparse = a[aPos] === undefined && !hasOwn(a, aPos)) &&
            !innerDeepEqual(a[aPos], b[i], mode, memos)) {
            aPos++;
            if (aPos > a.length - b.length + i) {
                return false;
            }
        }
        if (isSparse) {
            return partialSparseArrayEquiv(a, b, mode, memos, aPos, i);
        }
        aPos++;
    }
    return true;
}

function sparseArrayEquiv(a: any, b: any, mode: number, memos: Memos | undefined, start: number): boolean {
    const keysA = Object.keys(a);
    const keysB = Object.keys(b);
    if (keysA.length !== keysB.length) {
        return false;
    }
    for (let i = start; i < keysB.length; i++) {
        const key = keysB[i];
        if ((a[key] === undefined && !hasOwn(a, key)) || !innerDeepEqual(a[key], b[key], mode, memos)) {
            return false;
        }
    }
    return true;
}

function objEquiv(a: any, b: any, mode: number, keys1: string[] | undefined, keys2: string[], memos: Memos | undefined, iterationType: number): boolean {
    // The pair must have equivalent values for every corresponding key.
    if (keys2.length > 0) {
        let i = 0;
        // Ordered keys
        if (keys1 !== undefined) {
            for (; i < keys2.length; i++) {
                const key = keys2[i];
                if (keys1[i] !== key) {
                    break;
                }
                if (!innerDeepEqual(a[key], b[key], mode, memos)) {
                    return false;
                }
            }
        }
        // Unordered keys
        for (; i < keys2.length; i++) {
            const key = keys2[i];
            if (!hasOwn(a, key) || !hasEnumerable(a, key)) {
                return false;
            }
            if (!innerDeepEqual(a[key], b[key], mode, memos)) {
                return false;
            }
        }
    }

    if (iterationType === kIsArray) {
        if (mode === kPartial) {
            return partialArrayEquiv(a, b, mode, memos);
        }
        for (let i = 0; i < a.length; i++) {
            if (b[i] === undefined) {
                if (!hasOwn(b, i)) {
                    return sparseArrayEquiv(a, b, mode, memos, i);
                }
                if ((a[i] !== undefined || !hasOwn(a, i)) && (mode !== kLoose || a[i] !== null)) {
                    return false;
                }
            } else if ((a[i] === undefined || !innerDeepEqual(a[i], b[i], mode, memos)) &&
                (mode !== kLoose || b[i] !== null)) {
                return false;
            }
        }
    } else if (iterationType === kIsSet) {
        if (!setEquiv(a, b, mode, memos)) {
            return false;
        }
    } else if (iterationType === kIsMap) {
        if (!mapEquiv(a, b, mode, memos)) {
            return false;
        }
    }

    return true;
}

export function isDeepEqual(val1: unknown, val2: unknown): boolean {
    return innerDeepEqual(val1, val2, kLoose, undefined);
}

export function isDeepStrictEqual(val1: unknown, val2: unknown, skipPrototype?: boolean): boolean {
    return innerDeepEqual(val1, val2, skipPrototype ? kStrictWithoutPrototypes : kStrict, undefined);
}

export function isPartialStrictEqual(val1: unknown, val2: unknown): boolean {
    return innerDeepEqual(val1, val2, kPartial, undefined);
}
