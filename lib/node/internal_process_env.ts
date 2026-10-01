// process's argv, execPath, versions, exitCode, exit and env: Node's
// lib/internal/process/pre_execution.js, lib/internal/bootstrap/node.js and
// per_thread.js, and src/node_env_var.cc. Code generation binds
// `process.<name>` to these exports. A module of its own, apart from
// internal_process_methods.ts, since the builtin modules themselves read
// process.env.

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

// lib/internal/errors.js determineSpecificType.
function received(value: any): string {
    if (value === null || value === undefined) return 'Received ' + String(value);
    if (typeof value === 'function') return 'Received function ' + (value as Function).name;
    if (typeof value === 'object') return 'Received an instance of ' + (Array.isArray(value) ? 'Array' : 'Object');
    let shown = typeof value === 'string' ? "'" + (value as string) + "'" : String(value);
    if (typeof value === 'string' && (value as string).length > 25) shown = "'" + (value as string).slice(0, 25) + "'...";
    return 'Received type ' + typeof value + ' (' + shown + ')';
}

// ---- argv, execPath, versions (lib/internal/process/pre_execution.js) ----

let argvCache: string[] | undefined = undefined;

export function argv(): string[] {
    if (argvCache === undefined) {
        const a: string[] = [];
        const n = __kml_native.processArgc();
        for (let i = 0; i < n; i++) a.push(__kml_native.processArgv(i));
        // Node's argv[0] is the absolute executable path; a compiled
        // program is its own script, so argv[1] is that path too.
        if (n >= 2) {
            a[0] = execPath();
            a[1] = a[0];
        }
        argvCache = a;
    }
    return argvCache;
}

// Node's own command-line options: a compiled program has none.
export function execArgv(): string[] {
    return [];
}

export function argv0(): string {
    return __kml_native.processArgv0();
}

let execPathCache: string | undefined = undefined;

export function execPath(): string {
    if (execPathCache === undefined) execPathCache = __kml_native.processExecPath();
    return execPathCache;
}

export function version(): string {
    return __kml_native.processVersion(0);
}

let versionsCache: any = undefined;

// process.versions: the compatibility baseline (node, v8) and this
// compiler's own version; bundled-library versions this compiler does not
// ship are left out rather than invented.
export function versions(): any {
    if (versionsCache === undefined) {
        versionsCache = {
            node: __kml_native.processVersion(1),
            v8: __kml_native.processVersion(2),
            klain: __kml_native.processVersion(3),
        };
    }
    return versionsCache;
}

// ---- exitCode and exit (lib/internal/bootstrap/node.js, per_thread.js) ----

let exitCodeValue: any = undefined;

// The code a program set, or one the runtime set itself (13 for a
// top-level await that never settled).
export function getExitCode(): any {
    if (exitCodeValue === undefined) {
        const n = __kml_native.processGetExitCode();
        if (n !== 0) return n;
    }
    return exitCodeValue;
}

export function setExitCode(code: any): any {
    let value: any = code;
    if (code !== null && code !== undefined) {
        if (typeof code === 'string' && code !== '' && Number.isInteger(+code)) value = +code;
        if (typeof value !== 'number') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "code" argument must be of type number. ' + received(value));
        }
        if (!Number.isInteger(value)) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "code" is out of range. It must be an integer. Received ' + String(value));
        }
    }
    exitCodeValue = value;
    __kml_native.processSetExitCode(value === null || value === undefined ? 0 : value as number);
    return code;
}

export function exit(code?: number | string | null): never;
export function exit(code?: any): never {
    if (code !== undefined) setExitCode(code);
    const value: any = exitCodeValue;
    __kml_native.processReallyExit(value === null || value === undefined ? 0 : value as number);
    throw new Error('unreachable');
}

// ---- env (src/node_env_var.cc) ----

export function envGet(key: string): string | undefined {
    return __kml_native.envGet(key);
}

// Node stores String(value); the assignment's value is the value assigned.
export function envSet(key: string, value: any): any {
    if (typeof value === 'symbol') {
        throw new TypeError('Cannot convert a Symbol value to a string');
    }
    __kml_native.envSet(key, String(value));
    return value;
}

export function envDelete(key: string): boolean {
    __kml_native.envDelete(key);
    return true;
}
