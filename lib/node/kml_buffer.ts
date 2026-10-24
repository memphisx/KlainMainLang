// kml:global
// Buffer's byte-order swaps written in TypeScript (Node's buffer.swap16,
// swap32, swap64), reached through their `@lower` declarations in
// lib/node.d.ts. Each swaps the bytes of every 2-, 4- or 8-byte group in
// place and returns the buffer.

import { NodeRangeError } from './internal_errors';

function swapBytes(buf: Buffer, size: number): Buffer {
    const len = buf.length;
    if (len % size !== 0) {
        throw NodeRangeError('ERR_INVALID_BUFFER_SIZE', 'Buffer size must be a multiple of ' + size * 8 + '-bits');
    }
    for (let i = 0; i < len; i += size) {
        for (let lo = i, hi = i + size - 1; lo < hi; lo++, hi--) {
            const t = buf[lo];
            buf[lo] = buf[hi];
            buf[hi] = t;
        }
    }
    return buf;
}

export function __kml_Buffer_swap16(buf: Buffer): Buffer { // kml:lower swap16
    return swapBytes(buf, 2);
}

export function __kml_Buffer_swap32(buf: Buffer): Buffer { // kml:lower swap32
    return swapBytes(buf, 4);
}

export function __kml_Buffer_swap64(buf: Buffer): Buffer { // kml:lower swap64
    return swapBytes(buf, 8);
}
