// The IPC channel of a forked process, both ends: Node v24's setupChannel
// and handleConversion (lib/internal/child_process.js) with the json
// serialization (newline-framed JSON). A handle travels as its descriptor
// in the same write as its NODE_HANDLE message (SCM_RIGHTS); the receiver
// takes descriptors in arrival order, one per NODE_HANDLE message. Channel
// writes go to the native write queue directly, so a message carrying a
// handle keeps its place among the others. dgram sockets are not passed.
import { Server, Socket, _KmlNativeHandle } from 'net';

class NodeError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

function errnoException(errno: number, syscall: string): Error {
    const code = __kml_native.errnoName(errno);
    const e: any = new Error(syscall + ' ' + code);
    e.errno = __kml_native.uvErrno(errno);
    e.code = code;
    e.syscall = syscall;
    return e;
}

const MAX_HANDLE_RETRANSMISSIONS = 3;
const INTERNAL_PREFIX = 'NODE_';

function isInternal(message: any): boolean {
    return message !== null &&
        typeof message === 'object' &&
        typeof message.cmd === 'string' &&
        message.cmd.length > INTERNAL_PREFIX.length &&
        message.cmd.slice(0, INTERNAL_PREFIX.length) === INTERNAL_PREFIX;
}

// The channel's reference counting (Control): a message listener keeps the
// process alive, as a pending asynchronous write does.
export class Control {
    socket: Socket;
    refs = 0;
    refExplicitlySet = false;

    constructor(socket: Socket) {
        this.socket = socket;
    }

    refCounted(): void {
        if (++this.refs === 1 && !this.refExplicitlySet) this.socket.ref();
    }

    unrefCounted(): void {
        if (--this.refs === 0 && !this.refExplicitlySet) this.socket.unref();
    }

    ref(): void {
        this.refExplicitlySet = true;
        this.socket.ref();
    }

    unref(): void {
        this.refExplicitlySet = true;
        this.socket.unref();
    }
}

// The handle a sendable object stands for, and how the other end rebuilds
// it (handleConversion).
function sendConvert(chan: Channel, message: any, obj: any, options: any): number {
    if (message.type === 'net.Native') return (obj as _KmlNativeHandle).open();
    if (message.type === 'net.Server') return (obj as Server)._handle;
    // net.Socket
    const socket = obj as Socket;
    if (socket._handle < 0) return -1;
    const h = socket._handle;
    if (socket.server) socket.server._connections--;
    if (!options.keepOpen) {
        // Detached from the socket; closed once the other end acknowledges.
        __kml_native.tcpReadStop(h);
        socket._handle = -1;
        socket.setTimeout(0);
    }
    return h;
}

function gotConvert(message: any, handle: _KmlNativeHandle, emit: (obj: any) => void): void {
    if (message.type === 'net.Server') {
        const server = new Server();
        server.listen(handle, () => { emit(server); });
        return;
    }
    if (message.type === 'net.Socket') {
        emit(new Socket({ handle: handle.open(), readable: true, writable: true }));
        return;
    }
    emit(handle);
}

export class Channel {
    target: any;
    socket: Socket;
    handle: number;
    control: Control;
    connected = true;
    // Handles wait for the previous one's NODE_HANDLE_ACK; so do the
    // messages sent meanwhile, to keep their order.
    handleQueue: any[] | null = null;
    pendingMessage: any = null;
    pendingMessages: any[] = [];
    private buffer = '';
    private closed = false;

    constructor(target: any, h: number, onClose: () => void) {
        this.target = target;
        this.handle = h;
        __kml_native.ipcOpen(h);
        const socket = new Socket({ handle: h, readable: true, writable: true });
        this.socket = socket;
        this.control = new Control(socket);
        socket.setEncoding('utf8');
        socket.on('data', (chunk: string) => { this.onData(chunk); });
        const ended = (): void => { this.onEnd(); };
        socket.on('end', ended);
        socket.on('error', ended);
        socket.on('close', () => {
            this.onEnd();
            onClose();
        });
        target.on('internalMessage', (message: any, handle: any) => { this.onInternal(message, handle); });
        target.on('newListener', () => {
            process.nextTick(() => {
                if (this.closed || this.target.listenerCount('message') === 0) return;
                const messages = this.pendingMessages;
                if (messages.length === 0) return;
                this.pendingMessages = [];
                for (const m of messages) this.target.emit(m[0], m[1], m[2]);
            });
        });
    }

