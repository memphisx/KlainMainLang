// Node's `cluster`, ported from Node v24's lib/cluster.js and
// lib/internal/cluster/ (primary.js, child.js, worker.js, utils.js,
// round_robin_handle.js, shared_handle.js): one module whose `cluster` is
// the primary's or, with NODE_UNIQUE_ID set, a worker's. Workers are forks
// of this program over the IPC channel (internal_child_process_channel.ts);
// a worker's net.Server listen goes through the primary, which either
// accepts and hands each connection to a worker in turn (SCHED_RR) or
// sends the listening socket itself (SCHED_NONE). The inspector options and
// dgram sockets in a worker are not ported.
import { EventEmitter } from 'events';
import { fork } from 'child_process';
import type { ChildProcess, StdioOptions, SerializationType } from 'child_process';
import { relative as relativePath, resolve as resolvePath } from 'path';
import { _KmlNativeHandle, _kmlHandleAddress, _kmlSetClusterGetServer } from 'net';
import type { Server } from 'net';

// ---- types (@types/node's cluster.d.ts) ----

export interface ClusterSettings {
    execArgv?: string[] | undefined;
    exec?: string | undefined;
    args?: readonly string[] | undefined;
    silent?: boolean | undefined;
    stdio?: StdioOptions | undefined;
    uid?: number | undefined;
    gid?: number | undefined;
    inspectPort?: number | (() => number) | undefined;
    serialization?: SerializationType | undefined;
    cwd?: string | undefined;
    windowsHide?: boolean | undefined;
}

export interface Address {
    address: string;
    port: number;
    addressType: 4 | 6 | -1 | 'udp4' | 'udp6';
}

const SCHED_NONE = 1;
const SCHED_RR = 2;
const isChild = process.env.NODE_UNIQUE_ID !== undefined;

// ---- utils.js ----

const callbacks = new Map<number, (message: any, handle: any) => void>();
let seq = 0;

function sendHelper(proc: any, message: any, handle: any, cb?: (message: any, handle: any) => void): boolean {
    if (!proc.connected) return false;
    // Marked internal (INTERNAL_PREFIX).
    message = { cmd: 'NODE_CLUSTER', ...message, seq };
    if (typeof cb === 'function') callbacks.set(seq, cb);
    seq += 1;
    return proc.send(message, handle);
}

// An internalMessage listener handing normal messages to cb, and
// acknowledgements to the callback waiting for them.
function internal(worker: Worker, cb: (worker: Worker, message: any, handle: any) => void): (message: any, handle: any) => void {
    return (message: any, handle: any): void => {
        if (message.cmd !== 'NODE_CLUSTER') return;
        if (message.ack !== undefined) {
            const callback = callbacks.get(message.ack);
            if (callback !== undefined) {
                callbacks.delete(message.ack);
                callback(message, handle);
                return;
            }
        }
        cb(worker, message, handle);
    };
}

// ---- worker.js ----

export class Worker extends EventEmitter {
    id: number;
    process: ChildProcess;
    exitedAfterDisconnect: boolean = undefined as any;
    state: string;

    constructor(options?: { id?: number; process?: any; state?: string }) {
        super();
        const o = options !== undefined && options !== null && typeof options === 'object' ? options : {};
        this.state = o.state || 'none';
        this.id = (o.id ?? 0) | 0;
        this.process = o.process;
        if (o.process) {
            this.process.on('error', (code: any, signal: any) => { this.emit('error', code, signal); });
            this.process.on('message', (message: any, handle: any) => { this.emit('message', message, handle); });
        }
    }

    kill(signal?: string): void {
        this.destroy(signal);
    }

    send(message: any, sendHandle?: any, options?: any, callback?: (error: Error | null) => void): boolean {
        return this.process.send(message, sendHandle, options, callback);
    }

    isDead(): boolean {
        return this.process.exitCode != null || this.process.signalCode != null;
    }

    isConnected(): boolean {
        return this.process.connected;
    }

    disconnect(): this {
        if (isChild) {
            if (this.state !== 'disconnecting' && this.state !== 'destroying') {
                this.state = 'disconnecting';
                childDisconnect(this, false);
            }
            return this;
        }
        this.exitedAfterDisconnect = true;
        sendHelper(this.process, { act: 'disconnect' }, null);
        removeHandlesForWorker(this);
        removeWorker(this);
        return this;
    }

