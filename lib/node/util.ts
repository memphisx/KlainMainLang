// Node's `util` (lib/util.js), with format and formatWithOptions from
// lib/internal/util/inspect.js. inspect renders through the compiler's
// inspector (__kml_native.inspectWith), which takes the layout options.
//
// kml:default-namespace — `import util from 'util'` reads this module's
// exports, as Node's module.exports carries them.

import { isDeepStrictEqual as deepStrictEqual } from './internal_util_comparisons';
import { getColorDepth } from './internal_tty';
import * as types_ from './util_types';

export { promisify } from './internal_util';
export { inspect } from './internal_util_inspect';
export { TextEncoder, TextDecoder } from './kml_encoding';
export { parseArgs } from './internal_util_parse_args';
export type { ParseArgsConfig, ParseArgsOptionConfig } from './internal_util_parse_args';
import { inspect, inspectOption, colorCodes } from './internal_util_inspect';
import { NodeTypeError, NodeRangeError } from './internal_errors';
export type { InspectOptions } from './internal_util_inspect';

// The " Received …" tail of Node's ERR_INVALID_ARG_TYPE messages.
function received(value: any): string {
    if (value === null || value === undefined) {
        return ' Received ' + String(value);
    }
    if (typeof value === 'function') {
        return ' Received function ' + (value as Function).name;
    }
    if (typeof value === 'object') {
        return ' Received ' + inspect(value, { depth: -1 });
    }
    let shown = inspect(value, { colors: false });
    if (shown.length > 28) {
        shown = shown.slice(0, 25) + '...';
    }
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

// A number as %d and %i show it: -0 keeps its sign, and numericSeparator
// groups the integer digits.
function formatNumber(n: number, numericSeparator: boolean): string {
    if (Object.is(n, -0)) return '-0';
    const s = String(n);
    if (!numericSeparator || !Number.isFinite(n)) return s;
    const neg = s.startsWith('-');
    const body = neg ? s.slice(1) : s;
    const dot = body.indexOf('.');
    const int = dot === -1 ? body : body.slice(0, dot);
    const frac = dot === -1 ? '' : body.slice(dot);
    let grouped = '';
    for (let i = 0; i < int.length; i++) {
        if (i > 0 && (int.length - i) % 3 === 0) grouped += '_';
        grouped += int[i];
    }
    return (neg ? '-' : '') + grouped + frac;
}

function formatBigInt(n: bigint, numericSeparator: boolean): string {
    return formatNumberDigits(String(n), numericSeparator) + 'n';
}

function formatNumberDigits(s: string, numericSeparator: boolean): string {
    if (!numericSeparator) return s;
    const neg = s.startsWith('-');
    const body = neg ? s.slice(1) : s;
    let grouped = '';
    for (let i = 0; i < body.length; i++) {
        if (i > 0 && (body.length - i) % 3 === 0) grouped += '_';
        grouped += body[i];
    }
    return (neg ? '-' : '') + grouped;
}

// Whether value's toString is a builtin one (so %s inspects it) rather than
// one the program declared (so %s calls it).
function hasBuiltInToString(value: any): boolean {
    if (typeof value[Symbol.toPrimitive] === 'function') return false;
    if (Object.prototype.hasOwnProperty.call(value, 'toString')) return false;
    const key = __kml_native.protoKey(value);
    if (key.startsWith('C:') || key.startsWith('P:')) {
        return typeof value.toString !== 'function';
    }
    return true;
}

function tryStringify(arg: any): string {
    try {
        return JSON.stringify(arg);
    } catch (err: any) {
        if (err instanceof TypeError && typeof err.message === 'string' && err.message.includes('circular structure')) {
            return '[Circular]';
        }
        throw err;
    }
}

function formatWithOptionsInternal(inspectOptions: any, args: any[]): string {
    const first = args[0];
    let a = 0;
    let str = '';
    let join = '';
    const numericSeparator = !!inspectOption(inspectOptions, 'numericSeparator');

    if (typeof first === 'string') {
        if (args.length === 1) {
            return first;
        }
        let tempStr = '';
        let lastPos = 0;

        for (let i = 0; i < first.length - 1; i++) {
            if (first.charCodeAt(i) === 37) { // '%'
                i++;
                const nextChar = first.charCodeAt(i);
                if (a + 1 !== args.length) {
                    let matched = true;
                    switch (nextChar) {
                        case 115: { // 's'
                            a++;
                            const tempArg = args[a];
                            if (typeof tempArg === 'number') {
                                tempStr = formatNumber(tempArg, numericSeparator);
                            } else if (typeof tempArg === 'bigint') {
                                tempStr = formatBigInt(tempArg, numericSeparator);
                            } else if (typeof tempArg !== 'object' || tempArg === null || !hasBuiltInToString(tempArg)) {
                                tempStr = String(tempArg);
                            } else {
                                tempStr = inspect(tempArg, { ...inspectOptions, compact: 3, colors: false, depth: 0 });
                            }
                            break;
                        }
                        case 106: // 'j'
                            a++;
                            tempStr = tryStringify(args[a]);
                            break;
                        case 100: { // 'd'
                            a++;
                            const tempNum = args[a];
                            if (typeof tempNum === 'bigint') {
                                tempStr = formatBigInt(tempNum, numericSeparator);
                            } else if (typeof tempNum === 'symbol') {
                                tempStr = 'NaN';
                            } else {
                                tempStr = formatNumber(Number(tempNum), numericSeparator);
                            }
                            break;
                        }
                        case 79: // 'O'
                            a++;
                            tempStr = inspect(args[a], inspectOptions);
                            break;
                        case 111: // 'o'
                            a++;
                            tempStr = inspect(args[a], { ...inspectOptions, showHidden: true, showProxy: true, depth: 4 });
                            break;
                        case 105: { // 'i'
                            a++;
                            const tempInteger = args[a];
                            if (typeof tempInteger === 'bigint') {
                                tempStr = formatBigInt(tempInteger, numericSeparator);
                            } else if (typeof tempInteger === 'symbol') {
                                tempStr = 'NaN';
                            } else {
                                tempStr = formatNumber(Number.parseInt(String(tempInteger)), numericSeparator);
                            }
                            break;
                        }
                        case 102: { // 'f'
                            a++;
                            const tempFloat = args[a];
                            if (typeof tempFloat === 'symbol') {
                                tempStr = 'NaN';
                            } else {
                                tempStr = formatNumber(Number.parseFloat(String(tempFloat)), numericSeparator);
                            }
                            break;
                        }
                        case 99: // 'c'
                            a += 1;
                            tempStr = '';
                            break;
                        case 37: // '%'
                            str += first.slice(lastPos, i);
                            lastPos = i + 1;
                            matched = false;
                            continue;
                        default: // Any other character is not a correct placeholder
                            matched = false;
                            continue;
                    }
                    if (!matched) {
                        continue;
                    }
                    if (lastPos !== i - 1) {
                        str += first.slice(lastPos, i - 1);
                    }
                    str += tempStr;
                    lastPos = i + 1;
                } else if (nextChar === 37) {
                    str += first.slice(lastPos, i);
                    lastPos = i + 1;
                }
            }
        }
        if (lastPos !== 0) {
            a++;
            join = ' ';
            if (lastPos < first.length) {
                str += first.slice(lastPos);
            }
        }
    }

    while (a < args.length) {
        const value = args[a];
        str += join;
        str += typeof value !== 'string' ? inspect(value, inspectOptions) : value;
        join = ' ';
        a++;
    }
    return str;
}

/**
 * A printf-like format: %s, %d, %i, %f, %j, %o, %O, %c and %%.
 */
export function format(format?: any, ...param: any[]): string;
export function format(...args: any[]): string {
    return formatWithOptionsInternal(undefined, args);
}

/**
 * format, with the options inspect takes for its %o, %O and remaining
 * arguments.
 */
export function formatWithOptions(inspectOptions: InspectOptions, format?: any, ...param: any[]): string;
export function formatWithOptions(inspectOptions: any, ...args: any[]): string {
    if (inspectOptions === null || typeof inspectOptions !== 'object') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "inspectOptions" argument must be of type object.' + received(inspectOptions));
    }
    return formatWithOptionsInternal(inspectOptions, args);
}

