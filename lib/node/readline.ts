// Node's lib/readline.js, in TypeScript: the callback-style Interface over
// any Readable input and optional Writable output, and the cursor helpers.
// kml:default-namespace — `import readline from 'readline'` reads this
// module's exports.

import { inspect } from './internal_util_inspect';
import { Interface as _Interface, validateAbortSignal } from './internal_readline_interface';
import { createInterface as promisesCreateInterface } from './readline_promises';

export { clearLine, clearScreenDown, cursorTo, moveCursor } from './internal_readline_callbacks';
export { emitKeypressEvents } from './internal_readline_interface';
// `readline/promises`' createInterface (its Interface and Readline classes
// are imported from `readline/promises`: a class is not yet a value).
export const promises = { createInterface: promisesCreateInterface };

export class Interface extends _Interface {
    constructor(input: any, output?: any, completer?: any, terminal?: boolean) {
        if (input?.input && typeof input.completer === 'function' && input.completer.length !== 2) {
            const realCompleter = input.completer;
            input.completer = (v: string, cb: (err: any, result: any) => void): void => { cb(null, realCompleter(v)); };
        } else if (typeof completer === 'function' && completer.length !== 2) {
            const realCompleter = completer;
            completer = (v: string, cb: (err: any, result: any) => void): void => { cb(null, realCompleter(v)); };
        }

        super(input, output, completer, terminal);

        if (process.env.TERM === 'dumb') {
            this._kmlUseDumbTtyWrite();
        }
    }

    // Displays `query` by writing it to the `output`, calling cb with the
    // answer.
    question(query: string, optionsArg?: any, cbArg?: (answer: string) => void): void {
        let cb: any = typeof optionsArg === 'function' ? optionsArg : cbArg;
        const options: any = optionsArg === null || typeof optionsArg !== 'object' ? {} : optionsArg;

        if (options.signal) {
            validateAbortSignal(options.signal, 'options.signal');
            if (options.signal.aborted) {
                return;
            }

            const signal = options.signal;
            const onAbort = (): void => {
                this._kmlQuestionCancel();
            };
            signal.addEventListener('abort', onAbort, { once: true });
            const dispose = (): void => { signal.removeEventListener('abort', onAbort); };
            const originalCb = cb;
            cb = typeof cb === 'function' ? (answer: string): any => {
                dispose();
                return originalCb(answer);
            } : dispose;
        }

        if (typeof cb === 'function') {
            this._kmlQuestion(query, cb);
        }
    }

    // Overrides the parent method because `this.completer` in the legacy
    // implementation takes a callback instead of being an async function.
    async _tabComplete(lastKeypressWasTab: boolean): Promise<void> {
        this.pause();
        const string = this.line.slice(0, this.cursor);
        this.completer(string, (err: any, value: any) => {
            this.resume();

            if (err) {
                this._writeToOutput('Tab completion error: ' + inspect(err));
                return;
            }

            this._tabCompleter(lastKeypressWasTab, value);
        });
    }
}

// Creates a new Interface.
export function createInterface(input: any, output?: any, completer?: any, terminal?: boolean): Interface {
    return new Interface(input, output, completer, terminal);
}
