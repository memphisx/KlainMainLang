// kml:global
// Buffer called or constructed (`Buffer(n)`, `new Buffer('a')`), as Node's
// lib/buffer.js has it: deprecated (DEP0005, warned once), a number is
// Buffer.alloc's size and anything else is Buffer.from's input.

import { NodeTypeError, NodeRangeError } from './internal_errors';

let warned = false;

export function __kml_Buffer_ctor(args: any[]): Buffer { // kml:lower Buffer
    const arg: any = args[0];
    const encodingOrOffset: any = args[1];
    const length: any = args[2];
    if (!warned) {
        warned = true;
        process.emitWarning('Buffer() is deprecated due to security and usability issues. Please use the Buffer.alloc(), ' +
            'Buffer.allocUnsafe(), or Buffer.from() methods instead.', 'DeprecationWarning', 'DEP0005');
    }
    if (typeof arg === 'number') {
        if (typeof encodingOrOffset === 'string') {
            throw NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "string" argument must be of type string. Received type number (' + String(arg) + ')');
        }
        return Buffer.alloc(arg as number);
    }
    return (Buffer as any).from(arg, encodingOrOffset, length);
}

// Buffer.alloc's size, checked as Node's validateNumber(size, 'size', 0,
// kMaxLength) checks it.
export function __kml_buffer_size(size: any): number { // kml:lower Buffer
    if (typeof size !== 'number') {
        let shown = '';
        if (size === null) shown = 'Received null';
        else if (size === undefined) shown = 'Received undefined';
        else if (typeof size === 'function') shown = 'Received function ' + String((size as any).name);
        else if (typeof size === 'object') shown = 'Received an instance of ' + String(size.constructor?.name ?? 'Object');
        else {
            let v = typeof size === 'string' ? (size as string) : String(size);
            if (typeof size === 'string') {
                if (v.length > 28) v = v.slice(0, 25) + '...';
                v = "'" + v + "'";
            } else if (typeof size === 'bigint') v += 'n';
            shown = 'Received type ' + typeof size + ' (' + v + ')';
        }
        throw NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "size" argument must be of type number. ' + shown);
    }
    const n = size as number;
    if (!(n >= 0 && n <= 9007199254740991)) {
        let shown = String(n);
        if (Number.isInteger(n) && Math.abs(n) > 4294967296) {
            const neg = shown.startsWith('-');
            let digits = neg ? shown.slice(1) : shown;
            let out = '';
            while (digits.length > 3) {
                out = '_' + digits.slice(digits.length - 3) + out;
                digits = digits.slice(0, digits.length - 3);
            }
            shown = (neg ? '-' : '') + digits + out;
        }
        throw NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "size" is out of range. It must be >= 0 && <= 9007199254740991. Received ' + shown);
    }
    return n;
}
