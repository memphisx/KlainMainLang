// kml:global
// Iterator.prototype's helpers written in TypeScript (ECMA-262 §27.1.4),
// reached through their `@lower` declarations on IteratorObject in
// lib/es.d.ts. The receiver arrives as the first argument; any object with a
// `next` method works, as GetIteratorDirect allows.

// GetIteratorDirect's `next`, read once.
function nextMethod(it: any): any {
    if (it === null || (typeof it !== 'object' && typeof it !== 'function')) {
        throw new TypeError('Iterator helper called on non-object');
    }
    return it.next;
}

function requireCallable(fn: any): void {
    if (typeof fn !== 'function') {
        throw new TypeError(String(fn) + ' is not a function');
    }
}

// IteratorStep: the result object, checked to be one.
function step(it: any, next: any): any {
    const r: any = next.call(it);
    if (r === null || (typeof r !== 'object' && typeof r !== 'function')) {
        throw new TypeError('Iterator result ' + String(r) + ' is not an object');
    }
    return r;
}

// IteratorClose with a normal completion: call `return` when there is one;
// its result must be an object.
function closeIter(it: any): void {
    const ret: any = it.return;
    if (ret === undefined || ret === null) return;
    const r: any = ret.call(it);
    if (r === null || (typeof r !== 'object' && typeof r !== 'function')) {
        throw new TypeError('Iterator result ' + String(r) + ' is not an object');
    }
}

// IteratorClose with a throw completion: `return` is called and anything it
// throws is dropped; the original error propagates.
function closeIterOnThrow(it: any): void {
    try {
        const ret: any = it.return;
        if (ret !== undefined && ret !== null) ret.call(it);
    } catch (_) {
        // the original completion wins
    }
}

export function __kml_Iterator_every(it: any, predicate: any): boolean { // kml:lower every
    const next = nextMethod(it);
    try {
        requireCallable(predicate);
    } catch (e) {
        closeIterOnThrow(it);
        throw e;
    }
    let counter = 0;
    while (true) {
        const r = step(it, next);
        if (r.done) return true;
        let ok: any;
        try {
            ok = predicate(r.value, counter);
        } catch (e) {
            closeIterOnThrow(it);
            throw e;
        }
        if (!ok) {
            closeIter(it);
            return false;
        }
        counter++;
    }
}

export function __kml_Iterator_some(it: any, predicate: any): boolean { // kml:lower some
    const next = nextMethod(it);
    try {
        requireCallable(predicate);
    } catch (e) {
        closeIterOnThrow(it);
        throw e;
    }
    let counter = 0;
    while (true) {
        const r = step(it, next);
        if (r.done) return false;
        let ok: any;
        try {
            ok = predicate(r.value, counter);
        } catch (e) {
            closeIterOnThrow(it);
            throw e;
        }
        if (ok) {
            closeIter(it);
            return true;
        }
        counter++;
    }
}

export function __kml_Iterator_find(it: any, predicate: any): any { // kml:lower find
    const next = nextMethod(it);
    try {
        requireCallable(predicate);
    } catch (e) {
        closeIterOnThrow(it);
        throw e;
    }
    let counter = 0;
    while (true) {
        const r = step(it, next);
        if (r.done) return undefined;
        const v: any = r.value;
        let ok: any;
        try {
            ok = predicate(v, counter);
        } catch (e) {
            closeIterOnThrow(it);
            throw e;
        }
        if (ok) {
            closeIter(it);
            return v;
        }
        counter++;
    }
}

export function __kml_Iterator_forEach(it: any, fn: any): void { // kml:lower forEach
    const next = nextMethod(it);
    try {
        requireCallable(fn);
    } catch (e) {
        closeIterOnThrow(it);
        throw e;
    }
    let counter = 0;
    while (true) {
        const r = step(it, next);
        if (r.done) return;
        try {
            fn(r.value, counter);
        } catch (e) {
            closeIterOnThrow(it);
            throw e;
        }
        counter++;
    }
}

