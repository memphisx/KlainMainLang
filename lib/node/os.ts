// Node's lib/os.js, in TypeScript, over the host reads of osinfo.c
// (__kml_native.os*), which stand in for internalBinding('os').
// kml:default-namespace — `import os from 'os'` reads this module's exports.

import { Buffer } from 'buffer';
import { inspect } from './internal_util_inspect';
import { NodeTypeError, NodeRangeError } from './internal_errors';

const isWindows = process.platform === 'win32';

// Node's SystemError (internal/errors.js) for ERR_SYSTEM_ERROR: the context
// of the failed libuv call is `info`. Node's errno and syscall are accessors
// over the context; here they are copies of it.
class SystemError extends Error {
    code: string;
    info: any;
    errno: number;
    syscall: string;
    constructor(context: any) {
        super('A system error occurred: ' + context.syscall + ' returned ' + context.code + ' (' + context.message + ')');
        this.name = 'SystemError';
        this.code = 'ERR_SYSTEM_ERROR';
        this.info = context;
        this.errno = context.errno;
        this.syscall = context.syscall;
    }
}

// The ctx a failed libuv call fills in (node_os.cc's CollectUVExceptionInfo).
function uvContext(errno: number, syscall: string): any {
    return {
        errno: __kml_native.uvErrno(errno),
        code: __kml_native.errnoName(errno),
        message: __kml_native.errnoDesc(errno),
        syscall: syscall,
    };
}

function received(value: any): string {
    if (value === null || value === undefined) return ' Received ' + String(value);
    if (typeof value === 'function') return ' Received function ' + (value as Function).name;
    if (typeof value === 'object') {
        if (value.constructor && value.constructor.name) return ' Received an instance of ' + value.constructor.name;
        return ' Received ' + inspect(value, { depth: -1 });
    }
    let shown = inspect(value, { colors: false });
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

function validateInt32(value: any, name: string, min: number, max: number): void {
    if (typeof value !== 'number') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type number.' + received(value));
    }
    if (!Number.isInteger(value)) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be an integer. Received ' + String(value));
    }
    if (value < min || value > max) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be >= ' + min + ' && <= ' + max + '. Received ' + String(value));
    }
}

// internal/util's getCIDR.
function countBinaryOnes(n: number): number {
    n = n - ((n >>> 1) & 0x55555555);
    n = (n & 0x33333333) + ((n >>> 2) & 0x33333333);
    return ((n + (n >>> 4) & 0xF0F0F0F) * 0x1010101) >>> 24;
}

function getCIDR(address: string, netmask: string, family: string): string | null {
    let ones = 0;
    let split = '.';
    let range = 10;
    let groupLength = 8;
    let hasZeros = false;
    let lastPos = 0;

    if (family === 'IPv6') {
        split = ':';
        range = 16;
        groupLength = 16;
    }

    for (let i = 0; i < netmask.length; i++) {
        if (netmask[i] !== split) {
            if (i + 1 < netmask.length) {
                continue;
            }
            i++;
        }
        const part = netmask.slice(lastPos, i);
        lastPos = i + 1;
        if (part !== '') {
            if (hasZeros) {
                if (part !== '0') {
                    return null;
                }
            } else {
                const binary = parseInt(part, range);
                const binaryOnes = countBinaryOnes(binary);
                ones += binaryOnes;
                if (binaryOnes !== groupLength) {
                    if (binary.toString(2).includes('01')) {
                        return null;
                    }
                    hasZeros = true;
                }
            }
        }
    }

    return address + '/' + ones;
}

// A string read that throws ERR_SYSTEM_ERROR when it fails.
function checkedString(which: number, syscall: string): string {
    const s = __kml_native.osString(which);
    const errno = __kml_native.osErrno();
    if (errno !== 0) throw new SystemError(uvContext(errno, syscall));
    return s;
}

const osType = __kml_native.osString(0);
const osVersion = __kml_native.osString(1);
const osRelease = __kml_native.osString(2);
const osMachine = __kml_native.osString(3);

export function type(): string {
    return osType;
}

export function version(): string {
    return osVersion;
}

export function release(): string {
    return osRelease;
}

export function machine(): string {
    return osMachine;
}

export function hostname(): string {
    return checkedString(4, 'uv_os_gethostname');
}

export function homedir(): string {
    return checkedString(5, 'uv_os_homedir');
}

export function uptime(): number {
    return __kml_native.osNumber(0);
}

export function totalmem(): number {
    return __kml_native.osNumber(1);
}

export function freemem(): number {
    return __kml_native.osNumber(2);
}

export function availableParallelism(): number {
    return __kml_native.osNumber(3);
}

export function loadavg(): number[] {
    return [__kml_native.osNumber(4), __kml_native.osNumber(5), __kml_native.osNumber(6)];
}

export interface CpuInfo {
    model: string;
    speed: number;
    times: {
        user: number;
        nice: number;
        sys: number;
        idle: number;
        irq: number;
    };
}

export function cpus(): CpuInfo[] {
    const n = __kml_native.osCpus();
    const result: CpuInfo[] = [];
    for (let i = 0; i < n; i++) {
        result.push({
            model: __kml_native.osCpuModel(i),
            speed: __kml_native.osCpuValue(i, 0),
            times: {
                user: __kml_native.osCpuValue(i, 1),
                nice: __kml_native.osCpuValue(i, 2),
                sys: __kml_native.osCpuValue(i, 3),
                idle: __kml_native.osCpuValue(i, 4),
                irq: __kml_native.osCpuValue(i, 5),
            },
        });
    }
    return result;
}

export function arch(): string {
    return process.arch;
}

export function platform(): NodeJS.Platform {
    return process.platform;
}

