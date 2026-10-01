// structuredClone of a value whose type is only known at run time (`any`,
// `unknown`): the HTML structured clone algorithm as V8's ValueSerializer
// runs it for Node v24. Primitives are themselves; Dates, RegExps, Errors,
// ArrayBuffers, typed arrays, Maps, Sets, arrays and objects are copied,
// each object once (a cycle or a shared reference clones to the same copy).
// An object of any other kind copies its own enumerable properties into a
// plain object, as V8 does for a class instance. A function or a symbol is
// no cloneable value: DataCloneError. Values of a type known at compile time
// are cloned by the compiler.

function notCloneable(value: any): Error {
    return new DOMException(String(value) + ' could not be cloned.', 'DataCloneError');
}

// A TypedArray's copy: a new array of the same kind and elements.
function cloneView(value: any): any {
    const n: number = value.length;
    let copy: any;
    if (value instanceof Uint8Array) copy = new Uint8Array(n);
    else if (value instanceof Int8Array) copy = new Int8Array(n);
    else if (value instanceof Uint8ClampedArray) copy = new Uint8ClampedArray(n);
    else if (value instanceof Int16Array) copy = new Int16Array(n);
    else if (value instanceof Uint16Array) copy = new Uint16Array(n);
    else if (value instanceof Int32Array) copy = new Int32Array(n);
    else if (value instanceof Uint32Array) copy = new Uint32Array(n);
    else if (value instanceof Float32Array) copy = new Float32Array(n);
    else if (value instanceof Float64Array) copy = new Float64Array(n);
    else if (value instanceof BigInt64Array) copy = new BigInt64Array(n);
    else if (value instanceof BigUint64Array) copy = new BigUint64Array(n);
    else throw notCloneable(value);
    for (let i = 0; i < n; i++) copy[i] = value[i];
    return copy;
}

function cloneValue(value: any, memory: Map<any, any>): any {
    if (typeof value === 'function' || typeof value === 'symbol') throw notCloneable(value);
    if (value === null || typeof value !== 'object') return value;
    const seen = memory.get(value);
    if (seen !== undefined) return seen;
    if (value instanceof Date) {
        const d = new Date((value as Date).getTime());
        memory.set(value, d);
        return d;
    }
    if (value instanceof RegExp) {
        const r = value as RegExp;
        const copy = new RegExp(r.source, r.flags);
        memory.set(value, copy);
        return copy;
    }
    if (value instanceof Error) {
        const e = value as Error;
        let copy: Error;
        if (e instanceof TypeError) copy = new TypeError(e.message);
        else if (e instanceof RangeError) copy = new RangeError(e.message);
        else if (e instanceof SyntaxError) copy = new SyntaxError(e.message);
        else if (e instanceof ReferenceError) copy = new ReferenceError(e.message);
        else if (e instanceof EvalError) copy = new EvalError(e.message);
        else if (e instanceof URIError) copy = new URIError(e.message);
        else copy = new Error(e.message);
        memory.set(value, copy);
        if ((e as any).cause !== undefined) (copy as any).cause = cloneValue((e as any).cause, memory);
        return copy;
    }
    if (value instanceof ArrayBuffer) {
        const copy = (value as ArrayBuffer).slice(0);
        memory.set(value, copy);
        return copy;
    }
    if (ArrayBuffer.isView(value)) {
        const copy = cloneView(value);
        memory.set(value, copy);
        return copy;
    }
    if (value instanceof Map) {
        const copy = new Map<any, any>();
        memory.set(value, copy);
        value.forEach((v: any, k: any) => {
            copy.set(cloneValue(k, memory), cloneValue(v, memory));
        });
        return copy;
    }
    if (value instanceof Set) {
        const copy = new Set<any>();
        memory.set(value, copy);
        value.forEach((v: any) => {
            copy.add(cloneValue(v, memory));
        });
        return copy;
    }
    if (Array.isArray(value)) {
        const src: any = value;
        const elements = new Array<any>(src.length);
        const copy: any = elements;
        memory.set(value, copy);
        for (const key of Object.keys(src)) {
            copy[key] = cloneValue(src[key], memory);
        }
        return copy;
    }
    const copy: any = {};
    memory.set(value, copy);
    for (const key of Object.keys(value)) {
        copy[key] = cloneValue(value[key], memory);
    }
    return copy;
}

export function structuredCloneAny(value: any): any {
    return cloneValue(value, new Map<any, any>());
}
