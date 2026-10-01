// The bare `test` specifier: Node's own test-suite helpers (Node's
// test/common/index.js — mustCall and the rest, which Node's tests import),
// alongside the node:test runner's functions. Node has no bare `test`
// module; this compiler's copies of Node's tests import this one.
//
// kml:default-namespace — `import common from 'test'` reads this module's
// exports.

import assert from 'assert';
import { inspect } from './internal_util_inspect';
import * as runner from './test';

export const test = runner.test;
export const it = runner.it;
export const describe = runner.describe;
export const suite = runner.suite;
export const before = runner.before;
export const after = runner.after;
export const beforeEach = runner.beforeEach;
export const afterEach = runner.afterEach;

export const isWindows = process.platform === 'win32';
export const isLinux = process.platform === 'linux';
export const isMacOS = process.platform === 'darwin';
export const hasCrypto = true;
export const hasIntl = false;
export const isMainThread = true;

class CallCheck {
    name: string;
    exact = -1;
    minimum = -1;
    actual = 0;

    constructor(name: string) {
        this.name = name;
    }
}

const callChecks: CallCheck[] = [];
let checksHooked = false;

// runCallChecks: after a clean run, every mustCall wrapper must have been
// called as often as it expects.
function runCallChecks(exitCode: number): void {
    if (exitCode !== 0) return;
    let failed = false;
    for (const c of callChecks) {
        const segment = c.minimum >= 0 ? 'at least ' + String(c.minimum) : 'exactly ' + String(c.exact);
        const bad = c.minimum >= 0 ? c.actual < c.minimum : c.actual !== c.exact;
        if (bad) {
            failed = true;
            console.log('Mismatched ' + c.name + ' function calls. Expected ' + segment + ', actual ' + String(c.actual) + '.');
        }
    }
    if (failed) process.exit(1);
}

function track(c: CallCheck): void {
    callChecks.push(c);
    if (!checksHooked) {
        checksHooked = true;
        process.on('exit', runCallChecks);
    }
}

function noop(): void {}

function wrapCounted(fn: any, check: CallCheck): any {
    track(check);
    const f: any = fn;
    const wrapped = (...args: any[]): any => {
        check.actual++;
        return f(...args);
    };
    return wrapped;
}

function functionName(fn: any): string {
    const name = typeof fn === 'function' ? (fn as Function).name : '';
    return name === '' ? '<anonymous>' : name;
}

export function mustCall(exact?: number): () => void;
export function mustCall<T extends (...args: any[]) => any>(fn: T, exact?: number): T;
export function mustCall(fn?: any, exact?: number): any {
    let body: any = fn;
    let n = exact === undefined ? 1 : exact;
    if (typeof fn === 'number') {
        n = fn as number;
        body = noop;
    } else if (fn === undefined) {
        body = noop;
    }
    const check = new CallCheck(functionName(body));
    check.exact = n;
    return wrapCounted(body, check);
}

export function mustCallAtLeast(minimum?: number): () => void;
export function mustCallAtLeast<T extends (...args: any[]) => any>(fn: T, minimum?: number): T;
export function mustCallAtLeast(fn?: any, minimum?: number): any {
    let body: any = fn;
    let n = minimum === undefined ? 1 : minimum;
    if (typeof fn === 'number') {
        n = fn as number;
        body = noop;
    } else if (fn === undefined) {
        body = noop;
    }
    const check = new CallCheck(functionName(body));
    check.minimum = n;
    return wrapCounted(body, check);
}

export function mustSucceed(fn?: (...args: any[]) => any, exact?: number): (...args: any[]) => any {
    const body = (err: any, ...args: any[]): any => {
        assert.ifError(err);
        if (fn !== undefined) return fn(...args);
        return undefined;
    };
    const check = new CallCheck(functionName(fn === undefined ? body : fn));
    check.exact = exact === undefined ? 1 : exact;
    return wrapCounted(body, check);
}

export function mustNotCall(msg?: string): (...args: any[]) => any {
    return (...args: any[]): any => {
        const argsInfo = args.length > 0 ? '\ncalled with arguments: ' + args.map((a: any) => inspect(a)).join(', ') : '';
        assert.fail((msg === undefined ? 'function should not have been called' : msg) + argsInfo);
    };
}

export function expectsError(validator?: any, exact?: number): (...args: any[]) => any {
    const body = (...args: any[]): any => {
        if (args.length !== 1) assert.fail('Expected one argument, got ' + inspect(args));
        const error = args[0];
        if (validator !== undefined) {
            assert.throws(() => { throw error; }, validator);
        }
        return true;
    };
    const check = new CallCheck('expectsError');
    check.exact = exact === undefined ? 1 : exact;
    return wrapCounted(body, check);
}

// expectWarning(name, message[, code]) or expectWarning({ name: message | [message, code] }):
// the warnings the program must emit, checked as they arrive.
const expectedWarnings: any[] = [];
let warningsHooked = false;

export function expectWarning(nameOrMap: any, message?: any, code?: any): void {
    if (typeof nameOrMap === 'string') {
        expectedWarnings.push({ name: nameOrMap, message: message, code: code });
    } else if (typeof nameOrMap === 'object' && nameOrMap !== null) {
        for (const name of Object.keys(nameOrMap)) {
            const v: any = nameOrMap[name];
            if (Array.isArray(v)) expectedWarnings.push({ name: name, message: v[0], code: v[1] });
            else expectedWarnings.push({ name: name, message: v, code: undefined });
        }
    }
    if (!warningsHooked) {
        warningsHooked = true;
        process.on('warning', (warning: any) => {
            const next: any = expectedWarnings.shift();
            if (next === undefined) assert.fail('Unexpected extra warning received: ' + String(warning));
            assert.strictEqual(warning.name, next.name);
            if (next.message !== undefined) assert.strictEqual(warning.message, next.message);
            if (next.code !== undefined) assert.strictEqual(warning.code, next.code);
        });
    }
}

// skip: this test does not apply here; the run counts as a pass.
export function skip(msg?: string): void {
    console.log('1..0 # Skipped: ' + (msg === undefined ? '' : msg));
    process.exit(0);
}