export function __kml_Iterator_reduce(it: any, reducer: any, ...initial: any[]): any { // kml:lower reduce
    const next = nextMethod(it);
    try {
        requireCallable(reducer);
    } catch (e) {
        closeIterOnThrow(it);
        throw e;
    }
    let acc: any;
    let counter: number;
    if (initial.length === 0) {
        const first = step(it, next);
        if (first.done) throw new TypeError('Reduce of a done iterator with no initial value');
        acc = first.value;
        counter = 1;
    } else {
        acc = initial[0];
        counter = 0;
    }
    while (true) {
        const r = step(it, next);
        if (r.done) return acc;
        try {
            acc = reducer(acc, r.value, counter);
        } catch (e) {
            closeIterOnThrow(it);
            throw e;
        }
        counter++;
    }
}

export function __kml_Iterator_toArray(it: any): any[] { // kml:lower toArray
    const next = nextMethod(it);
    const out: any[] = [];
    while (true) {
        const r = step(it, next);
        if (r.done) return out;
        out.push(r.value);
    }
}

// ToIntegerOrInfinity of take/drop's limit, validated: a RangeError for NaN
// or a negative limit, the underlying iterator closed first.
function helperLimit(it: any, limit: any): number {
    let n: number;
    try {
        n = Number(limit);
    } catch (e) {
        closeIterOnThrow(it);
        throw e;
    }
    if (n !== n) {
        closeIterOnThrow(it);
        throw new RangeError(String(limit) + ' must be positive');
    }
    const k = n === Infinity || n === -Infinity ? n : Math.trunc(n);
    if (k < 0) {
        closeIterOnThrow(it);
        throw new RangeError(String(limit) + ' must be positive');
    }
    return k;
}

const KIND_MAP = 0;
const KIND_FILTER = 1;
const KIND_TAKE = 2;
const KIND_DROP = 3;
const KIND_FLATMAP = 4;

const STATE_START = 0;
const STATE_SUSPENDED = 1;
const STATE_RUNNING = 2;
const STATE_DONE = 3;

// An Iterator Helper object (§27.1.2.1): the lazy result of map, filter,
// take, drop and flatMap over the underlying iterator.
export class IteratorHelper {
    #src: any;
    #next: any;
    #kind: number;
    #fn: any;
    #remaining: number;
    #counter: number = 0;
    #state: number = STATE_START;
    #inner: any = undefined;
    #innerNext: any = undefined;

    constructor(src: any, next: any, kind: number, fn: any, remaining: number) {
        this.#src = src;
        this.#next = next;
        this.#kind = kind;
        this.#fn = fn;
        this.#remaining = remaining;
    }

