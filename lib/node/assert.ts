// Node's `assert` (lib/assert.js and lib/internal/assert/utils.js).
//
// The default export is `ok`, carrying every assertion as a property, as
// Node's module.exports is. assert.ok's generated message quotes the call's
// source: the compiler passes a call of `ok` (or `assert`, `strict`) its
// source text as a hidden last argument.

import { AssertionError, inspect } from './internal_assert_assertion_error';
import { isDeepEqual, isDeepStrictEqual, isPartialStrictEqual } from './internal_util_comparisons';

export { AssertionError } from './internal_assert_assertion_error';

// A TypeError carrying Node's error code.
class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

// The TypeError message Node's ERR_INVALID_ARG_TYPE formats for a value.
function receivedDescription(value: any): string {
    if (value === null || value === undefined) {
        return ' Received ' + String(value);
    }
    if (typeof value === 'function') {
        return ' Received function ' + (value as Function).name;
    }
    if (typeof value === 'object') {
        return ' Received ' + inspect(value);
    }
    let shown = inspect(value);
    if (shown.length > 28) {
        shown = shown.slice(0, 25) + '...';
    }
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

const kCallSite = '\u0000kml:callsite\u0000';

// The source text a call of ok carries, or undefined.
function callSite(args: any[]): string | undefined {
    if (args.length > 0) {
        const last = args[args.length - 1];
        if (typeof last === 'string' && last.startsWith(kCallSite)) {
            args.pop();
            return last.slice(kCallSite.length);
        }
    }
    return undefined;
}

// Escape control characters but not \n and \t to keep the line breaks and
// indentation intact.
function escapeControl(source: string): string {
    let out = '';
    for (let i = 0; i < source.length; i++) {
        const c = source.charCodeAt(i);
        if (c < 0x20 && c !== 0x09 && c !== 0x0a && c !== 0x0d) {
            if (c === 0x08) out += '\\b';
            else if (c === 0x0c) out += '\\f';
            else out += '\\u' + c.toString(16).padStart(4, '0');
        } else {
            out += source[i];
        }
    }
    return out;
}

function getErrMessage(source: string | undefined): string | undefined {
    if (source === undefined || source === '') {
        return undefined;
    }
    const nl = source.indexOf('\n');
    const line = nl === -1 ? source : source.slice(0, nl + 1);
    return `The expression evaluated to a falsy value:\n\n  ${escapeControl(line)}${nl === -1 ? '\n' : ''}`;
}

function innerOk(argLen: number, value: any, messageArg: any, source: string | undefined): void {
    if (!value) {
        let generatedMessage = false;
        let message: any = messageArg;

        if (argLen === 0) {
            generatedMessage = true;
            message = 'No value argument passed to `assert.ok()`';
        } else if (message === null || message === undefined) {
            generatedMessage = true;
            message = getErrMessage(source);
        } else if (message instanceof Error) {
            throw message;
        }

        const err = new AssertionError({
            actual: value,
            expected: true,
            message,
            operator: '==',
        });
        err.generatedMessage = generatedMessage;
        throw err;
    }
}

// The options of the Assert instance whose method is running (Node reads
// them off `this`): the diff mode and whether deep equality skips
// prototypes.
let activeDiff: 'simple' | 'full' = 'simple';
let activeSkipPrototype = false;

function innerFail(actual: any, expected: any, message: any, operator: string): never {
    if (message instanceof Error) throw message;
    throw new AssertionError({ actual, expected, message, operator, diff: activeDiff });
}

let warned = false;

/**
 * Throws an AssertionError with the given message (or 'Failed').
 */
export function fail(message?: string | Error): never;
export function fail(actual: unknown, expected: unknown, message?: string | Error, operator?: string, stackStartFn?: Function): never;
export function fail(...args: any[]): never {
    const argsLen = args.length;
    let actual: any = args[0];
    const expected: any = args[1];
    let message: any = args[2];
    let operator: any = args[3];

    let internalMessage = false;
    if ((actual === null || actual === undefined) && argsLen <= 1) {
        internalMessage = true;
        message = 'Failed';
    } else if (argsLen === 1) {
        message = actual;
        actual = undefined;
    } else {
        if (warned === false) {
            warned = true;
            process.emitWarning(
                'assert.fail() with more than one argument is deprecated. ' +
                'Please use assert.strictEqual() instead or only pass a message.',
                'DeprecationWarning',
                'DEP0094',
            );
        }
        if (argsLen === 2) {
            operator = '!=';
        }
    }

    if (message instanceof Error) throw message;

    const err = new AssertionError({
        actual,
        expected,
        operator: operator === undefined ? 'fail' : operator,
        message,
        diff: activeDiff,
    });
    if (internalMessage) {
        err.generatedMessage = true;
    }
    throw err;
}

/**
 * Tests whether value is truthy, as determined by !!value.
 */
export function ok(value: unknown, message?: string | Error): asserts value;
export function ok(...args: any[]): void {
    const source = callSite(args);
    innerOk(args.length, args[0], args[1], source);
}

/**
 * The equality assertion tests shallow, coercive equality with ==.
 */
export function equal(actual: unknown, expected: unknown, message?: string | Error): void {
    const a: any = actual;
    const b: any = expected;
    if (a != b && (!Number.isNaN(a) || !Number.isNaN(b))) {
        innerFail(actual, expected, message, '==');
    }
}

/**
 * The non-equality assertion tests for whether two objects are not equal
 * with !=.
 */
export function notEqual(actual: unknown, expected: unknown, message?: string | Error): void {
    const a: any = actual;
    const b: any = expected;
    if (a == b || (Number.isNaN(a) && Number.isNaN(b))) {
        innerFail(actual, expected, message, '!=');
    }
}

/**
 * The deep equivalence assertion tests a deep equality relation.
 */
export function deepEqual(actual: unknown, expected: unknown, message?: string | Error): void {
    if (!isDeepEqual(actual, expected)) {
        innerFail(actual, expected, message, 'deepEqual');
    }
}

/**
 * The deep non-equivalence assertion tests for any deep inequality.
 */
export function notDeepEqual(actual: unknown, expected: unknown, message?: string | Error): void {
    if (isDeepEqual(actual, expected)) {
        innerFail(actual, expected, message, 'notDeepEqual');
    }
}

/**
 * The deep strict equivalence assertion tests a deep strict equality
 * relation.
 */
export function deepStrictEqual<T>(actual: unknown, expected: T, message?: string | Error): asserts actual is T;
export function deepStrictEqual(actual: unknown, expected: unknown, message?: string | Error): void {
    if (!isDeepStrictEqual(actual, expected, activeSkipPrototype)) {
        innerFail(actual, expected, message, 'deepStrictEqual');
    }
}

/**
 * The deep strict non-equivalence assertion tests for any deep strict
 * inequality.
 */
export function notDeepStrictEqual(actual: unknown, expected: unknown, message?: string | Error): void {
    if (isDeepStrictEqual(actual, expected, activeSkipPrototype)) {
        innerFail(actual, expected, message, 'notDeepStrictEqual');
    }
}

/**
 * The strict equivalence assertion tests a strict equality relation.
 */
export function strictEqual<T>(actual: unknown, expected: T, message?: string | Error): asserts actual is T;
export function strictEqual(actual: unknown, expected: unknown, message?: string | Error): void {
    if (!Object.is(actual, expected)) {
        innerFail(actual, expected, message, 'strictEqual');
    }
}

/**
 * The strict non-equivalence assertion tests for any strict inequality.
 */
export function notStrictEqual(actual: unknown, expected: unknown, message?: string | Error): void {
    if (Object.is(actual, expected)) {
        innerFail(actual, expected, message, 'notStrictEqual');
    }
}

/**
 * The strict equivalence assertion test between two objects, `expected`
 * being a subset of `actual`.
 */
export function partialDeepStrictEqual(actual: unknown, expected: unknown, message?: string | Error): void {
    if (!isPartialStrictEqual(actual, expected)) {
        innerFail(actual, expected, message, 'partialDeepStrictEqual');
    }
}

// The constructor kinds of __kml_native.ctorKind.
const kNotConstructor = 0;
const kErrorConstructor = 2;

function isRegExp(v: any): boolean {
    return v instanceof RegExp;
}

// A placeholder of an error's compared keys, for the diff.
class Comparison {}

function compareExceptionKey(actual: any, expected: any, key: string, message: any, keys: string[], fnName: string): void {
    if (!(key in actual) || !isDeepStrictEqual(actual[key], expected[key])) {
        if (!message) {
            // Create placeholder objects to create a nice output.
            const a: any = new Comparison();
            const b: any = new Comparison();
            for (const k of keys) {
                if (k in actual) {
                    a[k] = actual[k];
                }
                if (k in expected) {
                    if (typeof actual[k] === 'string' && isRegExp(expected[k]) && expected[k].exec(actual[k]) !== null) {
                        b[k] = actual[k];
                    } else {
                        b[k] = expected[k];
                    }
                }
            }
            const err = new AssertionError({ actual: a, expected: b, operator: 'deepStrictEqual', diff: activeDiff });
            err.actual = actual;
            err.expected = expected;
            err.operator = fnName;
            throw err;
        }
        innerFail(actual, expected, message, fnName);
    }
}

function expectedException(actual: any, expected: any, messageArg: any, fnName: string): void {
    let message: any = messageArg;
    let generatedMessage = false;
    let throwError = false;

    if (typeof expected !== 'function') {
        // Handle regular expressions.
        if (isRegExp(expected)) {
            const str = String(actual);
            if (expected.exec(str) !== null) {
                return;
            }
            if (!message) {
                generatedMessage = true;
                message = 'The input did not match the regular expression ' +
                    `${inspect(expected)}. Input:\n\n${inspect(str)}\n`;
            }
            throwError = true;
            // Handle primitives properly.
        } else if (typeof actual !== 'object' || actual === null) {
            const err = new AssertionError({ actual, expected, message, operator: 'deepStrictEqual', diff: activeDiff });
            err.operator = fnName;
            throw err;
        } else {
            // Handle validation objects.
            const keys = Object.keys(expected);
            // Special handle errors to make sure the name and the message are
            // compared as well.
            if (expected instanceof Error) {
                keys.push('name', 'message');
            } else if (keys.length === 0) {
                throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'error' may not be an empty object. Received {}");
            }
            for (const key of keys) {
                if (typeof actual[key] === 'string' && isRegExp(expected[key]) && expected[key].exec(actual[key]) !== null) {
                    continue;
                }
                compareExceptionKey(actual, expected, key, message, keys, fnName);
            }
            return;
        }
    } else if (__kml_native.ctorKind(expected) !== kNotConstructor && actual instanceof expected) {
        // Check for matching Error classes.
        return;
    } else if (__kml_native.ctorKind(expected) === kErrorConstructor) {
        if (!message) {
            generatedMessage = true;
            message = 'The error is expected to be an instance of ' +
                `"${expected.name}". Received `;
            if (actual instanceof Error) {
                const name = actual.name;
                if (expected.name === name) {
                    message += 'an error with identical name but a different prototype.';
                } else {
                    message += `"${name}"`;
                }
                if (actual.message) {
                    message += `\n\nError message:\n\n${actual.message}`;
                }
            } else {
                message += `"${__kml_native.inspectWith(actual, -1, 3, 0, 80, 100)}"`;
            }
        }
        throwError = true;
    } else {
        // Check validation functions return value.
        const res = expected(actual);
        if (res !== true) {
            if (!message) {
                generatedMessage = true;
                const name = expected.name ? `"${expected.name}" ` : '';
                message = `The ${name}validation function is expected to return` +
                    ` "true". Received ${inspect(res)}`;
                if (actual instanceof Error) {
                    message += `\n\nCaught error:\n\n${actual}`;
                }
            }
            throwError = true;
        }
    }

    if (throwError) {
        const err = new AssertionError({ actual, expected, message, operator: fnName, diff: activeDiff });
        err.generatedMessage = generatedMessage;
        throw err;
    }
}

class NoException {}
const NO_EXCEPTION_SENTINEL: any = new NoException();

function getActual(fn: any): any {
    if (typeof fn !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "fn" argument must be of type function.' + receivedDescription(fn));
    }
    try {
        fn();
    } catch (e) {
        return e;
    }
    return NO_EXCEPTION_SENTINEL;
}

function checkIsPromise(obj: any): boolean {
    // Accept native ES6 promises and promises that are implemented in a similar
    // way. Do not accept thenables that use a function as `obj` and that have no
    // `catch` handler.
    return obj instanceof Promise ||
        (obj !== null && typeof obj === 'object' && typeof obj.then === 'function' && typeof obj.catch === 'function');
}

async function waitForActual(promiseFn: any): Promise<any> {
    let resultPromise: any;
    if (typeof promiseFn === 'function') {
        // Return a rejected promise if `promiseFn` throws synchronously.
        resultPromise = promiseFn();
        // Fail in case no promise is returned.
        if (!checkIsPromise(resultPromise)) {
            throw new NodeTypeError('ERR_INVALID_RETURN_VALUE', 'Expected instance of Promise to be returned from the "promiseFn" function but got ' +
                (resultPromise === null ? 'null' : typeof resultPromise === 'object' ? 'instance of ' + String(resultPromise) : 'type ' + typeof resultPromise) + '.');
        }
    } else if (checkIsPromise(promiseFn)) {
        resultPromise = promiseFn;
    } else {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "promiseFn" argument must be of type function or an instance of Promise.' + receivedDescription(promiseFn));
    }

    try {
        await resultPromise;
    } catch (e) {
        return e;
    }
    return NO_EXCEPTION_SENTINEL;
}

// argc is how many arguments followed the function: an error and a message
// both given make a string error ambiguous.
function expectsError(fnName: string, actual: any, argc: number, errorArg: any, messageArg: any): void {
    let error: any = errorArg;
    let message: any = messageArg;
    if (typeof error === 'string') {
        if (argc === 2) {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "error" argument must be of type function or an instance of Error, RegExp, or Object.' + receivedDescription(error));
        }
        if (typeof actual === 'object' && actual !== null) {
            if (actual.message === error) {
                throw new NodeTypeError('ERR_AMBIGUOUS_ARGUMENT', `The "error/message" argument is ambiguous. The error message "${actual.message}" is identical to the message.`);
            }
        } else if (actual === error) {
            throw new NodeTypeError('ERR_AMBIGUOUS_ARGUMENT', `The "error/message" argument is ambiguous. The error "${actual}" is identical to the message.`);
        }
        message = error;
        error = undefined;
    } else if (error !== null && error !== undefined && typeof error !== 'object' && typeof error !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "error" argument must be of type function or an instance of Error, RegExp, or Object.' + receivedDescription(error));
    }

    if (actual === NO_EXCEPTION_SENTINEL) {
        let details = '';
        if (error !== null && error !== undefined && error.name) {
            details += ` (${error.name})`;
        }
        details += message ? `: ${message}` : '.';
        const fnType = fnName === 'rejects' ? 'rejection' : 'exception';
        innerFail(undefined, error, `Missing expected ${fnType}${details}`, fnName);
    }

    if (!error) {
        return;
    }

    expectedException(actual, error, message, fnName);
}

function hasMatchingError(actual: any, expected: any): boolean {
    if (typeof expected !== 'function') {
        if (isRegExp(expected)) {
            const str = String(actual);
            return expected.exec(str) !== null;
        }
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "expected" argument must be of type function or an instance of RegExp.' + receivedDescription(expected));
    }
    // Guard instanceof against arrow functions as they don't have a prototype.
    if (__kml_native.ctorKind(expected) !== kNotConstructor && actual instanceof expected) {
        return true;
    }
    if (__kml_native.ctorKind(expected) === kErrorConstructor) {
        return false;
    }
    return expected(actual) === true;
}

function expectsNoError(fnName: string, actual: any, errorArg: any, messageArg: any): void {
    if (actual === NO_EXCEPTION_SENTINEL) {
        return;
    }
    let error: any = errorArg;
    let message: any = messageArg;
    if (typeof error === 'string') {
        message = error;
        error = undefined;
    }

    if (!error || hasMatchingError(actual, error)) {
        const details = message ? `: ${message}` : '.';
        const fnType = fnName === 'doesNotReject' ? 'rejection' : 'exception';
        const actualMessage = actual === null || actual === undefined ? undefined : actual.message;
        innerFail(actual, error, `Got unwanted ${fnType}${details}\n` + `Actual message: "${actualMessage}"`, fnName);
    }
    throw actual;
}

export type AssertPredicate = RegExp | (new () => object) | ((thrown: unknown) => boolean) | object | Error;

/**
 * Expects the function `block` to throw an error.
 */
export function throws(block: () => unknown, message?: string | Error): void;
export function throws(block: () => unknown, error: AssertPredicate, message?: string | Error): void;
export function throws(block: () => unknown, ...args: any[]): void {
    expectsError('throws', getActual(block), args.length, args[0], args[1]);
}

/**
 * Expects `block` function or its value to reject.
 */
export function rejects(block: (() => Promise<unknown>) | Promise<unknown>, message?: string | Error): Promise<void>;
export function rejects(block: (() => Promise<unknown>) | Promise<unknown>, error: AssertPredicate, message?: string | Error): Promise<void>;
export async function rejects(block: any, ...args: any[]): Promise<void> {
    expectsError('rejects', await waitForActual(block), args.length, args[0], args[1]);
}

/**
 * Asserts that the function `block` does not throw an error.
 */
export function doesNotThrow(block: () => unknown, message?: string | Error): void;
export function doesNotThrow(block: () => unknown, error: AssertPredicate, message?: string | Error): void;
export function doesNotThrow(block: () => unknown, ...args: any[]): void {
    expectsNoError('doesNotThrow', getActual(block), args[0], args[1]);
}

/**
 * Expects `block` or its value to not reject.
 */
export function doesNotReject(block: (() => Promise<unknown>) | Promise<unknown>, message?: string | Error): Promise<void>;
export function doesNotReject(block: (() => Promise<unknown>) | Promise<unknown>, error: AssertPredicate, message?: string | Error): Promise<void>;
export async function doesNotReject(block: any, ...args: any[]): Promise<void> {
    expectsNoError('doesNotReject', await waitForActual(block), args[0], args[1]);
}

/**
 * Throws `AssertionError` if the value is not `null` or `undefined`.
 */
export function ifError(value: unknown): asserts value is null | undefined {
    const err: any = value;
    if (err !== null && err !== undefined) {
        let message = 'ifError got unwanted exception: ';
        if (typeof err === 'object' && typeof err.message === 'string') {
            if (err.message.length === 0 && err instanceof Error) {
                message += err.name;
            } else {
                message += err.message;
            }
        } else {
            message += inspect(err);
        }

        throw new AssertionError({ actual: err, expected: null, operator: 'ifError', message });
    }
}

function internalMatch(string: any, regexp: any, messageArg: any, fnName: string): void {
    if (!isRegExp(regexp)) {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "regexp" argument must be an instance of RegExp.' + receivedDescription(regexp));
    }
    const match = fnName === 'match';
    if (typeof string !== 'string' || (regexp.exec(string) !== null) !== match) {
        if (messageArg instanceof Error) {
            throw messageArg;
        }

        const generatedMessage = !messageArg;

        const message: any = messageArg || (typeof string !== 'string' ?
            'The "string" argument must be of type string. Received type ' +
            `${typeof string} (${inspect(string)})` :
            (match ?
                'The input did not match the regular expression ' :
                'The input was expected to not match the regular expression ') +
            `${inspect(regexp)}. Input:\n\n${inspect(string)}\n`);
        const err = new AssertionError({ actual: string, expected: regexp, message, operator: fnName, diff: activeDiff });
        err.generatedMessage = generatedMessage;
        throw err;
    }
}

/**
 * Expects the `string` input to match the regular expression.
 */
export function match(value: string, regExp: RegExp, message?: string | Error): void {
    internalMatch(value, regExp, message, 'match');
}

/**
 * Expects the `string` input not to match the regular expression.
 */
export function doesNotMatch(value: string, regExp: RegExp, message?: string | Error): void {
    internalMatch(value, regExp, message, 'doesNotMatch');
}

export interface AssertOptions {
    diff?: 'simple' | 'full' | undefined;
    strict?: boolean | undefined;
    skipPrototype?: boolean | undefined;
}

/**
 * An assert whose options apply to its methods: `diff: 'full'` shows the
 * whole diff, `strict` (the default) makes `equal` `strictEqual` and so on,
 * `skipPrototype` compares objects without their prototypes.
 */
export class Assert {
    AssertionError: any = AssertionError;
    readonly diff: 'simple' | 'full';
    readonly strict: boolean;
    readonly skipPrototype: boolean;

    constructor(options?: AssertOptions) {
        const o: any = options === undefined ? {} : options;
        if (o.diff !== undefined && o.diff !== 'simple' && o.diff !== 'full') {
            throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The property 'options.diff' must be one of: 'simple', 'full'. Received " + inspect(o.diff));
        }
        this.diff = o.diff === undefined ? 'simple' : o.diff;
        this.strict = o.strict === undefined ? true : !!o.strict;
        this.skipPrototype = !!o.skipPrototype;
    }

    // run calls f with this instance's options active.
    private run(f: () => void): void {
        const d = activeDiff;
        const sp = activeSkipPrototype;
        activeDiff = this.diff;
        activeSkipPrototype = this.skipPrototype;
        try {
            f();
        } finally {
            activeDiff = d;
            activeSkipPrototype = sp;
        }
    }

    /** @callsite */
    ok(value: unknown, message?: string | Error): asserts value;
    ok(...args: any[]): void {
        const source = callSite(args);
        this.run(() => innerOk(args.length, args[0], args[1], source));
    }

    fail(message?: string | Error): never {
        this.run(() => fail(message));
        throw new Error('unreachable');
    }

    equal(actual: unknown, expected: unknown, message?: string | Error): void {
        this.run(() => {
            if (this.strict) {
                strictEqual(actual, expected, message);
            } else {
                equal(actual, expected, message);
            }
        });
    }

    notEqual(actual: unknown, expected: unknown, message?: string | Error): void {
        this.run(() => {
            if (this.strict) {
                notStrictEqual(actual, expected, message);
            } else {
                notEqual(actual, expected, message);
            }
        });
    }

    deepEqual(actual: unknown, expected: unknown, message?: string | Error): void {
        this.run(() => {
            if (this.strict) {
                deepStrictEqual(actual, expected, message);
            } else {
                deepEqual(actual, expected, message);
            }
        });
    }

    notDeepEqual(actual: unknown, expected: unknown, message?: string | Error): void {
        this.run(() => {
            if (this.strict) {
                notDeepStrictEqual(actual, expected, message);
            } else {
                notDeepEqual(actual, expected, message);
            }
        });
    }

    deepStrictEqual(actual: unknown, expected: unknown, message?: string | Error): void {
        this.run(() => deepStrictEqual(actual, expected, message));
    }

    notDeepStrictEqual(actual: unknown, expected: unknown, message?: string | Error): void {
        this.run(() => notDeepStrictEqual(actual, expected, message));
    }

    strictEqual(actual: unknown, expected: unknown, message?: string | Error): void {
        this.run(() => strictEqual(actual, expected, message));
    }

    notStrictEqual(actual: unknown, expected: unknown, message?: string | Error): void {
        this.run(() => notStrictEqual(actual, expected, message));
    }

    partialDeepStrictEqual(actual: unknown, expected: unknown, message?: string | Error): void {
        this.run(() => partialDeepStrictEqual(actual, expected, message));
    }

    match(value: string, regExp: RegExp, message?: string | Error): void {
        this.run(() => match(value, regExp, message));
    }

    doesNotMatch(value: string, regExp: RegExp, message?: string | Error): void {
        this.run(() => doesNotMatch(value, regExp, message));
    }

    throws(block: () => unknown, error?: any, message?: string | Error): void {
        this.run(() => {
            if (error === undefined) {
                throws(block);
            } else {
                throws(block, error, message);
            }
        });
    }

    doesNotThrow(block: () => unknown, error?: any, message?: string | Error): void {
        this.run(() => {
            if (error === undefined) {
                doesNotThrow(block);
            } else {
                doesNotThrow(block, error, message);
            }
        });
    }

    ifError(value: unknown): void {
        this.run(() => ifError(value));
    }
}

/**
 * A strict-only variant of assert: `equal` is `strictEqual`, and so on.
 */
export function strict(value: unknown, message?: string | Error): asserts value;
export function strict(...args: any[]): void {
    const source = callSite(args);
    innerOk(args.length, args[0], args[1], source);
}

strict.ok = strict;
strict.fail = fail;
strict.equal = strictEqual;
strict.notEqual = notStrictEqual;
strict.deepEqual = deepStrictEqual;
strict.notDeepEqual = notDeepStrictEqual;
strict.deepStrictEqual = deepStrictEqual;
strict.notDeepStrictEqual = notDeepStrictEqual;
strict.strictEqual = strictEqual;
strict.notStrictEqual = notStrictEqual;
strict.partialDeepStrictEqual = partialDeepStrictEqual;
strict.match = match;
strict.doesNotMatch = doesNotMatch;
strict.throws = throws;
strict.rejects = rejects;
strict.doesNotThrow = doesNotThrow;
strict.doesNotReject = doesNotReject;
strict.ifError = ifError;
strict.AssertionError = AssertionError;
strict.strict = strict;
strict.Assert = Assert;

ok.ok = ok;
ok.fail = fail;
ok.equal = equal;
ok.notEqual = notEqual;
ok.deepEqual = deepEqual;
ok.notDeepEqual = notDeepEqual;
ok.deepStrictEqual = deepStrictEqual;
ok.notDeepStrictEqual = notDeepStrictEqual;
ok.strictEqual = strictEqual;
ok.notStrictEqual = notStrictEqual;
ok.partialDeepStrictEqual = partialDeepStrictEqual;
ok.match = match;
ok.doesNotMatch = doesNotMatch;
ok.throws = throws;
ok.rejects = rejects;
ok.doesNotThrow = doesNotThrow;
ok.doesNotReject = doesNotReject;
ok.ifError = ifError;
ok.AssertionError = AssertionError;
ok.strict = strict;
ok.Assert = Assert;

export default ok;
