// process's methods over the process binding: Node's
// lib/internal/process/per_thread.js (hrtime, hrtime.bigint, kill,
// memoryUsage and its rss), lib/internal/bootstrap/switches/does_own_process_state.js
// (cwd, chdir, umask) and lib/internal/bootstrap/node.js (uptime, the
// credential reads). Code generation binds `process.<name>` to these
// exports; the process emitter object (internal_process.ts) holds them too.

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

// lib/internal/errors.js determineSpecificType: how a received value reads
// in an argument error.
function received(value: any): string {
    if (value === null || value === undefined) return 'Received ' + String(value);
    if (typeof value === 'function') return 'Received function ' + (value as Function).name;
    if (typeof value === 'object') return 'Received an instance of ' + (Array.isArray(value) ? 'Array' : 'Object');
    let shown = typeof value === 'string' ? "'" + (value as string) + "'" : String(value);
    if (typeof value === 'string' && (value as string).length > 25) shown = "'" + (value as string).slice(0, 25) + "'...";
    return 'Received type ' + typeof value + ' (' + shown + ')';
}

function invalidArgType(name: string, expected: string, value: any): NodeTypeError {
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be ' + expected + '. ' + received(value));
}

// ErrnoException: `${syscall} ${code}`.
function errnoException(err: number, syscall: string): Error {
    const errno = -err;
    const code = __kml_native.errnoName(errno);
    const e: any = new Error(syscall + ' ' + code);
    e.errno = __kml_native.uvErrno(errno);
    e.code = code;
    e.syscall = syscall;
    return e;
}

// UVException with a path and a destination.
function uvException(err: number, syscall: string, path: string, dest: string): Error {
    const errno = -err;
    const code = __kml_native.errnoName(errno);
    const e: any = new Error(code + ': ' + __kml_native.errnoDesc(errno) + ', ' + syscall + " '" + path + "' -> '" + dest + "'");
    e.errno = __kml_native.uvErrno(errno);
    e.code = code;
    e.syscall = syscall;
    e.path = path;
    e.dest = dest;
    return e;
}

export function cwd(): string {
    return __kml_native.processCwd();
}

export function chdir(directory: string): void {
    const d: any = directory;
    if (typeof d !== 'string') throw invalidArgType('directory', 'of type string', d);
    const err = __kml_native.processChdir(directory);
    if (err < 0) throw uvException(err, 'chdir', __kml_native.processCwd(), directory);
}

export function uptime(): number {
    return __kml_native.processUptime();
}

export function kill(pid: number, signal?: string | number): true {
    const p: any = pid;
    if (typeof p !== 'number' || p !== (p | 0)) throw invalidArgType('pid', 'of type number', p);
    const s: any = signal;
    let err = 0;
    if (typeof s === 'number' && s === (s | 0)) {
        err = __kml_native.killPid(pid, s as number);
    } else {
        const name: any = s || 'SIGTERM';
        const num = typeof name === 'string' ? __kml_native.signalNumber(name as string) : 0;
        if (num <= 0) throw new NodeTypeError('ERR_UNKNOWN_SIGNAL', 'Unknown signal: ' + String(name));
        err = __kml_native.killPid(pid, num);
    }
    if (err < 0) throw errnoException(err, 'kill');
    return true;
}

export function memoryUsage(): NodeJS.MemoryUsage {
    return {
        rss: __kml_native.processMemory(0),
        heapTotal: __kml_native.processMemory(1),
        heapUsed: __kml_native.processMemory(2),
        external: 0,
        arrayBuffers: 0,
    };
}

export function memoryUsageRss(): number {
    return __kml_native.processMemory(0);
}

// lib/internal/validators.js parseFileMode.
export function umask(mask?: string | number): number {
    if (mask === undefined) return __kml_native.processUmask(-1);
    const m: any = mask;
    let value = 0;
    if (typeof m === 'string') {
        if (!/^[0-7]+$/.test(m as string)) {
            throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'mask' must be a 32-bit unsigned integer or an octal string. Received '" + (m as string) + "'");
        }
        value = parseInt(m as string, 8);
    } else if (typeof m === 'number') {
        if (!Number.isInteger(m) || (m as number) < 0 || (m as number) > 4294967295) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "mask" is out of range. It must be >= 0 && <= 4294967295. Received ' + String(m));
        }
        value = m as number;
    } else {
        throw invalidArgType('mask', 'of type number', m);
    }
    return __kml_native.processUmask(value);
}

export function pid(): number {
    return __kml_native.processId(0);
}

export function ppid(): number {
    return __kml_native.processId(1);
}

export function getuid(): number {
    return __kml_native.processId(2);
}

export function geteuid(): number {
    return __kml_native.processId(3);
}

export function getgid(): number {
    return __kml_native.processId(4);
}

export function getegid(): number {
    return __kml_native.processId(5);
}
