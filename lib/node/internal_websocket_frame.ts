// The WebSocket frame codec (RFC 6455 §5) shared by klain:ws's server and
// the global WebSocket client (lib/node/kml_websocket.ts).
import { randomBytes } from 'crypto';

// One frame, its payload unmasked.
export interface WSFrame {
    fin: boolean;
    opcode: number;
    masked: boolean;
    payload: Buffer;
}

// A frame read off the front of a buffer, and the bytes after it.
export interface WSParsed {
    frame: WSFrame;
    rest: Buffer;
}

// encodeFrame builds one final frame; a client masks it (§5.3).
export function encodeFrame(opcode: number, payload: Buffer, mask: boolean): Buffer {
    const len = payload.length;
    const maskBit = mask ? 0x80 : 0;
    let head: Buffer;
    if (len < 126) {
        head = Buffer.alloc(2);
        head[1] = maskBit | len;
    } else if (len < 65536) {
        head = Buffer.alloc(4);
        head[1] = maskBit | 126;
        head.writeUInt16BE(len, 2);
    } else {
        head = Buffer.alloc(10);
        head[1] = maskBit | 127;
        head.writeUInt32BE(Math.floor(len / 4294967296), 2);
        head.writeUInt32BE(len % 4294967296, 6);
    }
    head[0] = 0x80 | opcode;
    if (!mask) return Buffer.concat([head, payload]);
    const key = randomBytes(4);
    const masked = Buffer.alloc(len);
    for (let i = 0; i < len; i++) masked[i] = payload[i] ^ key[i % 4];
    return Buffer.concat([head, key, masked]);
}

// parseFrame reads the frame at the front of buf, or null when buf does
// not hold a whole one yet.
export function parseFrame(buf: Buffer): WSParsed | null {
    if (buf.length < 2) return null;
    const fin = (buf[0] & 0x80) !== 0;
    const opcode = buf[0] & 0x0f;
    const masked = (buf[1] & 0x80) !== 0;
    let len = buf[1] & 0x7f;
    let off = 2;
    if (len === 126) {
        if (buf.length < 4) return null;
        len = buf.readUInt16BE(2);
        off = 4;
    } else if (len === 127) {
        if (buf.length < 10) return null;
        len = buf.readUInt32BE(2) * 4294967296 + buf.readUInt32BE(6);
        off = 10;
    }
    const keyOff = off;
    if (masked) off += 4;
    if (buf.length < off + len) return null;
    const payload = Buffer.alloc(len);
    for (let i = 0; i < len; i++) {
        payload[i] = masked ? buf[off + i] ^ buf[keyOff + (i % 4)] : buf[off + i];
    }
    return { frame: { fin, opcode, masked, payload }, rest: buf.subarray(off + len) };
}

// isUtf8 reports whether b is well-formed UTF-8 (no overlong forms, no
// surrogates, nothing past U+10FFFF), as a text frame must be (§8.1).
export function isUtf8(b: Buffer): boolean {
    let i = 0;
    while (i < b.length) {
        const c = b[i];
        if (c < 0x80) {
            i++;
            continue;
        }
        let n = 0;
        let min = 0;
        let cp = 0;
        if (c >= 0xc2 && c <= 0xdf) {
            n = 1;
            min = 0x80;
            cp = c & 0x1f;
        } else if (c >= 0xe0 && c <= 0xef) {
            n = 2;
            min = 0x800;
            cp = c & 0x0f;
        } else if (c >= 0xf0 && c <= 0xf4) {
            n = 3;
            min = 0x10000;
            cp = c & 0x07;
        } else {
            return false;
        }
        for (let k = 1; k <= n; k++) {
            if (i + k >= b.length) return false;
            const cc = b[i + k];
            if ((cc & 0xc0) !== 0x80) return false;
            cp = cp * 64 + (cc & 0x3f);
        }
        if (cp < min || cp > 0x10ffff || (cp >= 0xd800 && cp <= 0xdfff)) return false;
        i += n + 1;
    }
    return true;
}
