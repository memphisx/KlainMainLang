// Node's lib/internal/readline/callbacks.js: the cursor and clearing
// escapes `readline` and `tty.WriteStream` write to a stream.

import { NodeTypeError } from './internal_errors';

const kEscape = '\x1b';
export const kClearToLineBeginning = kEscape + '[1K';
export const kClearToLineEnd = kEscape + '[0K';
export const kClearLine = kEscape + '[2K';
export const kClearScreenDown = kEscape + '[0J';

function invalidArgValue(name: string, value: any): Error {
    return new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument '" + name + "' is invalid. Received " + String(value));
}

function done(callback: any): boolean {
    if (typeof callback === 'function') process.nextTick(() => { callback(null); });
    return true;
}

// Moves the cursor to (x, y), or to column x of the current line.
export function cursorTo(stream: any, x: any, y?: any, callback?: any): boolean {
    if (callback !== undefined && typeof callback !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "callback" argument must be of type function.');
    }
    if (typeof y === 'function') {
        callback = y;
        y = undefined;
    }
    if (typeof x === 'number' && Number.isNaN(x)) throw invalidArgValue('x', x);
    if (typeof y === 'number' && Number.isNaN(y)) throw invalidArgValue('y', y);
    if (stream === null || stream === undefined || (typeof x !== 'number' && typeof y !== 'number')) {
        return done(callback);
    }
    if (typeof x !== 'number') throw new NodeTypeError('ERR_INVALID_CURSOR_POS', 'Cannot set cursor row without setting its column');
    const data = typeof y !== 'number' ? kEscape + '[' + (x + 1) + 'G' : kEscape + '[' + (y + 1) + ';' + (x + 1) + 'H';
    return stream.write(data, callback);
}

// Moves the cursor relative to its current position.
export function moveCursor(stream: any, dx: number, dy: number, callback?: any): boolean {
    if (callback !== undefined && typeof callback !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "callback" argument must be of type function.');
    }
    if (stream === null || stream === undefined || !(dx || dy)) return done(callback);
    let data = '';
    if (dx < 0) data += kEscape + '[' + (-dx) + 'D';
    else if (dx > 0) data += kEscape + '[' + dx + 'C';
    if (dy < 0) data += kEscape + '[' + (-dy) + 'A';
    else if (dy > 0) data += kEscape + '[' + dy + 'B';
    return stream.write(data, callback);
}

// Clears the current line: dir < 0 to its start, > 0 to its end, 0 all.
export function clearLine(stream: any, dir: number, callback?: any): boolean {
    if (callback !== undefined && typeof callback !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "callback" argument must be of type function.');
    }
    if (stream === null || stream === undefined) return done(callback);
    const type = dir < 0 ? kClearToLineBeginning : dir > 0 ? kClearToLineEnd : kClearLine;
    return stream.write(type, callback);
}

// Clears the screen from the cursor down.
export function clearScreenDown(stream: any, callback?: any): boolean {
    if (callback !== undefined && typeof callback !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "callback" argument must be of type function.');
    }
    if (stream === null || stream === undefined) return done(callback);
    return stream.write(kClearScreenDown, callback);
}
