// Node's `node:ffi` (v26, --experimental-ffi), ported from lib/ffi.js and
// src/node_ffi.cc: DynamicLibrary, dlopen/dlclose/dlsym, typed calls and
// callbacks through libffi (ffi_native.c), and the raw-memory helpers. A
// signature is an ordinary value, checked when a function is requested; a
// bound function is a function object named for its symbol, with its
// argument count as `length` and its address as an own `pointer`. A
// library's `symbols` and `functions` enumerate in Node's std::unordered_map
// order (ffi_registry.c).
//
// kml:scheme-only — Node exposes this module only as `node:ffi`.
// kml:default-namespace — `import ffi from 'node:ffi'` reads this module's
// exports.

import { NodeError, NodeTypeError, NodeRangeError } from './internal_errors';

function invalidValue(message: string): Error {
    return new NodeTypeError('ERR_INVALID_ARG_VALUE', message);
}

function libraryClosed(): Error {
    return new NodeError('ERR_FFI_LIBRARY_CLOSED', 'Library is closed');
}

// ---- types ----

export const suffix: string = process.platform === 'darwin' ? 'dylib' : process.platform === 'win32' ? 'dll' : 'so';

export const types = {
    VOID: 'void',
    POINTER: 'pointer',
    BUFFER: 'buffer',
    ARRAY_BUFFER: 'arraybuffer',
    FUNCTION: 'function',
    BOOL: 'bool',
    CHAR: 'char',
    STRING: 'string',
    FLOAT: 'float',
    DOUBLE: 'double',
    INT_8: 'int8',
    UINT_8: 'uint8',
    INT_16: 'int16',
    UINT_16: 'uint16',
    INT_32: 'int32',
    UINT_32: 'uint32',
    INT_64: 'int64',
    UINT_64: 'uint64',
    FLOAT_32: 'float32',
    FLOAT_64: 'float64',
};

// The kinds ffi_native.c takes.
const K_VOID = 0, K_I8 = 1, K_U8 = 2, K_I16 = 3, K_U16 = 4, K_I32 = 5, K_U32 = 6;
const K_I64 = 7, K_U64 = 8, K_F32 = 9, K_F64 = 10, K_POINTER = 11, K_STRING = 12;
const K_BUFFER = 13, K_ARRAYBUFFER = 14, K_FUNCTION = 15;

// `char` is the platform's char: unsigned on Linux arm64.
const K_CHAR = process.platform === 'linux' && process.arch === 'arm64' ? K_U8 : K_I8;

function kindOf(name: string): number {
    switch (name) {
    case 'void': return K_VOID;
    case 'char': return K_CHAR;
    case 'int8': case 'i8': return K_I8;
    case 'uint8': case 'u8': case 'bool': return K_U8;
    case 'int16': case 'i16': return K_I16;
    case 'uint16': case 'u16': return K_U16;
    case 'int32': case 'i32': return K_I32;
    case 'uint32': case 'u32': return K_U32;
    case 'int64': case 'i64': return K_I64;
    case 'uint64': case 'u64': return K_U64;
    case 'float32': case 'f32': case 'float': return K_F32;
    case 'float64': case 'f64': case 'double': return K_F64;
    case 'pointer': case 'ptr': return K_POINTER;
    case 'string': case 'str': return K_STRING;
    case 'buffer': return K_BUFFER;
    case 'arraybuffer': return K_ARRAYBUFFER;
    case 'function': return K_FUNCTION;
    }
    return -1;
}

function isPointerKind(k: number): boolean {
    return k >= K_POINTER;
}

// What an argument of kind k must be (Node's message).
function kindMessage(k: number): string {
    switch (k) {
    case K_I8: return 'an int8';
    case K_U8: return 'a uint8';
    case K_I16: return 'an int16';
    case K_U16: return 'a uint16';
    case K_I32: return 'an int32';
    case K_U32: return 'a uint32';
    case K_I64: return 'an int64';
    case K_U64: return 'a uint64';
    case K_F32: return 'a float';
    case K_F64: return 'a double';
    }
    return 'a buffer, an ArrayBuffer, a string, or a bigint';
}

