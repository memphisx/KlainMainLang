// kml:global
// WebAssembly's JS API (the WebAssembly JS Interface spec, as V8 and Node
// implement it): modules compiled and run by the native engine behind the
// kml_wasm_* interface (klainwasm.c). A module is an id every thread reads,
// so one crosses to a worker; what an instance makes belongs to its thread.

import { HostToken, hostCloners, hostRevivers } from './internal_clone_hooks';
import { NodeTypeError, receivedSuffix } from './internal_errors';

const KIND_NAMES: string[] = ['function', 'global', 'table', 'memory', 'tag'];
const V_I32 = 0, V_I64 = 1, V_F32 = 2, V_F64 = 3, V_EXTERNREF = 4, V_FUNCREF = 5;

class CompileError extends Error {
    constructor(message?: string) {
        super(message);
        this.name = 'CompileError';
    }
}

class LinkError extends Error {
    constructor(message?: string) {
        super(message);
        this.name = 'LinkError';
    }
}

class RuntimeError extends Error {
    constructor(message?: string) {
        super(message);
        this.name = 'RuntimeError';
    }
}

// ---- values ----

// The JS values an externref holds: slot i + 1 (0 is null).
const refs: any[] = [];
const freeRefs: number[] = [];

function refSlot(v: any): number {
    if (v === null) return 0;
    let slot = freeRefs.pop();
    if (slot === undefined) {
        refs.push(v);
        return refs.length;
    }
    refs[slot - 1] = v;
    return slot;
}

function dropReleased(): void {
    for (let s = __kml_native.wasmReleased(); s !== 0; s = __kml_native.wasmReleased()) {
        refs[s - 1] = undefined;
        freeRefs.push(s);
    }
}

function kindOf(c: string): number {
    switch (c) {
        case 'i': return V_I32;
        case 'I': return V_I64;
        case 'f': return V_F32;
        case 'F': return V_F64;
        case 'e': return V_EXTERNREF;
        case 'r': return V_FUNCREF;
        default: return 6;
    }
}

function typeChar(t: string): string {
    switch (t) {
        case 'i32': return 'i';
        case 'i64': return 'I';
        case 'f32': return 'f';
        case 'f64': return 'F';
        case 'externref': return 'e';
        case 'anyfunc': case 'funcref': return 'r';
        default: return '';
    }
}

// ToBigInt (the spec's, not the BigInt function's: a number throws).
function toBigInt(v: any): bigint {
    if (typeof v === 'bigint') return v;
    if (typeof v === 'boolean') return v ? 1n : 0n;
    if (typeof v === 'string') return BigInt(v);
    if (typeof v === 'symbol') throw new TypeError('Cannot convert a Symbol value to a BigInt');
    throw new TypeError('Cannot convert ' + String(v) + ' to a BigInt');
}

function typeError(): TypeError {
    return new TypeError('type incompatibility when transforming from/to JS');
}

// ToWebAssemblyValue: push v as a value of kind k.
function pushValue(k: number, v: any): void {
    switch (k) {
        case V_I32: __kml_native.wasmArg(k, Number(v) | 0, ''); return;
        case V_I64: __kml_native.wasmArg(k, 0, String(BigInt.asIntN(64, toBigInt(v)))); return;
        case V_F32: __kml_native.wasmArg(k, Math.fround(Number(v)), ''); return;
        case V_F64: __kml_native.wasmArg(k, Number(v), ''); return;
        case V_EXTERNREF: __kml_native.wasmArg(k, refSlot(v), ''); return;
        case V_FUNCREF: {
            if (v === null) {
                __kml_native.wasmArg(k, -1, '');
                return;
            }
            const h = funcHandles.get(v);
            if (h === undefined) throw typeError();
            __kml_native.wasmArg(k, h, '');
            return;
        }
        default: throw typeError();
    }
}

// ToJSValue: a value of kind k read as num (an i64 as dec).
function jsValue(k: number, num: number, dec: () => string): any {
    switch (k) {
        case V_I64: return BigInt(dec());
        case V_EXTERNREF: return num === 0 ? null : refs[num - 1];
        case V_FUNCREF: return num < 0 ? null : funcOf(num, funcNames.get(num) ?? '0');
        case V_I32: case V_F32: case V_F64: return num;
        default: throw typeError();
    }
}

function resultValue(i: number): any {
    return jsValue(__kml_native.wasmResultKind(i), __kml_native.wasmResultNum(i), () => __kml_native.wasmResultDec(i));
}