    destroy(signal?: string): void {
        if (isChild) {
            if (this.state === 'destroying') return;
            this.exitedAfterDisconnect = true;
            if (!this.isConnected()) {
                process.exit(0);
            } else {
                this.state = 'destroying';
                childSend({ act: 'exitedAfterDisconnect' }, () => { process.disconnect!(); });
                process.once('disconnect', () => { process.exit(0); });
            }
            return;
        }
        this.process.kill((signal || 'SIGTERM') as NodeJS.Signals);
    }
}

// ---- the cluster object ----

export class Cluster extends EventEmitter {
    isWorker: boolean;
    isMaster: boolean; // Deprecated alias of isPrimary.
    isPrimary: boolean;
    Worker = Worker;
    settings: ClusterSettings = {};
    SCHED_NONE = SCHED_NONE;
    SCHED_RR = SCHED_RR;
    schedulingPolicy: number;
    workers?: NodeJS.Dict<Worker>;
    worker?: Worker;

    constructor(child: boolean) {
        super();
        this.isWorker = child;
        this.isMaster = !child;
        this.isPrimary = !child;
        const env = process.env.NODE_CLUSTER_SCHED_POLICY;
        this.schedulingPolicy = env === 'rr' ? SCHED_RR : env === 'none' ? SCHED_NONE :
            process.platform === 'win32' ? SCHED_NONE : SCHED_RR;
        if (!child) this.workers = {};
    }

    setupPrimary(options?: ClusterSettings): void {
        setupPrimary(options);
    }

    setupMaster(options?: ClusterSettings): void {
        setupPrimary(options);
    }

    fork(env?: any): Worker {
        return forkWorker(env);
    }

    disconnect(callback?: () => void): void {
        disconnectAll(callback);
    }
}

const cluster = new Cluster(isChild);
export default cluster;

// ---- primary.js ----

const intercom = new EventEmitter();
const handles = new Map<string, any>();
let ids = 0;
let initialized = false;
let schedulingPolicy = 0;

function setupPrimary(options?: ClusterSettings): void {
    const settings: ClusterSettings = {
        args: process.argv.slice(2),
        exec: process.argv[1],
        execArgv: process.execArgv,
        silent: false,
        ...cluster.settings,
        ...options,
    };
    cluster.settings = settings;
    if (initialized) {
        process.nextTick(() => { cluster.emit('setup', settings); });
        return;
    }
    initialized = true;
    schedulingPolicy = cluster.schedulingPolicy; // Freeze policy.
    if (schedulingPolicy !== SCHED_NONE && schedulingPolicy !== SCHED_RR) {
        throw new Error('Bad cluster.schedulingPolicy: ' + schedulingPolicy);
    }
    process.nextTick(() => { cluster.emit('setup', settings); });
}

function createWorkerProcess(id: number, env: any): ChildProcess {
    const workerEnv: any = { ...process.env, ...env, NODE_UNIQUE_ID: '' + id };
    const s = cluster.settings;
    // windowsHide is passed as Node's JavaScript passes it; ForkOptions
    // does not declare it.
    const options: any = {
        cwd: s.cwd,
        env: workerEnv,
        serialization: s.serialization,
        silent: s.silent,
        windowsHide: s.windowsHide,
        execArgv: (s.execArgv ?? []).slice(),
        stdio: s.stdio,
        gid: s.gid,
        uid: s.uid,
    };
    return fork(s.exec as string, (s.args ?? []) as string[], options);
}

function removeWorker(worker: Worker): void {
    const workers = cluster.workers as NodeJS.Dict<Worker>;
    delete workers[worker.id];
    if (Object.keys(workers).length === 0) intercom.emit('disconnect');
}

function removeHandlesForWorker(worker: Worker): void {
    for (const [key, handle] of handles) {
        if (handle.remove(worker)) handles.delete(key);
    }
}

function forkWorker(env?: any): Worker {
    setupPrimary();
    const id = ++ids;
    const workerProcess = createWorkerProcess(id, env);
    const worker = new Worker({ id, process: workerProcess });
    worker.on('message', (message: any, handle: any) => {
        cluster.emit('message', worker, message, handle);
    });
    worker.process.once('exit', (exitCode: number | null, signalCode: NodeJS.Signals | null) => {
        // Removed only once disconnected too; it may still be read.
        if (!worker.isConnected()) {
            removeHandlesForWorker(worker);
            removeWorker(worker);
        }
        worker.exitedAfterDisconnect = !!worker.exitedAfterDisconnect;
        worker.state = 'dead';
        worker.emit('exit', exitCode, signalCode);
        cluster.emit('exit', worker, exitCode, signalCode);
    });
    worker.process.once('disconnect', () => {
        removeHandlesForWorker(worker);
        if (worker.isDead()) removeWorker(worker);
        worker.exitedAfterDisconnect = !!worker.exitedAfterDisconnect;
        worker.state = 'disconnected';
        worker.emit('disconnect');
        cluster.emit('disconnect', worker);
    });
    worker.process.on('internalMessage', internal(worker, onPrimaryMessage));
    process.nextTick(() => { cluster.emit('fork', worker); });
    (cluster.workers as NodeJS.Dict<Worker>)[worker.id] = worker;
    return worker;
}

