// Node's `child_process`, ported from Node v24's lib/child_process.js and
// lib/internal/child_process.js: a ChildProcess is an EventEmitter over a
// native process handle (lib/native.d.ts), its stdio pipes net.Sockets over
// the stream handles the spawn made. spawnSync is native, as Node's
// spawn_sync.cc is. The fork channel speaks Node's json serialization
// (newline-framed JSON) with the program's own re-executed binary
// (internal_child_process_channel.ts, handles included); the 'advanced'
// serialization is not ported.
//
// kml:default-namespace — `import cp from 'child_process'` reads this
// module's exports, as Node's default export carries them.
import { EventEmitter } from 'events';
import { Socket, Server } from 'net';
import type { Socket as DgramSocket } from 'dgram';
import type { Readable, Writable } from 'stream';
import { resolve as resolvePath } from 'path';
import { Channel } from './internal_child_process_channel';
import { NodeError, NodeTypeError, NodeRangeError } from './internal_errors';

// ---- errors (lib/internal/errors.js) ----

// ErrnoException: `${syscall} ${code}`, with errno, code, syscall.
function errnoException(errnoIn: number, syscall: string): Error {
    // The natives report -errno; the name and uv number take errno.
    const errno = errnoIn < 0 ? -errnoIn : errnoIn;
    const code = __kml_native.errnoName(errno);
    const e: any = new Error(syscall + ' ' + code);
    e.errno = __kml_native.uvErrno(errno);
    e.code = code;
    e.syscall = syscall;
    return e;
}

function invalidArgValue(name: string, value: unknown, reason: string): Error {
    let shown: string;
    if (typeof value === 'string') shown = "'" + value + "'";
    else shown = String(value);
    return new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument '" + name + "' " + reason + '. Received ' + shown);
}

function invalidArgType(name: string, expected: string, value: unknown): Error {
    let received: string;
    if (value === null) received = 'null';
    else if (value === undefined) received = 'undefined';
    else if (typeof value === 'string') received = "type string ('" + value + "')";
    else if (typeof value === 'number') received = 'type number (' + value + ')';
    else if (typeof value === 'boolean') received = 'type boolean (' + value + ')';
    else received = 'an instance of Object';
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" ' + (name.indexOf('.') >= 0 ? 'property' : 'argument') + ' must be ' + expected + '. Received ' + received);
}

// ---- types (@types/node's child_process.d.ts) ----

export type Serializable = string | object | number | boolean | bigint;
export type SendHandle = Socket | Server | DgramSocket | undefined;
export type IOType = 'overlapped' | 'pipe' | 'ignore' | 'inherit';
export type StdioOptions = IOType | Array<IOType | 'ipc' | number | null | undefined>;
export type SerializationType = 'json' | 'advanced';
export type StdioPipeNamed = 'pipe' | 'overlapped';
export type StdioPipe = undefined | null | StdioPipeNamed;

export interface MessageOptions {
    keepOpen?: boolean | undefined;
}

export interface ProcessEnvOptions {
    uid?: number | undefined;
    gid?: number | undefined;
    cwd?: string | URL | undefined;
    env?: NodeJS.ProcessEnv | undefined;
}

export interface CommonOptions extends ProcessEnvOptions {
    windowsHide?: boolean | undefined;
    timeout?: number | undefined;
}

export interface Abortable {
    signal?: AbortSignal | undefined;
}

export interface MessagingOptions extends Abortable {
    serialization?: SerializationType | undefined;
    killSignal?: NodeJS.Signals | number | undefined;
    timeout?: number | undefined;
}

export interface CommonSpawnOptions extends CommonOptions, MessagingOptions, Abortable {
    argv0?: string | undefined;
    stdio?: StdioOptions | undefined;
    shell?: boolean | string | undefined;
    windowsVerbatimArguments?: boolean | undefined;
}

export interface SpawnOptions extends CommonSpawnOptions {
    detached?: boolean | undefined;
}

export interface SpawnOptionsWithoutStdio extends SpawnOptions {
    stdio?: StdioPipeNamed | StdioPipe[] | undefined;
}

export interface ExecOptions extends CommonOptions {
    shell?: string | undefined;
    signal?: AbortSignal | undefined;
    maxBuffer?: number | undefined;
    killSignal?: NodeJS.Signals | number | undefined;
    encoding?: string | null | undefined;
}

export interface ExecOptionsWithStringEncoding extends ExecOptions {
    encoding?: BufferEncoding | undefined;
}

export interface ExecOptionsWithBufferEncoding extends ExecOptions {
    encoding: 'buffer' | null;
}

export interface ExecException extends Error {
    cmd?: string | undefined;
    killed?: boolean | undefined;
    code?: number | undefined;
    signal?: NodeJS.Signals | undefined;
    stdout?: string | undefined;
    stderr?: string | undefined;
}

export interface ExecFileOptions extends CommonOptions, Abortable {
    maxBuffer?: number | undefined;
    killSignal?: NodeJS.Signals | number | undefined;
    windowsVerbatimArguments?: boolean | undefined;
    shell?: boolean | string | undefined;
    signal?: AbortSignal | undefined;
    encoding?: string | null | undefined;
}

export interface ExecFileOptionsWithStringEncoding extends ExecFileOptions {
    encoding?: BufferEncoding | undefined;
}

export interface ExecFileOptionsWithBufferEncoding extends ExecFileOptions {
    encoding: 'buffer' | null;
}

export interface ExecFileException extends ExecException {
    code?: number | undefined;
}

export interface ForkOptions extends ProcessEnvOptions, MessagingOptions, Abortable {
    execPath?: string | undefined;
    execArgv?: string[] | undefined;
    silent?: boolean | undefined;
    stdio?: StdioOptions | undefined;
    detached?: boolean | undefined;
    windowsVerbatimArguments?: boolean | undefined;
}

export interface SpawnSyncOptions extends CommonSpawnOptions {
    input?: string | NodeJS.ArrayBufferView | undefined;
    maxBuffer?: number | undefined;
    encoding?: BufferEncoding | 'buffer' | null | undefined;
}

