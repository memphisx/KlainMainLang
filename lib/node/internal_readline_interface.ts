// Node's lib/internal/readline/interface.js, emitKeypressEvents.js and the
// in-memory part of internal/repl/history.js (ReplHistory), in TypeScript:
// the Interface `readline` and `readline/promises` share. Node's
// symbol-keyed state is #private here; the members readline's legacy API
// exposes (`_ttyWrite`, `_refreshLine`, `_prompt`, …) are ordinary members.

import { EventEmitter, on } from 'events';
import { StringDecoder } from 'string_decoder';
import { inspect } from './internal_util_inspect';
import { clearScreenDown, cursorTo, moveCursor } from './internal_readline_callbacks';
import { charLengthAt, charLengthLeft, commonPrefix, emitKeys, getStringWidth, kEscape, reverseString, stripVTControlCharacters } from './internal_readline_utils';

class NodeError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

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

export class AbortError extends Error {
    code: string = 'ABORT_ERR';
    cause: any;
    constructor(message?: string, options?: { cause?: any }) {
        super(message === undefined ? 'The operation was aborted' : message);
        this.name = 'AbortError';
        this.cause = options?.cause;
    }
}

function useAfterClose(): Error {
    return new NodeError('ERR_USE_AFTER_CLOSE', 'readline was closed');
}

export function validateAbortSignal(signal: any, name: string): void {
    if (signal !== undefined && (signal === null || typeof signal !== 'object' || !('aborted' in signal))) {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" property must be an instance of AbortSignal.' + received(signal));
    }
}

function received(value: any): string {
    if (value === null || value === undefined) return ' Received ' + String(value);
    if (typeof value === 'function') return ' Received function ' + (value.name || '<anonymous>');
    if (typeof value === 'object') return ' Received ' + inspect(value, { depth: -1 });
    return ' Received type ' + typeof value + ' (' + inspect(value) + ')';
}

const kMaxUndoRedoStackSize = 2048;
const kMincrlfDelay = 100;

// The end of a line is signaled by either one of the following:
//  - \r\n
//  - \n
//  - \r followed by something other than \n
//  -   (Unicode 'LINE SEPARATOR')
//  -   (Unicode 'PARAGRAPH SEPARATOR')
// The index of the next line ending in str at or after from, or -1.
function nextLineEnding(str: string, from: number): number {
    for (let i = from; i < str.length; i++) {
        const c = str.charCodeAt(i);
        if (c === 10 || c === 13 || c === 0x2028 || c === 0x2029) return i;
    }
    return -1;
}

// The index just past the line ending at i.
function lineEndingEnd(str: string, i: number): number {
    return str.charCodeAt(i) === 13 && i + 1 < str.length && str.charCodeAt(i + 1) === 10 ? i + 2 : i + 1;
}

// GNU readline library - keyseq-timeout is 500ms (default)
const ESCAPE_CODE_TIMEOUT = 500;

// Max length of the kill ring
const kMaxLengthOfKillRing = 32;

export const kMultilinePrompt = '| ';

// ---- internal/readline/emitKeypressEvents ----

const KEYPRESS_DECODER = Symbol('keypress-decoder');

// Makes stream emit 'keypress' events for the keys its data decodes to.
export function emitKeypressEvents(stream: any, iface: any = {}): void {
    if (stream[KEYPRESS_DECODER]) return;

    const decoder = new StringDecoder('utf8');
    stream[KEYPRESS_DECODER] = decoder;

    let escapeDecoder = emitKeys(stream);
    escapeDecoder.next();

    const triggerEscape = (): void => { escapeDecoder.next(''); };
    const escapeCodeTimeout: number = iface.escapeCodeTimeout ?? ESCAPE_CODE_TIMEOUT;
    let timeoutId: any = undefined;

    const onData = (input: any): void => {
        if (stream.listenerCount('keypress') > 0) {
            const string = decoder.write(input);
            if (string) {
                clearTimeout(timeoutId);

                // This supports characters of length 2.
                iface._sawKeyPress = charLengthAt(string, 0) === string.length;
                iface.isCompletionEnabled = false;

                let length = 0;
                for (const character of string) {
                    length += character.length;
                    if (length === string.length) {
                        iface.isCompletionEnabled = true;
                    }

                    try {
                        escapeDecoder.next(character);
                        // Escape letter at the tail position
                        if (length === string.length && character === kEscape) {
                            timeoutId = setTimeout(triggerEscape, escapeCodeTimeout);
                        }
                    } catch (err) {
                        // If the generator throws (it could happen in the
                        // `keypress` event), we need to restart it.
                        escapeDecoder = emitKeys(stream);
                        escapeDecoder.next();
                        throw err;
                    }
                }
            }
        } else {
            // Nobody's watching anyway
            stream.removeListener('data', onData);
            stream.on('newListener', onNewListener);
        }
    };

    const onNewListener = (event: string | symbol): void => {
        if (event === 'keypress') {
            stream.on('data', onData);
            stream.removeListener('newListener', onNewListener);
        }
    };

    if (stream.listenerCount('keypress') > 0) {
        stream.on('data', onData);
    } else {
        stream.on('newListener', onNewListener);
    }
}

// ---- internal/repl/history (the in-memory history readline keeps) ----

const kHistorySize = 30;

function normalizeLineEndings(line: string, from: string, to: string): string {
    // Multiline history entries are saved reversed.
    return reverseString(line, from, to);
}

class ReplHistory {
    #context: any;
    #removeHistoryDuplicates: boolean;
    #size: number;
    #history: string[];
    #index = -1;

