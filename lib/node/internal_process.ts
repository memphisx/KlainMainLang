// `process` as an EventEmitter: Node's lib/internal/bootstrap/node.js (the
// process object inherits from EventEmitter; 'warning' has its default
// printer), lib/internal/process/per_thread.js (a signal's first listener
// starts a signal watcher, its last one's removal stops it) and the child
// side of lib/internal/child_process.js (_forkChild, setupChannel: a forked
// program's IPC channel on NODE_CHANNEL_FD, json serialization). The rest of
// `process` is still compiled directly; code generation binds the emitter
// members, send, disconnect, connected and channel, and `process` used as a
// value, to the object _kmlProcess makes on first use; that object holds
// internal_process_methods.ts's methods and internal_process_env.ts's
// members too.
//
// The runtime reports 'exit', 'uncaughtException' and 'unhandledRejection'
// through the hooks registered here (lib/native.d.ts processHook); the
// latter two only while they have listeners, since the runtime's own
// behaviour applies when nothing listens.

import { EventEmitter } from 'events';
import { Channel } from './internal_child_process_channel';
import { _kmlOnWarning, _kmlSetWarningSink } from './internal_process_warning';
import * as methods from './internal_process_methods';
import * as penv from './internal_process_env';
import * as phr from './internal_process_hrtime';

const kExit = 0;
const kUncaughtException = 1;
const kUnhandledRejection = 2;

class NodeError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

// ErrnoException: `${syscall} ${code}`, with errno, code, syscall.
function errnoException(errnoIn: number, syscall: string): Error {
    const errno = errnoIn < 0 ? -errnoIn : errnoIn;
    const code = __kml_native.errnoName(errno);
    const e: any = new Error(syscall + ' ' + code);
    e.errno = __kml_native.uvErrno(errno);
    e.code = code;
    e.syscall = syscall;
    return e;
}

let proc: any = null;

// ---- signals (per_thread.js) ----

const signalWatchers = new Set<string>();

function isSignal(type: any): boolean {
    return typeof type === 'string' && (type as string).startsWith('SIG') && __kml_native.signalNumber(type as string) > 0;
}

function startListeningIfSignal(type: any): void {
    if (isSignal(type) && !signalWatchers.has(type as string)) {
        const name = type as string;
        const err = __kml_native.signalStart(__kml_native.signalNumber(name), (signo: number) => {
            proc.emit(name, name);
        });
        if (err < 0) throw errnoException(err, 'uv_signal_start');
        signalWatchers.add(name);
    }
}

function stopListeningIfSignal(type: any): void {
    if (typeof type === 'string' && signalWatchers.has(type as string) && proc.listenerCount(type) === 0) {
        __kml_native.signalStop(__kml_native.signalNumber(type as string));
        signalWatchers.delete(type as string);
    }
}

// ---- the runtime's lifecycle events ----

function startListeningIfLifecycle(type: any): void {
    if (type === 'uncaughtException' && proc.listenerCount('uncaughtException') === 0) {
        __kml_native.processHook(kUncaughtException, (err: any, origin: any) => {
            if (origin === 1) {
                // lib/internal/process/promises.js: a rejection reason that
                // is not an Error is reported as an UnhandledPromiseRejection.
                let reported: any = err;
                if (!(err instanceof Error)) {
                    reported = new NodeError('ERR_UNHANDLED_REJECTION', 'This error originated either by throwing inside of an async function without a catch block, ' +
                        'or by rejecting a promise which was not handled with .catch(). The promise rejected with the reason "' + String(err) + '".');
                    reported.name = 'UnhandledPromiseRejection';
                }
                proc.emit('uncaughtException', reported, 'unhandledRejection');
            } else {
                proc.emit('uncaughtException', err, 'uncaughtException');
            }
        });
    } else if (type === 'unhandledRejection' && proc.listenerCount('unhandledRejection') === 0) {
        __kml_native.processHook(kUnhandledRejection, (reason: any, promise: any) => {
            proc.emit('unhandledRejection', reason, promise);
        });
    }
}

