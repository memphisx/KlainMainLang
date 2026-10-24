// Node's coded errors (lib/internal/errors.js): a plain Error, TypeError or
// RangeError carrying its `code` (ERR_*) as an own property — Node builds
// each as an instance of the base class itself, so its constructor is
// `TypeError`, not a subclass.

export interface NodeError extends Error {
    code: string;
}

export function NodeError(code: string, message: string): NodeError {
    const e = new Error(message) as NodeError;
    e.code = code;
    return e;
}

export interface NodeTypeError extends TypeError {
    code: string;
}

export function NodeTypeError(code: string, message: string): NodeTypeError {
    const e = new TypeError(message) as NodeTypeError;
    e.code = code;
    return e;
}

export interface NodeRangeError extends RangeError {
    code: string;
}

export function NodeRangeError(code: string, message: string): NodeRangeError {
    const e = new RangeError(message) as NodeRangeError;
    e.code = code;
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
