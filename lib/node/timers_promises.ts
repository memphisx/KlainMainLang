// Node's `timers/promises`, ported from Node v24's lib/timers/promises.js:
// promise forms of setTimeout and setImmediate, setInterval as an async
// iterator, and the scheduler. An AbortSignal cancels a pending timer and
// rejects with an AbortError; `ref: false` lets the process exit while the
// timer is pending.
//
// kml:default-namespace — `import timers from 'timers/promises'` reads this
// module's exports, as Node's default export carries them.

import { startTimeout, stopTimeout, startInterval, stopInterval, startImmediate, stopImmediate } from './internal_timers';
import { NodeTypeError } from './internal_errors';

class AbortError extends Error {
    code: string = 'ABORT_ERR';
    cause: any;
    constructor(cause: any) {
        super('The operation was aborted');
        this.name = 'AbortError';
        this.cause = cause;
    }
}

function received(value: any): string {
    if (value === null || value === undefined) return ' Received ' + String(value);
    if (typeof value === 'function') return ' Received function';
    if (typeof value === 'object') return ' Received an instance of Object';
    let shown = String(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    if (typeof value === 'string') shown = "'" + shown + "'";
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

export interface TimerOptions {
    ref?: boolean | undefined;
    signal?: AbortSignal | undefined;
}

// The options as Node validates them: an object, `signal` an AbortSignal,
// `ref` a boolean.
function readOptions(options: any): { signal: AbortSignal | undefined; ref: boolean } {
    if (options === null || typeof options !== 'object') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options" argument must be of type object.' + received(options));
    }
    const signal: any = options.signal;
    if (signal !== undefined && !(signal instanceof AbortSignal)) {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options.signal" property must be an instance of AbortSignal.' + received(signal));
    }
    const ref: any = options.ref === undefined ? true : options.ref;
    if (typeof ref !== 'boolean') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options.ref" property must be of type boolean.' + received(ref));
    }
    return { signal: signal as AbortSignal | undefined, ref: ref as boolean };
}

export function setTimeout<T = void>(delay?: number, value?: T, options?: TimerOptions): Promise<T>;
export function setTimeout<T = void>(delay?: number, value?: T, options: any = {}): Promise<T> {
    let opts: { signal: AbortSignal | undefined; ref: boolean };
    try {
        opts = readOptions(options);
    } catch (err) {
        return Promise.reject(err);
    }
    const signal = opts.signal;
    if (signal !== undefined && signal.aborted) return Promise.reject(new AbortError(signal.reason));
    let onAbort: (() => void) | undefined = undefined;
    const ret = new Promise<T>((resolve, reject) => {
        const timer = startTimeout(() => resolve(value as T), delay === undefined ? 1 : delay);
        if (!opts.ref) timer.unref();
        if (signal !== undefined) {
            onAbort = () => {
                stopTimeout(timer);
                reject(new AbortError(signal.reason));
            };
            signal.addEventListener('abort', onAbort);
        }
    });
    if (signal === undefined) return ret;
    return ret.finally(() => {
        if (onAbort !== undefined) signal.removeEventListener('abort', onAbort);
    });
}

export function setImmediate<T = void>(value?: T, options?: TimerOptions): Promise<T>;
export function setImmediate<T = void>(value?: T, options: any = {}): Promise<T> {
    let opts: { signal: AbortSignal | undefined; ref: boolean };
    try {
        opts = readOptions(options);
    } catch (err) {
        return Promise.reject(err);
    }
    const signal = opts.signal;
    if (signal !== undefined && signal.aborted) return Promise.reject(new AbortError(signal.reason));
    let onAbort: (() => void) | undefined = undefined;
    const ret = new Promise<T>((resolve, reject) => {
        const immediate = startImmediate(() => resolve(value as T));
        if (!opts.ref) immediate.unref();
        if (signal !== undefined) {
            onAbort = () => {
                stopImmediate(immediate);
                reject(new AbortError(signal.reason));
            };
            signal.addEventListener('abort', onAbort);
        }
    });
    if (signal === undefined) return ret;
    return ret.finally(() => {
        if (onAbort !== undefined) signal.removeEventListener('abort', onAbort);
    });
}

export function setInterval<T = void>(delay?: number, value?: T, options?: TimerOptions): AsyncGenerator<T>;
export function setInterval<T = void>(delay?: number, value?: T, options: any = {}): AsyncGenerator<T> {
    return intervalIterator(delay, value, options) as AsyncGenerator<T>;
}

// The body of setInterval: an async generator, so a bad option rejects the
// first next() rather than throwing from the call, as in Node.
async function* intervalIterator(delay: number | undefined, value: any, options: any): AsyncGenerator<any> {
    const opts = readOptions(options);
    const signal = opts.signal;
    if (signal !== undefined && signal.aborted) throw new AbortError(signal.reason);
    let onAbort: (() => void) | undefined = undefined;
    let interval: NodeJS.Timeout | undefined = undefined;
    try {
        let notYielded = 0;
        let wake: ((v: any) => void) | undefined = undefined;
        interval = startInterval(() => {
            notYielded++;
            if (wake !== undefined) {
                const w = wake;
                wake = undefined;
                w(undefined);
            }
        }, delay === undefined ? 1 : delay);
        if (!opts.ref) interval.unref();
        if (signal !== undefined) {
            onAbort = () => {
                if (interval !== undefined) stopInterval(interval);
                if (wake !== undefined) {
                    const w = wake;
                    wake = undefined;
                    w(Promise.reject(new AbortError(signal.reason)));
                }
            };
            signal.addEventListener('abort', onAbort);
        }
        while (signal === undefined || !signal.aborted) {
            if (notYielded === 0) {
                await new Promise<any>((resolve) => { wake = resolve; });
            }
            for (; notYielded > 0; notYielded--) {
                yield value;
            }
        }
        throw new AbortError(signal !== undefined ? signal.reason : undefined);
    } finally {
        if (interval !== undefined) stopInterval(interval);
        if (signal !== undefined && onAbort !== undefined) signal.removeEventListener('abort', onAbort);
    }
}

export class Scheduler {
    yield(): Promise<void> {
        return setImmediate<void>();
    }

    wait(delay: number, options?: TimerOptions): Promise<void> {
        return setTimeout<void>(delay, undefined, options === undefined ? {} : options);
    }
}

export const scheduler = new Scheduler();
