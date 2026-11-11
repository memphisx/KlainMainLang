// The Promise combinators over an untyped iterable, as the specification
// runs them: one item read, resolved and subscribed at a time, so a throw
// closes the iterator and rejects, and an endless iterator is never drained.

// PromiseResolve: a promise is itself.
function promiseResolve(value: any): any {
    if (value instanceof Promise) return value;
    return Promise.resolve(value);
}

// V8's rendering of a value that is not iterable.
function describe(v: any): string {
    if (v === undefined) return 'undefined';
    if (v === null) return 'object null';
    const t = typeof v;
    if (t === 'object' || t === 'function' || t === 'symbol' || t === 'bigint') return t;
    return t + ' ' + String(v);
}

type Step = (index: number, member: any, finish: () => void, res: (v: any) => void, rej: (e: any) => void, values: any[], errors: any[]) => void;

// NewPromiseCapability(C): a promise C makes, and the functions that
// settle it.
function capabilityOf(C: any): any {
    let res: any = undefined;
    let rej: any = undefined;
    const promise: any = new C((a: any, b: any): void => {
        if (res !== undefined || rej !== undefined) {
            throw new TypeError('Promise executor has already been invoked with non-undefined arguments');
        }
        res = a;
        rej = b;
    });
    if (typeof res !== 'function' || typeof rej !== 'function') {
        throw new TypeError('Promise resolve or reject function is not callable');
    }
    return { promise: promise, res: res, rej: rej };
}

// The combinator's steps once it has its capability: C.resolve read once,
// then the iterable's members resolved through it one at a time.
function run(C: any, iterable: any, subscribe: Step, settle: Settle, res: (v: any) => void, rej: (e: any) => void): void {
    let it: any;
    let next: any;
    let resolveFn: any = undefined;
    try {
        if (C !== Promise) {
            resolveFn = C.resolve;
            if (typeof resolveFn !== 'function') throw new TypeError('Promise resolve or reject function is not callable');
        }
        if (iterable === null || iterable === undefined || typeof iterable[Symbol.iterator] !== 'function') {
            throw new TypeError(describe(iterable) + ' is not iterable (cannot read property Symbol(Symbol.iterator))');
        }
        it = iterable[Symbol.iterator]();
        next = it.next;
    } catch (e) {
        rej(e);
        return;
    }
    const values: any[] = [];
    const errors: any[] = [];
    let remaining = 1;
    const finish = (): void => {
        remaining--;
        if (remaining === 0) settle(values, errors, res, rej);
    };
    let index = 0;
    for (;;) {
        let value: any;
        try {
            const step: any = next.call(it);
            if (step === null || (typeof step !== 'object' && typeof step !== 'function')) {
                throw new TypeError('Iterator result ' + String(step) + ' is not an object');
            }
            if (step.done) break;
            value = step.value;
        } catch (e) {
            rej(e); // the iterator failed: it is not closed
            return;
        }
        try {
            remaining++;
            const member: any = resolveFn === undefined ? promiseResolve(value) : resolveFn.call(C, value);
            subscribe(index, member, finish, res, rej, values, errors);
            index++;
        } catch (e) {
            try {
                const ret: any = it.return;
                if (ret !== undefined && ret !== null) ret.call(it);
            } catch (_) {
                // the throw that closed it wins
            }
            rej(e);
            return;
        }
    }
    finish();
}

function combine(iterable: any, subscribe: Step, settle: Settle, C: any, name: string): any {
    if (C === Promise) {
        return new Promise<any>((res: (v: any) => void, rej: (e: any) => void): void => {
            run(C, iterable, subscribe, settle, res, rej);
        });
    }
    // Promise.all.call(C, iterable): C's own promise and resolve.
    if (C === null || (typeof C !== 'object' && typeof C !== 'function')) {
        throw new TypeError(name + ' called on non-object');
    }
    const cap: any = capabilityOf(C);
    run(C, iterable, subscribe, settle, (v: any): void => { cap.res(v); }, (e: any): void => { cap.rej(e); });
    return cap.promise;
}

// Get(member, "then") then Call: a member's own `then` is the one called.
function invokeThen(m: any, onFulfilled: any, onRejected: any): void {
    const then: any = m.then;
    then.call(m, onFulfilled, onRejected);
}

type Settle = (values: any[], errors: any[], res: (v: any) => void, rej: (e: any) => void) => void;

const allStep: Step = (i, m, finish, _res, rej, values, _errors) => {
    values[i] = undefined;
    let called = false;
    invokeThen(m, (v: any): void => {
        if (called) return;
        called = true;
        values[i] = v;
        finish();
    }, rej);
};
const allSettledStep: Step = (i, m, finish, _res, _rej, values, _errors) => {
    values[i] = undefined;
    let called = false;
    invokeThen(m, (v: any): void => {
        if (called) return;
        called = true;
        values[i] = { status: 'fulfilled', value: v };
        finish();
    }, (e: any): void => {
        if (called) return;
        called = true;
        values[i] = { status: 'rejected', reason: e };
        finish();
    });
};
const anyStep: Step = (i, m, finish, res, _rej, _values, errors) => {
    errors[i] = undefined;
    let called = false;
    invokeThen(m, res, (e: any): void => {
        if (called) return;
        called = true;
        errors[i] = e;
        finish();
    });
};
const raceStep: Step = (_i, m, _finish, res, rej, _values, _errors) => {
    invokeThen(m, res, rej);
};
const collect: Settle = (values, _errors, res, _rej) => res(values);
const aggregate: Settle = (_values, errors, _res, rej) => rej(new AggregateError(errors, 'All promises were rejected'));
const never: Settle = (_values, _errors, _res, _rej) => {};

export function _kmlPromiseAllOf(iterable: any): Promise<any> { return combine(iterable, allStep, collect, Promise, ''); }
export function _kmlPromiseAllSettledOf(iterable: any): Promise<any> { return combine(iterable, allSettledStep, collect, Promise, ''); }
export function _kmlPromiseAnyOf(iterable: any): Promise<any> { return combine(iterable, anyStep, aggregate, Promise, ''); }
export function _kmlPromiseRaceOf(iterable: any): Promise<any> { return combine(iterable, raceStep, never, Promise, ''); }

// Promise.<combinator>.call(C, iterable): C's promise, C first as the call
// evaluates it.
export function _kmlPromiseAllOn(C: any, iterable: any): any { return combine(iterable, allStep, collect, C, 'Promise.all'); }
export function _kmlPromiseAllSettledOn(C: any, iterable: any): any { return combine(iterable, allSettledStep, collect, C, 'Promise.allSettled'); }
export function _kmlPromiseAnyOn(C: any, iterable: any): any { return combine(iterable, anyStep, aggregate, C, 'Promise.any'); }
export function _kmlPromiseRaceOn(C: any, iterable: any): any { return combine(iterable, raceStep, never, C, 'Promise.race'); }
