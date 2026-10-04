// Node's coded errors (lib/internal/errors.js): an Error, TypeError or
// RangeError carrying its `code` (ERR_*) as an own property.

export class NodeError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

export class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

export class NodeRangeError extends RangeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
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
    return new NodeTypeError('ERR_MISSING_ARGS', `The ${shown} must be specified`);
}