export interface SpawnSyncOptionsWithStringEncoding extends SpawnSyncOptions {
    encoding: BufferEncoding;
}

export interface SpawnSyncOptionsWithBufferEncoding extends SpawnSyncOptions {
    encoding?: 'buffer' | null | undefined;
}

export interface SpawnSyncReturns<T> {
    pid: number;
    output: Array<T | null>;
    stdout: T;
    stderr: T;
    status: number | null;
    signal: NodeJS.Signals | null;
    error?: Error | undefined;
}

export interface CommonExecOptions extends CommonOptions {
    input?: string | NodeJS.ArrayBufferView | undefined;
    stdio?: StdioOptions | undefined;
    killSignal?: NodeJS.Signals | number | undefined;
    maxBuffer?: number | undefined;
    encoding?: BufferEncoding | 'buffer' | null | undefined;
}

export interface ExecSyncOptions extends CommonExecOptions {
    shell?: string | undefined;
}

export interface ExecSyncOptionsWithStringEncoding extends ExecSyncOptions {
    encoding: BufferEncoding;
}

export interface ExecSyncOptionsWithBufferEncoding extends ExecSyncOptions {
    encoding?: 'buffer' | null | undefined;
}

export interface ExecFileSyncOptions extends CommonExecOptions {
    shell?: boolean | string | undefined;
}

export interface ExecFileSyncOptionsWithStringEncoding extends ExecFileSyncOptions {
    encoding: BufferEncoding;
}

export interface ExecFileSyncOptionsWithBufferEncoding extends ExecFileSyncOptions {
    encoding?: 'buffer' | null | undefined;
}

// ---- signals (lib/internal/util.js convertToValidSignal) ----

function convertToValidSignal(signal: string | number): number {
    if (typeof signal === 'number') {
        if (__kml_native.signalName(signal) !== '' || signal === 0) return signal;
    } else {
        const n = __kml_native.signalNumber(signal);
        if (n > 0) return n;
    }
    throw new NodeTypeError('ERR_UNKNOWN_SIGNAL', 'Unknown signal: ' + signal);
}

function sanitizeKillSignal(killSignal: NodeJS.Signals | number | undefined): number {
    if (killSignal === undefined) return convertToValidSignal('SIGTERM');
    return convertToValidSignal(killSignal);
}

function validateTimeout(timeout: number | undefined): void {
    if (timeout !== undefined && !(Number.isInteger(timeout) && timeout >= 0)) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "timeout" is out of range. It must be an unsigned integer. Received ' + timeout);
    }
}

function validateMaxBuffer(maxBuffer: number | undefined): void {
    if (maxBuffer !== undefined && !(maxBuffer >= 0)) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "options.maxBuffer" is out of range. It must be a positive number. Received ' + maxBuffer);
    }
}

function validateArgumentNullCheck(arg: string, propName: string): void {
    if (arg.indexOf('\u0000') >= 0) {
        throw invalidArgValue(propName, arg, 'must be a string without null bytes');
    }
}

// ---- spawn arguments (normalizeSpawnArguments) ----

interface NormalizedSpawn {
    file: string;
    args: string[];
    envPairs: string[];
    envGiven: boolean;
    cwd: string;
    detached: boolean;
    windowsHide: boolean;
    windowsVerbatimArguments: boolean;
    uid: number;
    gid: number;
    stdio: StdioOptions | undefined;
    timeout: number | undefined;
    killSignal: NodeJS.Signals | number | undefined;
    signal: AbortSignal | undefined;
    serialization: SerializationType | undefined;
    // fork's deliberate re-run of this program (not refused at its start).
    selfRun: boolean;
}

function pathOf(p: string | URL | undefined, name: string): string {
    if (p === undefined) return '';
    if (typeof p === 'string') {
        validateArgumentNullCheck(p, name);
        return p;
    }
    if (p.protocol !== 'file:') {
        throw new NodeTypeError('ERR_INVALID_URL_SCHEME', 'The URL must be of scheme file');
    }
    return decodeURIComponent(p.pathname);
}

function normalizeSpawnArguments(file: string, args: readonly string[], options: SpawnOptions): NormalizedSpawn {
    validateArgumentNullCheck(file, 'file');
    if (file.length === 0) throw invalidArgValue('file', file, 'cannot be empty');
    let argv: string[] = args.slice();
    for (let i = 0; i < argv.length; i++) validateArgumentNullCheck(argv[i], 'args[' + i + ']');
    const cwd = pathOf(options.cwd, 'options.cwd');
    if (options.uid !== undefined && !Number.isInteger(options.uid)) {
        throw invalidArgType('options.uid', 'of type int32', options.uid);
    }
    if (options.gid !== undefined && !Number.isInteger(options.gid)) {
        throw invalidArgType('options.gid', 'of type int32', options.gid);
    }
    if (options.argv0 !== undefined) validateArgumentNullCheck(options.argv0, 'options.argv0');
    let windowsVerbatimArguments = options.windowsVerbatimArguments === true;
    let command = file;
    const shell = options.shell;
    if (shell !== undefined && shell !== false) {
        if (typeof shell === 'string') validateArgumentNullCheck(shell, 'options.shell');
        const line = argv.length > 0 ? file + ' ' + argv.join(' ') : file;
        if (process.platform === 'win32') {
            if (typeof shell === 'string') command = shell;
            else command = process.env.comspec || 'cmd.exe';
            // '/d /s /c' is used only for cmd.exe.
            if (/^(?:.*\\)?cmd(?:\.exe)?$/i.test(command)) {
                argv = ['/d', '/s', '/c', '"' + line + '"'];
                windowsVerbatimArguments = true;
            } else {
                argv = ['-c', line];
            }
        } else {
            command = typeof shell === 'string' ? shell : '/bin/sh';
            argv = ['-c', line];
        }
    }
    argv.unshift(options.argv0 !== undefined ? options.argv0 : command);
    // The child's environment: the given one, or ours (an empty list,
    // inherited as it stands, process.env's writes included).
    const envPairs: string[] = [];
    const env = options.env;
    if (env !== undefined) {
        let keys: string[] = [];
        for (const key in env) keys.push(key);
        if (process.platform === 'win32') {
            // Windows keys are case-insensitive: the first, in sorted order.
            keys.sort();
            const seen = new Set<string>();
            keys = keys.filter((k) => {
                const upper = k.toUpperCase();
                if (seen.has(upper)) return false;
                seen.add(upper);
                return true;
            });
        }
        for (const key of keys) {
            const value = env[key];
            if (value !== undefined) {
                validateArgumentNullCheck(key, "options.env['" + key + "']");
                validateArgumentNullCheck(value, "options.env['" + key + "']");
                envPairs.push(key + '=' + value);
            }
        }
    }
    return {
        file: command,
        args: argv,
        envPairs: envPairs,
        envGiven: env !== undefined,
        cwd: cwd,
        detached: options.detached === true,
        windowsHide: options.windowsHide === true,
        windowsVerbatimArguments: windowsVerbatimArguments,
        uid: options.uid !== undefined ? options.uid : -1,
        gid: options.gid !== undefined ? options.gid : -1,
        stdio: options.stdio,
        timeout: options.timeout,
        killSignal: options.killSignal,
        signal: options.signal,
        serialization: options.serialization,
        selfRun: false,
    };
}