/**
 * Deep strict equality, as assert.deepStrictEqual decides it.
 */
export function isDeepStrictEqual(val1: unknown, val2: unknown, skipPrototype?: boolean): boolean {
    return deepStrictEqual(val1, val2, skipPrototype);
}

/**
 * Takes an async function (or one returning a Promise) and returns a
 * function in the error-first callback style.
 */
export function callbackify(original: (...args: any[]) => Promise<any>): (...args: any[]) => void {
    const fn: any = original;
    if (typeof fn !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "original" argument must be of type function.' + received(fn));
    }
    return (...args: any[]): void => {
        const maybeCb: any = args.pop();
        if (typeof maybeCb !== 'function') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "last argument" argument must be of type function.' + received(maybeCb));
        }
        const p: Promise<any> = fn(...args);
        p.then(
            (ret: any) => process.nextTick(() => maybeCb(null, ret)),
            (rej: any) => process.nextTick(() => callbackifyOnRejected(rej, maybeCb)),
        );
    };
}

class FalsyValueRejection extends Error {
    code = 'ERR_FALSY_VALUE_REJECTION';
    reason: any;
    constructor(reason: any) {
        super('Promise was rejected with falsy value');
        this.reason = reason;
    }
}

function callbackifyOnRejected(reason: any, cb: any): void {
    // `!reason` guard inspired by bluebird (Ref: https://goo.gl/t5IS6M).
    // Because `null` is a special error value in callbacks which means "no
    // error occurred", we error-wrap so the callback consumer can distinguish
    // between "the promise rejected with null" or "the promise fulfilled with
    // undefined".
    if (!reason) {
        reason = new FalsyValueRejection(reason);
    }
    cb(reason);
}