export function tmpdir(): string {
    if (isWindows) {
        const path = process.env.TEMP ||
            process.env.TMP ||
            (process.env.SystemRoot || process.env.windir) + '\\temp';

        if (path.length > 1 && path[path.length - 1] === '\\' && path[path.length - 2] !== ':') {
            return path.slice(0, -1);
        }

        return path;
    }

    // credentials' getTempDir: TMPDIR, TMP, then TEMP, without a trailing '/'.
    let dir = process.env.TMPDIR || process.env.TMP || process.env.TEMP || '';
    if (dir.length > 1 && dir.endsWith('/')) dir = dir.slice(0, -1);
    return dir || '/tmp';
}

const kEndianness: 'BE' | 'LE' = __kml_native.osNumber(7) !== 0 ? 'BE' : 'LE';

export function endianness(): 'BE' | 'LE' {
    return kEndianness;
}

export function networkInterfaces(): any {
    const n = __kml_native.osNetifs();
    const result: any = {};

    for (let i = 0; i < n; i++) {
        const name = __kml_native.osNetifString(i, 0);
        const address = __kml_native.osNetifString(i, 1);
        const netmask = __kml_native.osNetifString(i, 2);
        const family = __kml_native.osNetifNumber(i, 0) === 6 ? 'IPv6' : 'IPv4';
        const entry: any = {
            address: address,
            netmask: netmask,
            family: family,
            mac: __kml_native.osNetifString(i, 3),
            internal: __kml_native.osNetifNumber(i, 1) !== 0,
            cidr: getCIDR(address, netmask, family),
        };
        const scopeid = __kml_native.osNetifNumber(i, 2);
        if (scopeid !== -1)
            entry.scopeid = scopeid;

        const existing = result[name];
        if (existing !== undefined)
            existing.push(entry);
        else
            result[name] = [entry];
    }

    return result;
}

export function setPriority(pid: number, priority?: number): void;
export function setPriority(pid?: any, priority?: any): void {
    if (priority === undefined) {
        priority = pid;
        pid = 0;
    }

    validateInt32(pid, 'pid', -2147483648, 2147483647);
    validateInt32(priority, 'priority', -20, 19);

    const errno = __kml_native.osSetPriority(pid, priority);
    if (errno !== 0)
        throw new SystemError(uvContext(errno, 'uv_os_setpriority'));
}

export function getPriority(pid?: number): number;
export function getPriority(pid?: any): number {
    if (pid === undefined)
        pid = 0;
    else
        validateInt32(pid, 'pid', -2147483648, 2147483647);

    const priority = __kml_native.osGetPriority(pid);
    const errno = __kml_native.osErrno();
    if (errno !== 0)
        throw new SystemError(uvContext(errno, 'uv_os_getpriority'));

    return priority;
}

// A passwd string in the requested encoding: a Buffer for 'buffer'.
function encoded(s: string, encoding: any): any {
    if (encoding === 'buffer') return Buffer.from(s);
    if (encoding === undefined || encoding === null || encoding === 'utf8' || encoding === 'utf-8') return s;
    return Buffer.from(s).toString(encoding);
}

export function userInfo(options?: any): any {
    if (typeof options !== 'object')
        options = null;

    const errno = __kml_native.osUserInfo();
    if (errno !== 0)
        throw new SystemError(uvContext(errno, 'uv_os_get_passwd'));

    const encoding = options === null ? undefined : options.encoding;
    const hasShell = __kml_native.osUserInfoNumber(2) !== 0;
    return {
        uid: __kml_native.osUserInfoNumber(0),
        gid: __kml_native.osUserInfoNumber(1),
        username: encoded(__kml_native.osUserInfoString(0), encoding),
        homedir: encoded(__kml_native.osUserInfoString(1), encoding),
        shell: hasShell ? encoded(__kml_native.osUserInfoString(2), encoding) : null,
    };
}

// internalBinding('constants').os: null-prototype groups, signals frozen.
function constantGroup(group: number): any {
    const out: any = Object.create(null);
    const n = __kml_native.osConstantCount(group);
    for (let i = 0; i < n; i++) {
        out[__kml_native.osConstantName(group, i)] = __kml_native.osConstantValue(group, i);
    }
    return out;
}

function makeConstants(): any {
    const c: any = Object.create(null);
    c.UV_UDP_REUSEADDR = 4;
    c.dlopen = constantGroup(3);
    c.errno = constantGroup(0);
    c.signals = Object.freeze(constantGroup(1));
    c.priority = constantGroup(2);
    return c;
}

// As in Node, these convert to their result (`${os.hostname}`).
(availableParallelism as any)[Symbol.toPrimitive] = () => availableParallelism();
(freemem as any)[Symbol.toPrimitive] = () => freemem();
(hostname as any)[Symbol.toPrimitive] = () => hostname();
(version as any)[Symbol.toPrimitive] = () => version();
(type as any)[Symbol.toPrimitive] = () => type();
(release as any)[Symbol.toPrimitive] = () => release();
(machine as any)[Symbol.toPrimitive] = () => machine();
(homedir as any)[Symbol.toPrimitive] = () => homedir();
(totalmem as any)[Symbol.toPrimitive] = () => totalmem();
(uptime as any)[Symbol.toPrimitive] = () => uptime();
(arch as any)[Symbol.toPrimitive] = () => process.arch;
(platform as any)[Symbol.toPrimitive] = () => process.platform;
(tmpdir as any)[Symbol.toPrimitive] = () => tmpdir();
(endianness as any)[Symbol.toPrimitive] = () => kEndianness;

export const constants: any = makeConstants();

export const EOL: string = isWindows ? '\r\n' : '\n';

export const devNull: string = isWindows ? '\\\\.\\nul' : '/dev/null';