// NUL-terminated strings back to back, as the native spawn reads them.
function packStrings(list: string[]): Uint8Array {
    let joined = '';
    for (const s of list) joined += s + '\u0000';
    return Buffer.from(joined, 'utf8');
}

// ---- stdio (getValidStdio) ----

interface StdioEntry {
    type: string; // 'pipe', 'ipc', 'ignore', 'inherit', 'fd'
    fd: number;
}

function stdioStringToArray(stdio: string, channel?: string): Array<IOType | 'ipc' | number | null | undefined> {
    const out: Array<IOType | 'ipc' | number | null | undefined> = [];
    switch (stdio) {
        case 'ignore':
        case 'overlapped':
        case 'pipe':
            out.push(stdio, stdio, stdio);
            break;
        case 'inherit':
            out.push(0, 1, 2);
            break;
        default:
            throw invalidArgValue('stdio', stdio, 'is invalid');
    }
    if (channel === 'ipc') out.push('ipc');
    return out;
}

function getValidStdio(stdio: StdioOptions, sync: boolean): StdioEntry[] {
    let list: Array<IOType | 'ipc' | number | null | undefined>;
    if (typeof stdio === 'string') list = stdioStringToArray(stdio);
    else list = stdio.slice();
    while (list.length < 3) list.push(undefined);
    const out: StdioEntry[] = [];
    let ipc = false;
    for (let i = 0; i < list.length; i++) {
        let s = list[i];
        if (s === null || s === undefined) s = i < 3 ? 'pipe' : 'ignore';
        if (s === 'ignore') {
            out.push({ type: 'ignore', fd: -1 });
        } else if (s === 'pipe' || s === 'overlapped' || (typeof s === 'number' && s < 0)) {
            out.push({ type: 'pipe', fd: -1 });
        } else if (s === 'ipc') {
            if (sync || ipc) {
                if (!sync) throw new NodeError('ERR_IPC_ONE_PIPE', 'Child process can have only one IPC pipe');
                throw new NodeError('ERR_IPC_SYNC_FORK', 'IPC cannot be used with synchronous forks');
            }
            ipc = true;
            out.push({ type: 'ipc', fd: -1 });
        } else if (s === 'inherit') {
            out.push({ type: 'inherit', fd: i });
        } else if (typeof s === 'number') {
            out.push({ type: 'fd', fd: s });
        } else {
            throw invalidArgValue('stdio', s, 'is invalid');
        }
    }
    return out;
}

// The native spawn's stdio spec.
function stdioSpec(entries: StdioEntry[]): string {
    const parts: string[] = [];
    for (const e of entries) {
        if (e.type === 'pipe') parts.push('p');
        else if (e.type === 'ipc') parts.push('c');
        else if (e.type === 'ignore') parts.push('i');
        else parts.push(String(e.fd));
    }
    return parts.join(',');
}

function spawnFlags(o: NormalizedSpawn): number {
    return (o.detached ? 1 : 0) | (o.windowsVerbatimArguments ? 2 : 0) | (o.selfRun ? 4 : 0);
}

// ---- ChildProcess (lib/internal/child_process.js) ----

export class ChildProcess extends EventEmitter {
    stdin: Writable | null = null;
    stdout: Readable | null = null;
    stderr: Readable | null = null;
    readonly stdio: Array<Readable | Writable | null> = [];
    killed = false;
    connected = false;
    exitCode: number | null = null;
    signalCode: NodeJS.Signals | null = null;
    spawnargs: string[] = [];
    spawnfile: string = '';
    pid: number | undefined = undefined;
    _handle: number = -1;
    private closesNeeded = 1;
    private closesGot = 0;
    // The IPC channel's Control (ref/unref), as Node exposes it.
    channel: any = null;
    _kmlChannel: Channel | null = null;

    constructor() {
        super();
    }

    // The native handle's exit: exitCode < 0 is a failed spawn's errno.
    private onExit(exitCode: number, signal: number): void {
        if (signal !== 0) {
            this.signalCode = __kml_native.signalName(signal) as NodeJS.Signals;
        } else {
            this.exitCode = exitCode;
        }
        if (this.stdin) this.stdin.destroy();
        if (this._handle >= 0) {
            __kml_native.processClose(this._handle);
            this._handle = -1;
        }
        if (exitCode < 0) {
            const syscall = this.spawnfile ? 'spawn ' + this.spawnfile : 'spawn';
            const err: any = errnoException(exitCode, syscall);
            if (this.spawnfile) err.path = this.spawnfile;
            err.spawnargs = this.spawnargs.slice(1);
            this.emit('error', err);
        } else {
            this.emit('exit', this.exitCode, this.signalCode);
        }
        // Any stdio stream not yet read is drained, so it ends and closes;
        // on the next tick, so a listener may still start reading first.
        process.nextTick(() => this.flushStdio());
        this.maybeClose();
    }

