// Node's `console` module (lib/console.js, lib/internal/console/
// constructor.js): the global console as its default export, and the
// Console class writing to the streams it is given. A Console's methods
// are bound to it, as Node binds them, so `const { log } = c` works.

import { formatWithOptions } from 'util';
import { inspect } from './internal_util_inspect';
import { table as renderTable } from './internal_console_table';
import { NodeTypeError } from './internal_errors';
import { _kmlConsole } from './internal_ns_console';

function noop(): void {}

function writable(s: any): boolean {
    return s !== null && s !== undefined && typeof s.write === 'function';
}

export class Console {
    #stdout: any;
    #stderr: any;
    #ignoreErrors: boolean;
    #inspectOptions: any;
    #indent: string;
    #groupIndentation: number;
    #counts: Map<string, number>;
    #timers: Map<string, number>;

    log: (...args: any[]) => void;
    info: (...args: any[]) => void;
    debug: (...args: any[]) => void;
    warn: (...args: any[]) => void;
    error: (...args: any[]) => void;
    dir: (obj?: any, options?: any) => void;
    dirxml: (...args: any[]) => void;
    trace: (...args: any[]) => void;
    assert: (value?: any, ...args: any[]) => void;
    count: (label?: any) => void;
    countReset: (label?: any) => void;
    group: (...args: any[]) => void;
    groupCollapsed: (...args: any[]) => void;
    groupEnd: () => void;
    time: (label?: any) => void;
    timeEnd: (label?: any) => void;
    timeLog: (label?: any, ...args: any[]) => void;
    table: (data: any, properties?: any) => void;

    constructor(stdout?: any, stderr?: any, ignoreErrors?: any) {
        let options: any = stdout;
        if (!options || typeof options.write === 'function') {
            options = { stdout, stderr, ignoreErrors };
        }
        const out = options.stdout;
        const err = options.stderr === undefined ? out : options.stderr;
        if (!writable(out)) {
            throw NodeTypeError('ERR_CONSOLE_WRITABLE_STREAM', 'Console expects a writable stream instance for stdout');
        }
        if (!writable(err)) {
            throw NodeTypeError('ERR_CONSOLE_WRITABLE_STREAM', 'Console expects a writable stream instance for stderr');
        }
        this.#stdout = out;
        this.#stderr = err;
        this.#ignoreErrors = options.ignoreErrors === undefined ? true : !!options.ignoreErrors;
        this.#inspectOptions = options.inspectOptions === undefined ? {} : options.inspectOptions;
        this.#groupIndentation = options.groupIndentation === undefined ? 2 : options.groupIndentation;
        this.#indent = '';
        this.#counts = new Map<string, number>();
        this.#timers = new Map<string, number>();

        this.log = (...args: any[]): void => this.#write(true, this.#format(args));
        this.info = this.log;
        this.debug = this.log;
        this.dirxml = this.log;
        this.warn = (...args: any[]): void => this.#write(false, this.#format(args));
        this.error = this.warn;
        this.dir = (obj?: any, opts?: any): void => {
            const o: any = Object.assign({ customInspect: false }, this.#inspectOptions, opts);
            this.#write(true, inspect(obj, o));
        };
        this.trace = (...args: any[]): void => {
            const e: any = new Error(this.#format(args));
            e.name = 'Trace';
            const stack: string = String(e.stack);
            this.#write(false, stack);
        };
        this.assert = (value?: any, ...args: any[]): void => {
            if (!value) {
                if (args.length > 0 && typeof args[0] === 'string') {
                    args[0] = 'Assertion failed: ' + (args[0] as string);
                } else {
                    args.unshift('Assertion failed');
                }
                this.warn(...args);
            }
        };
        this.count = (label?: any): void => {
            const l = label === undefined ? 'default' : String(label);
            const n = (this.#counts.get(l) ?? 0) + 1;
            this.#counts.set(l, n);
            this.log(l + ': ' + n);
        };
        this.countReset = (label?: any): void => {
            const l = label === undefined ? 'default' : String(label);
            if (!this.#counts.has(l)) {
                process.emitWarning("Count for '" + l + "' does not exist");
                return;
            }
            this.#counts.delete(l);
        };
        this.group = (...args: any[]): void => {
            if (args.length > 0) this.log(...args);
            this.#indent += ' '.repeat(this.#groupIndentation);
        };
        this.groupCollapsed = this.group;
        this.groupEnd = (): void => {
            this.#indent = this.#indent.slice(0, this.#indent.length - this.#groupIndentation);
        };
        this.time = (label?: any): void => {
            const l = label === undefined ? 'default' : String(label);
            if (this.#timers.has(l)) {
                process.emitWarning("Label '" + l + "' already exists for console.time()");
                return;
            }
            this.#timers.set(l, performance.now());
        };
        this.timeEnd = (label?: any): void => {
            const l = label === undefined ? 'default' : String(label);
            if (this.#timeLog('timeEnd', l, [])) this.#timers.delete(l);
        };
        this.timeLog = (label?: any, ...args: any[]): void => {
            const l = label === undefined ? 'default' : String(label);
            this.#timeLog('timeLog', l, args);
        };
        this.table = (data: any, properties?: any): void => {
            renderTable(data, properties, (s: any) => this.log(s));
        };
    }

    #format(args: any[]): string {
        return formatWithOptions(this.#inspectOptions, ...args);
    }

    #timeLog(name: string, label: string, args: any[]): boolean {
        const start = this.#timers.get(label);
        if (start === undefined) {
            process.emitWarning("No such label '" + label + "' for console." + name + '()');
            return false;
        }
        const ms = performance.now() - start;
        const shown = ms >= 1000 ? (ms / 1000).toFixed(3) + 's' : ms.toFixed(3) + 'ms';
        this.log(label + ': ' + shown, ...args);
        return true;
    }

    // Node's kWriteToConsole: the group indent on every line; with
    // ignoreErrors, a write's error (thrown, or emitted with no listener)
    // is swallowed.
    #write(useStdout: boolean, text: string): void {
        const stream = useStdout ? this.#stdout : this.#stderr;
        let s = text;
        if (this.#indent.length > 0) {
            s = this.#indent + s.split('\n').join('\n' + this.#indent);
        }
        s += '\n';
        if (!this.#ignoreErrors) {
            stream.write(s);
            return;
        }
        const added = typeof stream.listenerCount === 'function' && stream.listenerCount('error') === 0;
        try {
            if (added) stream.once('error', noop);
            stream.write(s, (err: any): void => {
                if (err !== null && err !== undefined && typeof stream.listenerCount === 'function' && stream.listenerCount('error') === 0) {
                    stream.once('error', noop);
                }
            });
        } catch (e: any) {
            if (e instanceof RangeError && String(e.message).includes('call stack')) throw e;
        } finally {
            if (added) stream.removeListener('error', noop);
        }
    }
}

export default _kmlConsole();