    constructor(context: any, options: any) {
        if (options.history !== undefined && !Array.isArray(options.history)) {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "history" argument must be an instance of Array.' + received(options.history));
        }
        if (options.size !== undefined) {
            if (typeof options.size !== 'number') {
                throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "size" argument must be of type number.' + received(options.size));
            }
            if (options.size < 0 || Number.isNaN(options.size)) {
                throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "size" is out of range. It must be >= 0. Received ' + inspect(options.size));
            }
        }
        this.#context = context;
        this.#removeHistoryDuplicates = options.removeHistoryDuplicates || false;
        this.#size = options.size ?? context.historySize ?? kHistorySize;
        this.#history = options.history ?? [];
    }

    addHistory(isMultiline: boolean, lastCommandErrored: boolean): string {
        const line: string = this.#context.line;

        if (line.length === 0) return '';

        // If the history is disabled then return the line
        if (this.#size === 0) return line;

        // If the trimmed line is empty then return the line
        if (line.trim().length === 0) return line;

        // Each line would be saved in the history while creating a new
        // multiline, and we don't want that.
        if (isMultiline && this.#index === -1) {
            this.#history.shift();
        } else if (lastCommandErrored) {
            // If the last command errored and we are trying to edit the
            // history to fix it remove the broken one from the history
            this.#history.shift();
        }

        const normalizedLine = normalizeLineEndings(line, '\n', '\r');

        if (this.#history.length === 0 || this.#history[0] !== normalizedLine) {
            if (this.#removeHistoryDuplicates) {
                // Remove older history line if identical to new one
                const dupIndex = this.#history.indexOf(normalizedLine);
                if (dupIndex !== -1) this.#history.splice(dupIndex, 1);
            }

            // Add the new line to the history
            this.#history.unshift(normalizedLine);

            // Only store so many
            if (this.#history.length > this.#size)
                this.#history.pop();
        }

        this.#index = -1;

        const finalLine = isMultiline ? reverseString(this.#history[0]) : this.#history[0];

        // The listener could change the history object, possibly to remove
        // the last added entry if it is sensitive and should not be persisted
        // in the history, like a password
        this.#context.emit('history', this.#history);

        return finalLine;
    }

    canNavigateToNext(): boolean {
        return this.#index > -1 && this.#history.length > 0;
    }

    navigateToNext(substringSearch: string | null): string | null {
        if (!this.canNavigateToNext()) {
            return null;
        }
        const search = substringSearch || '';
        let index = this.#index - 1;

        while (
            index >= 0 &&
            (!this.#history[index].startsWith(search) ||
                this.#context.line === this.#history[index])
        ) {
            index--;
        }

        this.#index = index;

        if (index === -1) {
            return search;
        }

        return normalizeLineEndings(this.#history[index], '\r', '\n');
    }

    canNavigateToPrevious(): boolean {
        return this.#history.length !== this.#index && this.#history.length > 0;
    }

    navigateToPrevious(substringSearch: string | null = ''): string | null {
        if (!this.canNavigateToPrevious()) {
            return null;
        }
        const search = substringSearch || '';
        let index = this.#index + 1;

        while (
            index < this.#history.length &&
            (!this.#history[index].startsWith(search) ||
                this.#context.line === this.#history[index])
        ) {
            index++;
        }

        this.#index = index;

        if (index === this.#history.length) {
            return search;
        }

        return normalizeLineEndings(this.#history[index], '\r', '\n');
    }

    get size(): number { return this.#size; }
    get isFlushing(): boolean { return false; }
    get history(): string[] { return this.#history; }
    set history(value: string[]) { this.#history = value; }
    get index(): number { return this.#index; }
    set index(value: number) { this.#index = value; }
}

// ---- internal/readline/interface ----

export interface Key {
    sequence?: string | null;
    name?: string | undefined;
    ctrl?: boolean;
    meta?: boolean;
    shift?: boolean;
    code?: string;
}

export interface CursorPos {
    rows: number;
    cols: number;
}

export class Interface extends EventEmitter {
    terminal: boolean;
    line = '';
    cursor = 0;
    input: any;
    output: any;
    completer: any;
    crlfDelay: number;
    historyManager: any;
    isCompletionEnabled = true;
    escapeCodeTimeout = ESCAPE_CODE_TIMEOUT;
    tabSize = 8;
    closed = false;
    paused = false;
    prevRows = 0;
    _sawReturnAt = 0;
    _sawKeyPress = false;
    _previousKey: any = null;
    _prompt = '> ';
    _oldPrompt = '';
    _line_buffer: string | null = null;
    _questionCallback: ((answer: string) => void) | null = null;
    _decoder: any = undefined;
    #isMultiline = false;
    #substringSearch: string | null = null;
    #undoStack: { text: string; cursor: number }[] = [];
    #redoStack: { text: string; cursor: number }[] = [];
    #previousCursorCols = -1;
    #killRing: string[] = [];
    #killRingCursor = 0;
    #yanking = false;
    #lastCommandErrored = false;
    #previousLine = '';
    #previousCursor = 0;
    #previousPrevRows = 0;
    #lineObjectStream: any = undefined;
    #questionReject: ((err: any) => void) | undefined = undefined;
    #dumb = false;

    constructor(input: any, output?: any, completer?: any, terminal?: boolean) {
        super();

        let crlfDelay: any;
        let prompt = '> ';
        let signal: any;

        if (input?.input) {
            // An options object was given
            output = input.output;
            completer = input.completer;
            terminal = input.terminal;
            signal = input.signal;

            // It is possible to configure the history through the input object
            const historySize = input.historySize;
            const history = input.history;
            const removeHistoryDuplicates = input.removeHistoryDuplicates;

            if (input.tabSize !== undefined) {
                if (typeof input.tabSize !== 'number' || !Number.isInteger(input.tabSize) || input.tabSize < 1 || input.tabSize > 4294967295) {
                    throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "tabSize" is out of range. It must be >= 1 && <= 4294967295. Received ' + inspect(input.tabSize));
                }
                this.tabSize = input.tabSize;
            }
            if (input.prompt !== undefined) {
                prompt = input.prompt;
            }
            if (input.escapeCodeTimeout !== undefined) {
                if (Number.isFinite(input.escapeCodeTimeout)) {
                    this.escapeCodeTimeout = input.escapeCodeTimeout;
                } else {
                    throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The property 'input.escapeCodeTimeout' is invalid. Received " + inspect(this.escapeCodeTimeout));
                }
            }

            if (signal) {
                validateAbortSignal(signal, 'options.signal');
            }

            crlfDelay = input.crlfDelay;
            input = input.input;

            input.size = historySize;
            input.history = history;
            input.removeHistoryDuplicates = removeHistoryDuplicates;
        }

        this.historyManager = new ReplHistory(this, input);

        if (completer !== undefined && typeof completer !== 'function') {
            throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'completer' is invalid. Received " + inspect(completer));
        }

        // Backwards compat; check the isTTY prop of the output stream when
        // `terminal` was not specified
        if (terminal === undefined && !(output === null || output === undefined)) {
            terminal = !!output.isTTY;
        }

        this.output = output;
        this.input = input;

        this.crlfDelay = crlfDelay ? Math.max(kMincrlfDelay, crlfDelay) : kMincrlfDelay;
        this.completer = completer;

        this.setPrompt(prompt);

        this.terminal = !!terminal;

        const onerror = (err: any): void => {
            this.emit('error', err);
        };

        const ondata = (data: any): void => {
            this._normalWrite(data);
        };

        const onend = (): void => {
            if (typeof this._line_buffer === 'string' && this._line_buffer.length > 0) {
                this.emit('line', this._line_buffer);
            }
            this.close();
        };

        const ontermend = (): void => {
            if (typeof this.line === 'string' && this.line.length > 0) {
                this.emit('line', this.line);
            }
            this.close();
        };

        const onkeypress = (s: any, key: any): void => {
            this._ttyWrite(s, key);
            if (key?.sequence) {
                // If the key.sequence is half of a surrogate pair (>= 0xd800
                // and <= 0xdfff), refresh the line so the character is
                // displayed appropriately.
                const ch = key.sequence.codePointAt(0);
                if (ch >= 0xd800 && ch <= 0xdfff) this._refreshLine();
            }
        };

        const onresize = (): void => {
            this._refreshLine();
        };

        input.on('error', onerror);

        if (!this.terminal) {
            const onSelfCloseWithoutTerminal = (): void => {
                input.removeListener('data', ondata);
                input.removeListener('error', onerror);
                input.removeListener('end', onend);
            };

            input.on('data', ondata);
            input.on('end', onend);
            this.once('close', onSelfCloseWithoutTerminal);
            this._decoder = new StringDecoder('utf8');
        } else {
            const onSelfCloseWithTerminal = (): void => {
                input.removeListener('keypress', onkeypress);
                input.removeListener('error', onerror);
                input.removeListener('end', ontermend);
                if (output !== null && output !== undefined) {
                    output.removeListener('resize', onresize);
                }
            };

            emitKeypressEvents(input, this);

            // `input` usually refers to stdin
            input.on('keypress', onkeypress);
            input.on('end', ontermend);

            this._setRawMode(true);
            this.terminal = true;

            // Cursor position on the line.
            this.cursor = 0;

            if (output !== null && output !== undefined)
                output.on('resize', onresize);

            this.once('close', onSelfCloseWithTerminal);
        }

        if (signal) {
            const onAborted = (): void => { this.close(); };
            if (signal.aborted) {
                process.nextTick(onAborted);
            } else {
                signal.addEventListener('abort', onAborted, { once: true });
                this.once('close', () => { signal.removeEventListener('abort', onAborted); });
            }
        }

        // Current line
        this._setLine('');

        input.resume();
    }

    get columns(): number {
        if (this.output?.columns) return this.output.columns;
        return Infinity;
    }

    get history(): string[] { return this.historyManager.history; }
    set history(newHistory: string[]) { this.historyManager.history = newHistory; }
    get historyIndex(): number { return this.historyManager.index; }
    set historyIndex(historyIndex: number) { this.historyManager.index = historyIndex; }
    // Undefined while the history manager is being made (Node defines these
    // properties after it), so ReplHistory falls back to its default size.
    get historySize(): number { return this.historyManager?.size; }
    get isFlushing(): boolean { return this.historyManager.isFlushing; }

    // Sets the prompt written to the output.
    setPrompt(prompt: string): void {
        this._prompt = prompt;
    }

    // Returns the current prompt used by `rl.prompt()`.
    getPrompt(): string {
        return this._prompt;
    }

    _setRawMode(mode: boolean): boolean {
        const wasInRawMode = this.input.isRaw;

        if (typeof this.input.setRawMode === 'function') {
            this.input.setRawMode(mode);
        }

        return wasInRawMode;
    }

    // Writes the configured `prompt` to a new line in `output`.
    prompt(preserveCursor?: boolean): void {
        if (this.paused) this.resume();
        if (this.terminal && process.env.TERM !== 'dumb') {
            if (!preserveCursor) this.cursor = 0;
            this._refreshLine();
        } else {
            this._writeToOutput(this._prompt);
        }
    }

    _kmlQuestion(query: string, cb: (answer: string) => void): void {
        if (this.closed) {
            throw useAfterClose();
        }
        if (this._questionCallback) {
            this.prompt();
        } else {
            this._oldPrompt = this._prompt;
            this.setPrompt(query);
            this._questionCallback = cb;
            this.prompt();
        }
    }

    _kmlSetQuestionReject(reject: ((err: any) => void) | undefined): void {
        this.#questionReject = reject;
    }

    _setLine(line: string = ''): void {
        this.line = line;
        this.#isMultiline = line.includes('\n');
    }

    _onLine(line: string): void {
        if (this._questionCallback) {
            const cb = this._questionCallback;
            this._questionCallback = null;
            this.setPrompt(this._oldPrompt);
            cb(line);
        } else {
            this.emit('line', line);
        }
    }

    _beforeEdit(oldText: string, oldCursor: number): void {
        this._pushToUndoStack(oldText, oldCursor);
    }

    _kmlQuestionCancel(): void {
        if (this._questionCallback) {
            this._questionCallback = null;
            this.setPrompt(this._oldPrompt);
            this.clearLine();
        }
    }

    _writeToOutput(stringToWrite: string): void {
        if (typeof stringToWrite !== 'string') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "stringToWrite" argument must be of type string.' + received(stringToWrite));
        }

        if (this.output !== null && this.output !== undefined) {
            this.output.write(stringToWrite);
        }
    }

    _addHistory(): string {
        return this.historyManager.addHistory(this.#isMultiline, this.#lastCommandErrored);
    }

    _refreshLine(): void {
        // line length
        const line = this._prompt + this.line;
        const dispPos = this._getDisplayPos(line);
        const lineCols = dispPos.cols;
        const lineRows = dispPos.rows;

        // cursor position
        const cursorPos = this.getCursorPos();

        // First move to the bottom of the current line, based on cursor pos
        const prevRows = this.prevRows || 0;
        if (prevRows > 0) {
            moveCursor(this.output, 0, -prevRows);
        }

        // Cursor to left edge.
        cursorTo(this.output, 0);
        // erase data
        clearScreenDown(this.output);

        if (this.#isMultiline) {
            const lines = this.line.split('\n');
            // Write first line with normal prompt
            this._writeToOutput(this._prompt + lines[0]);

            // For continuation lines, add the "|" prefix
            for (let i = 1; i < lines.length; i++) {
                this._writeToOutput('\n' + kMultilinePrompt + lines[i]);
            }
        } else {
            // Write the prompt and the current buffer content.
            this._writeToOutput(line);
        }

        // Force terminal to allocate a new line
        if (lineCols === 0) {
            this._writeToOutput(' ');
        }

        // Move cursor to original position.
        cursorTo(this.output, cursorPos.cols);

        const diff = lineRows - cursorPos.rows;
        if (diff > 0) {
            moveCursor(this.output, 0, -diff);
        }

        this.prevRows = cursorPos.rows;
    }

    // Closes the Interface.
    close(): void {
        if (this.closed) return;
        this.pause();
        if (this.terminal) {
            this._setRawMode(false);
        }
        this.closed = true;
        this.emit('close');
    }

    // Pauses the `input` stream.
    pause(): this {
        if (this.closed) {
            throw useAfterClose();
        }
        if (this.paused) return this;
        this.input.pause();
        this.paused = true;
        this.emit('pause');
        return this;
    }

    // Resumes the `input` stream if paused.
    resume(): this {
        if (this.closed) {
            throw useAfterClose();
        }
        if (!this.paused) return this;
        this.input.resume();
        this.paused = false;
        this.emit('resume');
        return this;
    }

    // Writes either `data` or a `key` sequence identified by `key` to the
    // `output`.
    write(d: any, key?: Key): void {
        if (this.closed) {
            throw useAfterClose();
        }
        if (this.paused) this.resume();
        if (this.terminal) {
            this._ttyWrite(d, key);
        } else {
            this._normalWrite(d);
        }
    }

    _normalWrite(b: any): void {
        if (b === undefined) {
            return;
        }
        let string: string = this._decoder.write(b);
        if (this._sawReturnAt && Date.now() - this._sawReturnAt <= this.crlfDelay) {
            if (string.codePointAt(0) === 10) string = string.slice(1);
            this._sawReturnAt = 0;
        }

        // Run test() on the new string chunk, not on the entire line buffer.
        const newPartContainsEnding = nextLineEnding(string, 0);
        if (newPartContainsEnding !== -1) {
            if (this._line_buffer) {
                string = this._line_buffer + string;
                this._line_buffer = null;
            }
            this._sawReturnAt = string.endsWith('\r') ? Date.now() : 0;

            let start = 0;
            let ending = nextLineEnding(string, 0);
            while (ending !== -1) {
                this._onLine(string.slice(start, ending));
                start = lineEndingEnd(string, ending);
                ending = nextLineEnding(string, start);
            }
            // Either '' or (conceivably) the unfinished portion of the next line
            this._line_buffer = string.slice(start);
        } else if (string) {
            // No newlines this time, save what we have for next time
            if (this._line_buffer) {
                this._line_buffer += string;
            } else {
                this._line_buffer = string;
            }
        }
    }

    _insertString(c: string): void {
        this._beforeEdit(this.line, this.cursor);
        if (!this.isCompletionEnabled) {
            if (this.cursor < this.line.length) {
                const beg = this.line.slice(0, this.cursor);
                const end = this.line.slice(this.cursor, this.line.length);
                this.line = beg + c + end;
            } else {
                this.line += c;
            }
            this.cursor += c.length;
            this._writeToOutput(c);
            return;
        }
        if (this.cursor < this.line.length) {
            const beg = this.line.slice(0, this.cursor);
            const end = this.line.slice(this.cursor, this.line.length);
            this._setLine(beg + c + end);
            this.cursor += c.length;
            this._refreshLine();
        } else {
            const oldPos = this.getCursorPos();
            this.line += c;
            this.cursor += c.length;
            const newPos = this.getCursorPos();

            if (oldPos.rows < newPos.rows) {
                this._refreshLine();
            } else {
                this._writeToOutput(c);
            }
        }
    }

    async _tabComplete(lastKeypressWasTab: boolean): Promise<void> {
        this.pause();
        const string = this.line.slice(0, this.cursor);
        let value: any;
        try {
            value = await this.completer(string);
        } catch (err) {
            this._writeToOutput('Tab completion error: ' + inspect(err));
            return;
        } finally {
            this.resume();
        }
        this._tabCompleter(lastKeypressWasTab, value);
    }

    _tabCompleter(lastKeypressWasTab: boolean, result: any): void {
        // Result and the text that was completed.
        const completions: string[] = result[0];
        const completeOn: string = result[1];

        if (!completions || completions.length === 0) {
            return;
        }

        // If there is a common prefix to all matches, then apply that portion.
        const prefix = commonPrefix(completions.filter((e: string) => e !== ''));
        if (prefix.startsWith(completeOn) && prefix.length > completeOn.length) {
            this._insertString(prefix.slice(completeOn.length));
            return;
        } else if (!completeOn.startsWith(prefix)) {
            this._setLine(this.line.slice(0, this.cursor - completeOn.length) +
                prefix +
                this.line.slice(this.cursor, this.line.length));
            this.cursor = this.cursor - completeOn.length + prefix.length;
            this._refreshLine();
            return;
        }

        if (!lastKeypressWasTab) {
            return;
        }

        this._beforeEdit(this.line, this.cursor);

        // Apply/show completions.
        const completionsWidth = completions.map((e: string) => getStringWidth(e));
        const width = Math.max(...completionsWidth) + 2; // 2 space padding
        let maxColumns = Math.floor(this.columns / width) || 1;
        if (maxColumns === Infinity) {
            maxColumns = 1;
        }
        let output = '\r\n';
        let lineIndex = 0;
        let whitespace = 0;
        for (let i = 0; i < completions.length; i++) {
            const completion = completions[i];
            if (completion === '' || lineIndex === maxColumns) {
                output += '\r\n';
                lineIndex = 0;
                whitespace = 0;
            } else {
                output += ' '.repeat(whitespace);
            }
            if (completion !== '') {
                output += completion;
                whitespace = width - completionsWidth[i];
                lineIndex++;
            } else {
                output += '\r\n';
            }
        }
        if (lineIndex !== 0) {
            output += '\r\n\r\n';
        }
        this._writeToOutput(output);
        this._refreshLine();
    }

    _wordLeft(): void {
        if (this.cursor > 0) {
            // Reverse the string and match a word near beginning to avoid
            // quadratic time complexity
            const leading = this.line.slice(0, this.cursor);
            const reversed = Array.from(leading).reverse().join('');
            const match = /^\s*(?:[^\w\s]+|\w+)?/.exec(reversed) as string[];
            this._moveCursor(-match[0].length);
        }
    }

    _wordRight(): void {
        if (this.cursor < this.line.length) {
            const trailing = this.line.slice(this.cursor);
            const match = /^(?:\s+|[^\w\s]+|\w+)\s*/.exec(trailing) as string[];
            this._moveCursor(match[0].length);
        }
    }

    _deleteLeft(): void {
        if (this.cursor > 0 && this.line.length > 0) {
            this._beforeEdit(this.line, this.cursor);
            // The number of UTF-16 units comprising the character to the left
            const charSize = charLengthLeft(this.line, this.cursor);
            this.line = this.line.slice(0, this.cursor - charSize) + this.line.slice(this.cursor, this.line.length);

            this.cursor -= charSize;
            this._refreshLine();
        }
    }

    _deleteRight(): void {
        if (this.cursor < this.line.length) {
            this._beforeEdit(this.line, this.cursor);
            // The number of UTF-16 units comprising the character to the left
            const charSize = charLengthAt(this.line, this.cursor);
            this.line = this.line.slice(0, this.cursor) + this.line.slice(this.cursor + charSize, this.line.length);
            this._refreshLine();
        }
    }

    _deleteWordLeft(): void {
        if (this.cursor > 0) {
            this._beforeEdit(this.line, this.cursor);
            // Reverse the string and match a word near beginning to avoid
            // quadratic time complexity
            let leading = this.line.slice(0, this.cursor);
            const reversed = Array.from(leading).reverse().join('');
            const match = /^\s*(?:[^\w\s]+|\w+)?/.exec(reversed) as string[];
            leading = leading.slice(0, leading.length - match[0].length);
            this.line = leading + this.line.slice(this.cursor, this.line.length);
            this.cursor = leading.length;
            this._refreshLine();
        }
    }

    _deleteWordRight(): void {
        if (this.cursor < this.line.length) {
            this._beforeEdit(this.line, this.cursor);
            const trailing = this.line.slice(this.cursor);
            const match = /^(?:\s+|\W+|\w+)\s*/.exec(trailing) as string[];
            this.line = this.line.slice(0, this.cursor) + trailing.slice(match[0].length);
            this._refreshLine();
        }
    }

    _deleteLineLeft(): void {
        this._beforeEdit(this.line, this.cursor);
        const del = this.line.slice(0, this.cursor);
        this._setLine(this.line.slice(this.cursor));
        this.cursor = 0;
        this._pushToKillRing(del);
        this._refreshLine();
    }

    _deleteLineRight(): void {
        this._beforeEdit(this.line, this.cursor);
        const del = this.line.slice(this.cursor);
        this._setLine(this.line.slice(0, this.cursor));
        this._pushToKillRing(del);
        this._refreshLine();
    }

    _pushToKillRing(del: string): void {
        if (!del || del === this.#killRing[0]) return;
        this.#killRing.unshift(del);
        this.#killRingCursor = 0;
        while (this.#killRing.length > kMaxLengthOfKillRing)
            this.#killRing.pop();
    }

    _yank(): void {
        if (this.#killRing.length > 0) {
            this.#yanking = true;
            this._insertString(this.#killRing[this.#killRingCursor]);
        }
    }

    _yankPop(): void {
        if (!this.#yanking) {
            return;
        }
        if (this.#killRing.length > 1) {
            const lastYank = this.#killRing[this.#killRingCursor];
            this.#killRingCursor++;
            if (this.#killRingCursor >= this.#killRing.length) {
                this.#killRingCursor = 0;
            }
            const currentYank = this.#killRing[this.#killRingCursor];
            const head = this.line.slice(0, this.cursor - lastYank.length);
            const tail = this.line.slice(this.cursor);
            this._setLine(head + currentYank + tail);
            this.cursor = head.length + currentYank.length;
            this._refreshLine();
        }
    }

    _savePreviousState(): void {
        this.#previousLine = this.line;
        this.#previousCursor = this.cursor;
        this.#previousPrevRows = this.prevRows;
    }

    _restorePreviousState(): void {
        this._setLine(this.#previousLine);
        this.cursor = this.#previousCursor;
        this.prevRows = this.#previousPrevRows;
    }

    clearLine(): void {
        this._moveCursor(+Infinity);
        this._writeToOutput('\r\n');
        this._setLine('');
        this.cursor = 0;
        this.prevRows = 0;
    }

    _line(): void {
        this._savePreviousState();
        const line = this._addHistory();
        this.#undoStack = [];
        this.#redoStack = [];
        this.clearLine();
        this._onLine(line);
    }

    _pushToUndoStack(text: string, cursor: number): void {
        if (this.#undoStack.push({ text: text, cursor: cursor }) > kMaxUndoRedoStackSize) {
            this.#undoStack.shift();
        }
    }

    _undo(): void {
        if (this.#undoStack.length <= 0) return;

        this.#redoStack.push({ text: this.line, cursor: this.cursor });

        const entry = this.#undoStack.pop() as { text: string; cursor: number };
        this._setLine(entry.text);
        this.cursor = entry.cursor;

        this._refreshLine();
    }

    _redo(): void {
        if (this.#redoStack.length <= 0) return;

        this.#undoStack.push({ text: this.line, cursor: this.cursor });

        const entry = this.#redoStack.pop() as { text: string; cursor: number };
        this._setLine(entry.text);
        this.cursor = entry.cursor;

        this._refreshLine();
    }

    _multilineMove(direction: number, splitLines: string[], pos: CursorPos): void {
        const rows = pos.rows;
        const cols = pos.cols;
        const curr = splitLines[rows];
        const down = direction === 1;
        const adj = splitLines[rows + direction];
        const promptLen = kMultilinePrompt.length;
        let amountToMove: number;
        // Clamp distance to end of current + prompt + next/prev line + newline
        const clamp = down ?
            curr.length - cols + promptLen + adj.length + 1 :
            -cols + 1;
        const shouldClamp = cols > adj.length + 1;

        if (shouldClamp) {
            if (this.#previousCursorCols === -1) {
                this.#previousCursorCols = cols;
            }
            amountToMove = clamp;
        } else {
            if (down) {
                amountToMove = curr.length + 1;
            } else {
                amountToMove = -adj.length - 1;
            }
            if (this.#previousCursorCols !== -1) {
                if (this.#previousCursorCols <= adj.length) {
                    amountToMove += this.#previousCursorCols - cols;
                    this.#previousCursorCols = -1;
                } else {
                    amountToMove = clamp;
                }
            }
        }

        this._moveCursor(amountToMove);
    }

    _moveDownOrHistoryNext(): void {
        const cursorPos = this.getCursorPos();
        const splitLines = this.line.split('\n');
        if (this.#isMultiline && cursorPos.rows < splitLines.length - 1) {
            this._multilineMove(1, splitLines, cursorPos);
            return;
        }
        this.#previousCursorCols = -1;
        this._historyNext();
    }

    _historyNext(): void {
        if (!this.historyManager.canNavigateToNext()) { return; }

        this._beforeEdit(this.line, this.cursor);
        this._setLine(this.historyManager.navigateToNext(this.#substringSearch));
        this.cursor = this.line.length; // Set cursor to end of line.
        this._refreshLine();
    }

    _moveUpOrHistoryPrev(): void {
        const cursorPos = this.getCursorPos();
        if (this.#isMultiline && cursorPos.rows > 0) {
            const splitLines = this.line.split('\n');
            this._multilineMove(-1, splitLines, cursorPos);
            return;
        }
        this.#previousCursorCols = -1;
        this._historyPrev();
    }

    _historyPrev(): void {
        if (!this.historyManager.canNavigateToPrevious()) { return; }

        this._beforeEdit(this.line, this.cursor);
        this._setLine(this.historyManager.navigateToPrevious(this.#substringSearch));
        this.cursor = this.line.length; // Set cursor to end of line.
        this._refreshLine();
    }

    // Returns the last character's display position of the given string
    _getDisplayPos(str: string): CursorPos {
        let offset = 0;
        const col = this.columns;
        let rows = 0;
        str = stripVTControlCharacters(str);

        for (const char of str) {
            if (char === '\n') {
                // Rows must be incremented by 1 even if offset = 0 or col =
                // +Infinity.
                rows += Math.ceil(offset / col) || 1;
                // Only add prefix offset for continuation lines in user input
                // (not prompts)
                offset = this.#isMultiline ? kMultilinePrompt.length : 0;
                continue;
            }
            // Tabs must be aligned by an offset of the tab size.
            if (char === '\t') {
                offset += this.tabSize - (offset % this.tabSize);
                continue;
            }
            const width = getStringWidth(char, false /* stripVTControlCharacters */);
            if (width === 0 || width === 1) {
                offset += width;
            } else {
                // width === 2
                if ((offset + 1) % col === 0) {
                    offset++;
                }
                offset += 2;
            }
        }

        const cols = offset % col;
        rows += (offset - cols) / col;

        return { cols: cols, rows: rows };
    }

    // Returns the real position of the cursor in relation to the input
    // prompt + string.
    getCursorPos(): CursorPos {
        const strBeforeCursor = this._prompt + this.line.slice(0, this.cursor);

        return this._getDisplayPos(strBeforeCursor);
    }

    _getCursorPos(): CursorPos {
        return this.getCursorPos();
    }

    // This function moves cursor dx places to the right (-dx for left) and
    // refreshes the line if it is needed.
    _moveCursor(dx: number): void {
        if (dx === 0) {
            return;
        }
        const oldPos = this.getCursorPos();
        this.cursor += dx;

        // Bounds check
        if (this.cursor < 0) {
            this.cursor = 0;
        } else if (this.cursor > this.line.length) {
            this.cursor = this.line.length;
        }

        const newPos = this.getCursorPos();

        // Check if cursor stayed on the line.
        if (oldPos.rows === newPos.rows) {
            const diffWidth = newPos.cols - oldPos.cols;
            moveCursor(this.output, diffWidth, 0);
        } else {
            this._refreshLine();
        }
    }

    // Handle a write from the tty
    _ttyWrite(s: any, keyArg?: any): void {
        if (this.#dumb) {
            this._ttyWriteDumb(s, keyArg);
            return;
        }
        const previousKey = this._previousKey;
        const key: any = keyArg || {};
        this._previousKey = key;
        let shouldResetPreviousCursorCols = true;

        if (!key.meta || key.name !== 'y') {
            // Reset yanking state unless we are doing yank pop.
            this.#yanking = false;
        }

        // Activate or deactivate substring search.
        if ((key.name === 'up' || key.name === 'down') && !key.ctrl && !key.meta && !key.shift) {
            if (this.#substringSearch === null && !this.#isMultiline) {
                this.#substringSearch = this.line.slice(0, this.cursor);
            }
        } else if (this.#substringSearch !== null) {
            this.#substringSearch = null;
            // Reset the index in case there's no match.
            if (this.history.length === this.historyIndex) {
                this.historyIndex = -1;
            }
        }

        // Undo & Redo
        if (typeof key.sequence === 'string') {
            switch (key.sequence.codePointAt(0)) {
                case 0x1f:
                    this._undo();
                    return;
                case 0x1e:
                    this._redo();
                    return;
                default:
                    break;
            }
        }

        // Ignore escape key, fixes
        // https://github.com/nodejs/node-v0.x-archive/issues/2876.
        if (key.name === 'escape') return;

        if (key.ctrl && key.shift) {
            /* Control and shift pressed */
            switch (key.name) {
                // TODO(BridgeAR): The transmitted escape sequence is `\b` and
                // that is identical to <ctrl>-h. It should have a unique
                // escape sequence.
                case 'backspace':
                    this._deleteLineLeft();
                    break;

                case 'delete':
                    this._deleteLineRight();
                    break;
            }
        } else if (key.ctrl) {
            /* Control key pressed */

            switch (key.name) {
                case 'c':
                    if (this.listenerCount('SIGINT') > 0) {
                        this.emit('SIGINT');
                    } else {
                        // This readline instance is finished
                        this.close();
                        this.#questionReject?.(new AbortError('Aborted with Ctrl+C'));
                    }
                    break;

                case 'h': // delete left
                    this._deleteLeft();
                    break;

                case 'd': // delete right or EOF
                    if (this.cursor === 0 && this.line.length === 0) {
                        // This readline instance is finished
                        this.close();
                        this.#questionReject?.(new AbortError('Aborted with Ctrl+D'));
                    } else if (this.cursor < this.line.length) {
                        this._deleteRight();
                    }
                    break;

                case 'u': // Delete from current to start of line
                    this._deleteLineLeft();
                    break;

                case 'k': // Delete from current to end of line
                    this._deleteLineRight();
                    break;

                case 'a': // Go to the start of the line
                    this._moveCursor(-Infinity);
                    break;

                case 'e': // Go to the end of the line
                    this._moveCursor(+Infinity);
                    break;

                case 'b': // back one character
                    this._moveCursor(-charLengthLeft(this.line, this.cursor));
                    break;

                case 'f': // Forward one character
                    this._moveCursor(+charLengthAt(this.line, this.cursor));
                    break;

                case 'l': // Clear the whole screen
                    cursorTo(this.output, 0, 0);
                    clearScreenDown(this.output);
                    this._refreshLine();
                    break;

                case 'n': // next history item
                    this._historyNext();
                    break;

                case 'p': // Previous history item
                    this._historyPrev();
                    break;

                case 'y': // Yank killed string
                    this._yank();
                    break;

                case 'z':
                    if (process.platform === 'win32') break;
                    if (this.listenerCount('SIGTSTP') > 0) {
                        this.emit('SIGTSTP');
                    } else {
                        process.once('SIGCONT', () => {
                            // Don't raise events if stream has already been
                            // abandoned.
                            if (!this.paused) {
                                // Stream must be paused and resumed after SIGCONT
                                // to catch SIGINT, SIGTSTP, and EOF.
                                this.pause();
                                this.emit('SIGCONT');
                            }
                            // Explicitly re-enable "raw mode" and move the cursor
                            // to the correct position.
                            // See https://github.com/joyent/node/issues/3295.
                            this._setRawMode(true);
                            this._refreshLine();
                        });
                        this._setRawMode(false);
                        process.kill(process.pid, 'SIGTSTP');
                    }
                    break;

                case 'w': // Delete backwards to a word boundary
                case 'backspace':
                    this._deleteWordLeft();
                    break;

                case 'delete': // Delete forward to a word boundary
                    this._deleteWordRight();
                    break;

                case 'left':
                    this._wordLeft();
                    break;

                case 'right':
                    this._wordRight();
                    break;
            }
        } else if (key.meta) {
            /* Meta key pressed */

            switch (key.name) {
                case 'b': // backward word
                    this._wordLeft();
                    break;

                case 'f': // forward word
                    this._wordRight();
                    break;

                case 'd': // delete forward word
                case 'delete':
                    this._deleteWordRight();
                    break;

                case 'backspace': // Delete backwards to a word boundary
                    this._deleteWordLeft();
                    break;

                case 'y': // Doing yank pop
                    this._yankPop();
                    break;
            }
        } else {
            /* No modifier keys used */

            // \r bookkeeping is only relevant if a \n comes right after.
            if (this._sawReturnAt && key.name !== 'enter') this._sawReturnAt = 0;

            let insert = false;
            switch (key.name) {
                case 'return': // Carriage return, i.e. \r
                    this._sawReturnAt = Date.now();
                    this._line();
                    break;

                case 'enter':
                    // When key interval > crlfDelay
                    if (this._sawReturnAt === 0 || Date.now() - this._sawReturnAt > this.crlfDelay) {
                        this._line();
                    }
                    this._sawReturnAt = 0;
                    break;

                case 'backspace':
                    this._deleteLeft();
                    break;

                case 'delete':
                    this._deleteRight();
                    break;

                case 'left':
                    // Obtain the code point to the left
                    this._moveCursor(-charLengthLeft(this.line, this.cursor));
                    break;

                case 'right':
                    this._moveCursor(+charLengthAt(this.line, this.cursor));
                    break;

                case 'home':
                    this._moveCursor(-Infinity);
                    break;

                case 'end':
                    this._moveCursor(+Infinity);
                    break;

                case 'up':
                    shouldResetPreviousCursorCols = false;
                    this._moveUpOrHistoryPrev();
                    break;

                case 'down':
                    shouldResetPreviousCursorCols = false;
                    this._moveDownOrHistoryNext();
                    break;

                case 'tab':
                    // If tab completion enabled, do that...
                    if (typeof this.completer === 'function' && this.isCompletionEnabled) {
                        const lastKeypressWasTab = previousKey && previousKey.name === 'tab';
                        this._tabComplete(lastKeypressWasTab);
                        break;
                    }
                    insert = true;
                    break;

                default:
                    insert = true;
            }
            if (insert && typeof s === 'string' && s) {
                // Keep track of the end of the last match.
                let lastIndex = 0;
                let ending = nextLineEnding(s, 0);
                while (ending !== -1) {
                    this._insertString(s.slice(lastIndex, ending));
                    lastIndex = lineEndingEnd(s, ending);
                    this._line();
                    ending = nextLineEnding(s, lastIndex);
                }
                // This ensures that the last line is written if it doesn't end
                // in a newline. Note that the last line may be the first line,
                // in which case this still works.
                this._insertString(s.slice(lastIndex));
            }
        }
        if (shouldResetPreviousCursorCols) {
            this.#previousCursorCols = -1;
        }
    }

    // readline's Interface under TERM=dumb writes keys through _ttyWriteDumb.
    _kmlUseDumbTtyWrite(): void {
        this.#dumb = true;
    }

    // readline's `_ttyWrite` for TERM=dumb: no line editing.
    _ttyWriteDumb(s: any, keyArg?: any): void {
        const key: any = keyArg || {};
        if (key.name === 'escape') return;

        if (this._sawReturnAt && key.name !== 'enter')
            this._sawReturnAt = 0;

        if (key.ctrl) {
            if (key.name === 'c') {
                if (this.listenerCount('SIGINT') > 0) {
                    this.emit('SIGINT');
                } else {
                    // This readline instance is finished
                    this.close();
                }

                return;
            } else if (key.name === 'd') {
                this.close();
                return;
            }
        }

        switch (key.name) {
            case 'return': // Carriage return, i.e. \r
                this._sawReturnAt = Date.now();
                this._line();
                break;

            case 'enter':
                // When key interval > crlfDelay
                if (this._sawReturnAt === 0 || Date.now() - this._sawReturnAt > this.crlfDelay) {
                    this._line();
                }
                this._sawReturnAt = 0;
                break;

            default:
                if (typeof s === 'string' && s) {
                    this.line += s;
                    this.cursor += s.length;
                    this._writeToOutput(s);
                }
        }
    }

    // An AsyncIterator over each line in the input stream, as a string.
    [Symbol.asyncIterator](): any {
        if (this.#lineObjectStream === undefined) {
            const opts: any = { close: ['close'], highWaterMark: 1024, _kmlFirstEventParam: true };
            this.#lineObjectStream = on(this, 'line', opts);
        }
        return this.#lineObjectStream;
    }

    [Symbol.dispose](): void {
        this.close();
    }
}
