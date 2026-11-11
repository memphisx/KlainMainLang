// ReadableStream's async iterator: the Streams standard's
// ReadableStreamAsyncIteratorPrototype, behind stream.values() and
// stream[Symbol.asyncIterator](), as Node's values() builds it. It holds the
// stream's default reader; next() reads a chunk, return() releases the lock
// (cancelling the stream unless preventCancel), and the calls run one after
// another, chained on the one in flight.

class StreamAsyncIterator<R> {
    #reader: ReadableStreamDefaultReader<R>;
    #stream: ReadableStream<R> | undefined;
    #preventCancel: boolean;
    #done = false;
    #current: Promise<any> | undefined = undefined;
    #started = false;

    constructor(stream: ReadableStream<R>, preventCancel: boolean) {
        this.#reader = stream.getReader();
        this.#stream = stream;
        this.#preventCancel = preventCancel;
    }

    #release(): void {
        this.#stream = undefined;
        this.#reader.releaseLock();
    }

    #nextSteps(): Promise<IteratorResult<R, undefined>> {
        if (this.#done) return Promise.resolve({ done: true, value: undefined });
        if (this.#stream === undefined) {
            return Promise.reject(new TypeError("The reader is not bound to a ReadableStream"));
        }
        const { promise, resolve, reject } = Promise.withResolvers<IteratorResult<R, undefined>>();
        __kml_native.rsReadRequest(this.#stream, (kind: number, value: any) => {
            this.#current = undefined;
            if (kind === 0) {
                resolve({ value: value as R, done: false });
                return;
            }
            this.#done = true;
            this.#release();
            if (kind === 1) resolve({ done: true, value: undefined });
            else reject(value);
        });
        return promise;
    }

    async #returnSteps(value: any): Promise<IteratorResult<R, any>> {
        if (this.#done) return { done: true, value };
        this.#done = true;
        if (this.#stream === undefined) {
            throw new TypeError("The reader is not bound to a ReadableStream");
        }
        if (!this.#preventCancel) {
            const result = this.#reader.cancel(value);
            this.#release();
            await result;
            return { done: true, value };
        }
        this.#release();
        return { done: true, value };
    }

    next(): Promise<IteratorResult<R, undefined>> {
        // The first read waits a microtask, so the source has started.
        if (!this.#started) {
            this.#current = Promise.resolve();
            this.#started = true;
        }
        const prev = this.#current;
        if (prev !== undefined) {
            const p = prev.then(() => this.#nextSteps(), () => this.#nextSteps());
            this.#current = p;
            return p;
        }
        // No read in flight and a chunk queued: it, at once.
        const stream = this.#stream;
        if (!this.#done && stream !== undefined && __kml_native.rsHasQueued(stream)) {
            return Promise.resolve({ done: false, value: __kml_native.rsTakeQueued(stream) as R });
        }
        const p = this.#nextSteps();
        this.#current = p;
        return p;
    }

    return(value?: any): Promise<IteratorResult<R, any>> {
        this.#started = true;
        const prev = this.#current;
        const p = prev !== undefined ? prev.then(() => this.#returnSteps(value), () => this.#returnSteps(value)) : this.#returnSteps(value);
        this.#current = p;
        return p;
    }

    [Symbol.asyncIterator](): StreamAsyncIterator<R> {
        return this;
    }
}

export function values<R>(stream: ReadableStream<R>, options?: ReadableStreamIteratorOptions): StreamAsyncIterator<R> {
    const preventCancel = options !== undefined && options !== null && !!options.preventCancel;
    return new StreamAsyncIterator<R>(stream, preventCancel);
}