    private flushStdio(): void {
        for (const stream of this.stdio) {
            if (stream === null) continue;
            const r: any = stream;
            if (r.readable && r.readableFlowing === null) r.resume();
        }
    }

    private maybeClose(): void {
        this.closesGot++;
        if (this.closesGot === this.closesNeeded) {
            this.emit('close', this.exitCode, this.signalCode);
        }
    }

    spawn(options: SpawnOptions & { file: string; args: string[]; envPairs?: string[] }): void {
        const o = normalizeSpawnArguments(options.file, options.args.slice(1), options);
        this._spawnNormalized(o, options.args[0]);
    }

    _spawnNormalized(o: NormalizedSpawn, argv0: string): void {
        const stdio = getValidStdio(o.stdio !== undefined ? o.stdio : 'pipe', false);
        let ipcFd = -1;
        for (let i = 0; i < stdio.length; i++) {
            if (stdio[i].type === 'ipc') ipcFd = i;
        }
        const envPairs = o.envPairs.slice();
        let envGiven = o.envGiven;
        if (ipcFd >= 0) {
            if (!envGiven) {
                // The channel's variables join ours.
                for (const key in process.env) {
                    const v = process.env[key];
                    if (v !== undefined) envPairs.push(key + '=' + v);
                }
                envGiven = true;
            }
            envPairs.push('NODE_CHANNEL_FD=' + ipcFd);
            envPairs.push('NODE_CHANNEL_SERIALIZATION_MODE=' + (o.serialization !== undefined ? o.serialization : 'json'));
        }
        this.spawnfile = o.file;
        this.spawnargs = o.args;
        const args = o.args.slice();
        args[0] = argv0 !== undefined ? argv0 : args[0];
        const handle = __kml_native.processSpawn(o.file, packStrings(args), envGiven ? packStrings(envPairs) : new Uint8Array(0),
            o.cwd, spawnFlags(o), stdioSpec(stdio), o.uid, o.gid,
            (exitCode: number, signal: number) => { this.onExit(exitCode, signal); });
        if (handle < 0) {
            const errno = -handle;
            const code = __kml_native.errnoName(errno);
            if (code === 'EACCES' || code === 'EAGAIN' || code === 'EMFILE' || code === 'ENFILE' || code === 'ENOENT') {
                // A run-time error is an 'error' event, not a throw.
                process.nextTick(() => { this.onExit(-errno, 0); });
                if (code === 'EMFILE' || code === 'ENFILE') return;
            } else {
                throw errnoException(-errno, 'spawn');
            }
        } else {
            this._handle = handle;
            this.pid = __kml_native.processPid(handle);
            process.nextTick(() => { this.emit('spawn'); });
        }
        for (let i = 0; i < stdio.length; i++) {
            const entry = stdio[i];
            if (entry.type === 'ignore' || entry.type === 'inherit' || entry.type === 'fd') {
                this.stdio.push(null);
                continue;
            }
            const h = handle >= 0 ? __kml_native.processStdio(i) : -1;
            if (entry.type === 'ipc') {
                this.closesNeeded++;
                this.stdio.push(null);
                if (h >= 0) this.setupChannel(h);
                continue;
            }
            const socket = new Socket(h >= 0 ? { handle: h, readable: i > 0, writable: i === 0 } : { readable: i > 0, writable: i === 0 });
            if (i > 0 && handle >= 0) {
                this.closesNeeded++;
                socket.on('close', () => { this.maybeClose(); });
            }
            this.stdio.push(socket);
        }
        const s0 = this.stdio.length > 0 ? this.stdio[0] : null;
        const s1 = this.stdio.length > 1 ? this.stdio[1] : null;
        const s2 = this.stdio.length > 2 ? this.stdio[2] : null;
        this.stdin = s0 !== null ? (s0 as Writable) : null;
        this.stdout = s1 !== null ? (s1 as Readable) : null;
        this.stderr = s2 !== null ? (s2 as Readable) : null;
    }

    kill(signal?: NodeJS.Signals | number): boolean {
        const sig = signal === 0 ? 0 : convertToValidSignal(signal === undefined ? 'SIGTERM' : signal);
        if (this._handle >= 0) {
            const err = __kml_native.processKill(this._handle, sig);
            if (err === 0) {
                this.killed = true;
                return true;
            }
            const code = __kml_native.errnoName(-err);
            if (code === 'ESRCH') {
                // Already dead.
            } else if (code === 'EINVAL' || code === 'ENOSYS') {
                throw errnoException(err, 'kill');
            } else {
                this.emit('error', errnoException(err, 'kill'));
            }
        }
        return false;
    }

    ref(): void {
        if (this._handle >= 0) __kml_native.processRef(this._handle, true);
        if (this._kmlChannel !== null) this._kmlChannel.socket.ref();
    }

    unref(): void {
        if (this._handle >= 0) __kml_native.processRef(this._handle, false);
        if (this._kmlChannel !== null) this._kmlChannel.socket.unref();
    }

    // ---- the IPC channel (setupChannel) ----

    private setupChannel(h: number): void {
        const chan = new Channel(this, h, () => { this.maybeClose(); });
        this._kmlChannel = chan;
        this.channel = chan.control;
        this.connected = true;
    }

    send(message: Serializable, callback?: (error: Error | null) => void): boolean;
    send(message: Serializable, sendHandle?: SendHandle, callback?: (error: Error | null) => void): boolean;
    send(message: Serializable, sendHandle?: SendHandle, options?: MessageOptions, callback?: (error: Error | null) => void): boolean;
    send(message: Serializable, a?: any, b?: any, c?: any): boolean {
        const chan = this._kmlChannel;
        if (chan === null) {
            const ex = new NodeError('ERR_IPC_CHANNEL_CLOSED', 'Channel closed');
            const cb = typeof a === 'function' ? a : typeof b === 'function' ? b : typeof c === 'function' ? c : undefined;
            if (cb !== undefined) process.nextTick(() => { cb(ex); });
            else process.nextTick(() => { this.emit('error', ex); });
            return false;
        }
        return chan.send(message, a, b, c);
    }

