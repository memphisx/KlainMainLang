// Node's lib/readline/promises.js and lib/internal/readline/promises.js, in
// TypeScript: an Interface whose question returns a promise, and Readline,
// cursor control written on commit.
// kml:default-namespace — `import readline from 'readline/promises'` reads
// this module's exports.

import { Interface as _Interface, AbortError, validateAbortSignal } from './internal_readline_interface';
import { kClearLine, kClearScreenDown, kClearToLineBeginning, kClearToLineEnd, kEscape } from './internal_readline_utils';

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeRangeError extends RangeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

function validateInteger(value: any, name: string, min: number = Number.MIN_SAFE_INTEGER, max: number = Number.MAX_SAFE_INTEGER): void {
    if (typeof value !== 'number') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type number. Received ' + (value === undefined || value === null ? String(value) : 'type ' + typeof value));
    }
    if (!Number.isInteger(value)) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be an integer. Received ' + String(value));
    }
    if (value < min || value > max) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be >= ' + min + ' && <= ' + max + '. Received ' + String(value));
    }
}

function CSI(s: string): string {
    return kEscape + '[' + s;
}

export class Readline {
    #autoCommit = false;
    #stream: any;
    #todo: string[] = [];

    constructor(stream: any, options?: { autoCommit?: boolean }) {
        if (stream === null || typeof stream !== 'object' || typeof stream.write !== 'function') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "stream" argument must be an instance of Writable.');
        }
        this.#stream = stream;
        if (options?.autoCommit !== undefined && options?.autoCommit !== null) {
            if (typeof options.autoCommit !== 'boolean') {
                throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options.autoCommit" property must be of type boolean.');
            }
            this.#autoCommit = options.autoCommit;
        }
    }

    #push(data: string): void {
        if (this.#autoCommit) process.nextTick(() => { this.#stream.write(data); });
        else this.#todo.push(data);
    }

    // Moves the cursor to the x and y coordinate on the given stream.
    cursorTo(x: number, y?: number): this {
        validateInteger(x, 'x');
        if (y !== undefined && y !== null) validateInteger(y, 'y');

        const data = y === undefined || y === null ? CSI((x + 1) + 'G') : CSI((y + 1) + ';' + (x + 1) + 'H');
        this.#push(data);
        return this;
    }

    // Moves the cursor relative to its current location.
    moveCursor(dx: number, dy: number): this {
        if (dx || dy) {
            validateInteger(dx, 'dx');
            validateInteger(dy, 'dy');

            let data = '';

            if (dx < 0) {
                data += CSI((-dx) + 'D');
            } else if (dx > 0) {
                data += CSI(dx + 'C');
            }

            if (dy < 0) {
                data += CSI((-dy) + 'A');
            } else if (dy > 0) {
                data += CSI(dy + 'B');
            }
            this.#push(data);
        }
        return this;
    }

    // Clears the current line the cursor is on: -1 left of the cursor, +1
    // right of it, 0 the entire line.
    clearLine(dir: -1 | 0 | 1): this {
        validateInteger(dir, 'dir', -1, 1);

        const data =
            dir < 0 ? kClearToLineBeginning :
                dir > 0 ? kClearToLineEnd :
                    kClearLine;
        this.#push(data);
        return this;
    }

    // Clears the screen from the current position of the cursor down.
    clearScreenDown(): this {
        this.#push(kClearScreenDown);
        return this;
    }

    // Sends all the pending actions to the stream; resolves once they are
    // flushed.
    commit(): Promise<void> {
        return new Promise<void>((resolve) => {
            this.#stream.write(this.#todo.join(''), resolve);
            this.#todo = [];
        });
    }

    // Clears the pending actions without sending them.
    rollback(): this {
        this.#todo = [];
        return this;
    }
}

export class Interface extends _Interface {
    question(query: string, options: { signal?: AbortSignal } = {}): Promise<string> {
        return new Promise<string>((resolve, reject) => {
            let cb = resolve;

            if (options?.signal) {
                validateAbortSignal(options.signal, 'options.signal');
                if (options.signal.aborted) {
                    return reject(new AbortError(undefined, { cause: options.signal.reason }));
                }

                const signal = options.signal;
                const onAbort = (): void => {
                    this._kmlQuestionCancel();
                    reject(new AbortError(undefined, { cause: signal.reason }));
                };
                signal.addEventListener('abort', onAbort, { once: true });

                cb = (answer: string): void => {
                    signal.removeEventListener('abort', onAbort);
                    resolve(answer);
                };
            }

            this._kmlSetQuestionReject(reject);

            this._kmlQuestion(query, cb);
        });
    }
}

export function createInterface(input: any, output?: any, completer?: any, terminal?: boolean): Interface {
    return new Interface(input, output, completer, terminal);
}