// ---- functions ----

// Each function extern's name: its function index, which V8 names it by.
const funcNames: Map<number, string> = new Map<number, string>();

// The exported function of each function extern, and back.
const funcObjects: Map<number, any> = new Map<number, any>();
const funcHandles: Map<any, number> = new Map<any, number>();
// The JS function each host function calls.
const hostFns: any[] = [];
// What a JS function a wasm call reached threw, rethrown once the call
// unwinds to JS.
let pendingThrow: any = undefined;
let hostInstalled = false;

function installHost(): void {
    if (hostInstalled) return;
    hostInstalled = true;
    __kml_native.wasmSetHost((id: number, nargs: number) => {
        const args: any[] = [];
        for (let i = 0; i < nargs; i++) {
            args.push(jsValue(__kml_native.wasmHostArgKind(i), __kml_native.wasmHostArgNum(i), () => __kml_native.wasmHostArgDec(i)));
        }
        try {
            const r = hostFns[id].fn.apply(undefined, args);
            const n = __kml_native.wasmHostResultKind(-1);
            if (n === 1) {
                setHostResult(0, r);
            } else if (n > 1) {
                const list = Array.from(r as any);
                if (list.length !== n) throw new TypeError('multi-return length mismatch');
                for (let i = 0; i < n; i++) setHostResult(i, list[i]);
            }
        } catch (e) {
            pendingThrow = e;
            __kml_native.wasmHostThrow();
        }
    });
}

function setHostResult(i: number, v: any): void {
    const k = __kml_native.wasmHostResultKind(i);
    switch (k) {
        case V_I32: __kml_native.wasmHostResult(i, Number(v) | 0, ''); return;
        case V_I64: __kml_native.wasmHostResult(i, 0, String(BigInt.asIntN(64, toBigInt(v)))); return;
        case V_F32: __kml_native.wasmHostResult(i, Math.fround(Number(v)), ''); return;
        case V_F64: __kml_native.wasmHostResult(i, Number(v), ''); return;
        case V_EXTERNREF: __kml_native.wasmHostResult(i, refSlot(v), ''); return;
        case V_FUNCREF: {
            if (v === null) {
                __kml_native.wasmHostResult(i, -1, '');
                return;
            }
            const h = funcHandles.get(v);
            if (h === undefined) throw typeError();
            __kml_native.wasmHostResult(i, h, '');
            return;
        }
        default: throw typeError();
    }
}

// A call's failure as the error V8 throws.
function callFailure(outcome: number): any {
    if (outcome === 2) {
        const e = pendingThrow;
        pendingThrow = undefined;
        return e;
    }
    if (outcome === 3) return new RangeError(__kml_native.wasmError());
    return new RuntimeError(__kml_native.wasmError());
}

// The exported function of extern h (one per function, as V8's).
function funcOf(h: number, name: string): any {
    const known = funcObjects.get(h);
    if (known !== undefined) return known;
    const type = __kml_native.wasmExternType(h);
    const sep = type.indexOf('>');
    const params = type.slice(0, sep);
    const results = type.slice(sep + 1);
    const fn = function (...args: any[]): any {
        for (let i = 0; i < params.length; i++) pushValue(kindOf(params[i]), args[i]);
        const outcome = __kml_native.wasmCall(h, results.length);
        dropReleased();
        if (outcome !== 0) throw callFailure(outcome);
        if (results.length === 0) return undefined;
        if (results.length === 1) return resultValue(0);
        const out: any[] = [];
        for (let i = 0; i < results.length; i++) out.push(resultValue(i));
        return out;
    };
    Object.defineProperty(fn, 'name', { value: name, writable: false, enumerable: false, configurable: true });
    Object.defineProperty(fn, 'length', { value: params.length, writable: false, enumerable: false, configurable: true });
    funcObjects.set(h, fn);
    funcHandles.set(fn, h);
    return fn;
}

// ---- buffer sources ----

function bufferBytes(bytes: any, api: string): Uint8Array {
    if (bytes instanceof ArrayBuffer || bytes instanceof SharedArrayBuffer) return new Uint8Array(bytes.slice(0));
    if (ArrayBuffer.isView(bytes)) {
        const v: any = bytes;
        return new Uint8Array(v.buffer.slice(v.byteOffset, v.byteOffset + v.byteLength));
    }
    throw new TypeError(api + ': Argument 0 must be a buffer source');
}