function disconnectAll(cb?: () => void): void {
    const workers = Object.values(cluster.workers as NodeJS.Dict<Worker>);
    if (workers.length === 0) {
        process.nextTick(() => { intercom.emit('disconnect'); });
    } else {
        for (const worker of workers) {
            if (worker !== undefined && worker.isConnected()) worker.disconnect();
        }
    }
    if (typeof cb === 'function') intercom.once('disconnect', cb);
}

function onPrimaryMessage(worker: Worker, message: any, handle: any): void {
    switch (message.act) {
        case 'close': {
            const h = handles.get(message.key);
            if (h && h.remove(worker)) handles.delete(message.key);
            break;
        }
        case 'exitedAfterDisconnect':
            worker.exitedAfterDisconnect = true;
            sendHelper(worker.process, { ack: message.seq }, null);
            break;
        case 'listening': {
            const info = { addressType: message.addressType, address: message.address, port: message.port, fd: message.fd };
            worker.state = 'listening';
            worker.emit('listening', info);
            cluster.emit('listening', worker, info);
            break;
        }
        case 'online':
            worker.state = 'online';
            worker.emit('online');
            cluster.emit('online', worker);
            break;
        case 'queryServer':
            queryServer(worker, message);
            break;
    }
}

function queryServer(worker: Worker, message: any): void {
    // A disconnecting worker gets no new server.
    if (worker.exitedAfterDisconnect) return;
    const key = message.address + ':' + message.port + ':' + message.addressType + ':' + message.fd +
        (message.port === 0 ? ':' + message.index : '');
    const cachedHandle = handles.get(key);
    let handle: any = undefined;
    if (cachedHandle && !cachedHandle.has(worker)) handle = cachedHandle;
    if (handle === undefined) {
        let address = message.address;
        // The shortest path for a Unix socket (their ~100 byte limit).
        if (message.port < 0 && typeof address === 'string' && process.platform !== 'win32') {
            address = relativePath(process.cwd(), address);
            if (message.address.length < address.length) address = message.address;
        }
        if (schedulingPolicy !== SCHED_RR || message.addressType === 'udp4' || message.addressType === 'udp6') {
            handle = new SharedHandle(key, address, message);
        } else {
            handle = new RoundRobinHandle(key, address, message);
        }
        if (!cachedHandle) handles.set(key, handle);
    }
    if (!handle.data) handle.data = message.data;
    handle.add(worker, (errno: number, reply: any, serverHandle: any) => {
        if (!errno) handles.set(key, handle); // Update in case it was replaced.
        const cur = handles.get(key);
        const data = cur === undefined ? undefined : cur.data;
        if (!cachedHandle && errno) handles.delete(key);
        sendHelper(worker.process, { errno, key, ack: message.seq, data, ...reply }, serverHandle);
    });
}

// ---- round_robin_handle.js: the primary accepts, workers take turns ----

class RoundRobinHandle {
    key: string;
    all = new Map<number, Worker>();
    free = new Map<number, Worker>();
    handles: _KmlNativeHandle[] = [];
    handle = -1;
    errno = 0;
    data: any = undefined;

    constructor(key: string, address: any, message: any) {
        this.key = key;
        const backlog = message.backlog || 511;
        const onConnection = (status: number, client: number): void => {
            this.distribute(status, new _KmlNativeHandle(client, -1));
        };
        let h: number;
        if (message.fd >= 0) h = __kml_native.tcpListenOpen(message.fd, onConnection);
        else if (message.port >= 0) h = __kml_native.tcpListen(address === null ? '' : address, message.port, backlog, onConnection);
        else h = __kml_native.pipeListen(address, backlog, onConnection);
        if (h < 0) this.errno = -h;
        else this.handle = h;
    }

