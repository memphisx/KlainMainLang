// Node's assert.CallTracker (lib/internal/assert/calltracker.js),
// deprecated (DEP0173): a tracked function is a Proxy of the function
// whose apply trap records each call.

import { AssertionError, inspect } from './internal_assert_assertion_error';
import { NodeTypeError, NodeRangeError } from './internal_errors';

interface TrackedCall { thisArg: any; arguments: any[]; }
interface CallReport { message: string; actual: number; expected: number; operator: string; stack: Error; }

class CallTrackerContext {
    #expected: number;
    #calls: TrackedCall[] = [];
    #name: string;
    #stackTrace: Error;
    constructor(expected: number, stackTrace: Error, name: string) {
        this.#expected = expected;
        this.#stackTrace = stackTrace;
        this.#name = name;
    }
    track(thisArg: any, args: any[]): void {
        const argsClone = Object.freeze(args.slice());
        this.#calls.push(Object.freeze({ thisArg: thisArg, arguments: argsClone }));
    }
    get delta(): number { return this.#calls.length - this.#expected; }
    reset(): void { this.#calls = []; }
    getCalls(): TrackedCall[] { return Object.freeze(this.#calls.slice()); }
    report(): CallReport | undefined {
        if (this.delta !== 0) {
            const message = 'Expected the ' + this.#name + ' function to be ' +
                'executed ' + this.#expected + ' time(s) but was ' +
                'executed ' + this.#calls.length + ' time(s).';
            return { message: message, actual: this.#calls.length, expected: this.#expected, operator: this.#name, stack: this.#stackTrace };
        }
        return undefined;
    }
}

let warned = false;

function noop(): void {}

export class CallTracker {
    #callChecks: Set<CallTrackerContext> = new Set();
    #trackedFunctions: WeakMap<object, CallTrackerContext> = new WeakMap();

    constructor() {
        if (!warned) {
            warned = true;
            process.emitWarning('assert.CallTracker is deprecated.', 'DeprecationWarning', 'DEP0173');
        }
    }

    #getTrackedFunction(tracked: any): CallTrackerContext {
        const ctx = typeof tracked === 'function' || (typeof tracked === 'object' && tracked !== null) ? this.#trackedFunctions.get(tracked) : undefined;
        if (ctx === undefined) {
            throw NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'tracked' is not a tracked function. Received " + inspect(tracked));
        }
        return ctx;
    }

    reset(tracked?: any): void {
        if (tracked === undefined) {
            this.#callChecks.forEach((check) => check.reset());
            return;
        }
        this.#getTrackedFunction(tracked).reset();
    }

    getCalls(tracked: any): TrackedCall[] {
        return this.#getTrackedFunction(tracked).getCalls();
    }

    calls(fn?: any, expected: any = 1): any {
        if (typeof fn === 'number') {
            expected = fn;
            fn = noop;
        } else if (fn === undefined) {
            fn = noop;
        }
        if (typeof expected !== 'number') {
            throw NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "expected" argument must be of type number. Received ' + inspect(expected));
        }
        if (!Number.isInteger(expected)) {
            throw NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "expected" is out of range. It must be an integer. Received ' + inspect(expected));
        }
        if (expected < 1 || expected > 4294967295) {
            throw NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "expected" is out of range. It must be >= 1 && <= 4294967295. Received ' + inspect(expected));
        }
        const context = new CallTrackerContext(expected, new Error(), (fn as Function).name || 'calls');
        const tracked = new Proxy(fn, {
            apply(target: any, thisArg: any, argList: any[]): any {
                context.track(thisArg, argList);
                return Reflect.apply(target, thisArg, argList);
            },
        });
        this.#callChecks.add(context);
        this.#trackedFunctions.set(tracked, context);
        return tracked;
    }

    report(): CallReport[] {
        const errors: CallReport[] = [];
        for (const context of this.#callChecks) {
            const message = context.report();
            if (message !== undefined) {
                errors.push(message);
            }
        }
        return errors;
    }

    verify(): void {
        const errors = this.report();
        if (errors.length === 0) {
            return;
        }
        const message = errors.length === 1 ? errors[0].message : 'Functions were not called the expected number of times';
        throw new AssertionError({ message: message });
    }
}
