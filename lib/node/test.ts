// Node's `node:test` (lib/internal/test_runner): test/it, describe/suite,
// the hooks, the TestContext, and the spec reporter's output. Tests are
// registered while the module runs and run afterwards, one at a time, in
// registration order; a suite's function runs when it is registered. The
// reporter's lines are held until the event loop turns, or the run ends,
// as Node's reporter stream delivers them.
//
// The bare `test` specifier also carries Node's own test-suite helpers
// (test/common: mustCall and the rest), which Node's tests import.

import { inspect } from './internal_util_inspect';

type TestFn = (t: TestContext) => any;
type HookFn = (t: TestContext) => any;

interface TestOptions {
    skip?: boolean | string;
    todo?: boolean | string;
    only?: boolean;
    concurrency?: number | boolean;
    timeout?: number;
    signal?: AbortSignal;
    plan?: number;
}

class Hooks {
    before: HookFn[] = [];
    after: HookFn[] = [];
    beforeEach: HookFn[] = [];
    afterEach: HookFn[] = [];
}

class Test {
    name: string;
    fn: TestFn | undefined;
    parent: Test | undefined;
    isSuite: boolean;
    depth: number;
    skip: boolean | string = false;
    todo: boolean | string = false;
    subtests: Test[] = [];
    hooks: Hooks = new Hooks();
    diagnostics: string[] = [];
    error: any = undefined;
    hasError = false;
    subtestFailed = false;
    finished = false;
    headerPrinted = false;
    durationMs = 0;
    plan = -1;
    assertions = 0;
    context: TestContext | undefined = undefined;
    pending: Promise<void>[] = [];

    constructor(name: string, fn: TestFn | undefined, parent: Test | undefined, isSuite: boolean) {
        this.name = name;
        this.fn = fn;
        this.parent = parent;
        this.isSuite = isSuite;
        this.depth = parent === undefined ? -1 : parent.depth + 1;
    }

    fullName(): string {
        if (this.parent === undefined || this.parent.parent === undefined) return this.name;
        return this.parent.fullName() + ' > ' + this.name;
    }

    failed(): boolean {
        return this.hasError || this.subtestFailed;
    }
}

// The TestContext a test function receives (lib/internal/test_runner/test.js).
export class TestContext {
    _test: Test;

    constructor(test: Test) {
        this._test = test;
    }

    get name(): string {
        return this._test.name;
    }

    get fullName(): string {
        return this._test.fullName();
    }

    diagnostic(message: string): void {
        this._test.diagnostics.push(message);
    }

    skip(message?: string): void {
        if (message === undefined) this._test.skip = true;
        else this._test.skip = message;
    }

    todo(message?: string): void {
        if (message === undefined) this._test.todo = true;
        else this._test.todo = message;
    }

    plan(count: number): void {
        this._test.plan = count;
    }

    before(fn: HookFn): void {
        this._test.hooks.before.push(fn);
    }

    after(fn: HookFn): void {
        this._test.hooks.after.push(fn);
    }

    beforeEach(fn: HookFn): void {
        this._test.hooks.beforeEach.push(fn);
    }

    afterEach(fn: HookFn): void {
        this._test.hooks.afterEach.push(fn);
    }

    test(name: any, options?: any, fn?: any): Promise<void> {
        const sub = makeTest(this._test, name, options, fn, false);
        this._test.subtests.push(sub);
        const done = runTest(sub);
        this._test.pending.push(done);
        return done;
    }
}

// ---- the reporter (lib/internal/test_runner/reporter/spec.js) ----

let held: string[] = [];
let flushScheduled = false;
let runStart = 0n;

function flush(): void {
    flushScheduled = false;
    const lines = held;
    held = [];
    for (const line of lines) console.log(line);
}

function emit(line: string): void {
    held.push(line);
    if (!flushScheduled) {
        flushScheduled = true;
        setImmediate(flush);
    }
}

function indent(depth: number): string {
    let s = '';
    for (let i = 0; i < depth; i++) s += '  ';
    return s;
}

function ms(n: number): string {
    return '(' + String(n) + 'ms)';
}

function printHeaders(test: Test | undefined): void {
    if (test === undefined || test.parent === undefined || test.headerPrinted) return;
    printHeaders(test.parent);
    test.headerPrinted = true;
    emit(indent(test.depth) + '▶ ' + test.name);
}