// An object a constructor below adopts instead of making: the extern id
// (or module id) of a wasm object that reached JS, or -1. Each class makes
// every instance through its constructor, so each is a real instance.
let adopt = -1;
let adoptBytes: Uint8Array | null = null;
let adoptType = '';

// ---- Module ----

function readU32(b: Uint8Array, at: number[]): number {
    let result = 0, shift = 0;
    for (;;) {
        const byte = b[at[0]++];
        result += (byte & 0x7f) * Math.pow(2, shift);
        if ((byte & 0x80) === 0) return result;
        shift += 7;
    }
}

function readName(b: Uint8Array, at: number[]): string {
    const n = readU32(b, at);
    const s = new TextDecoder().decode(b.subarray(at[0], at[0] + n));
    at[0] += n;
    return s;
}

// Each section's id and extent (the module validated already).
function sections(b: Uint8Array): number[][] {
    const out: number[][] = [];
    const at = [8];
    while (at[0] < b.length) {
        const id = b[at[0]++];
        const size = readU32(b, at);
        out.push([id, at[0], at[0] + size]);
        at[0] += size;
    }
    return out;
}

// The function index each exported function has (V8 names the function by
// it): imported functions count first.
function exportedFuncIndices(b: Uint8Array): Map<string, number> {
    const out = new Map<string, number>();
    for (const s of sections(b)) {
        if (s[0] !== 7) continue;
        const at = [s[1]];
        const n = readU32(b, at);
        for (let i = 0; i < n; i++) {
            const name = readName(b, at);
            const kind = b[at[0]++];
            const index = readU32(b, at);
            if (kind === 0) out.set(name, index);
        }
    }
    return out;
}

function readS32(b: Uint8Array, at: number[]): number {
    let result = 0, shift = 0, byte = 0;
    do {
        byte = b[at[0]++];
        result |= (byte & 0x7f) << shift;
        shift += 7;
    } while (byte & 0x80);
    if (shift < 32 && (byte & 0x40)) result |= -1 << shift;
    return result;
}

// An active element segment's constant offset (`i32.const n`), or -1.
function readOffset(b: Uint8Array, at: number[]): number {
    if (b[at[0]] !== 0x41) {
        while (b[at[0]++] !== 0x0b) { /* another constant expression: skip it */ }
        return -1;
    }
    at[0]++;
    const n = readS32(b, at);
    at[0]++; // end
    return n;
}

// Each active element segment's table, offset and function indices
// (the segment forms whose elements are function indices or ref.func).
function elementSegments(b: Uint8Array): number[][] {
    const out: number[][] = [];
    for (const s of sections(b)) {
        if (s[0] !== 9) continue;
        const at = [s[1]];
        const n = readU32(b, at);
        for (let i = 0; i < n; i++) {
            const flags = readU32(b, at);
            let table = 0, offset = -1;
            if (flags === 2 || flags === 6) table = readU32(b, at);
            if ((flags & 1) === 0) offset = readOffset(b, at);
            if (flags === 1 || flags === 2 || flags === 3 || flags === 5 || flags === 6 || flags === 7) at[0]++; // elemkind or reftype
            const count = readU32(b, at);
            const funcs: number[] = [table, offset];
            for (let j = 0; j < count; j++) {
                if ((flags & 4) === 0) {
                    funcs.push(readU32(b, at));
                } else if (b[at[0]] === 0xd2) {
                    at[0]++;
                    funcs.push(readU32(b, at));
                    at[0]++; // end
                } else {
                    while (b[at[0]++] !== 0x0b) { /* ref.null */ }
                    funcs.push(-1);
                }
            }
            if ((flags & 1) === 0 && offset >= 0) out.push(funcs);
        }
    }
    return out;
}

// Each table index's exported name, and the number of imported tables.
function tableNames(b: Uint8Array): Map<number, string> {
    const out = new Map<number, string>();
    for (const s of sections(b)) {
        if (s[0] !== 7) continue;
        const at = [s[1]];
        const n = readU32(b, at);
        for (let i = 0; i < n; i++) {
            const name = readName(b, at);
            const kind = b[at[0]++];
            const index = readU32(b, at);
            if (kind === 1) out.set(index, name);
        }
    }
    return out;
}

class Module {
    _kmlId: number;
    _kmlBytes: Uint8Array;
    _kmlFuncIndex: Map<string, number> | null = null;