function stopListeningIfLifecycle(type: any): void {
    if (type === 'uncaughtException' && proc.listenerCount('uncaughtException') === 0) {
        __kml_native.processUnhook(kUncaughtException);
    } else if (type === 'unhandledRejection' && proc.listenerCount('unhandledRejection') === 0) {
        __kml_native.processUnhook(kUnhandledRejection);
    }
}

// ---- the fork channel (_forkChild) ----

function setupChannel(p: any, fd: number): void {
    __kml_native.ipcChildClaim();
    const h = __kml_native.streamOpen(fd, true);
    if (h < 0) return;
    const chan = new Channel(p, h, () => {});
    // Only a message or disconnect listener keeps the process alive.
    chan.socket.unref();
    p.channel = chan.control;
    p.connected = true;
    p.send = (message: any, a?: any, b?: any, c?: any): boolean => chan.send(message, a, b, c);
    p._send = (message: any, handle: any, options: any, cb: any): boolean => chan._send(message, handle, options, cb);
    p.disconnect = (): void => { chan.disconnect(); };
    p.on('newListener', (name: any) => {
        if (name === 'message' || name === 'disconnect') chan.control.refCounted();
    });
    p.on('removeListener', (name: any) => {
        if (name === 'message' || name === 'disconnect') chan.control.unrefCounted();
    });
}

// The process object: an EventEmitter whose exitCode is the one the
// program exits with.
class ProcessObject extends EventEmitter {
    get exitCode(): any {
        return penv.getExitCode();
    }

    set exitCode(code: any) {
        penv.setExitCode(code);
    }
}

export function _kmlProcess(): any {
    if (proc !== null) return proc;
    const p: any = new ProcessObject();
    proc = p;
    p.cwd = methods.cwd;
    p.chdir = methods.chdir;
    p.uptime = methods.uptime;
    const hrtime: any = phr.hrtime;
    hrtime.bigint = phr.hrtimeBigint;
    p.hrtime = hrtime;
    p.kill = methods.kill;
    const memoryUsage: any = methods.memoryUsage;
    memoryUsage.rss = methods.memoryUsageRss;
    p.memoryUsage = memoryUsage;
    p.umask = methods.umask;
    p.argv = penv.argv();
    p.argv0 = penv.argv0();
    p.execArgv = penv.execArgv();
    p.execPath = penv.execPath();
    p.version = penv.version();
    p.versions = penv.versions();
    p.platform = process.platform;
    p.arch = process.arch;
    p.exit = penv.exit;
    // The environment, live: reads and writes reach the environment block.
    p.env = new Proxy({}, {
        get: (target: any, key: any): any => typeof key === 'string' ? penv.envGet(key as string) : undefined,
        set: (target: any, key: any, value: any): boolean => { penv.envSet(String(key), value); return true; },
        deleteProperty: (target: any, key: any): boolean => penv.envDelete(String(key)),
        has: (target: any, key: any): boolean => typeof key === 'string' && penv.envGet(key as string) !== undefined,
    });
    p.pid = methods.pid();
    p.ppid = methods.ppid();
    if (process.platform !== 'win32') {
        p.getuid = methods.getuid;
        p.geteuid = methods.geteuid;
        p.getgid = methods.getgid;
        p.getegid = methods.getegid;
    }
    p.on('newListener', startListeningIfSignal);
    p.on('removeListener', stopListeningIfSignal);
    p.on('newListener', startListeningIfLifecycle);
    p.on('removeListener', stopListeningIfLifecycle);
    p.on('warning', _kmlOnWarning);
    _kmlSetWarningSink((warning: any) => { p.emit('warning', warning); });
    __kml_native.processHook(kExit, (code: any, unused: any) => {
        p._exiting = true;
        p.emit('exit', code);
    });
    const channelFd = process.env.NODE_CHANNEL_FD;
    if (channelFd !== undefined) {
        delete process.env.NODE_CHANNEL_FD;
        setupChannel(p, parseInt(channelFd, 10));
    }
    return p;
}