function directive(test: Test): string {
    if (test.skip !== false) return ' # ' + (typeof test.skip === 'string' ? test.skip : 'SKIP');
    if (test.todo !== false) return ' # ' + (typeof test.todo === 'string' ? test.todo : 'TODO');
    return '';
}

function report(test: Test): void {
    printHeaders(test.parent);
    const pad = indent(test.depth);
    let symbol = test.failed() ? '✖ ' : '✔ ';
    if (test.skip !== false) symbol = '﹣ ';
    emit(pad + symbol + test.name + ' ' + ms(test.durationMs) + directive(test));
    for (const d of test.diagnostics) emit(pad + 'ℹ ' + d);
}

// ---- running ----

const root = new Test('<root>', undefined, undefined, true);
let current: Test = root;
let started = false;
const failures: Test[] = [];
const counts = { tests: 0, suites: 0, pass: 0, fail: 0, cancelled: 0, skipped: 0, todo: 0 };

function errorText(err: any): string {
    if (err instanceof Error) {
        const e: any = err;
        const code = typeof e.code === 'string' ? ' [' + (e.code as string) + ']' : '';
        return (err as Error).name + code + ': ' + (err as Error).message;
    }
    return inspect(err);
}

async function callHook(fn: HookFn, test: Test): Promise<void> {
    const ctx = test.context === undefined ? new TestContext(test) : test.context;
    await fn(ctx);
}

// The beforeEach hooks of every enclosing test, outermost first; the
// afterEach hooks, innermost first.
function eachHooks(test: Test, kind: string): HookFn[] {
    const chain: Test[] = [];
    for (let p = test.parent; p !== undefined; p = p.parent) chain.unshift(p);
    const out: HookFn[] = [];
    for (const p of chain) {
        const list = kind === 'before' ? p.hooks.beforeEach : p.hooks.afterEach;
        for (const h of list) out.push(h);
    }
    if (kind === 'after') out.reverse();
    return out;
}

function count(test: Test): void {
    if (test.isSuite) {
        counts.suites++;
        return;
    }
    counts.tests++;
    if (test.skip !== false) counts.skipped++;
    else if (test.todo !== false) counts.todo++;
    else if (test.failed()) counts.fail++;
    else counts.pass++;
}

// Milliseconds since t0 (nanoseconds from process.hrtime.bigint), as the
// runner measures them.
function since(t0: bigint): number {
    return Number(process.hrtime.bigint() - t0) / 1e6;
}

async function runTest(test: Test): Promise<void> {
    const t0 = process.hrtime.bigint();
    let turned = false;
    setImmediate(() => { turned = true; });
    if (test.skip === false) {
        const ctx = new TestContext(test);
        test.context = ctx;
        const outer = current;
        current = test;
        try {
            if (!test.isSuite) {
                for (const h of eachHooks(test, 'before')) await callHook(h, test);
            }
            for (const h of test.hooks.before) await callHook(h, test);
            if (test.fn !== undefined && !test.isSuite) {
                const fn = test.fn;
                await fn(ctx);
            }
            while (test.pending.length > 0) {
                const pending = test.pending;
                test.pending = [];
                for (const p of pending) await p;
            }
            for (const sub of test.subtests) {
                if (!sub.finished) await runTest(sub);
            }
            if (test.plan >= 0 && test.plan !== test.subtests.length + test.assertions) {
                throw new Error('plan expected ' + String(test.plan) + ' assertions but received ' + String(test.subtests.length + test.assertions));
            }
        } catch (err) {
            test.error = err;
            test.hasError = true;
        }
        try {
            for (const h of test.hooks.after) await callHook(h, test);
            if (!test.isSuite) {
                for (const h of eachHooks(test, 'after')) await callHook(h, test);
            }
        } catch (err) {
            if (!test.hasError) {
                test.error = err;
                test.hasError = true;
            }
        }
        current = outer;
    }
    for (const sub of test.subtests) {
        if (sub.failed() && sub.todo === false) test.subtestFailed = true;
    }
    test.finished = true;
    test.durationMs = since(t0);
    count(test);
    if (test.hasError && test.todo === false) failures.push(test);
    if (test.parent !== undefined) report(test);
    // A test that waited on the event loop reports before the next starts,
    // as Node's reporter has by then caught up.
    if (turned) flush();
}