    constructor(bytes: any) {
        if (adopt >= 0) {
            this._kmlId = adopt;
            this._kmlBytes = adoptBytes as Uint8Array;
            adopt = -1;
            adoptBytes = null;
            return;
        }
        const b = bufferBytes(bytes, 'WebAssembly.Module()');
        this._kmlBytes = b;
        this._kmlId = compileOrThrow(b, 'WebAssembly.Module()');
    }

    static exports(moduleObject: any): any[] {
        const m = checkModule(moduleObject, 'WebAssembly.Module.exports()');
        const out: any[] = [];
        for (let i = 0; ; i++) {
            const k = __kml_native.wasmExport(m._kmlId, i);
            if (k < 0) break;
            out.push({ name: __kml_native.wasmDesc(1), kind: KIND_NAMES[k] });
        }
        return out;
    }

    static imports(moduleObject: any): any[] {
        const m = checkModule(moduleObject, 'WebAssembly.Module.imports()');
        const out: any[] = [];
        for (let i = 0; ; i++) {
            const k = __kml_native.wasmImport(m._kmlId, i);
            if (k < 0) break;
            out.push({ module: __kml_native.wasmDesc(0), name: __kml_native.wasmDesc(1), kind: KIND_NAMES[k] });
        }
        return out;
    }

    static customSections(moduleObject: any, sectionName: any): ArrayBuffer[] {
        const m = checkModule(moduleObject, 'WebAssembly.Module.customSections()');
        if (sectionName === undefined) throw new TypeError('WebAssembly.Module.customSections(): Argument 1 is required');
        const want = String(sectionName);
        const out: ArrayBuffer[] = [];
        const b = m._kmlBytes;
        for (const s of sections(b)) {
            if (s[0] !== 0) continue;
            const at = [s[1]];
            if (readName(b, at) === want) out.push(b.buffer.slice(b.byteOffset + at[0], b.byteOffset + s[2]));
        }
        return out;
    }

    get [Symbol.toStringTag](): string { return 'WebAssembly.Module'; }
}

function compileOrThrow(b: Uint8Array, api: string): number {
    const id = __kml_native.wasmCompile(b);
    if (id < 0) throw new CompileError(api + ': ' + __kml_native.wasmError());
    return id;
}

function checkModule(m: any, api: string): Module {
    if (!(m instanceof Module)) throw new TypeError(api + ': Argument 0 must be a WebAssembly.Module');
    return m as Module;
}

// A module crossing to a worker (worker_threads' message clone): its id
// and bytes, the same compiled module on the other side.
export function _kmlWasmModuleOf(id: number, bytes: Uint8Array): any {
    adopt = id;
    adoptBytes = bytes;
    return new Module(undefined);
}

hostCloners.push((v: any): HostToken | undefined => {
    if (!(v instanceof Module)) return undefined;
    const m = v as Module;
    return new HostToken('wasm-module', [m._kmlId, m._kmlBytes]);
});
hostRevivers.set('wasm-module', (data: any[]) => _kmlWasmModuleOf(data[0], data[1]));
// A shared memory crosses as another handle on the same memory; one that
// is not shared cannot be cloned, as in V8.
hostCloners.push((v: any): HostToken | undefined => {
    if (!(v instanceof Memory)) return undefined;
    const token = __kml_native.wasmMemoryShare((v as Memory)._kmlH);
    if (token < 0) throw new DOMException('#<Memory> could not be cloned.', 'DataCloneError');
    return new HostToken('wasm-memory', [token]);
});
hostRevivers.set('wasm-memory', (data: any[]) => {
    const h = __kml_native.wasmMemoryAdopt(data[0]);
    const known = externObjects.get(h);
    if (known !== undefined) return known;
    const m = Memory._kmlOf(h);
    externObjects.set(h, m);
    return m;
});

// ---- Instance ----