    add(worker: Worker, send: (errno: number, reply: any, handle: any) => void): void {
        this.all.set(worker.id, worker);
        // The listen completed synchronously; the reply goes out as the
        // 'listening' event would.
        process.nextTick(() => {
            if (this.errno !== 0) {
                send(this.errno, null, null);
                return;
            }
            const out = this.handle >= 0 ? _kmlHandleAddress(this.handle) : null;
            send(0, out !== null ? { sockname: out } : null, null);
            this.handoff(worker); // In case there are connections pending.
        });
    }

    remove(worker: Worker): boolean {
        const existed = this.all.delete(worker.id);
        if (!existed) return false;
        this.free.delete(worker.id);
        if (this.all.size !== 0) return false;
        for (const h of this.handles) h.close();
        this.handles = [];
        if (this.handle >= 0) __kml_native.tcpClose(this.handle, (s: number, u: number) => {});
        this.handle = -1;
        return true;
    }

    distribute(err: number, handle: _KmlNativeHandle): void {
        // A failed accept is skipped.
        if (err) return;
        this.handles.push(handle);
        for (const [workerId, worker] of this.free) {
            this.free.delete(workerId);
            this.handoff(worker);
            break;
        }
    }

    handoff(worker: Worker): void {
        if (!this.all.has(worker.id)) return; // Closing the server.
        if (this.handles.length === 0) {
            this.free.set(worker.id, worker); // Ready for the next one.
            return;
        }
        const handle = this.handles.shift() as _KmlNativeHandle;
        const message = { act: 'newconn', key: this.key };
        sendHelper(worker.process, message, handle, (reply: any) => {
            if (reply.accepted) handle.close();
            else this.distribute(0, handle); // Shutting down: another worker.
            this.handoff(worker);
        });
    }

    has(worker: Worker): boolean {
        return this.all.has(worker.id);
    }
}

// ---- shared_handle.js: every worker accepts on the one socket ----

class SharedHandle {
    key: string;
    workers = new Map<number, Worker>();
    handle: _KmlNativeHandle | null = null;
    errno = 0;
    data: any = undefined;

    constructor(key: string, address: any, message: any) {
        this.key = key;
        let h: number;
        if (message.fd >= 0) h = message.fd;
        else h = __kml_native.tcpBind(address === null ? '' : address, message.port, message.backlog || 511);
        if (h < 0) this.errno = -h;
        else this.handle = message.fd >= 0 ? new _KmlNativeHandle(-1, h) : new _KmlNativeHandle(h, -1);
    }

    add(worker: Worker, send: (errno: number, reply: any, handle: any) => void): void {
        this.workers.set(worker.id, worker);
        send(this.errno, null, this.handle);
    }

    remove(worker: Worker): boolean {
        if (!this.workers.has(worker.id)) return false;
        this.workers.delete(worker.id);
        if (this.workers.size !== 0) return false;
        if (this.handle !== null) this.handle.close();
        this.handle = null;
        return true;
    }

    has(worker: Worker): boolean {
        return this.workers.has(worker.id);
    }
}

// ---- child.js ----

const childHandles = new Map<string, any>();
const indexes = new Map<string, { nextIndex: number; set: Set<number> }>();

function childSend(message: any, cb?: (message: any, handle: any) => void): boolean {
    return sendHelper(process, message, null, cb);
}

function setupWorker(): void {
    const worker = new Worker({ id: +(process.env.NODE_UNIQUE_ID as string) | 0, process: process, state: 'online' });
    cluster.worker = worker;
    process.once('disconnect', () => {
        worker.emit('disconnect');
        // Unexpected: the primary exited, or some such; so does the worker.
        if (!worker.exitedAfterDisconnect) process.exit(0);
    });
    process.on('internalMessage' as any, internal(worker, (w: Worker, message: any, handle: any) => {
        if (message.act === 'newconn') onconnection(message, handle);
        else if (message.act === 'disconnect') childDisconnect(w, true);
    }));
    childSend({ act: 'online' });
}

function getServer(obj: Server, options: any, cb: (errno: number, handle: any) => void): void {
    let address = options.address;
    // A Unix socket path, absolute.
    if (options.port < 0 && typeof address === 'string' && process.platform !== 'win32') {
        address = resolvePath(address);
    }
    const indexesKey = [address, options.port, options.addressType, options.fd].join(':');
    let indexSet = indexes.get(indexesKey);
    if (indexSet === undefined) {
        indexSet = { nextIndex: 0, set: new Set<number>() };
        indexes.set(indexesKey, indexSet);
    }
    const index = indexSet.nextIndex++;
    indexSet.set.add(index);
    const message: any = { act: 'queryServer', index, data: null, ...options };
    message.address = address;
    childSend(message, (reply: any, handle: any) => {
        if (handle) {
            shared(reply, handle, indexesKey, index, cb);
        } else {
            rr(reply, indexesKey, index, cb);
        }
    });
    obj.once('listening', () => {
        // A short-lived server may be closed already.
        if (!indexes.has(indexesKey)) return;
        (cluster.worker as Worker).state = 'listening';
        const a: any = obj.address();
        message.act = 'listening';
        message.port = (a !== null && typeof a === 'object' ? a.port : 0) || options.port;
        childSend(message);
    });
}