    disconnect(): void {
        if (this._kmlChannel === null) {
            this.emit('error', new NodeError('ERR_IPC_DISCONNECTED', 'IPC channel is already disconnected'));
            return;
        }
        this._kmlChannel.disconnect();
    }
}

export interface ChildProcessWithoutNullStreams extends ChildProcess {
    stdin: Writable;
    stdout: Readable;
    stderr: Readable;
}

// ---- spawn ----

function abortChildProcess(child: ChildProcess, killSignal: number, reason: any): void {
    try {
        if (child.kill(killSignal)) {
            const e: any = new Error('The operation was aborted');
            e.name = 'AbortError';
            e.code = 'ABORT_ERR';
            e.cause = reason;
            child.emit('error', e);
        }
    } catch (err) {
        child.emit('error', err);
    }
}

function spawnNormalized(o: NormalizedSpawn): ChildProcess {
    validateTimeout(o.timeout);
    const killSignal = sanitizeKillSignal(o.killSignal);
    const child = new ChildProcess();
    child._spawnNormalized(o, o.args[0]);
    const timeout = o.timeout;
    if (timeout !== undefined && timeout > 0) {
        let timer: ReturnType<typeof setTimeout> | null = setTimeout(() => {
            if (timer !== null) {
                try {
                    child.kill(killSignal);
                } catch (err) {
                    child.emit('error', err);
                }
                timer = null;
            }
        }, timeout);
        child.once('exit', () => {
            if (timer !== null) {
                clearTimeout(timer);
                timer = null;
            }
        });
    }
    const signal = o.signal;
    if (signal !== undefined) {
        if (signal.aborted) {
            process.nextTick(() => { abortChildProcess(child, killSignal, signal.reason); });
        } else {
            const onAbort = () => { abortChildProcess(child, killSignal, signal.reason); };
            signal.addEventListener('abort', onAbort, { once: true });
            child.once('exit', () => { signal.removeEventListener('abort', onAbort); });
        }
    }
    return child;
}

export function spawn(command: string, options?: SpawnOptionsWithoutStdio): ChildProcessWithoutNullStreams;
export function spawn(command: string, options: SpawnOptions): ChildProcess;
export function spawn(command: string, args?: readonly string[], options?: SpawnOptionsWithoutStdio): ChildProcessWithoutNullStreams;
export function spawn(command: string, args: readonly string[], options: SpawnOptions): ChildProcess;
export function spawn(command: string, a?: any, b?: any): ChildProcess {
    let args: readonly string[] = [];
    let options: SpawnOptions = {};
    if (Array.isArray(a)) {
        args = a;
        if (b !== undefined && b !== null) options = b;
    } else if (a !== undefined && a !== null) {
        options = a;
    }
    return spawnNormalized(normalizeSpawnArguments(command, args, options));
}

// ---- exec / execFile ----

const MAX_BUFFER = 1024 * 1024;

function execFileImpl(file: string, args: readonly string[], options: ExecFileOptions, callback: any): ChildProcess {
    const encodingOpt = options.encoding === undefined ? 'utf8' : options.encoding;
    const timeout = options.timeout !== undefined ? options.timeout : 0;
    const maxBuffer = options.maxBuffer !== undefined ? options.maxBuffer : MAX_BUFFER;
    validateTimeout(timeout);
    validateMaxBuffer(maxBuffer);
    const killSignal = sanitizeKillSignal(options.killSignal);
    const child = spawn(file, args, {
        cwd: options.cwd,
        env: options.env,
        gid: options.gid,
        shell: options.shell,
        signal: options.signal,
        uid: options.uid,
        windowsHide: options.windowsHide === true,
        windowsVerbatimArguments: options.windowsVerbatimArguments === true,
    });
    const encoding: BufferEncoding | null = encodingOpt !== null && encodingOpt !== 'buffer' && Buffer.isEncoding(encodingOpt) ? (encodingOpt as BufferEncoding) : null;
    // A stream with an encoding delivers strings, else Buffers.
    const stdoutText: string[] = [];
    const stderrText: string[] = [];
    const stdoutBytes: Buffer[] = [];
    const stderrBytes: Buffer[] = [];
    let stdoutLen = 0;
    let stderrLen = 0;
    let killed = false;
    let exited = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let ex: any = null;
    let cmd = file;

    const join = (text: string[], bytes: Buffer[], stream: Readable | null): any => {
        if (encoding !== null || (stream !== null && stream.readableEncoding !== null)) {
            return text.join('');
        }
        return Buffer.concat(bytes);
    };
    const push = (text: string[], bytes: Buffer[], chunk: any): void => {
        if (typeof chunk === 'string') text.push(chunk);
        else bytes.push(chunk);
    };

    const exithandler = (code?: number | null, signal?: NodeJS.Signals | null): void => {
        if (exited) return;
        exited = true;
        if (timer !== null) {
            clearTimeout(timer);
            timer = null;
        }
        if (!callback) return;
        const stdout = join(stdoutText, stdoutBytes, child.stdout);
        const stderr = join(stderrText, stderrBytes, child.stderr);
        if (!ex && code === 0 && signal === null) {
            callback(null, stdout, stderr);
            return;
        }
        if (args.length > 0) cmd += ' ' + args.join(' ');
        if (!ex) {
            ex = new Error('Command failed: ' + cmd + '\n' + stderr);
            ex.code = code !== undefined && code !== null && code < 0 ? __kml_native.errnoName(code) : code;
            ex.killed = child.killed || killed;
            ex.signal = signal;
        }
        ex.cmd = cmd;
        callback(ex, stdout, stderr);
    };

    const kill = (): void => {
        if (child.stdout) child.stdout.destroy();
        if (child.stderr) child.stderr.destroy();
        killed = true;
        try {
            child.kill(killSignal);
        } catch (e) {
            ex = e;
            exithandler();
        }
    };

    if (timeout > 0) {
        timer = setTimeout(() => {
            kill();
            timer = null;
        }, timeout);
    }

    const out = child.stdout;
    if (out !== null) {
        if (encoding !== null) out.setEncoding(encoding);
        out.on('data', (chunk: any) => {
            if (maxBuffer === Infinity) {
                push(stdoutText, stdoutBytes, chunk);
                return;
            }
            const enc = out.readableEncoding;
            const length = enc !== null ? Buffer.byteLength(chunk, enc as BufferEncoding) : chunk.length;
            stdoutLen += length;
            if (stdoutLen > maxBuffer) {
                const truncatedLen = maxBuffer - (stdoutLen - length);
                push(stdoutText, stdoutBytes, chunk.slice(0, truncatedLen));
                ex = new NodeRangeError('ERR_CHILD_PROCESS_STDIO_MAXBUFFER', 'stdout maxBuffer length exceeded');
                kill();
            } else {
                push(stdoutText, stdoutBytes, chunk);
            }
        });
    }
    const err = child.stderr;
    if (err !== null) {
        if (encoding !== null) err.setEncoding(encoding);
        err.on('data', (chunk: any) => {
            if (maxBuffer === Infinity) {
                push(stderrText, stderrBytes, chunk);
                return;
            }
            const enc = err.readableEncoding;
            const length = enc !== null ? Buffer.byteLength(chunk, enc as BufferEncoding) : chunk.length;
            stderrLen += length;
            if (stderrLen > maxBuffer) {
                const truncatedLen = maxBuffer - (stderrLen - length);
                push(stderrText, stderrBytes, chunk.slice(0, truncatedLen));
                ex = new NodeRangeError('ERR_CHILD_PROCESS_STDIO_MAXBUFFER', 'stderr maxBuffer length exceeded');
                kill();
            } else {
                push(stderrText, stderrBytes, chunk);
            }
        });
    }
    child.addListener('close', (code: number | null, signal: NodeJS.Signals | null) => { exithandler(code, signal); });
    child.addListener('error', (e: Error) => {
        ex = e;
        if (child.stdout) child.stdout.destroy();
        if (child.stderr) child.stderr.destroy();
        exithandler();
    });
    return child;
}