function makeTest(parent: Test, name: any, options: any, fn: any, isSuite: boolean): Test {
    let title: any = name;
    let opts: any = options;
    let body: any = fn;
    if (typeof title === 'function') {
        body = title;
        title = (title as Function).name;
        opts = undefined;
    } else if (typeof title === 'object' && title !== null) {
        body = opts;
        opts = title;
        title = typeof body === 'function' ? (body as Function).name : '<anonymous>';
    }
    if (typeof opts === 'function') {
        body = opts;
        opts = undefined;
    }
    const label = typeof title === 'string' && title !== '' ? title as string : '<anonymous>';
    const test = new Test(label, typeof body === 'function' ? body as TestFn : undefined, parent, isSuite);
    if (opts !== undefined && opts !== null) {
        if (opts.skip === true) test.skip = true;
        else if (opts.skip !== undefined && opts.skip !== false) test.skip = String(opts.skip);
        if (opts.todo === true) test.todo = true;
        else if (opts.todo !== undefined && opts.todo !== false) test.todo = String(opts.todo);
        if (typeof opts.plan === 'number') test.plan = opts.plan as number;
    }
    return test;
}

async function runRoot(): Promise<void> {
    runStart = process.hrtime.bigint();
    try {
        for (const h of root.hooks.before) await callHook(h, root);
    } catch (err) {
        root.error = err;
        root.hasError = true;
    }
    for (const test of root.subtests) {
        if (!test.finished) await runTest(test);
    }
    try {
        for (const h of root.hooks.after) await callHook(h, root);
    } catch (err) {
        root.error = err;
        root.hasError = true;
    }
    summary();
}

function summary(): void {
    emit('ℹ tests ' + String(counts.tests));
    emit('ℹ suites ' + String(counts.suites));
    emit('ℹ pass ' + String(counts.pass));
    emit('ℹ fail ' + String(counts.fail));
    emit('ℹ cancelled ' + String(counts.cancelled));
    emit('ℹ skipped ' + String(counts.skipped));
    emit('ℹ todo ' + String(counts.todo));
    emit('ℹ duration_ms ' + String(since(runStart)));
    if (failures.length > 0) {
        emit('');
        emit('✖ failing tests:');
        for (const f of failures) {
            emit('');
            emit('✖ ' + f.name + ' ' + ms(f.durationMs));
            for (const line of errorText(f.error).split('\n')) emit('  ' + line);
        }
    }
    flush();
    if (counts.fail > 0 || root.hasError) process.exitCode = 1;
}

function register(test: Test): void {
    const parent = test.parent === undefined ? root : test.parent;
    parent.subtests.push(test);
    if (!started) {
        started = true;
        setImmediate(() => { runRoot(); });
    }
}

// ---- the API ----

export function test(name?: any, options?: any, fn?: any): Promise<void> {
    if (current !== root && !current.isSuite && current.context !== undefined) {
        return current.context.test(name, options, fn);
    }
    const t = makeTest(current, name, options, fn, false);
    register(t);
    return Promise.resolve();
}

export function describe(name?: any, options?: any, fn?: any): Promise<void> {
    const s = makeTest(current, name, options, fn, true);
    register(s);
    const body = s.fn;
    if (body !== undefined && s.skip === false) {
        const outer = current;
        current = s;
        try {
            body(new TestContext(s));
        } catch (err) {
            s.error = err;
            s.hasError = true;
        }
        current = outer;
    }
    return Promise.resolve();
}

export const it = test;
export const suite = describe;

export function before(fn: HookFn): void {
    current.hooks.before.push(fn);
}

export function after(fn: HookFn): void {
    current.hooks.after.push(fn);
}

export function beforeEach(fn: HookFn): void {
    current.hooks.beforeEach.push(fn);
}

export function afterEach(fn: HookFn): void {
    current.hooks.afterEach.push(fn);
}

export function skip(name?: any, options?: any, fn?: any): Promise<void> {
    const t = makeTest(current, name, options, fn, false);
    t.skip = true;
    register(t);
    return Promise.resolve();
}

export function todo(name?: any, options?: any, fn?: any): Promise<void> {
    const t = makeTest(current, name, options, fn, false);
    t.todo = true;
    register(t);
    return Promise.resolve();
}

export const only = test;

export function getTestContext(): TestContext | undefined {
    return current === root ? undefined : current.context;
}

// The default export is `test`, carrying the rest as properties.
test.test = test;
test.it = test;
test.describe = describe;
test.suite = describe;
test.before = before;
test.after = after;
test.beforeEach = beforeEach;
test.afterEach = afterEach;
test.skip = skip;
test.todo = todo;
test.only = test;
test.getTestContext = getTestContext;

export default test;