function importValue(m: Module, importObject: any, i: number, api: string): number {
    const kind = __kml_native.wasmImport(m._kmlId, i);
    const modName = __kml_native.wasmDesc(0);
    const name = __kml_native.wasmDesc(1);
    const type = __kml_native.wasmDesc(2);
    const what = 'Import #' + i + ' "' + modName + '"';
    const ns = importObject[modName];
    if ((typeof ns !== 'object' && typeof ns !== 'function') || ns === null) {
        throw new TypeError(api + ': ' + what + ': module is not an object or function');
    }
    const v = ns[name];
    const at = what + ' "' + name + '"';
    switch (kind) {
        case 0: {
            if (typeof v !== 'function') throw new LinkError(api + ': ' + at + ': function import requires a callable');
            const own = funcHandles.get(v);
            if (own !== undefined) return own;
            installHost();
            hostFns.push({ fn: v });
            return __kml_native.wasmHostFunc(type, hostFns.length - 1);
        }
        case 1: {
            if (v instanceof Global) return (v as Global)._kmlH;
            const k = kindOf(type[1]);
            if (type[0] === 'm') throw new LinkError(api + ': ' + at + ': imported mutable global must be a WebAssembly.Global object');
            if (k === V_I64 && typeof v !== 'bigint') throw new LinkError(api + ': ' + at + ': global import must be a BigInt value');
            if ((k === V_I32 || k === V_F32 || k === V_F64) && typeof v !== 'number') {
                throw new LinkError(api + ': ' + at + ': global import must be a number, valid Wasm reference, or WebAssembly.Global object');
            }
            pushValue(k, v);
            return __kml_native.wasmGlobalNew(type.charCodeAt(1), false);
        }
        case 2:
            if (!(v instanceof Table)) throw new LinkError(api + ': ' + at + ': table import requires a WebAssembly.Table');
            return (v as Table)._kmlH;
        case 3:
            if (!(v instanceof Memory)) throw new LinkError(api + ': ' + at + ': memory import must be a WebAssembly.Memory object');
            return (v as Memory)._kmlH;
        default:
            throw new LinkError(api + ': ' + at + ': tag import requires a WebAssembly.Tag');
    }
}

function instantiateModule(m: Module, importObject: any, api: string): any {
    const handles: number[] = [];
    let first = __kml_native.wasmImport(m._kmlId, 0);
    if (first >= 0 && importObject === undefined) {
        throw new TypeError(api + ': Imports argument must be present and must be an object');
    }
    if (importObject !== undefined && (typeof importObject !== 'object' || importObject === null)) {
        throw new TypeError(api + ': Argument 1 must be an object');
    }
    for (let i = 0; __kml_native.wasmImport(m._kmlId, i) >= 0; i++) handles.push(importValue(m, importObject, i, api));
    for (const h of handles) __kml_native.wasmInstImport(h);
    const inst = __kml_native.wasmInstantiate(m._kmlId);
    dropReleased();
    if (inst < 0) {
        if (inst === -4) throw new LinkError(api + ': ' + __kml_native.wasmError());
        throw callFailure(-inst);
    }
    if (m._kmlFuncIndex === null) m._kmlFuncIndex = exportedFuncIndices(m._kmlBytes);
    // Each table's extern: the imported ones first, then the exported.
    const tables = new Map<number, number>();
    let importedTables = 0;
    for (let i = 0; i < handles.length; i++) {
        if (__kml_native.wasmImport(m._kmlId, i) === 2) tables.set(importedTables++, handles[i]);
    }
    const exportIds = new Map<string, number>();
    for (let i = 0; ; i++) {
        const h = __kml_native.wasmInstExport(inst, i);
        if (h < 0) break;
        exportIds.set(__kml_native.wasmDesc(1), h);
    }
    tableNames(m._kmlBytes).forEach((name: string, index: number) => {
        const h = exportIds.get(name);
        if (h !== undefined && !tables.has(index)) tables.set(index, h);
    });
    // The functions an element segment put in a table JS can read: named
    // by their indices.
    for (const seg of elementSegments(m._kmlBytes)) {
        const th = tables.get(seg[0]);
        if (th === undefined) continue;
        for (let j = 2; j < seg.length; j++) {
            if (seg[j] < 0 || __kml_native.wasmTableGet(th, seg[1] + j - 2) < 0) continue;
            const fh = __kml_native.wasmResultNum(0);
            if (fh >= 0 && !funcNames.has(fh)) funcNames.set(fh, String(seg[j]));
        }
    }
    const exports: any = Object.create(null);
    for (let i = 0; ; i++) {
        const h = __kml_native.wasmInstExport(inst, i);
        if (h < 0) break;
        const name = __kml_native.wasmDesc(1);
        const index = m._kmlFuncIndex.get(name);
        if (index !== undefined && !funcNames.has(h)) funcNames.set(h, String(index));
        exports[name] = externValue(h, funcNames.get(h) ?? '0');
    }
    return Object.freeze(exports);
}

// The JS object of extern h (one per extern).
const externObjects: Map<number, any> = new Map<number, any>();