// A signature's libffi identity: aliases and the pointer family coincide.
function kindKey(k: number): string {
    if (isPointerKind(k)) return 'p';
    return ['v', 'i8', 'u8', 'i16', 'u16', 'i32', 'u32', 'i64', 'u64', 'f', 'd'][k];
}

export interface FunctionSignature {
    arguments?: string[] | undefined;
    return?: string | undefined;
}

class Signature {
    ret: number;
    args: number[];
    constructor(ret: number, args: number[]) {
        this.ret = ret;
        this.args = args;
    }
    key(): string {
        return kindKey(this.ret) + '(' + this.args.map((k: number) => kindKey(k)).join(',') + ')';
    }
    kinds(): string {
        return this.ret + ':' + this.args.join(',');
    }
}

// ParseFunctionSignature: `return` and `arguments` are read, other keys
// ignored. notObject is the message for a signature that is no object.
function parseSignature(name: string, sig: any, notObject: string, notObjectCode: string): Signature {
    if (sig === null || typeof sig !== 'object' || Array.isArray(sig)) {
        throw new NodeTypeError(notObjectCode, notObject);
    }
    let ret = K_VOID;
    const r = sig.return;
    if (r !== undefined) {
        if (typeof r !== 'string') throw invalidValue('Return value type of function ' + name + ' must be a string');
        ret = kindOf(r as string);
        if (ret < 0) throw invalidValue('Unsupported FFI type: ' + r);
    }
    const args: number[] = [];
    const a = sig.arguments;
    if (a !== undefined) {
        if (!Array.isArray(a)) throw invalidValue('Arguments list of function ' + name + ' must be an array');
        const list: any[] = a;
        for (let i = 0; i < list.length; i++) {
            const t = list[i];
            if (typeof t !== 'string') throw invalidValue('Argument ' + i + ' of function ' + name + ' must be a string');
            const k = kindOf(t as string);
            if (k < 0) throw invalidValue('Unsupported FFI type: ' + t);
            if (k === K_VOID) {
                throw invalidValue('Argument ' + i + " of function " + name + " must not be 'void'; use an empty array for no-argument functions");
            }
            args.push(k);
        }
    }
    return new Signature(ret, args);
}

// A call interface per signature, shared.
const cifs = new Map<string, number>();

function cifOf(sig: Signature): number {
    const kinds = sig.kinds();
    let id = cifs.get(kinds);
    if (id === undefined) {
        id = __kml_native.ffiCif(kinds);
        cifs.set(kinds, id);
    }
    return id;
}

function checkName(name: any, what: string): string {
    if (typeof name !== 'string') throw new NodeTypeError('ERR_INVALID_ARG_TYPE', what + ' name must be a string');
    if ((name as string).indexOf('\0') >= 0) throw invalidValue(what + ' name must not contain null bytes');
    return name as string;
}

function statusError(st: number): Error {
    if (st === 1) return libraryClosed();
    if (st === 3) return invalidValue(__kml_native.ffiError());
    return new NodeError('ERR_FFI_CALL_FAILED', __kml_native.ffiError());
}

// A pointer argument's address (a bigint, null, a string, a buffer or an
// ArrayBuffer).
function addressOf(value: any): number {
    const v: any = value instanceof ArrayBuffer ? new Uint8Array(value as ArrayBuffer) : value;
    const a = __kml_native.ffiPtr(v);
    if (a === -2) throw invalidValue('Argument 0 must be a non-negative pointer bigint');
    if (a < 0) throw invalidValue('Argument 0 must be a buffer, an ArrayBuffer, a string, or a bigint');
    return a;
}

// ---- DynamicLibrary ----

