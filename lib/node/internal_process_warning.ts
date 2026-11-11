// process.emitWarning: Node's lib/internal/process/warning.js. The warning
// is emitted as the process 'warning' event on the next tick; the default
// listener (onWarning, registered when the process emitter is made) prints
// it to stderr. Until something makes the process emitter
// (internal_process.ts), the default listener is the only one there can be,
// so the warning goes to it directly. This module imports nothing, so
// `events` itself can emit warnings.

import { NodeTypeError } from './internal_errors';

function received(value: any): string {
    if (value === null || value === undefined) return ' Received ' + String(value);
    if (typeof value === 'function') return ' Received function';
    if (typeof value === 'object') return ' Received an instance of Object';
    let shown = String(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    if (typeof value === 'string') shown = "'" + shown + "'";
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

function validateString(value: any, name: string): void {
    if (typeof value !== 'string') {
        throw NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type string.' + received(value));
    }
}

let sink: ((warning: any) => void) | null = null;
// The process object once something made it: its noDeprecation and
// trace flags a program may set. Before that, none can be set.
let proc: any = null;

export function _kmlSetWarningProcess(p: any): void {
    proc = p;
}

// The process emitter's 'warning' emit, once it exists.
export function _kmlSetWarningSink(fn: (warning: any) => void): void {
    sink = fn;
}

function writeOut(message: string): void {
    console.error(message);
}

let traceWarningHelperShown = false;

// The program's name as Node's path.basename(process.argv0, '.exe').
function argv0Name(): string {
    const a: any = process.argv0;
    let n = typeof a === 'string' && a !== '' ? a : 'node';
    const cut = Math.max(n.lastIndexOf('/'), n.lastIndexOf('\\'));
    if (cut >= 0) n = n.slice(cut + 1);
    if (n.endsWith('.exe') && n.length > 4) n = n.slice(0, -4);
    return n;
}

// Node's onWarning: the default 'warning' listener.
export function _kmlOnWarning(warning: any): void {
    if (!(warning instanceof Error)) return;
    const w: any = warning;
    const p: any = proc !== null ? proc : {};
    const isDeprecation = w.name === 'DeprecationWarning';
    if (isDeprecation && p.noDeprecation) return;
    const trace = p.traceProcessWarnings || (isDeprecation && p.traceDeprecation);
    let msg = '(node:' + process.pid + ') ';
    if (w.code) msg += '[' + w.code + '] ';
    if (trace && w.stack) msg += w.stack;
    else msg += String(warning);
    if (typeof w.detail === 'string') msg += '\n' + w.detail;
    if (!trace && !traceWarningHelperShown) {
        const flag = isDeprecation ? '--trace-deprecation' : '--trace-warnings';
        msg += '\n(Use `' + argv0Name() + ' ' + flag + ' ...` to show where the warning was created)';
        traceWarningHelperShown = true;
    }
    writeOut(msg);
}

function createWarningObject(message: string, type: any, code: any, detail: any): Error {
    const warning: any = new Error(message);
    warning.name = String(type || 'Warning');
    if (code !== undefined) warning.code = code;
    if (detail !== undefined) warning.detail = detail;
    return warning;
}

function doEmitWarning(warning: any): void {
    if (sink !== null) sink(warning);
    else _kmlOnWarning(warning);
}

export function emitWarning(warning: string | Error, type?: any, code?: any, ctor?: any): void {
    let detail: any = undefined;
    let t: any = type;
    let c: any = code;
    if (t !== null && typeof t === 'object' && !Array.isArray(t)) {
        const options: any = t;
        c = options.code;
        if (typeof options.detail === 'string') detail = options.detail;
        t = options.type || 'Warning';
    } else if (typeof t === 'function') {
        c = undefined;
        t = 'Warning';
    }
    if (t !== undefined) validateString(t, 'type');
    if (typeof c === 'function') {
        c = undefined;
    } else if (c !== undefined) {
        validateString(c, 'code');
    }
    let w: any = warning;
    if (typeof w === 'string') {
        w = createWarningObject(w, t, c, detail);
    } else if (!(w instanceof Error)) {
        throw NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "warning" argument must be of type string or an instance of Error.' + received(w));
    }
    const out = w;
    process.nextTick(() => { doEmitWarning(out); });
}