function externValue(h: number, funcName: string): any {
    const kind = __kml_native.wasmExternKind(h);
    if (kind === 0) return funcOf(h, funcName);
    const known = externObjects.get(h);
    if (known !== undefined) return known;
    let obj: any;
    const type = __kml_native.wasmExternType(h);
    if (kind === 1) obj = Global._kmlOf(h, type);
    else if (kind === 2) obj = Table._kmlOf(h, type);
    else obj = Memory._kmlOf(h);
    externObjects.set(h, obj);
    return obj;
}

// The API an Instance is made for (its errors' prefix).
let instanceApi = 'WebAssembly.Instance()';

class Instance {
    readonly exports: any;

    constructor(module: any, importObject?: any) {
        const api = instanceApi;
        instanceApi = 'WebAssembly.Instance()';
        const m = checkModule(module, api);
        this.exports = instantiateModule(m, importObject, api);
    }

    get [Symbol.toStringTag](): string { return 'WebAssembly.Instance'; }
}

// ---- Memory ----

function descriptorInt(d: any, key: string, api: string, required: boolean, max: number): number | undefined {
    const v = d[key];
    if (v === undefined) {
        if (required) throw new TypeError(api + ": Property '" + key + "' is required");
        return undefined;
    }
    const n = Number(v);
    if (!Number.isFinite(n) || n < 0 || Math.trunc(n) > max) {
        throw new TypeError(api + ": Property '" + key + "': value " + String(v) + ' is ' + (n < 0 ? 'below the lower bound.' : 'above the upper bound ' + max + '.'));
    }
    return Math.trunc(n);
}

class Memory {
    _kmlH: number = -1;
    _kmlShared: boolean = false;
    _kmlBuf: any = null;
    _kmlAddr: number = 0;
    _kmlBytes: number = -1;

    constructor(descriptor: any) {
        if (adopt >= 0) {
            this._kmlH = adopt;
            this._kmlShared = __kml_native.wasmMemoryShared(adopt);
            adopt = -1;
            return;
        }
        const api = 'WebAssembly.Memory()';
        if (descriptor === undefined || descriptor === null || typeof descriptor !== 'object') {
            throw new TypeError(api + ': Argument 0 must be a memory descriptor');
        }
        const initial = descriptorInt(descriptor, 'initial', api, true, 65536) as number;
        const maximum = descriptorInt(descriptor, 'maximum', api, false, 65536);
        if (maximum !== undefined && maximum < initial) {
            throw new RangeError(api + ": Property 'maximum': value " + maximum + ' is below the lower bound ' + initial + '.');
        }
        const shared = descriptor.shared === true;
        if (shared && maximum === undefined) throw new TypeError(api + ': If shared is true, maximum property should be defined.');
        const h = __kml_native.wasmMemoryNew(initial, maximum === undefined ? -1 : maximum, shared);
        if (h < 0) throw new RangeError(api + ': could not allocate memory');
        this._kmlH = h;
        this._kmlShared = shared;
        externObjects.set(h, this);
    }

    static _kmlOf(h: number): Memory {
        adopt = h;
        return new Memory(undefined);
    }

    // The memory's bytes: a new ArrayBuffer once the memory grew.
    get buffer(): ArrayBuffer {
        const addr = __kml_native.wasmMemoryAddr(this._kmlH);
        const n = __kml_native.wasmMemoryBytes(this._kmlH);
        if (this._kmlBuf === null || addr !== this._kmlAddr || n !== this._kmlBytes) {
            this._kmlBuf = __kml_native.ffiView(addr, n, this._kmlShared ? 2 : 1);
            this._kmlAddr = addr;
            this._kmlBytes = n;
        }
        return this._kmlBuf;
    }

    grow(delta: any): number {
        const api = 'WebAssembly.Memory.grow()';
        const d = Number(delta);
        if (!Number.isFinite(d) || d < 0 || d > 4294967295) throw new TypeError(api + ": Argument 0 must be convertible to a valid number");
        const prev = __kml_native.wasmMemoryGrow(this._kmlH, Math.trunc(d));
        if (prev < 0) throw new RangeError(api + ': Maximum memory size exceeded');
        this._kmlBuf = null;
        return prev;
    }

    get [Symbol.toStringTag](): string { return 'WebAssembly.Memory'; }
}

// ---- Global ----

class Global {
    _kmlH: number = -1;
    _kmlKind: number = V_I32;
    _kmlMutable: boolean = false;

