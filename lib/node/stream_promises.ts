// Node's `stream/promises`: pipeline and finished as promises.
import { Stream, promises } from 'stream';

export function pipeline(...streams: Stream[]): Promise<void> {
    return promises.pipeline(...streams);
}

export function finished(stream: Stream): Promise<void> {
    return promises.finished(stream);
}