const deprecationsWarned: { [code: string]: boolean } = {};
const deprecationsWarnedNoCode = new Set<string>();

/**
 * Wraps fn so its first call emits a DeprecationWarning (once per code).
 */
export function deprecate<T extends Function>(fn: T, msg: string, code?: string): T;
export function deprecate(fn: any, msg: string, code?: string): any {
    const f: any = fn;
    if (code !== undefined && typeof code !== 'string') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "code" argument must be of type string.' + received(code));
    }
    let warned = false;
    const deprecated: any = (...args: any[]): any => {
        const proc: any = process;
        if (!proc.noDeprecation && !warned) {
            warned = true;
            if (code !== undefined) {
                if (deprecationsWarned[code] !== true) {
                    deprecationsWarned[code] = true;
                    process.emitWarning(msg, 'DeprecationWarning', code);
                }
            } else {
                if (!deprecationsWarnedNoCode.has(msg)) {
                    deprecationsWarnedNoCode.add(msg);
                    process.emitWarning(msg, 'DeprecationWarning');
                }
            }
        }
        return f(...args);
    };
    return deprecated;
}

let debugSections: string[] = [];
let debugSectionsRead = false;

// The NODE_DEBUG sections, as patterns: `*` matches anything.
function debugEnabled(section: string): boolean {
    if (!debugSectionsRead) {
        debugSectionsRead = true;
        const env = process.env.NODE_DEBUG;
        debugSections = env === undefined || env === '' ? [] : env.split(/[\s,]+/).filter((s) => s !== '').map((s) => s.toUpperCase());
    }
    const upper = section.toUpperCase();
    for (const pattern of debugSections) {
        if (pattern === upper) return true;
        if (pattern.includes('*')) {
            const re = new RegExp('^' + pattern.replace(/[|\\{}()[\]^$+?.]/g, '\\$&').replace(/\*/g, '.*') + '$', 'i');
            if (re.test(upper)) return true;
        }
    }
    return false;
}

/**
 * A logger writing to stderr when NODE_DEBUG names section: `SECTION
 * <pid>: <message>`.
 */
export function debuglog(section: string, callback?: (fn: (msg: string, ...param: unknown[]) => void) => void): any {
    const enabled = debugEnabled(section);
    const upper = section.toUpperCase();
    const logger: any = (...args: any[]): void => {
        if (enabled) {
            process.stderr.write(format('%s %d: %s', upper, process.pid, formatWithOptionsInternal(undefined, args)) + '\n');
        }
    };
    logger.enabled = enabled;
    if (callback !== undefined) {
        callback(logger);
    }
    return logger;
}

export const debug = debuglog;

// ANSI escape sequences (Node's `ansi` pattern).
const ansi = /[\u001B\u009B][[\]()#;?]*(?:(?:(?:(?:;[-a-zA-Z\d\/\#&.:=?%@~_]+)*|[a-zA-Z\d]+(?:;[-a-zA-Z\d\/\#&.:=?%@~_]*)*)?(?:\u0007|\u001B\|\u009C))|(?:(?:\d{1,4}(?:;\d{0,4})*)?[\dA-PR-TZcf-ntqry=><~]))/g;

/**
 * Removes ANSI escape codes from str.
 */
export function stripVTControlCharacters(str: string): string {
    if (typeof str !== 'string') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "str" argument must be of type string.' + received(str));
    }
    if (str.indexOf('\u001B') === -1 && str.indexOf('\u009B') === -1) {
        return str;
    }
    return str.replace(ansi, '');
}