    constructor(descriptor: any, v?: any) {
        if (adopt >= 0) {
            this._kmlH = adopt;
            this._kmlKind = kindOf(adoptType[1]);
            this._kmlMutable = adoptType[0] === 'm';
            adopt = -1;
            return;
        }
        const api = 'WebAssembly.Global()';
        if (descriptor === undefined || descriptor === null || typeof descriptor !== 'object') {
            throw new TypeError(api + ': Argument 0 must be a global descriptor');
        }
        const mutable = !!descriptor.mutable;
        const c = typeChar(String(descriptor.value));
        if (c === '') throw new TypeError(api + ": Descriptor property 'value' must be a WebAssembly type");
        const k = kindOf(c);
        let init = v;
        if (init === undefined) init = k === V_I64 ? 0n : k === V_EXTERNREF ? undefined : k === V_FUNCREF ? null : 0;
        pushValue(k, init);
        const h = __kml_native.wasmGlobalNew(c.charCodeAt(0), mutable);
        if (h < 0) throw new TypeError(api + ': ' + __kml_native.wasmError());
        this._kmlH = h;
        this._kmlKind = k;
        this._kmlMutable = mutable;
        externObjects.set(h, this);
    }

    static _kmlOf(h: number, type: string): Global {
        adopt = h;
        adoptType = type;
        return new Global(undefined);
    }

    get value(): any {
        __kml_native.wasmGlobalGet(this._kmlH);
        return resultValue(0);
    }

    set value(v: any) {
        if (!this._kmlMutable) throw new TypeError("WebAssembly.Global.value: Can't set the value of an immutable global.");
        pushValue(this._kmlKind, v);
        __kml_native.wasmGlobalSet(this._kmlH);
    }

    valueOf(): any { return this.value; }

    get [Symbol.toStringTag](): string { return 'WebAssembly.Global'; }
}

// ---- Table ----

class Table {
    _kmlH: number = -1;
    _kmlKind: number = V_FUNCREF;

    constructor(descriptor: any, value?: any) {
        if (adopt >= 0) {
            this._kmlH = adopt;
            this._kmlKind = kindOf(adoptType[0]);
            adopt = -1;
            return;
        }
        const api = 'WebAssembly.Table()';
        if (descriptor === undefined || descriptor === null || typeof descriptor !== 'object') {
            throw new TypeError(api + ': Argument 0 must be a table descriptor');
        }
        const el = String(descriptor.element);
        const c = el === 'anyfunc' || el === 'funcref' ? 'r' : el === 'externref' ? 'e' : '';
        if (c === '') throw new TypeError(api + ": Descriptor property 'element' must be a WebAssembly reference type");
        const initial = descriptorInt(descriptor, 'initial', api, true, 10000000) as number;
        const maximum = descriptorInt(descriptor, 'maximum', api, false, 4294967295);
        if (maximum !== undefined && maximum < initial) {
            throw new RangeError(api + ": Property 'maximum': value " + maximum + ' is below the lower bound ' + initial + '.');
        }
        const k = kindOf(c);
        pushValue(k, value === undefined ? (k === V_FUNCREF ? null : undefined) : value);
        const h = __kml_native.wasmTableNew(c.charCodeAt(0), initial, maximum === undefined ? -1 : maximum);
        if (h < 0) throw new RangeError(api + ': ' + __kml_native.wasmError());
        this._kmlH = h;
        this._kmlKind = k;
        externObjects.set(h, this);
    }

    static _kmlOf(h: number, type: string): Table {
        adopt = h;
        adoptType = type;
        return new Table(undefined);
    }

    get length(): number { return __kml_native.wasmTableSize(this._kmlH); }

    get(index: any): any {
        const i = Number(index);
        if (__kml_native.wasmTableGet(this._kmlH, i) < 0) {
            throw new RangeError('WebAssembly.Table.get(): invalid address ' + index + ' in ' + this._kmlTypeName() + ' table of size ' + this.length);
        }
        return resultValue(0);
    }

    set(index: any, value?: any): void {
        const i = Number(index);
        pushValue(this._kmlKind, value === undefined ? (this._kmlKind === V_FUNCREF ? null : undefined) : value);
        if (__kml_native.wasmTableSet(this._kmlH, i) < 0) {
            throw new RangeError('WebAssembly.Table.set(): invalid address ' + index + ' in ' + this._kmlTypeName() + ' table of size ' + this.length);
        }
    }