    private onData(chunk: string): void {
        this.buffer += chunk;
        let nl = this.buffer.indexOf('\n');
        while (nl >= 0) {
            const line = this.buffer.slice(0, nl);
            this.buffer = this.buffer.slice(nl + 1);
            if (line.length > 0) {
                const message: any = JSON.parse(line);
                if (isInternal(message)) {
                    if (message.cmd === 'NODE_HANDLE') {
                        const fd = __kml_native.ipcTakeFd(this.handle);
                        this.handleMessage(message, fd >= 0 ? new _KmlNativeHandle(-1, fd) : undefined, true);
                    } else {
                        this.handleMessage(message, undefined, true);
                    }
                } else {
                    this.handleMessage(message, undefined, false);
                }
            }
            nl = this.buffer.indexOf('\n');
        }
    }

    private onEnd(): void {
        if (this.closed) return;
        this.closed = true;
        if (this.pendingMessage !== null) this.closePendingHandle();
        if (!this.connected) return;
        this.connected = false;
        this.target.connected = false;
        process.nextTick(() => { this.target.emit('disconnect'); });
    }

    private handleMessage(message: any, handle: any, internal: boolean): void {
        if (this.closed) return;
        const eventName = internal ? 'internalMessage' : 'message';
        process.nextTick(() => {
            if (eventName === 'internalMessage' || this.target.listenerCount('message') > 0) {
                this.target.emit(eventName, message, handle);
                return;
            }
            this.pendingMessages.push([eventName, message, handle]);
        });
    }

    private closePendingHandle(): void {
        const pm = this.pendingMessage;
        this.pendingMessage = null;
        if (pm !== null && pm.handle !== null) __kml_native.tcpClose(pm.handle, (s: number, u: number) => {});
    }

    private onInternal(message: any, handle: any): void {
        if (message.cmd === 'NODE_HANDLE_ACK' || message.cmd === 'NODE_HANDLE_NACK') {
            if (this.pendingMessage !== null) {
                if (message.cmd === 'NODE_HANDLE_ACK') {
                    this.closePendingHandle();
                } else if (this.pendingMessage.retransmissions++ === MAX_HANDLE_RETRANSMISSIONS) {
                    this.closePendingHandle();
                    process.emitWarning('Handle did not reach the receiving process correctly', 'SentHandleNotReceivedWarning');
                }
            }
            const queue = this.handleQueue === null ? [] : this.handleQueue;
            this.handleQueue = null;
            const pm = this.pendingMessage;
            if (pm !== null) this._send(pm.message, pm.sendable, pm.options, pm.callback);
            for (const args of queue) this._send(args.message, args.handle, args.options, args.callback);
            // A disconnect waiting for the queue.
            if (!this.connected && !this.closed && this.handleQueue === null) this._disconnect();
            return;
        }
        if (message.cmd !== 'NODE_HANDLE') return;
        if (!handle) {
            this._send({ cmd: 'NODE_HANDLE_NACK' }, null, { swallowErrors: true });
            return;
        }
        this._send({ cmd: 'NODE_HANDLE_ACK' }, null, { swallowErrors: true });
        gotConvert(message, handle as _KmlNativeHandle, (obj: any) => {
            this.handleMessage(message.msg, obj, isInternal(message.msg));
        });
    }

    send(message: any, handle?: any, options?: any, callback?: any): boolean {
        if (typeof handle === 'function') {
            callback = handle;
            handle = undefined;
            options = undefined;
        } else if (typeof options === 'function') {
            callback = options;
            options = undefined;
        } else if (options !== undefined && (options === null || typeof options !== 'object')) {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options" argument must be of type object.');
        }
        options = { swallowErrors: false, ...options };
        if (this.connected) return this._send(message, handle, options, callback);
        const ex = new NodeError('ERR_IPC_CHANNEL_CLOSED', 'Channel closed');
        if (typeof callback === 'function') {
            process.nextTick(() => { callback(ex); });
        } else {
            process.nextTick(() => { this.target.emit('error', ex); });
        }
        return false;
    }