export class DynamicLibrary {
    #id: number;
    #path: string;
    // The function objects handed out, by name (the registry keeps order).
    #functions = new Map<string, any>();
    #callbacks = new Map<number, any>();

    constructor(path: string | null) {
        if (path !== null && path !== undefined && typeof path !== 'string') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'Library path must be a string or null');
        }
        const isNull = path === null || path === undefined;
        const id = __kml_native.ffiOpen(isNull ? '' : path as string, isNull);
        if (id < 0) throw new NodeError('ERR_FFI_CALL_FAILED', __kml_native.ffiError());
        this.#id = id;
        this.#path = isNull ? '' : path as string;
    }

    get path(): string {
        return this.#path;
    }

    get symbols(): any {
        return this.getSymbols();
    }

    private _kmlCheck(): void {
        if (__kml_native.ffiClosed(this.#id)) throw libraryClosed();
    }

    get functions(): any {
        return this.getFunctions();
    }

    close(): void {
        if (__kml_native.ffiClosed(this.#id)) return;
        __kml_native.ffiClose(this.#id);
        this.#functions.clear();
    }

    [Symbol.dispose](): void {
        this.close();
    }

    getSymbol(name: string): bigint {
        this._kmlCheck();
        const n = checkName(name, 'Symbol');
        const st = __kml_native.ffiSymbol(this.#id, n);
        if (st !== 0) throw statusError(st);
        return BigInt(__kml_native.ffiLastAddr());
    }

    getSymbols(): any {
        const n = __kml_native.ffiCount(this.#id, false);
        if (n < 0) throw libraryClosed();
        const out: any = Object.create(null);
        for (let i = 0; i < n; i++) out[__kml_native.ffiListName(i)] = BigInt(__kml_native.ffiListAddr(i, false));
        return out;
    }

    // PrepareFunction for each name, then (all having succeeded) the
    // function objects.
    private _kmlPrepare(name: string, sig: Signature): number {
        const st = __kml_native.ffiPrepare(this.#id, name, sig.key());
        if (st !== 0) throw statusError(st);
        return __kml_native.ffiLastAddr();
    }

    private _kmlFunctionObject(name: string, sig: Signature, addr: number): any {
        const existing = this.#functions.get(name);
        if (existing !== undefined) return existing;
        const fn = makeFunction(this, name, sig, addr);
        this.#functions.set(name, fn);
        return fn;
    }

    // The implementation takes the signature as given: Node validates its
    // members at run time, and a typed parameter would convert them first.
    getFunction(name: string, signature: FunctionSignature): any;
    getFunction(name: string, signature: any): any {
        this._kmlCheck();
        const n = checkName(name, 'Function');
        const sig = parseSignature(n, signature, 'Function signature must be an object', 'ERR_INVALID_ARG_TYPE');
        const addr = this._kmlPrepare(n, sig);
        __kml_native.ffiCommit(this.#id);
        return this._kmlFunctionObject(n, sig, addr);
    }

    getFunctions(definitions?: { [name: string]: FunctionSignature }): any {
        if (definitions === undefined) {
            const n = __kml_native.ffiCount(this.#id, true);
            if (n < 0) throw libraryClosed();
            const out: any = Object.create(null);
            for (let i = 0; i < n; i++) {
                const name = __kml_native.ffiListName(i);
                out[name] = this.#functions.get(name);
            }
            return out;
        }
        this._kmlCheck();
        const defs: any = definitions;
        if (defs === null || typeof defs !== 'object' || Array.isArray(defs)) {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'Functions signatures must be an object');
        }
        const names: string[] = Object.keys(defs);
        const sigs: Signature[] = [];
        const addrs: number[] = [];
        for (const name of names) {
            if (name.indexOf('\0') >= 0) throw invalidValue('Function name must not contain null bytes');
            const sig = parseSignature(name, defs[name], 'Signature of function ' + name + ' must be an object', 'ERR_INVALID_ARG_VALUE');
            addrs.push(this._kmlPrepare(name, sig));
            sigs.push(sig);
        }
        const out: any = Object.create(null);
        for (let i = 0; i < names.length; i++) {
            __kml_native.ffiPrepare(this.#id, names[i], sigs[i].key());
            __kml_native.ffiCommit(this.#id);
            out[names[i]] = this._kmlFunctionObject(names[i], sigs[i], addrs[i]);
        }
        return out;
    }

    registerCallback(signatureOrCallback: any, maybeCallback?: any): bigint {
        this._kmlCheck();
        let sigValue: any = {};
        let cb: any = signatureOrCallback;
        if (typeof signatureOrCallback !== 'function') {
            sigValue = signatureOrCallback;
            cb = maybeCallback;
        }
        if (typeof cb !== 'function') throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'Callback must be a function');
        const sig = parseSignature('callback', sigValue, 'Callback signature must be an object', 'ERR_INVALID_ARG_TYPE');
        const f: any = cb;
        const code = __kml_native.ffiClosure(cifOf(sig), (unused: number, unused2: number) => {
            const n = __kml_native.ffiCallbackArgc();
            const args: any[] = [];
            for (let i = 0; i < n; i++) args.push(__kml_native.ffiCallbackArg(i));
            let result: any = undefined;
            try {
                result = f(...args);
            } catch (e) {
                // The callback runs inside the foreign call: its error is
                // uncaught once that returns.
                setImmediate(() => { throw e; });
                return;
            }
            __kml_native.ffiCallbackReturn(result);
        });
        if (code === 0) throw new NodeError('ERR_FFI_CALL_FAILED', 'Failed to create the callback');
        this.#callbacks.set(code, f);
        return BigInt(code);
    }

    private _kmlCallbackCode(pointer: any): number {
        if (typeof pointer !== 'bigint') throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'Callback pointer must be a bigint');
        return Number(pointer as bigint);
    }

    unregisterCallback(pointer: bigint): void {
        const code = this._kmlCallbackCode(pointer);
        if (!this.#callbacks.has(code)) return;
        this.#callbacks.delete(code);
        __kml_native.ffiClosureFree(code);
    }

    // A registered callback is always strongly held here.
    refCallback(pointer: bigint): void {
        this._kmlCallbackCode(pointer);
    }

    unrefCallback(pointer: bigint): void {
        this._kmlCallbackCode(pointer);
    }

    _kmlIsClosed(): boolean {
        return __kml_native.ffiClosed(this.#id);
    }
}

// A bound function: its symbol's name, its argument count, its address.
function makeFunction(lib: DynamicLibrary, name: string, sig: Signature, addr: number): any {
    const cif = cifOf(sig);
    const n = sig.args.length;
    const fn = (...args: any[]): any => {
        if (lib._kmlIsClosed()) throw libraryClosed();
        if (args.length !== n) throw invalidValue('Invalid argument count: expected ' + n + ', got ' + args.length);
        for (let i = 0; i < n; i++) {
            const st = __kml_native.ffiArg(i, sig.args[i], args[i]);
            if (st === 2) throw invalidValue('Argument ' + i + ' must be a non-negative pointer bigint');
            if (st !== 0) throw invalidValue('Argument ' + i + ' must be ' + kindMessage(sig.args[i]));
        }
        return __kml_native.ffiCall(cif, addr);
    };
    Object.defineProperty(fn, 'name', { value: name, configurable: true });
    Object.defineProperty(fn, 'length', { value: n, configurable: true });
    (fn as any).pointer = BigInt(addr);
    return fn;
}

// ---- module functions ----

export function dlopen(path: string | null, definitions?: { [name: string]: FunctionSignature }): { lib: DynamicLibrary; functions: any } {
    const lib = new DynamicLibrary(path);
    let functions: any;
    if (definitions === undefined) {
        functions = Object.freeze(Object.create(null));
    } else {
        try {
            functions = lib.getFunctions(definitions);
        } catch (e) {
            lib.close();
            throw e;
        }
    }
    return { lib: lib, functions: functions };
}

export function dlclose(library: DynamicLibrary): void {
    library.close();
}

export function dlsym(library: DynamicLibrary, name: string): bigint {
    return library.getSymbol(name);
}

// ---- memory ----

function offsetOf(offset: any): number {
    if (offset === undefined) return 0;
    if (typeof offset === 'bigint') return Number(offset as bigint);
    return offset as number;
}

function getAt(kind: number, pointer: any, offset: any): any {
    return __kml_native.ffiGet(kind, addressOf(pointer), offsetOf(offset));
}

function setAt(kind: number, pointer: any, offset: any, value: any): void {
    const st = __kml_native.ffiSet(kind, addressOf(pointer), offsetOf(offset), value);
    if (st !== 0) throw invalidValue('Argument 2 must be ' + kindMessage(kind));
}

export function getInt8(pointer: bigint, offset?: number): number { return getAt(K_I8, pointer, offset); }
export function getUint8(pointer: bigint, offset?: number): number { return getAt(K_U8, pointer, offset); }
export function getInt16(pointer: bigint, offset?: number): number { return getAt(K_I16, pointer, offset); }
export function getUint16(pointer: bigint, offset?: number): number { return getAt(K_U16, pointer, offset); }
export function getInt32(pointer: bigint, offset?: number): number { return getAt(K_I32, pointer, offset); }
export function getUint32(pointer: bigint, offset?: number): number { return getAt(K_U32, pointer, offset); }
export function getInt64(pointer: bigint, offset?: number): bigint { return getAt(K_I64, pointer, offset); }
export function getUint64(pointer: bigint, offset?: number): bigint { return getAt(K_U64, pointer, offset); }
export function getFloat32(pointer: bigint, offset?: number): number { return getAt(K_F32, pointer, offset); }
export function getFloat64(pointer: bigint, offset?: number): number { return getAt(K_F64, pointer, offset); }

export function setInt8(pointer: bigint, offset: number, value: number): void { setAt(K_I8, pointer, offset, value); }
export function setUint8(pointer: bigint, offset: number, value: number): void { setAt(K_U8, pointer, offset, value); }
export function setInt16(pointer: bigint, offset: number, value: number): void { setAt(K_I16, pointer, offset, value); }
export function setUint16(pointer: bigint, offset: number, value: number): void { setAt(K_U16, pointer, offset, value); }
export function setInt32(pointer: bigint, offset: number, value: number): void { setAt(K_I32, pointer, offset, value); }
export function setUint32(pointer: bigint, offset: number, value: number): void { setAt(K_U32, pointer, offset, value); }
export function setInt64(pointer: bigint, offset: number, value: bigint): void { setAt(K_I64, pointer, offset, value); }
export function setUint64(pointer: bigint, offset: number, value: bigint): void { setAt(K_U64, pointer, offset, value); }
export function setFloat32(pointer: bigint, offset: number, value: number): void { setAt(K_F32, pointer, offset, value); }
export function setFloat64(pointer: bigint, offset: number, value: number): void { setAt(K_F64, pointer, offset, value); }

// The NUL-terminated string at pointer (null for a null pointer).
export function toString(pointer: bigint): string | null {
    const a = addressOf(pointer);
    if (a === 0) return null;
    return __kml_native.ffiCString(a);
}

function lengthOf(length: any): number {
    return typeof length === 'bigint' ? Number(length as bigint) : length as number;
}

// length bytes at pointer: a copy, or (copy false) a Buffer over them.
export function toBuffer(pointer: bigint, length: number | bigint, copy?: boolean): Buffer {
    const a = addressOf(pointer);
    const n = lengthOf(length);
    if (copy === false) return __kml_native.ffiView(a, n, false);
    const out = Buffer.alloc(n);
    __kml_native.ffiCopyIn(a, out);
    return out;
}

export function toArrayBuffer(pointer: bigint, length: number | bigint, copy?: boolean): ArrayBuffer {
    const a = addressOf(pointer);
    const n = lengthOf(length);
    if (copy === false) return __kml_native.ffiView(a, n, true);
    const ab = new ArrayBuffer(n);
    __kml_native.ffiCopyIn(a, new Uint8Array(ab));
    return ab;
}

// The address of a Buffer's, TypedArray's or ArrayBuffer's bytes.
export function getRawPointer(source: any): bigint {
    return BigInt(addressOf(source));
}

function received(value: any): string {
    if (typeof value === 'bigint') return 'type bigint (' + String(value) + 'n)';
    if (typeof value === 'number') return 'type number (' + String(value) + ')';
    if (typeof value === 'boolean') return 'type boolean (' + String(value) + ')';
    if (value === null) return 'null';
    if (value === undefined) return 'undefined';
    return 'an instance of ' + (value.constructor ? value.constructor.name : 'Object');
}

function validateLength(length: any): number {
    if (typeof length !== 'number') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "len" argument must be of type number. Received ' + received(length));
    }
    const n = length as number;
    if (!Number.isInteger(n)) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "len" is out of range. It must be an integer. Received ' + String(n));
    }
    if (n < 0 || n > Number.MAX_SAFE_INTEGER) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "len" is out of range. It must be >= 0 && <= 9007199254740991. Received ' + String(n));
    }
    return n;
}

// Copy the string, encoded, and its terminator (two bytes for UTF-16) to
// pointer; length must hold them.
export function exportString(str: any, pointer: bigint, length: any, encoding?: string): void {
    if (typeof str !== 'string') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "string" argument must be of type string. Received ' + received(str));
    }
    const a = addressOf(pointer);
    const n = validateLength(length);
    const enc = encoding === undefined ? 'utf8' : encoding;
    if (!Buffer.isEncoding(enc)) {
        throw new NodeTypeError('ERR_UNKNOWN_ENCODING', 'Unknown encoding: ' + enc);
    }
    const bytes = Buffer.from(str, enc as BufferEncoding);
    const lower = String(enc).toLowerCase();
    const term = lower === 'utf16le' || lower === 'utf-16le' || lower === 'ucs2' || lower === 'ucs-2' ? 2 : 1;
    const needed = bytes.length + term;
    if (n < needed) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "len" is out of range. It must be >= ' + needed + '. Received ' + String(n));
    }
    const out = Buffer.alloc(needed);
    bytes.copy(out, 0);
    __kml_native.ffiCopyOut(out, a);
}

function exportBytes(name: string, bytes: Uint8Array, pointer: bigint, length: number): void {
    const a = addressOf(pointer);
    const n = validateLength(length);
    if (n < bytes.length) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "len" is out of range. It must be >= ' + bytes.length + '. Received ' + String(n));
    }
    __kml_native.ffiCopyOut(bytes, a);
}

function viewBytes(source: any): Uint8Array {
    if (source instanceof Uint8Array) return source as Uint8Array;
    if (source instanceof ArrayBuffer) return new Uint8Array(source as ArrayBuffer);
    const out = new Uint8Array(source.byteLength as number);
    __kml_native.typedBytes(source, out);
    return out;
}

export function exportBuffer(buffer: NodeJS.ArrayBufferView, pointer: bigint, length: number): void {
    exportBytes('buffer', viewBytes(buffer), pointer, length);
}

export function exportArrayBuffer(arrayBuffer: ArrayBuffer, pointer: bigint, length: number): void {
    exportBytes('arrayBuffer', viewBytes(arrayBuffer), pointer, length);
}

export function exportArrayBufferView(view: NodeJS.ArrayBufferView, pointer: bigint, length: number): void {
    exportBytes('view', viewBytes(view), pointer, length);
}