export function exec(command: string, callback?: (error: ExecException | null, stdout: string, stderr: string) => void): ChildProcess;
export function exec(command: string, options: ExecOptionsWithBufferEncoding, callback?: (error: ExecException | null, stdout: Buffer, stderr: Buffer) => void): ChildProcess;
export function exec(command: string, options: ExecOptionsWithStringEncoding, callback?: (error: ExecException | null, stdout: string, stderr: string) => void): ChildProcess;
export function exec(command: string, options: ExecOptions | undefined | null, callback?: (error: ExecException | null, stdout: string | Buffer, stderr: string | Buffer) => void): ChildProcess;
export function exec(command: string, a?: any, b?: any): ChildProcess {
    validateArgumentNullCheck(command, 'command');
    let options: ExecOptions = {};
    let callback: any = undefined;
    if (typeof a === 'function') {
        callback = a;
    } else {
        if (a !== undefined && a !== null) options = a;
        callback = b;
    }
    let shell: string | boolean = true;
    if (typeof options.shell === 'string') shell = options.shell;
    return execFileImpl(command, [], {
        cwd: options.cwd,
        env: options.env,
        uid: options.uid,
        gid: options.gid,
        windowsHide: options.windowsHide,
        timeout: options.timeout,
        maxBuffer: options.maxBuffer,
        killSignal: options.killSignal,
        encoding: options.encoding,
        signal: options.signal,
        shell: shell,
    }, callback);
}

export function execFile(file: string, callback?: (error: ExecFileException | null, stdout: string, stderr: string) => void): ChildProcess;
export function execFile(file: string, args: readonly string[] | undefined | null, callback?: (error: ExecFileException | null, stdout: string, stderr: string) => void): ChildProcess;
export function execFile(file: string, options: ExecFileOptionsWithBufferEncoding, callback?: (error: ExecFileException | null, stdout: Buffer, stderr: Buffer) => void): ChildProcess;
export function execFile(file: string, options: ExecFileOptionsWithStringEncoding, callback?: (error: ExecFileException | null, stdout: string, stderr: string) => void): ChildProcess;
export function execFile(file: string, options: ExecFileOptions | undefined | null, callback?: (error: ExecFileException | null, stdout: string | Buffer, stderr: string | Buffer) => void): ChildProcess;
export function execFile(file: string, args: readonly string[] | undefined | null, options: ExecFileOptionsWithBufferEncoding, callback?: (error: ExecFileException | null, stdout: Buffer, stderr: Buffer) => void): ChildProcess;
export function execFile(file: string, args: readonly string[] | undefined | null, options: ExecFileOptionsWithStringEncoding, callback?: (error: ExecFileException | null, stdout: string, stderr: string) => void): ChildProcess;
export function execFile(file: string, args: readonly string[] | undefined | null, options: ExecFileOptions | undefined | null, callback?: (error: ExecFileException | null, stdout: string | Buffer, stderr: string | Buffer) => void): ChildProcess;
export function execFile(file: string, a?: any, b?: any, c?: any): ChildProcess {
    // normalizeExecFileArgs: the callback is the last function given.
    let args: any = a;
    let options: any = b;
    let callback: any = c;
    if (Array.isArray(args)) {
        args = args.slice();
    } else if (args !== null && args !== undefined && typeof args === 'object') {
        callback = options;
        options = args;
        args = null;
    } else if (typeof args === 'function') {
        callback = args;
        options = null;
        args = null;
    }
    if (args === null || args === undefined) args = [];
    if (typeof options === 'function') {
        callback = options;
    } else if (options !== null && options !== undefined && typeof options !== 'object') {
        throw invalidArgType('options', 'object', options);
    }
    if (options === null || options === undefined || typeof options === 'function') options = {};
    if (callback !== null && callback !== undefined && typeof callback !== 'function') {
        throw invalidArgType('callback', 'function', callback);
    }
    return execFileImpl(file, args, options, callback);
}

// util.promisify(exec) / util.promisify(execFile) resolve `{ stdout, stderr }`
// and reject with the error carrying both (customPromiseExecFunction); the
// promise's `child` is the process.
const kCustomPromisified: unique symbol = Symbol.for('nodejs.util.promisify.custom');

