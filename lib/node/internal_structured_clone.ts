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

// An object V8 has no serialization for: a promise or a weak collection
// names its class; a platform object (a URL, a fetch Headers) is of an
// unsupported type. Null for any other object.
function uncloneableObject(value: any): Error | null {
    let name = '';
    if (value instanceof Promise) name = 'Promise';
    else if (value instanceof WeakMap) name = 'WeakMap';
    else if (value instanceof WeakSet) name = 'WeakSet';
    else if (value instanceof WeakRef) name = 'WeakRef';
    else if (value instanceof FinalizationRegistry) name = 'FinalizationRegistry';
    if (name !== '') return new DOMException('#<' + name + '> could not be cloned.', 'DataCloneError');
    if (value instanceof URL || value instanceof URLSearchParams || value instanceof Headers ||
        value instanceof Request || value instanceof Response)
        return new DOMException('Cannot clone object of unsupported type.', 'DataCloneError');
    return null;
}

// A view's copy: a view of the same kind, offset and length over its
// buffer's copy (one copy per buffer, so views sharing a buffer still do).
function cloneView(value: any, memory: Map<any, any>): any {
    const buf: any = cloneValue(value.buffer, memory);
    const off: number = value.byteOffset;
    if (value instanceof DataView) return new DataView(buf, off, value.byteLength);
    const n: number = value.length;
    if (value instanceof Uint8Array) return new Uint8Array(buf, off, n);
    if (value instanceof Int8Array) return new Int8Array(buf, off, n);
    if (value instanceof Uint8ClampedArray) return new Uint8ClampedArray(buf, off, n);
    if (value instanceof Int16Array) return new Int16Array(buf, off, n);
    if (value instanceof Uint16Array) return new Uint16Array(buf, off, n);
    if (value instanceof Int32Array) return new Int32Array(buf, off, n);
    if (value instanceof Uint32Array) return new Uint32Array(buf, off, n);
    if (value instanceof Float32Array) return new Float32Array(buf, off, n);
    if (value instanceof Float64Array) return new Float64Array(buf, off, n);
    if (value instanceof BigInt64Array) return new BigInt64Array(buf, off, n);
    if (value instanceof BigUint64Array) return new BigUint64Array(buf, off, n);
    throw notCloneable(value);
}

// A wrapper object's copy (`new Number(3)`), or undefined for any other
// object. A Symbol wrapper is no cloneable value.
function cloneWrapper(value: any): any {
    const t: string = Object.prototype.toString.call(value);
    if (t === '[object Boolean]') return new Boolean(value.valueOf());
    if (t === '[object Number]') return new Number(value.valueOf());
    if (t === '[object String]') return new String(value.valueOf());
    if (t === '[object BigInt]') return Object(value.valueOf());
    if (t === '[object Symbol]') throw new DOMException(t + ' could not be cloned.', 'DataCloneError');
    return undefined;
}

function cloneValue(value: any, memory: Map<any, any>): any {
    if (typeof value === 'function' || typeof value === 'symbol') throw notCloneable(value);
    if (value === null || typeof value !== 'object') return value;
    const seen = memory.get(value);
    if (seen !== undefined) return seen;
    if (__kml_native.isProxy(value)) throw new DOMException('#<Object> could not be cloned.', 'DataCloneError');
    const refused = uncloneableObject(value);
    if (refused !== null) throw refused;
    const wrapped = cloneWrapper(value);
    if (wrapped !== undefined) {
        memory.set(value, wrapped);
        return wrapped;
    }
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
    // A SharedArrayBuffer is shared, not copied.
    if (value instanceof SharedArrayBuffer) return value;
    if (value instanceof ArrayBuffer) {
        const copy = (value as ArrayBuffer).slice(0);
        memory.set(value, copy);
        return copy;
    }
    if (ArrayBuffer.isView(value)) {
        const copy = cloneView(value, memory);
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

// options is evaluated by the caller and otherwise unused: a transferred
// ArrayBuffer is copied, not detached.
export function structuredCloneAny(value: any, options?: any): any {
    return cloneValue(value, new Map<any, any>());
}