function removeIndexesKey(indexesKey: string, index: number): void {
    const indexSet = indexes.get(indexesKey);
    if (!indexSet) return;
    indexSet.set.delete(index);
    if (indexSet.set.size === 0) indexes.delete(indexesKey);
}

// A shared listening socket: its close tells the primary.
function shared(message: any, handle: _KmlNativeHandle, indexesKey: string, index: number, cb: (errno: number, handle: any) => void): void {
    const key = message.key;
    handle.onClose = (): void => {
        childSend({ act: 'close', key });
        childHandles.delete(key);
        removeIndexesKey(indexesKey, index);
    };
    childHandles.set(key, handle);
    cb(message.errno, handle);
}

// Round-robin's faux handle: the primary delivers each connection it
// accepted through onconnection; an interval keeps the process alive as
// the handle would.
class RRHandle {
    key: string | undefined;
    indexesKey: string;
    index: number;
    fakeHandle: any = null;
    sockname: any;
    onconnection: ((status: number, client: any) => void) | null = null;
    _kmlOwner: any = null;
    getsockname: ((out: any) => number) | null = null;

    constructor(message: any, indexesKey: string, index: number) {
        this.key = message.key;
        this.indexesKey = indexesKey;
        this.index = index;
        this.sockname = message.sockname;
        if (message.sockname) {
            // TCP handles only.
            this.getsockname = (out: any): number => {
                if (this.key !== undefined) Object.assign(out, this.sockname);
                return 0;
            };
        }
    }

    ref(): void {
        if (this.fakeHandle === null) this.fakeHandle = setInterval(() => {}, 2147483647);
    }

    unref(): void {
        if (this.fakeHandle !== null) {
            clearInterval(this.fakeHandle);
            this.fakeHandle = null;
        }
    }

    listen(backlog: number): number {
        return 0;
    }

    close(cb?: () => void): void {
        // Connections the primary sends before it sees this close go back
        // to it (onconnection).
        if (this.key === undefined) return;
        this.unref();
        childSend({ act: 'close', key: this.key });
        childHandles.delete(this.key);
        removeIndexesKey(this.indexesKey, this.index);
        this.key = undefined;
    }
}

function rr(message: any, indexesKey: string, index: number, cb: (errno: number, handle: any) => void): void {
    if (message.errno) {
        cb(message.errno, null);
        return;
    }
    const handle = new RRHandle(message, indexesKey, index);
    handle.ref();
    childHandles.set(message.key, handle);
    cb(0, handle);
}

// A round-robin connection.
function onconnection(message: any, handle: _KmlNativeHandle): void {
    const key = message.key;
    const server = childHandles.get(key);
    let accepted = server !== undefined;
    if (accepted && server._kmlOwner) {
        const self = server._kmlOwner;
        if (self.maxConnections != null && self._connections >= self.maxConnections) accepted = false;
    }
    childSend({ ack: message.seq, accepted });
    if (accepted) server.onconnection(0, handle);
    else handle.close();
}

function childDisconnect(worker: Worker, primaryInitiated: boolean): void {
    worker.exitedAfterDisconnect = true;
    let waitingCount = 1;
    const checkWaitingCount = (): void => {
        waitingCount--;
        if (waitingCount === 0) {
            // A worker-initiated disconnect waits for the primary's ack, so
            // exitedAfterDisconnect is set there first.
            if (primaryInitiated) {
                process.disconnect!();
            } else {
                childSend({ act: 'exitedAfterDisconnect' }, () => { process.disconnect!(); });
            }
        }
    };
    for (const handle of childHandles.values()) {
        waitingCount++;
        if (handle._kmlOwner) handle._kmlOwner.close(checkWaitingCount);
        else handle.close(checkWaitingCount);
    }
    childHandles.clear();
    checkWaitingCount();
}

if (isChild) {
    _kmlSetClusterGetServer(getServer);
    setupWorker();
}
