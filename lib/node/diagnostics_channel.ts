// Node's `diagnostics_channel`, ported from Node v24's
// lib/diagnostics_channel.js: named channels, their subscribers and bound
// stores, and tracing channels. A channel with neither subscribers nor
// stores is inactive (Node swaps its prototype; here a flag reads the
// same). Node keeps channels in a map of weak references, so an unused
// channel can be collected; here the map holds them, which only changes
// memory, not what a program sees.
//
// kml:default-namespace — `import dc from 'diagnostics_channel'` reads this
// module's exports, as Node's default export carries them.

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

function received(value: any): string {
    if (value === null || value === undefined) return ' Received ' + String(value);
    if (typeof value === 'function') return ' Received function';
    if (typeof value === 'object') return ' Received an instance of Object';
    let shown = String(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    if (typeof value === 'string') shown = "'" + shown + "'";
    return ' Received type ' + typeof value + ' (' + shown + ')';
}

function validateFunction(value: any, name: string): void {
    if (typeof value !== 'function') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be of type function.' + received(value));
    }
}

// A subscriber's throw, reported from a tick of its own so it does not stop
// the others: Node's triggerUncaughtException, which emits the process
// 'uncaughtException' event when something listens and is fatal otherwise.
function triggerUncaughtException(err: any): void {
    process.nextTick(() => {
        if (process.listenerCount('uncaughtException') > 0) {
            process.emit('uncaughtException', err, 'uncaughtException');
            return;
        }
        throw err;
    });
}

export type ChannelListener = (message: unknown, name: string | symbol) => void;

function defaultTransform(data: any): any {
    return data;
}

function wrapStoreRun(store: any, data: any, next: () => any, transform: (data: any) => any): () => any {
    return () => {
        let context: any;
        try {
            context = transform(data);
        } catch (err) {
            triggerUncaughtException(err);
            return next();
        }
        return store.run(context, next);
    };
}

const channels = new Map<string | symbol, Channel>();

export class Channel {
    readonly name: string | symbol;
    private active = false;
    _subscribers: ChannelListener[] = [];
    _stores = new Map<any, (data: any) => any>();

    constructor(name: string | symbol) {
        this.name = name;
        channels.set(name, this);
    }

    private maybeMarkInactive(): void {
        if (this._subscribers.length === 0 && this._stores.size === 0) this.active = false;
    }

    get hasSubscribers(): boolean {
        return this.active;
    }

    subscribe(onMessage: ChannelListener): void {
        this.active = true;
        validateFunction(onMessage, 'subscription');
        const next = this._subscribers.slice();
        next.push(onMessage);
        this._subscribers = next;
    }

    unsubscribe(onMessage: ChannelListener): boolean {
        if (!this.active) return false;
        const index = this._subscribers.indexOf(onMessage);
        if (index === -1) return false;
        const before = this._subscribers.slice(0, index);
        const after = this._subscribers.slice(index + 1);
        this._subscribers = before.concat(after);
        this.maybeMarkInactive();
        return true;
    }

    bindStore(store: any, transform?: (context: any) => any): void {
        this.active = true;
        this._stores.set(store, transform !== undefined ? transform : defaultTransform);
    }

    unbindStore(store: any): boolean {
        if (!this.active || !this._stores.has(store)) return false;
        this._stores.delete(store);
        this.maybeMarkInactive();
        return true;
    }

    publish(message: unknown): void {
        if (!this.active) return;
        const subscribers = this._subscribers;
        for (let i = 0; i < subscribers.length; i++) {
            try {
                const onMessage = subscribers[i];
                onMessage(message, this.name);
            } catch (err) {
                triggerUncaughtException(err);
            }
        }
    }

    runStores(context: any, fn: (...args: any[]) => any, thisArg?: any, ...args: any[]): any {
        if (!this.active || this._stores.size === 0) {
            this.publish(context);
            return Reflect.apply(fn, thisArg, args);
        }
        let run = (): any => {
            this.publish(context);
            return Reflect.apply(fn, thisArg, args);
        };
        for (const entry of this._stores.entries()) {
            run = wrapStoreRun(entry[0], context, run, entry[1]);
        }
        return run();
    }
}

export function channel(name: string | symbol): Channel {
    const existing = channels.get(name);
    if (existing !== undefined) return existing;
    if (typeof name !== 'string' && typeof name !== 'symbol') {
        throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "channel" argument must be of type string or symbol.' + received(name));
    }
    return new Channel(name);
}

export function subscribe(name: string | symbol, onMessage: ChannelListener): void {
    channel(name).subscribe(onMessage);
}

export function unsubscribe(name: string | symbol, onMessage: ChannelListener): boolean {
    return channel(name).unsubscribe(onMessage);
}

export function hasSubscribers(name: string | symbol): boolean {
    const existing = channels.get(name);
    if (existing === undefined) return false;
    return existing.hasSubscribers;
}

// ---- tracing channels ----

export interface TracingChannelSubscribers {
    start?: (message: any) => void;
    end?: (message: any) => void;
    asyncStart?: (message: any) => void;
    asyncEnd?: (message: any) => void;
    error?: (message: any) => void;
}

export interface TracingChannelCollection {
    start: Channel;
    end: Channel;
    asyncStart: Channel;
    asyncEnd: Channel;
    error: Channel;
}

function tracingChannelFrom(nameOrChannels: any, name: string): Channel {
    if (typeof nameOrChannels === 'string') {
        return channel('tracing:' + nameOrChannels + ':' + name);
    }
    if (typeof nameOrChannels === 'object' && nameOrChannels !== null) {
        const ch: any = nameOrChannels[name];
        if (!(ch instanceof Channel)) {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "nameOrChannels.' + name + '" property must be an instance of Channel.' + received(ch));
        }
        return ch as Channel;
    }
    throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "nameOrChannels" argument must be of type string or an instance of TracingChannel or Object.' + received(nameOrChannels));
}