    next(): any {
        if (this.#state === STATE_RUNNING) throw new TypeError('Generator is already running');
        if (this.#state === STATE_DONE) return { value: undefined, done: true };
        this.#state = STATE_RUNNING;
        try {
            const r = this.#step();
            this.#state = r.done ? STATE_DONE : STATE_SUSPENDED;
            return r;
        } catch (e) {
            this.#state = STATE_DONE;
            throw e;
        }
    }

    return(): any {
        if (this.#state === STATE_RUNNING) throw new TypeError('Generator is already running');
        if (this.#state === STATE_DONE) return { value: undefined, done: true };
        this.#state = STATE_DONE;
        if (this.#inner !== undefined) {
            const inner = this.#inner;
            this.#inner = undefined;
            try {
                closeIter(inner);
            } catch (e) {
                closeIterOnThrow(this.#src);
                throw e;
            }
        }
        closeIter(this.#src);
        return { value: undefined, done: true };
    }

    [Symbol.iterator](): any {
        return this;
    }

    get [Symbol.toStringTag](): string {
        return 'Iterator Helper';
    }

    // Iterator.prototype's methods, which a helper inherits.
    map(fn: any): any { return __kml_Iterator_map(this, fn); }
    filter(fn: any): any { return __kml_Iterator_filter(this, fn); }
    take(limit: any): any { return __kml_Iterator_take(this, limit); }
    drop(limit: any): any { return __kml_Iterator_drop(this, limit); }
    flatMap(fn: any): any { return __kml_Iterator_flatMap(this, fn); }
    reduce(fn: any, ...initial: any[]): any { return __kml_Iterator_reduce(this, fn, ...initial); }
    toArray(): any[] { return __kml_Iterator_toArray(this); }
    forEach(fn: any): void { __kml_Iterator_forEach(this, fn); }
    some(fn: any): boolean { return __kml_Iterator_some(this, fn); }
    every(fn: any): boolean { return __kml_Iterator_every(this, fn); }
    find(fn: any): any { return __kml_Iterator_find(this, fn); }

    #call(v: any): any {
        try {
            return this.#fn(v, this.#counter);
        } catch (e) {
            closeIterOnThrow(this.#src);
            throw e;
        }
    }

    #step(): any {
        const src = this.#src;
        const next = this.#next;
        switch (this.#kind) {
            case KIND_MAP: {
                const r = step(src, next);
                if (r.done) return { value: undefined, done: true };
                const v = this.#call(r.value);
                this.#counter++;
                return { value: v, done: false };
            }
            case KIND_FILTER:
                while (true) {
                    const r = step(src, next);
                    if (r.done) return { value: undefined, done: true };
                    const v: any = r.value;
                    const keep = this.#call(v);
                    this.#counter++;
                    if (keep) return { value: v, done: false };
                }
            case KIND_TAKE: {
                if (this.#remaining === 0) {
                    closeIter(src);
                    return { value: undefined, done: true };
                }
                if (this.#remaining !== Infinity) this.#remaining--;
                const r = step(src, next);
                if (r.done) return { value: undefined, done: true };
                return { value: r.value, done: false };
            }
            case KIND_DROP: {
                while (this.#remaining > 0) {
                    if (this.#remaining !== Infinity) this.#remaining--;
                    const d = step(src, next);
                    if (d.done) return { value: undefined, done: true };
                }
                const r = step(src, next);
                if (r.done) return { value: undefined, done: true };
                return { value: r.value, done: false };
            }
        }
        // flatMap: drain each mapped value's iterator in turn.
        while (true) {
            if (this.#inner !== undefined) {
                let ir: any;
                try {
                    ir = step(this.#inner, this.#innerNext);
                } catch (e) {
                    this.#inner = undefined;
                    closeIterOnThrow(src);
                    throw e;
                }
                if (!ir.done) return { value: ir.value, done: false };
                this.#inner = undefined;
            }
            const r = step(src, next);
            if (r.done) return { value: undefined, done: true };
            const mapped = this.#call(r.value);
            this.#counter++;
            try {
                const inner = flattenable(mapped);
                this.#innerNext = inner.next;
                this.#inner = inner;
            } catch (e) {
                closeIterOnThrow(src);
                throw e;
            }
        }
    }
}

// GetIteratorFlattenable(obj, reject-primitives): an object's iterator, or
// the object itself when it has no Symbol.iterator.
function flattenable(obj: any): any {
    if (obj === null || (typeof obj !== 'object' && typeof obj !== 'function')) {
        throw new TypeError(String(obj) + ' is not an object');
    }
    const method: any = obj[Symbol.iterator];
    const it: any = method === undefined || method === null ? obj : method.call(obj);
    if (it === null || (typeof it !== 'object' && typeof it !== 'function')) {
        throw new TypeError(String(it) + ' is not an object');
    }
    return it;
}

function helperOver(it: any, kind: number, fn: any): any {
    const next = nextMethod(it);
    try {
        requireCallable(fn);
    } catch (e) {
        closeIterOnThrow(it);
        throw e;
    }
    return new IteratorHelper(it, next, kind, fn, 0);
}

export function __kml_Iterator_map(it: any, mapper: any): any { // kml:lower map
    return helperOver(it, KIND_MAP, mapper);
}

export function __kml_Iterator_filter(it: any, predicate: any): any { // kml:lower filter
    return helperOver(it, KIND_FILTER, predicate);
}

export function __kml_Iterator_flatMap(it: any, mapper: any): any { // kml:lower flatMap
    return helperOver(it, KIND_FLATMAP, mapper);
}

export function __kml_Iterator_take(it: any, limit: any): any { // kml:lower take
    const next = nextMethod(it);
    return new IteratorHelper(it, next, KIND_TAKE, undefined, helperLimit(it, limit));
}

export function __kml_Iterator_drop(it: any, limit: any): any { // kml:lower drop
    const next = nextMethod(it);
    return new IteratorHelper(it, next, KIND_DROP, undefined, helperLimit(it, limit));
}