    _send(message: any, handle: any, options: any, callback?: any): boolean {
        if (message === undefined) {
            throw new NodeTypeError('ERR_MISSING_ARGS', 'The "message" argument must be specified');
        }
        if (typeof message !== 'string' && typeof message !== 'object' &&
            typeof message !== 'number' && typeof message !== 'boolean') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "message" argument must be one of type string, object, number, or boolean.');
        }
        if (typeof options === 'boolean') options = { swallowErrors: options };
        let sendHandle = -1;
        const sendable = handle;
        if (handle) {
            message = { cmd: 'NODE_HANDLE', type: null, msg: message };
            if (handle instanceof Socket) message.type = 'net.Socket';
            else if (handle instanceof Server) message.type = 'net.Server';
            else if (handle instanceof _KmlNativeHandle) message.type = 'net.Native';
            else throw new NodeTypeError('ERR_INVALID_HANDLE_TYPE', 'This handle type cannot be sent');
            if (this.handleQueue !== null) {
                this.handleQueue.push({ callback, handle, options, message: message.msg });
                return this.handleQueue.length === 1;
            }
            sendHandle = sendConvert(this, message, handle, options);
            // A handle already sent, or none behind the object: the message
            // alone.
            if (sendHandle < 0) message = message.msg;
        } else if (this.handleQueue !== null &&
            !(message && (message.cmd === 'NODE_HANDLE_ACK' || message.cmd === 'NODE_HANDLE_NACK'))) {
            this.handleQueue.push({ callback, handle: null, options, message });
            return this.handleQueue.length === 1;
        }
        const buf = Buffer.from(JSON.stringify(message) + '\n', 'utf8');
        const done = (status: number, unused: number): void => {
            this.control.unrefCounted();
            if (typeof callback === 'function') callback(status === 0 ? null : errnoException(status, 'write'));
        };
        this.control.refCounted();
        const r = sendHandle >= 0
            ? __kml_native.ipcWriteHandle(this.handle, buf, 0, buf.length, sendHandle, done)
            : __kml_native.tcpWrite(this.handle, buf, 0, buf.length, done);
        if (r >= 0) {
            if (r === 1) {
                this.control.unrefCounted();
                if (typeof callback === 'function') process.nextTick(() => { callback(null); });
            }
            if (sendHandle >= 0) {
                if (this.handleQueue === null) this.handleQueue = [];
                // A detached socket is closed once the other end
                // acknowledges it (postSend); other handles stay open.
                const keep = message.type !== 'net.Socket' || options.keepOpen === true;
                if (this.pendingMessage === null) {
                    this.pendingMessage = { callback, message: message.msg, sendable, options, handle: keep ? null : sendHandle, retransmissions: 0 };
                }
            }
        } else {
            this.control.unrefCounted();
            if (sendHandle >= 0 && message.type === 'net.Socket' && options.keepOpen !== true) {
                __kml_native.tcpClose(sendHandle, (s: number, u: number) => {});
            }
            if (!options.swallowErrors) {
                const ex = errnoException(-r, 'write');
                if (typeof callback === 'function') process.nextTick(() => { callback(ex); });
                else process.nextTick(() => { this.target.emit('error', ex); });
            }
        }
        return true;
    }

    disconnect(): void {
        if (!this.connected) {
            this.target.emit('error', new NodeError('ERR_IPC_DISCONNECTED', 'IPC channel is already disconnected'));
            return;
        }
        this.connected = false;
        this.target.connected = false;
        if (this.handleQueue === null) this._disconnect();
    }

    _disconnect(): void {
        if (this.closed) return;
        this.closed = true;
        if (this.pendingMessage !== null) this.closePendingHandle();
        this.socket.end();
        process.nextTick(() => { this.target.emit('disconnect'); });
    }
}