/**
 * The string with every lone surrogate replaced by U+FFFD. Strings here are
 * UTF-8, which holds no lone surrogate: the string itself.
 */
export function toUSVString(input: string): string {
    return `${input}`;
}

function validateErrno(err: any): void {
    if (typeof err !== 'number') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "err" argument must be of type number.' + received(err));
    }
    if (err >= 0 || !Number.isSafeInteger(err)) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "err" is out of range. It must be a negative integer. Received ' + String(err));
    }
}

// libuv's own error numbers, the same on every platform: the getaddrinfo
// codes and the few that are no errno (uv-common.c).
const uvOwnErrors: [number, string, string][] = [
    [-3000, 'EAI_ADDRFAMILY', 'address family not supported'],
    [-3001, 'EAI_AGAIN', 'temporary failure'],
    [-3002, 'EAI_BADFLAGS', 'bad ai_flags value'],
    [-3003, 'EAI_CANCELED', 'request canceled'],
    [-3004, 'EAI_FAIL', 'permanent failure'],
    [-3005, 'EAI_FAMILY', 'ai_family not supported'],
    [-3006, 'EAI_MEMORY', 'out of memory'],
    [-3007, 'EAI_NODATA', 'no address'],
    [-3008, 'EAI_NONAME', 'unknown node or service'],
    [-3009, 'EAI_OVERFLOW', 'argument buffer overflow'],
    [-3010, 'EAI_SERVICE', 'service not available for socket type'],
    [-3011, 'EAI_SOCKTYPE', 'socket type not supported'],
    [-3013, 'EAI_BADHINTS', 'invalid value for hints'],
    [-3014, 'EAI_PROTOCOL', 'resolved protocol is unknown'],
    [-4080, 'ECHARSET', 'invalid Unicode character'],
    [-4095, 'EOF', 'end of file'],
];

function uvOwnError(err: number): [number, string, string] | undefined {
    for (const e of uvOwnErrors) {
        if (e[0] === err) return e;
    }
    return undefined;
}

/**
 * The name of a system error number (`-2` is `'ENOENT'`).
 */
export function getSystemErrorName(err: number): string {
    validateErrno(err);
    const own = uvOwnError(err);
    if (own !== undefined) return own[1];
    return __kml_native.errnoName(-err);
}

/**
 * The message of a system error number.
 */
export function getSystemErrorMessage(err: number): string {
    validateErrno(err);
    const own = uvOwnError(err);
    if (own !== undefined) return own[2];
    return __kml_native.errnoDesc(-err);
}

/**
 * Every system error number, mapped to its name and message.
 */
export function getSystemErrorMap(): Map<number, [string, string]> {
    const out = new Map<number, [string, string]>();
    for (let e = 1; e < 200; e++) {
        const name = __kml_native.errnoName(e);
        if (name !== 'UNKNOWN') {
            out.set(-e, [name, __kml_native.errnoDesc(e)]);
        }
    }
    for (const own of uvOwnErrors) {
        out.set(own[0], [own[1], own[2]]);
    }
    return out;
}

const hexColorRegExp = /^#(?:[0-9a-fA-F]{3}){1,2}$/;

// A stream shows colours when it is a terminal and its environment allows
// them (FORCE_COLOR forces them).
function streamColorizes(stream: any): boolean {
    if (process.env.FORCE_COLOR !== undefined) {
        return getColorDepth() > 2;
    }
    return stream !== undefined && stream !== null && stream.isTTY === true && getColorDepth() > 2;
}

/**
 * text wrapped in the escape codes of format (a name from inspect.colors,
 * a `#rgb` colour, or an array of them); plain when the stream (stdout by
 * default) does not show colours.
 */
