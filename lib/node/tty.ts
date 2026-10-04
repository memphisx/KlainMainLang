// Node's lib/tty.js and lib/internal/tty.js, in TypeScript: terminal
// streams, net.Sockets over a descriptor's stream handle
// (__kml_native.streamOpen), its raw mode and window size.
// kml:default-namespace — `import tty from 'tty'` reads this module's
// exports.

import { Socket } from 'net';
import { getColorDepth } from './internal_tty';
import { cursorTo as rlCursorTo, moveCursor as rlMoveCursor, clearLine as rlClearLine, clearScreenDown as rlClearScreenDown } from './internal_readline_callbacks';
import { NodeError, NodeRangeError } from './internal_errors';

function errnoException(errno: number, syscall: string): Error {
    const code = __kml_native.errnoName(errno);
    const e: any = new Error(syscall + ' ' + code);
    e.errno = __kml_native.uvErrno(errno);
    e.code = code;
    e.syscall = syscall;
    return e;
}

export function isatty(fd: number): boolean {
    return Number.isInteger(fd) && fd >= 0 && fd <= 2147483647 && __kml_native.guessHandleType(fd) === 'TTY';
}

// A terminal descriptor's stream handle; ERR_TTY_INIT_FAILED otherwise.
function ttyHandle(fd: number, readable: boolean): number {
    if (!Number.isInteger(fd) || fd < 0 || fd > 2147483647) {
        throw new NodeRangeError('ERR_INVALID_FD', '"fd" must be a positive integer: ' + fd);
    }
    const h = __kml_native.streamOpen(fd, readable);
    if (h < 0 || !isatty(fd)) {
        const code = __kml_native.errnoName(h < 0 ? -h : 22);
        const e: any = new Error('TTY initialization failed: uv_tty_init returned ' + code);
        e.code = 'ERR_TTY_INIT_FAILED';
        throw e;
    }
    return h;
}

export class ReadStream extends Socket {
    isRaw = false;
    isTTY = true;
    fd: number;

    constructor(fd: number) {
        super({ handle: ttyHandle(fd, true), manualStart: true });
        this.fd = fd;
    }

    setRawMode(mode: boolean): this {
        const flag = !!mode;
        const err = this._handle >= 0 ? __kml_native.ttySetRawMode(this._handle, flag) : 0;
        if (err < 0) {
            this.emit('error', errnoException(-err, 'setRawMode'));
            return this;
        }
        this.isRaw = flag;
        return this;
    }
}

export class WriteStream extends Socket {
    isTTY = true;
    fd: number;
    columns = 0;
    rows = 0;

    constructor(fd: number) {
        super({ handle: ttyHandle(fd, false), manualStart: true });
        this.fd = fd;
        // Prevents interleaved or dropped stdout/stderr output for terminals.
        this._kmlSyncFd = fd;
        const size = __kml_native.ttyWindowSize(fd);
        if (size >= 0) {
            this.columns = Math.floor(size / 65536);
            this.rows = size % 65536;
        }
    }

    // Re-reads the window size: 'resize' when it changed.
    _refreshSize(): void {
        const oldCols = this.columns;
        const oldRows = this.rows;
        const size = __kml_native.ttyWindowSize(this.fd);
        if (size < 0) {
            this.emit('error', errnoException(-size, 'getWindowSize'));
            return;
        }
        const newCols = Math.floor(size / 65536);
        const newRows = size % 65536;
        if (oldCols !== newCols || oldRows !== newRows) {
            this.columns = newCols;
            this.rows = newRows;
            this.emit('resize');
        }
    }

    getWindowSize(): [number, number] {
        return [this.columns, this.rows];
    }

    getColorDepth(env?: object): number {
        return getColorDepth(env);
    }

    hasColors(count?: number | object, env?: object): boolean {
        let n: any = count;
        let e: any = env;
        if (e === undefined && (n === undefined || (typeof n === 'object' && n !== null))) {
            e = n;
            n = 16;
        } else if (typeof n !== 'number' || !Number.isInteger(n) || n < 2) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "count" is out of range. It must be >= 2. Received ' + String(n));
        }
        return n <= Math.pow(2, getColorDepth(e));
    }

    cursorTo(x: number, y?: number | (() => void), callback?: () => void): boolean {
        return rlCursorTo(this, x, y, callback);
    }

    moveCursor(dx: number, dy: number, callback?: () => void): boolean {
        return rlMoveCursor(this, dx, dy, callback);
    }

    clearLine(dir: number, callback?: () => void): boolean {
        return rlClearLine(this, dir, callback);
    }

    clearScreenDown(callback?: () => void): boolean {
        return rlClearScreenDown(this, callback);
    }
}

export { getColorDepth } from './internal_tty';
