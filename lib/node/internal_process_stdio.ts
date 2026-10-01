// process.stdin, process.stdout and process.stderr: Node's
// lib/internal/bootstrap/switches/is_main_thread.js (getStdin,
// createWritableStdioStream) and lib/internal/fs/sync_write_stream.js, in
// TypeScript. Each stream is made on first use, by what its descriptor is:
// a terminal's is a tty stream, a pipe's or socket's a net.Socket, a file's
// a synchronous Writable (stdout, stderr) or a Socket over the file's
// descriptor (stdin). Code generation binds `process.stdin` & co. to these
// getters.

import { Socket } from 'net';
import { Readable, Writable } from 'stream';
import { ReadStream, WriteStream } from 'tty';

// Node's SyncWriteStream: a Writable whose writes are blocking writes to
// the descriptor, for stdout and stderr redirected to a file.
class SyncWriteStream extends Writable {
    fd: number;
    readable = false;
    autoClose = false;

    constructor(fd: number) {
        super({ autoDestroy: true });
        this.fd = fd;
    }

    _write(chunk: any, encoding: BufferEncoding, cb: (error?: Error | null) => void): void {
        const buf: Buffer = typeof chunk === 'string' ? Buffer.from(chunk as string, encoding) : chunk as Buffer;
        const w = __kml_native.writeSync(this.fd, buf, 0, buf.length);
        if (w < 0) {
            const e: any = new Error(__kml_native.errnoName(-w) + ': write');
            e.errno = __kml_native.uvErrno(-w);
            e.code = __kml_native.errnoName(-w);
            e.syscall = 'write';
            cb(e);
            return;
        }
        cb(null);
    }
}

function createWritableStdioStream(fd: number): any {
    let stream: any;
    switch (__kml_native.guessHandleType(fd)) {
        case 'TTY': {
            const ws: any = new WriteStream(fd);
            ws._type = 'tty';
            stream = ws;
            break;
        }
        case 'FILE': {
            const fs: any = new SyncWriteStream(fd);
            fs._type = 'fs';
            stream = fs;
            break;
        }
        case 'PIPE':
        case 'TCP': {
            const h = __kml_native.streamOpen(fd, false);
            const sock: any = new Socket({ handle: h, readable: false, writable: true, manualStart: true });
            // POSIX pipes are written synchronously, as Node's stdio does on
            // Linux (and keeps them ordered with console.log).
            sock._kmlSyncFd = fd;
            sock._type = 'pipe';
            stream = sock;
            break;
        }
        default: {
            // A black-hole output (a non-console Windows application).
            const w: any = new Writable({ write: (buf: any, enc: BufferEncoding, cb: (error?: Error | null) => void) => { cb(); } });
            stream = w;
            break;
        }
    }
    // For supporting legacy API we put the FD here.
    stream.fd = fd;
    stream._isStdio = true;
    return stream;
}

let stdinStream: any = null;
let stdoutStream: any = null;
let stderrStream: any = null;

export function _kmlStdout(): any {
    if (stdoutStream !== null) return stdoutStream;
    const s: any = createWritableStdioStream(1);
    stdoutStream = s;
    if (s.isTTY === true) {
        process.on('SIGWINCH', () => { s._refreshSize(); });
    }
    return s;
}

export function _kmlStderr(): any {
    if (stderrStream !== null) return stderrStream;
    const s: any = createWritableStdioStream(2);
    stderrStream = s;
    if (s.isTTY === true) {
        process.on('SIGWINCH', () => { s._refreshSize(); });
    }
    return s;
}

export function _kmlStdin(): any {
    if (stdinStream !== null) return stdinStream;
    const fd = 0;
    let s: any;
    switch (__kml_native.guessHandleType(fd)) {
        case 'TTY': {
            s = new ReadStream(fd);
            break;
        }
        case 'FILE':
        case 'PIPE':
        case 'TCP': {
            const h = __kml_native.streamOpen(fd, true);
            s = new Socket({ handle: h, readable: true, writable: false, manualStart: true });
            break;
        }
        default: {
            // A contentless input (a non-console Windows application).
            const r: any = new Readable({ read: (size: number) => {} });
            r.push(null);
            s = r;
            break;
        }
    }
    // For supporting legacy API we put the FD here.
    s.fd = fd;
    stdinStream = s;
    // A paused stdin stops reading (one tick later, once the stream has), so
    // the process can exit.
    s.on('pause', () => {
        process.nextTick(() => {
            if (typeof s._kmlReadStopIfPaused === 'function') s._kmlReadStopIfPaused();
        });
    });
    return s;
}