export function styleText(format: string | string[], text: string, options?: { validateStream?: boolean; stream?: any }): string {
    if (typeof text !== 'string') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "text" argument must be of type string.' + received(text));
    }
    const o: any = options;
    const validateStream = o === undefined || o.validateStream === undefined ? true : o.validateStream;
    let skipColorize = false;
    if (validateStream) {
        const stream = o !== undefined && o.stream !== undefined ? o.stream : process.stdout;
        skipColorize = !streamColorizes(stream);
    }
    const formats: string[] = Array.isArray(format) ? format : [format];
    let openCodes = '';
    let closeCodes = '';
    let processed = text;
    for (const key of formats) {
        if (key === 'none') continue;
        if (typeof key === 'string' && key[0] === '#') {
            if (hexColorRegExp.exec(key) === null) {
                throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'format' must be a valid hex color (#RGB or #RRGGBB). Received " + inspect(key));
            }
            if (skipColorize) continue;
            const hex = key.length === 4 ? key[1] + key[1] + key[2] + key[2] + key[3] + key[3] : key.slice(1);
            const r = parseInt(hex.slice(0, 2), 16);
            const g = parseInt(hex.slice(2, 4), 16);
            const b = parseInt(hex.slice(4, 6), 16);
            const open = `\u001b[38;2;${r};${g};${b}m`;
            const close = '\u001b[39m';
            openCodes += open;
            closeCodes = close + closeCodes;
            processed = processed.split(close).join(close + open);
            continue;
        }
        const codes = colorCodes(key);
        if (codes === undefined) {
            const names = Object.keys(inspect.colors).map((n) => `'${n}'`).join(', ');
            throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'format' must be one of: " + names + '. Received ' + inspect(key));
        }
        const open = `\u001b[${codes[0]}m`;
        const close = `\u001b[${codes[1]}m`;
        openCodes += open;
        closeCodes = close + closeCodes;
        const keepClose = codes[0] === 1 || codes[0] === 2;
        processed = processed.split(close).join(keepClose ? close + open : open);
    }
    if (skipColorize) return text;
    return `${openCodes}${processed}${closeCodes}`;
}

/**
 * Deprecated: Array.isArray.
 */
export const isArray = deprecate((object: unknown): boolean => Array.isArray(object), 'The `util.isArray` API is deprecated. Please use `Array.isArray()` instead.', 'DEP0044');

/**
 * Resolves when signal aborts.
 */
export function aborted(signal: AbortSignal, resource: any): Promise<void> {
    if (resource === undefined || resource === null || (typeof resource !== 'object' && typeof resource !== 'function')) {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "resource" argument must be of type object.' + received(resource));
    }
    return new Promise<void>((resolve) => {
        if (signal.aborted) {
            resolve();
            return;
        }
        signal.addEventListener('abort', () => resolve(), { once: true });
    });
}

// util.types is util/types.
export const types = {
    isAnyArrayBuffer: types_.isAnyArrayBuffer,
    isArrayBuffer: types_.isArrayBuffer,
    isArrayBufferView: types_.isArrayBufferView,
    isArgumentsObject: types_.isArgumentsObject,
    isAsyncFunction: types_.isAsyncFunction,
    isBigInt64Array: types_.isBigInt64Array,
    isBigUint64Array: types_.isBigUint64Array,
    isBooleanObject: types_.isBooleanObject,
    isBoxedPrimitive: types_.isBoxedPrimitive,
    isDataView: types_.isDataView,
    isDate: types_.isDate,
    isExternal: types_.isExternal,
    isFloat16Array: types_.isFloat16Array,
    isFloat32Array: types_.isFloat32Array,
    isFloat64Array: types_.isFloat64Array,
    isGeneratorFunction: types_.isGeneratorFunction,
    isGeneratorObject: types_.isGeneratorObject,
    isInt8Array: types_.isInt8Array,
    isInt16Array: types_.isInt16Array,
    isInt32Array: types_.isInt32Array,
    isMap: types_.isMap,
    isMapIterator: types_.isMapIterator,
    isModuleNamespaceObject: types_.isModuleNamespaceObject,
    isNativeError: types_.isNativeError,
    isNumberObject: types_.isNumberObject,
    isPromise: types_.isPromise,
    isProxy: types_.isProxy,
    isRegExp: types_.isRegExp,
    isSet: types_.isSet,
    isSetIterator: types_.isSetIterator,
    isSharedArrayBuffer: types_.isSharedArrayBuffer,
    isStringObject: types_.isStringObject,
    isSymbolObject: types_.isSymbolObject,
    isTypedArray: types_.isTypedArray,
    isUint8Array: types_.isUint8Array,
    isUint8ClampedArray: types_.isUint8ClampedArray,
    isUint16Array: types_.isUint16Array,
    isUint32Array: types_.isUint32Array,
    isWeakMap: types_.isWeakMap,
    isWeakSet: types_.isWeakSet,
    isKeyObject: types_.isKeyObject,
    isCryptoKey: types_.isCryptoKey,
};
