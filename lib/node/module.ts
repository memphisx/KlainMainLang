// Node's `module` (lib/internal/modules/cjs/loader.js and lib/module.js):
// the Module function, builtinModules, isBuiltin and createRequire. A
// compiled program has every module it can load linked in: each file's
// literal requires, and the builtins its string literals name when it
// requires by a computed name, register here (`_kmlRegister`), and a
// require function answers from that registry. A module it does not have
// throws Node's MODULE_NOT_FOUND.

import { dirname, resolve, isAbsolute } from './path';
import { fileURLToPath } from './url';
import { NodeTypeError } from './internal_errors';

export const builtinModules: string[] = [
    '_http_agent', '_http_client', '_http_common', '_http_incoming', '_http_outgoing', '_http_server',
    '_stream_duplex', '_stream_passthrough', '_stream_readable', '_stream_transform', '_stream_wrap',
    '_stream_writable', '_tls_common', '_tls_wrap', 'assert', 'assert/strict', 'async_hooks', 'buffer',
    'child_process', 'cluster', 'console', 'constants', 'crypto', 'dgram', 'diagnostics_channel', 'dns',
    'dns/promises', 'domain', 'events', 'fs', 'fs/promises', 'http', 'http2', 'https', 'inspector',
    'inspector/promises', 'module', 'net', 'os', 'path', 'path/posix', 'path/win32', 'perf_hooks',
    'process', 'punycode', 'querystring', 'readline', 'readline/promises', 'repl', 'stream',
    'stream/consumers', 'stream/promises', 'stream/web', 'string_decoder', 'sys', 'timers',
    'timers/promises', 'tls', 'trace_events', 'tty', 'url', 'util', 'util/types', 'v8', 'vm', 'wasi',
    'worker_threads', 'zlib', 'node:sea', 'node:sqlite', 'node:test', 'node:test/reporters',
];

// The builtins only reachable with the `node:` scheme.
const schemeOnly: string[] = ['sea', 'sqlite', 'test', 'test/reporters'];

export function isBuiltin(moduleName: any): boolean {
    if (typeof moduleName !== 'string') return false;
    if (moduleName.startsWith('node:')) {
        const bare = moduleName.slice(5);
        return builtinModules.includes(bare) || schemeOnly.includes(bare);
    }
    return builtinModules.includes(moduleName);
}

// The loaded modules: a builtin's by its bare name, a file's by its path.
const builtins: Map<string, any> = new Map();
const files: Map<string, any> = new Map();

export function _kmlRegister(key: string, value: any, isFile: boolean): void {
    if (isFile) files.set(key, value);
    else builtins.set(key, value);
}

const extensions: string[] = ['', '.js', '.json', '.node', '.cjs', '.mjs', '.ts', '.cts', '.mts'];

function notFound(request: string, from: string): any {
    const e: any = new Error("Cannot find module '" + request + "'\nRequire stack:\n- " + from);
    e.code = 'MODULE_NOT_FOUND';
    e.requireStack = [from];
    return e;
}

function lookup(request: string, from: string): { key: string; found: boolean; value: any } {
    if (request.startsWith('node:')) {
        const bare = request.slice(5);
        if (builtins.has(bare)) return { key: request, found: true, value: builtins.get(bare) };
        if (!isBuiltin(request)) {
            const e: any = new Error('No such built-in module: ' + request);
            e.code = 'ERR_UNKNOWN_BUILTIN_MODULE';
            throw e;
        }
        return { key: request, found: false, value: undefined };
    }
    if (builtinModules.includes(request)) {
        return { key: request, found: builtins.has(request), value: builtins.get(request) };
    }
    if (request.startsWith('./') || request.startsWith('../') || request.startsWith('/') || request === '.' || request === '..') {
        const base = isAbsolute(request) ? request : resolve(dirname(from), request);
        for (const ext of extensions) {
            if (files.has(base + ext)) return { key: base + ext, found: true, value: files.get(base + ext) };
        }
        for (const ext of extensions) {
            if (ext !== '' && files.has(base + '/index' + ext)) {
                return { key: base + '/index' + ext, found: true, value: files.get(base + '/index' + ext) };
            }
        }
    }
    return { key: request, found: false, value: undefined };
}

function validateRequest(request: any): void {
    if (typeof request !== 'string') {
        const shown = request === null ? 'null' : typeof request === 'object' ? 'an instance of ' + (request.constructor?.name ?? 'Object') : 'type ' + typeof request + ' (' + String(request) + ')';
        throw NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "id" argument must be of type string. Received ' + shown);
    }
    if (request === '') {
        const e: any = new TypeError("The argument 'id' must be a non-empty string. Received ''");
        e.code = 'ERR_INVALID_ARG_VALUE';
        throw e;
    }
}

// The require function of the file at `from`.
export function _kmlRequireFor(from: string): any {
    const req: any = function require(request: any): any {
        validateRequest(request);
        const r = lookup(request, from);
        if (!r.found) throw notFound(request, from);
        return r.value;
    };
    const resolveFn: any = function resolve(request: any, options?: any): string {
        validateRequest(request);
        const r = lookup(request, from);
        if (!r.found && !isBuiltin(request)) throw notFound(request, from);
        return r.key;
    };
    resolveFn.paths = (request: string): string[] | null => (isBuiltin(request) ? null : [resolve(dirname(from), 'node_modules')]);
    req.resolve = resolveFn;
    req.cache = Object.create(null);
    req.extensions = Object.create(null);
    req.main = undefined;
    return req;
}

export function createRequire(filename: any): any {
    let path: string = '';
    if (typeof filename === 'string' && filename.startsWith('file:')) path = fileURLToPath(filename);
    else if (typeof filename === 'object' && filename !== null && typeof filename.href === 'string') path = fileURLToPath(filename.href);
    else if (typeof filename === 'string' && isAbsolute(filename)) path = filename;
    else {
        const e: any = new TypeError('The argument \'filename\' must be a file URL object, file URL string, or absolute path string. Received ' +
            (typeof filename === 'string' ? "'" + filename + "'" : String(filename)));
        e.code = 'ERR_INVALID_ARG_VALUE';
        throw e;
    }
    return _kmlRequireFor(path);
}

// Node's syncBuiltinESMExports: the named exports of a builtin already
// read from its one exports object here.
export function syncBuiltinESMExports(): void {}

// kml:callable Module — Node's is a function that constructs when called without `new`
export class Module {
    id: string;
    path: string;
    exports: any = {};
    filename: string | null = null;
    loaded = false;
    children: any[] = [];
    paths: string[] = [];
    parent: any;

    constructor(id: string = '', parent?: any) {
        this.id = id;
        this.path = dirname(id === '' ? '.' : id);
        this.parent = parent;
    }

    require(id: any): any {
        return _kmlRequireFor(this.filename ?? this.id)(id);
    }

    static builtinModules = builtinModules;
    static isBuiltin = isBuiltin;
    static createRequire = createRequire;
    static syncBuiltinESMExports = syncBuiltinESMExports;
    static Module: any = null;
}
Module.Module = Module;

export default Module;