export class TracingChannel implements TracingChannelCollection {
    start: Channel;
    end: Channel;
    asyncStart: Channel;
    asyncEnd: Channel;
    error: Channel;

    constructor(nameOrChannels: string | TracingChannelCollection) {
        this.start = tracingChannelFrom(nameOrChannels, 'start');
        this.end = tracingChannelFrom(nameOrChannels, 'end');
        this.asyncStart = tracingChannelFrom(nameOrChannels, 'asyncStart');
        this.asyncEnd = tracingChannelFrom(nameOrChannels, 'asyncEnd');
        this.error = tracingChannelFrom(nameOrChannels, 'error');
    }

    get hasSubscribers(): boolean {
        return this.start.hasSubscribers || this.end.hasSubscribers || this.asyncStart.hasSubscribers ||
            this.asyncEnd.hasSubscribers || this.error.hasSubscribers;
    }

    subscribe(handlers: TracingChannelSubscribers): void {
        if (handlers.start !== undefined) this.start.subscribe(handlers.start);
        if (handlers.end !== undefined) this.end.subscribe(handlers.end);
        if (handlers.asyncStart !== undefined) this.asyncStart.subscribe(handlers.asyncStart);
        if (handlers.asyncEnd !== undefined) this.asyncEnd.subscribe(handlers.asyncEnd);
        if (handlers.error !== undefined) this.error.subscribe(handlers.error);
    }

    unsubscribe(handlers: TracingChannelSubscribers): boolean {
        let done = true;
        if (handlers.start !== undefined && !this.start.unsubscribe(handlers.start)) done = false;
        if (handlers.end !== undefined && !this.end.unsubscribe(handlers.end)) done = false;
        if (handlers.asyncStart !== undefined && !this.asyncStart.unsubscribe(handlers.asyncStart)) done = false;
        if (handlers.asyncEnd !== undefined && !this.asyncEnd.unsubscribe(handlers.asyncEnd)) done = false;
        if (handlers.error !== undefined && !this.error.unsubscribe(handlers.error)) done = false;
        return done;
    }

    traceSync(fn: (...args: any[]) => any, context?: any, thisArg?: any, ...args: any[]): any {
        if (!this.hasSubscribers) return Reflect.apply(fn, thisArg, args);
        const ctx: any = context !== undefined ? context : {};
        const end = this.end;
        const error = this.error;
        return this.start.runStores(ctx, () => {
            try {
                const result = Reflect.apply(fn, thisArg, args);
                ctx.result = result;
                return result;
            } catch (err) {
                ctx.error = err;
                error.publish(ctx);
                throw err;
            } finally {
                end.publish(ctx);
            }
        });
    }

    tracePromise(fn: (...args: any[]) => any, context?: any, thisArg?: any, ...args: any[]): Promise<any> {
        if (!this.hasSubscribers) return Reflect.apply(fn, thisArg, args);
        const ctx: any = context !== undefined ? context : {};
        const end = this.end;
        const asyncStart = this.asyncStart;
        const asyncEnd = this.asyncEnd;
        const error = this.error;
        const reject = (err: any): Promise<any> => {
            ctx.error = err;
            error.publish(ctx);
            asyncStart.publish(ctx);
            asyncEnd.publish(ctx);
            return Promise.reject(err);
        };
        const resolve = (result: any): any => {
            ctx.result = result;
            asyncStart.publish(ctx);
            asyncEnd.publish(ctx);
            return result;
        };
        return this.start.runStores(ctx, () => {
            try {
                let promise: any = Reflect.apply(fn, thisArg, args);
                // Convert thenables to native promises.
                if (!(promise instanceof Promise)) promise = Promise.resolve(promise);
                return (promise as Promise<any>).then(resolve, reject);
            } catch (err) {
                ctx.error = err;
                error.publish(ctx);
                throw err;
            } finally {
                end.publish(ctx);
            }
        });
    }

    traceCallback(fn: (...args: any[]) => any, position?: number, context?: any, thisArg?: any, ...args: any[]): any {
        if (!this.hasSubscribers) return Reflect.apply(fn, thisArg, args);
        const ctx: any = context !== undefined ? context : {};
        const pos = position !== undefined ? position : -1;
        const end = this.end;
        const asyncStart = this.asyncStart;
        const asyncEnd = this.asyncEnd;
        const error = this.error;
        const index = pos < 0 ? args.length + pos : pos;
        const callback: any = args[index];
        validateFunction(callback, 'callback');
        const wrappedCallback = function (this: any, ...cbArgs: any[]): void {
            const err = cbArgs[0];
            if (err) {
                ctx.error = err;
                error.publish(ctx);
            } else {
                ctx.result = cbArgs[1];
            }
            const self = this;
            // Using runStores here enables manual context failure recovery.
            asyncStart.runStores(ctx, () => {
                try {
                    return Reflect.apply(callback, self, cbArgs);
                } finally {
                    asyncEnd.publish(ctx);
                }
            });
        };
        args.splice(index, 1, wrappedCallback);
        return this.start.runStores(ctx, () => {
            try {
                return Reflect.apply(fn, thisArg, args);
            } catch (err) {
                ctx.error = err;
                error.publish(ctx);
                throw err;
            } finally {
                end.publish(ctx);
            }
        });
    }
}

export function tracingChannel(nameOrChannels: string | TracingChannelCollection): TracingChannel {
    return new TracingChannel(nameOrChannels);
}