    grow(delta: any, value?: any): number {
        pushValue(this._kmlKind, value === undefined ? (this._kmlKind === V_FUNCREF ? null : undefined) : value);
        const prev = __kml_native.wasmTableGrow(this._kmlH, Number(delta));
        if (prev < 0) throw new RangeError('WebAssembly.Table.grow(): failed to grow table by ' + delta);
        return prev;
    }

    _kmlTypeName(): string { return this._kmlKind === V_FUNCREF ? 'funcref' : 'externref'; }

    get [Symbol.toStringTag](): string { return 'WebAssembly.Table'; }
}

// ---- the namespace's functions ----

function validate(bytes: any): boolean {
    return __kml_native.wasmValidate(bufferBytes(bytes, 'WebAssembly.validate()'));
}

// An asynchronous compile settles as V8's AsyncCompileJob does: it decodes
// in one task and finishes (or rejects) in the next, and an instantiation
// of what it compiled is a third.
function compileAsync(bytes: any, api: string): Promise<Module> {
    return new Promise<Module>((resolve, reject) => {
        let b: Uint8Array;
        try {
            b = bufferBytes(bytes, api);
        } catch (e) {
            reject(e);
            return;
        }
        setImmediate(() => {
            const id = __kml_native.wasmCompile(b);
            const err = id < 0 ? new CompileError(api + ': ' + __kml_native.wasmError()) : null;
            setImmediate(() => {
                if (err !== null) {
                    reject(err);
                    return;
                }
                adopt = id;
                adoptBytes = b;
                resolve(new Module(undefined));
            });
        });
    });
}

// What make returns, in a later task.
function afterTask(make: () => any): Promise<any> {
    return new Promise<any>((resolve, reject) => {
        setImmediate(() => {
            try {
                resolve(make());
            } catch (e) {
                reject(e);
            }
        });
    });
}

function compile(bytes: any): Promise<Module> {
    return compileAsync(bytes, 'WebAssembly.compile()');
}

function newInstance(m: Module, importObject: any): Instance {
    instanceApi = 'WebAssembly.instantiate()';
    return new Instance(m, importObject);
}

function instantiate(source: any, importObject?: any): Promise<any> {
    if (source instanceof Module) {
        return new Promise<any>((resolve, reject) => {
            // Instantiated now, as V8 does: the promise is already settled.
            try {
                resolve(newInstance(source as Module, importObject));
            } catch (e) {
                reject(e);
            }
        });
    }
    return compileAsync(source, 'WebAssembly.instantiate()').then((m: Module) => afterTask(() => ({ module: m, instance: newInstance(m, importObject) })));
}

// The bytes of a Response, checked as Node's wasm_web_api does.
async function responseBytes(source: any): Promise<Uint8Array> {
    const response = await source;
    if (!(response instanceof Response)) {
        throw NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "source" argument must be an instance of Response or an Promise resolving to Response.' + receivedSuffix(response));
    }
    const type = response.headers.get('Content-Type');
    if (type !== 'application/wasm') throw NodeTypeError('ERR_WEBASSEMBLY_RESPONSE', "WebAssembly response has unsupported MIME type '" + type + "'");
    if (!response.ok) throw NodeTypeError('ERR_WEBASSEMBLY_RESPONSE', 'WebAssembly response has status code ' + response.status);
    if (response.bodyUsed !== false) throw NodeTypeError('ERR_WEBASSEMBLY_RESPONSE', 'WebAssembly response body has already been used');
    return new Uint8Array(await response.arrayBuffer());
}

async function compileStreaming(source: any): Promise<Module> {
    return compileAsync(await responseBytes(source), 'WebAssembly.compileStreaming()');
}

async function instantiateStreaming(source: any, importObject?: any): Promise<any> {
    const m = await compileAsync(await responseBytes(source), 'WebAssembly.instantiateStreaming()');
    return afterTask(() => ({ module: m, instance: newInstance(m, importObject) }));
}

export const WebAssembly: any = {}; // kml:global
Object.defineProperty(WebAssembly, Symbol.toStringTag, { value: 'WebAssembly', configurable: true });
for (const [k, v] of [
    ['compile', compile], ['validate', validate], ['instantiate', instantiate], ['compileStreaming', compileStreaming],
    ['instantiateStreaming', instantiateStreaming], ['Module', Module], ['Instance', Instance], ['Table', Table],
    ['Memory', Memory], ['Global', Global], ['CompileError', CompileError], ['LinkError', LinkError],
    ['RuntimeError', RuntimeError],
] as any[]) {
    Object.defineProperty(WebAssembly, k, { value: v, writable: true, enumerable: false, configurable: true });
}