function customPromiseExecFunction(orig: (a0: any, a1?: any, a2?: any, a3?: any) => ChildProcess): (...args: any[]) => Promise<any> {
    return (...args: any[]): Promise<any> => {
        let settle: (err: any, stdout: any, stderr: any) => void = (err: any, stdout: any, stderr: any): void => {};
        const promise = new Promise<any>((resolve, reject) => {
            settle = (err: any, stdout: any, stderr: any): void => {
                if (err !== null) {
                    err.stdout = stdout;
                    err.stderr = stderr;
                    reject(err);
                } else {
                    resolve({ stdout: stdout, stderr: stderr });
                }
            };
        });
        const cb = (err: any, stdout: any, stderr: any): void => settle(err, stdout, stderr);
        const all: any[] = args.slice();
        all.push(cb);
        const child = orig(all[0], all[1], all[2], all[3]);
        const p: any = promise;
        p.child = child;
        return promise;
    };
}

const execAny: any = exec;
Object.defineProperty(execAny, kCustomPromisified, {
    enumerable: false,
    value: customPromiseExecFunction((a0: any, a1?: any, a2?: any, a3?: any): ChildProcess => exec(a0, a1, a2)),
});
const execFileAny: any = execFile;
Object.defineProperty(execFileAny, kCustomPromisified, {
    enumerable: false,
    value: customPromiseExecFunction((a0: any, a1?: any, a2?: any, a3?: any): ChildProcess => execFile(a0, a1, a2, a3)),
});

// ---- the synchronous forms ----

function spawnSyncNormalized(o: NormalizedSpawn, input: any, maxBufferOpt: number | undefined, encoding: BufferEncoding | 'buffer' | null | undefined): any {
    validateTimeout(o.timeout);
    const maxBuffer = maxBufferOpt !== undefined ? maxBufferOpt : MAX_BUFFER;
    validateMaxBuffer(maxBuffer);
    const killSignal = sanitizeKillSignal(o.killSignal);
    const stdio = getValidStdio(o.stdio !== undefined ? o.stdio : 'pipe', true);
    let inputBytes: Uint8Array = new Uint8Array(0);
    if (input !== undefined) {
        if (typeof input === 'string') {
            const text: string = input;
            inputBytes = Buffer.from(text, encoding !== undefined && encoding !== null && encoding !== 'buffer' ? encoding : 'utf8');
        } else {
            // The view's bytes, whatever its element type.
            const view: any = input;
            inputBytes = new Uint8Array(view.buffer, view.byteOffset, view.byteLength);
        }
    }
    const rc = __kml_native.spawnSync(o.file, packStrings(o.args), o.envGiven ? packStrings(o.envPairs) : new Uint8Array(0),
        o.cwd, spawnFlags(o), stdioSpec(stdio), o.uid, o.gid, inputBytes,
        o.timeout !== undefined ? o.timeout : 0, maxBuffer === Infinity ? 0 : maxBuffer, killSignal);
    const decode = encoding !== undefined && encoding !== null && encoding !== 'buffer';
    const result: any = {};
    if (rc < 0) {
        const error: any = errnoException(rc, 'spawnSync ' + o.file);
        error.path = o.file;
        error.spawnargs = o.args.slice(1);
        result.error = error;
        result.status = null;
        result.signal = null;
        result.output = null;
        result.pid = 0;
        result.stdout = undefined;
        result.stderr = undefined;
        return result;
    }
    const output: any[] = [];
    for (let i = 0; i < stdio.length; i++) {
        // Only a pipe the child writes has output: stdin's (the child reads
        // it) is null, as getValidStdio marks it readable only.
        if (stdio[i].type !== 'pipe' || i === 0) {
            output.push(null);
            continue;
        }
        const buf = Buffer.alloc(__kml_native.spawnSyncResult(4 + i));
        __kml_native.spawnSyncTake(i, buf);
        if (decode) {
            output.push(buf.toString(encoding as BufferEncoding));
        } else {
            output.push(buf);
        }
    }
    const errno = __kml_native.spawnSyncResult(3);
    if (errno !== 0) {
        const error: any = errnoException(errno, 'spawnSync ' + o.file);
        error.path = o.file;
        error.spawnargs = o.args.slice(1);
        result.error = error;
    }
    const sig = __kml_native.spawnSyncResult(2);
    result.status = sig !== 0 ? null : __kml_native.spawnSyncResult(1);
    result.signal = sig !== 0 ? __kml_native.signalName(sig) : null;
    result.output = output;
    result.pid = __kml_native.spawnSyncResult(0);
    result.stdout = output.length > 1 ? output[1] : null;
    result.stderr = output.length > 2 ? output[2] : null;
    return result;
}

export function spawnSync(command: string): SpawnSyncReturns<Buffer>;
export function spawnSync(command: string, options: SpawnSyncOptionsWithStringEncoding): SpawnSyncReturns<string>;
export function spawnSync(command: string, options: SpawnSyncOptionsWithBufferEncoding): SpawnSyncReturns<Buffer>;
export function spawnSync(command: string, options?: SpawnSyncOptions): SpawnSyncReturns<string | Buffer>;
export function spawnSync(command: string, args: readonly string[]): SpawnSyncReturns<Buffer>;
export function spawnSync(command: string, args: readonly string[], options: SpawnSyncOptionsWithStringEncoding): SpawnSyncReturns<string>;
export function spawnSync(command: string, args: readonly string[], options: SpawnSyncOptionsWithBufferEncoding): SpawnSyncReturns<Buffer>;
export function spawnSync(command: string, args?: readonly string[], options?: SpawnSyncOptions): SpawnSyncReturns<string | Buffer>;
export function spawnSync(command: string, a?: any, b?: any): any {
    let args: readonly string[] = [];
    let options: SpawnSyncOptions = {};
    if (Array.isArray(a)) {
        args = a;
        if (b !== undefined && b !== null) options = b;
    } else if (a !== undefined && a !== null) {
        options = a;
    }
    return spawnSyncNormalized(normalizeSpawnArguments(command, args, options), options.input, options.maxBuffer, options.encoding);
}

