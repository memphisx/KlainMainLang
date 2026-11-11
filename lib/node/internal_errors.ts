// Node's coded errors (lib/internal/errors.js): a plain Error, TypeError or
// RangeError carrying its `code` (ERR_*) as an own property — Node builds
// each as an instance of the base class itself, so its constructor is
// `TypeError`, not a subclass.

// Node's makeNodeErrorWithCode: the code on the error, its stack's first
// line and its own toString naming it (`TypeError [ERR_X]: …`).
function coded(e: any, code: string): void {
    e.code = code;
    const head = String(e.name) + ': ' + String(e.message);
    const stack: any = e.stack;
    if (typeof stack === 'string' && stack.startsWith(head)) {
        Object.defineProperty(e, 'stack', { value: String(e.name) + ' [' + code + ']: ' + String(e.message) + stack.slice(head.length), writable: true, enumerable: false, configurable: true });
    }
    Object.defineProperty(e, 'toString', {
        value: function (this: any): string { return String(this.name) + ' [' + code + ']: ' + String(this.message); },
        writable: true, enumerable: false, configurable: true,
    });
}

export interface NodeError extends Error {
    code: string;
}

export function NodeError(code: string, message: string): NodeError {
    const e = new Error(message) as NodeError;
    coded(e, code);
    return e;
}

export interface NodeTypeError extends TypeError {
    code: string;
}

export function NodeTypeError(code: string, message: string): NodeTypeError {
    const e = new TypeError(message) as NodeTypeError;
    coded(e, code);
    return e;
}

export interface NodeRangeError extends RangeError {
    code: string;
}

export function NodeRangeError(code: string, message: string): NodeRangeError {
    const e = new RangeError(message) as NodeRangeError;
    coded(e, code);
    return e;
}

// ERR_MISSING_ARGS's message for the argument names.
export function missingArgs(...names: string[]): NodeTypeError {
    let shown = '';
    if (names.length === 1) {
        shown = `"${names[0]}" argument`;
    } else if (names.length === 2) {
        shown = `"${names[0]}" and "${names[1]}" arguments`;
    } else {
        const quoted = names.map((n) => `"${n}"`);
        shown = `${quoted.slice(0, -1).join(', ')}, and ${quoted[quoted.length - 1]} arguments`;
    }
    return NodeTypeError('ERR_MISSING_ARGS', `The ${shown} must be specified`);
}

// ERR_INVALID_ARG_TYPE's " Received …" suffix for value.
export function receivedSuffix(value: any): string {
    if (value === null || value === undefined) return ' Received ' + String(value);
    if (typeof value === 'function') return ' Received function ' + (value as Function).name;
    if (typeof value === 'object') {
        if (value.constructor && value.constructor.name) return ' Received an instance of ' + value.constructor.name;
        return ' Received ' + String(value);
    }
    let shown = typeof value === 'string' ? "'" + value + "'" : String(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    return ' Received type ' + typeof value + ' (' + shown + ')';
}