function checkExecSyncError(ret: any, cmd: string): any {
    let err: any = undefined;
    if (ret.error) {
        err = ret.error;
        for (const k in ret) {
            if (k !== 'error') err[k] = ret[k];
        }
    } else if (ret.status !== 0) {
        let msg = 'Command failed: ' + cmd;
        if (ret.stderr && ret.stderr.length > 0) msg += '\n' + ret.stderr.toString();
        err = new Error(msg);
        for (const k in ret) err[k] = ret[k];
    }
    return err;
}

function execSyncCommon(file: string, args: readonly string[], options: ExecFileSyncOptions, cmd: string): any {
    const inheritStderr = options.stdio === undefined;
    const spawnOpts: SpawnSyncOptions = {
        cwd: options.cwd,
        env: options.env,
        uid: options.uid,
        gid: options.gid,
        windowsHide: options.windowsHide,
        timeout: options.timeout,
        killSignal: options.killSignal,
        stdio: options.stdio,
        shell: options.shell,
        maxBuffer: options.maxBuffer,
        encoding: options.encoding,
        input: options.input,
    };
    const ret = spawnSyncNormalized(normalizeSpawnArguments(file, args, spawnOpts), options.input, options.maxBuffer, options.encoding);
    if (inheritStderr && ret.stderr) process.stderr.write(ret.stderr);
    const err = checkExecSyncError(ret, cmd);
    if (err) throw err;
    return ret.stdout;
}

export function execSync(command: string): Buffer;
export function execSync(command: string, options: ExecSyncOptionsWithStringEncoding): string;
export function execSync(command: string, options: ExecSyncOptionsWithBufferEncoding): Buffer;
export function execSync(command: string, options?: ExecSyncOptions): string | Buffer;
export function execSync(command: string, options?: ExecSyncOptions): any {
    validateArgumentNullCheck(command, 'command');
    const o: ExecSyncOptions = options !== undefined && options !== null ? options : {};
    let shell: string | boolean = true;
    if (typeof o.shell === 'string') shell = o.shell;
    return execSyncCommon(command, [], {
        cwd: o.cwd,
        env: o.env,
        uid: o.uid,
        gid: o.gid,
        windowsHide: o.windowsHide,
        timeout: o.timeout,
        killSignal: o.killSignal,
        stdio: o.stdio,
        shell: shell,
        maxBuffer: o.maxBuffer,
        encoding: o.encoding,
        input: o.input,
    }, command);
}

export function execFileSync(file: string): Buffer;
export function execFileSync(file: string, options: ExecFileSyncOptionsWithStringEncoding): string;
export function execFileSync(file: string, options: ExecFileSyncOptionsWithBufferEncoding): Buffer;
export function execFileSync(file: string, options?: ExecFileSyncOptions): string | Buffer;
export function execFileSync(file: string, args: readonly string[]): Buffer;
export function execFileSync(file: string, args: readonly string[], options: ExecFileSyncOptionsWithStringEncoding): string;
export function execFileSync(file: string, args: readonly string[], options: ExecFileSyncOptionsWithBufferEncoding): Buffer;
export function execFileSync(file: string, args?: readonly string[], options?: ExecFileSyncOptions): string | Buffer;
export function execFileSync(file: string, a?: any, b?: any): any {
    let args: readonly string[] = [];
    let options: ExecFileSyncOptions = {};
    if (Array.isArray(a)) {
        args = a;
        if (b !== undefined && b !== null) options = b;
    } else if (a !== undefined && a !== null) {
        options = a;
    }
    const given: any = options;
    const argv0: string = given.argv0 !== undefined ? given.argv0 : file;
    const cmd = [argv0].concat(args.slice()).join(' ');
    return execSyncCommon(file, args, options, cmd);
}

// ---- fork ----

export function fork(modulePath: string | URL, options?: ForkOptions): ChildProcess;
export function fork(modulePath: string | URL, args?: readonly string[], options?: ForkOptions): ChildProcess;
export function fork(modulePath: string | URL, a?: any, b?: any): ChildProcess {
    const path = pathOf(modulePath, 'modulePath');
    let args: readonly string[] = [];
    let options: ForkOptions = {};
    if (Array.isArray(a)) {
        args = a;
        if (b !== undefined && b !== null) options = b;
    } else if (a !== undefined && a !== null) {
        options = a;
    }
    // The child is this program's own binary, re-executed: a module is one
    // compiled program, so only this program's entry can be forked.
    const resolved = resolvePath(path);
    const argv1 = process.argv[1];
    if (resolved !== __kml_native.entryPath() && (argv1 === undefined || resolved !== resolvePath(argv1))) {
        throw new NodeError('ERR_CHILD_PROCESS_FORK_MODULE', "fork('" + path + "'): only this program's own module can be forked; a different module is a second compiled program");
    }
    const execPath = options.execPath !== undefined ? options.execPath : process.execPath;
    let stdio: StdioOptions;
    if (typeof options.stdio === 'string') {
        stdio = stdioStringToArray(options.stdio, 'ipc');
    } else if (options.stdio === undefined) {
        stdio = stdioStringToArray(options.silent === true ? 'pipe' : 'inherit', 'ipc');
    } else {
        stdio = options.stdio;
        if (stdio.indexOf('ipc') < 0) {
            throw new NodeError('ERR_CHILD_PROCESS_IPC_REQUIRED', "Forked processes must have an IPC channel, missing value 'ipc' in options.stdio");
        }
    }
    // The child is this binary, whose process.argv repeats the executable
    // at index 1 (a single-executable program's layout): its own arguments
    // follow from index 2, where a forked Node child finds them.
    const fullArgs: string[] = args.slice();
    const normalized = normalizeSpawnArguments(execPath, fullArgs, {
        cwd: options.cwd,
        env: options.env,
        uid: options.uid,
        gid: options.gid,
        detached: options.detached,
        stdio: stdio,
        windowsVerbatimArguments: options.windowsVerbatimArguments,
        serialization: options.serialization,
        killSignal: options.killSignal,
        timeout: options.timeout,
        signal: options.signal,
        shell: false,
    });
    normalized.selfRun = true;
    return spawnNormalized(normalized);
}
