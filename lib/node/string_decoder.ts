// Node's lib/string_decoder.js, in TypeScript: decodes a stream of Buffers
// into strings, holding back a multi-byte character split across chunks
// (the JavaScript implementation Node shipped before moving it to C++; the
// observable behaviour is the same).

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

function normalizeEncoding(enc: any): string | undefined {
    const e = enc === undefined || enc === null || enc === '' ? 'utf8' : String(enc).toLowerCase();
    switch (e) {
        case 'utf8':
        case 'utf-8':
            return 'utf8';
        case 'ucs2':
        case 'ucs-2':
        case 'utf16le':
        case 'utf-16le':
            return 'utf16le';
        case 'latin1':
        case 'binary':
            return 'latin1';
        case 'base64':
        case 'base64url':
        case 'ascii':
        case 'hex':
            return e;
        default:
            return undefined;
    }
}

// The number of bytes a UTF-8 sequence starting with byte has (0 for
// ASCII), -1 for a continuation byte, -2 for an invalid byte.
function utf8CheckByte(byte: number): number {
    if (byte <= 0x7F) return 0;
    else if (byte >> 5 === 0x06) return 2;
    else if (byte >> 4 === 0x0E) return 3;
    else if (byte >> 3 === 0x1E) return 4;
    return byte >> 6 === 0x02 ? -1 : -2;
}

export class StringDecoder {
    encoding: string;
    lastNeed = 0;
    lastTotal = 0;
    lastChar: Buffer;

    constructor(encoding?: BufferEncoding | string) {
        const enc = normalizeEncoding(encoding);
        if (enc === undefined) {
            throw new NodeTypeError('ERR_UNKNOWN_ENCODING', 'Unknown encoding: ' + String(encoding));
        }
        this.encoding = enc;
        this.lastChar = Buffer.alloc(4);
    }

    // Returns the decoded chunk, holding back an incomplete trailing
    // character until the next write.
    write(buf: Buffer | string): string {
        if (typeof buf === 'string') return buf;
        if (buf.length === 0) return '';
        if (this.encoding !== 'utf8' && this.encoding !== 'utf16le' && this.encoding !== 'base64' && this.encoding !== 'base64url') {
            return buf.toString(this.encoding as BufferEncoding);
        }
        let r: string | undefined = undefined;
        let i = 0;
        if (this.lastNeed > 0) {
            r = this.fillLast(buf);
            if (r === undefined) return '';
            i = this.lastNeed;
            this.lastNeed = 0;
        }
        if (i < buf.length) return r !== undefined && r !== '' ? r + this.text(buf, i) : this.text(buf, i);
        return r !== undefined ? r : '';
    }

    // Returns what is left: a held-back incomplete character as U+FFFD
    // (UTF-8), its code units (UTF-16) or its encoding (base64).
    end(buf?: Buffer | string): string {
        let r = buf !== undefined && buf.length > 0 ? this.write(buf) : '';
        if (this.lastNeed > 0) {
            if (this.encoding === 'utf8') {
                r += '�';
            } else if (this.encoding === 'utf16le') {
                r += this.lastChar.toString('utf16le', 0, this.lastTotal - this.lastNeed);
            } else {
                r += this.lastChar.toString(this.encoding as BufferEncoding, 0, 3 - this.lastNeed);
            }
        }
        this.lastNeed = 0;
        this.lastTotal = 0;
        return r;
    }

    // Decodes buf from index i, holding back an incomplete trailing
    // character.
    text(buf: Buffer, i: number): string {
        if (this.encoding === 'utf16le') return this.utf16Text(buf, i);
        if (this.encoding === 'base64' || this.encoding === 'base64url') return this.base64Text(buf, i);
        const total = this.utf8CheckIncomplete(buf, i);
        if (this.lastNeed === 0) return buf.toString('utf8', i);
        this.lastTotal = total;
        const end = buf.length - (total - this.lastNeed);
        buf.copy(this.lastChar, 0, end);
        return buf.toString('utf8', i, end);
    }

    // Completes the held-back character from buf's first bytes; undefined
    // while still incomplete.
    fillLast(buf: Buffer): string | undefined {
        const p = this.lastTotal - this.lastNeed;
        if (this.encoding === 'utf8') {
            const r = this.utf8CheckExtraBytes(buf);
            if (r !== undefined) return r;
        }
        if (this.lastNeed <= buf.length) {
            buf.copy(this.lastChar, p, 0, this.lastNeed);
            return this.lastChar.toString(this.encoding as BufferEncoding, 0, this.lastTotal);
        }
        buf.copy(this.lastChar, p, 0, buf.length);
        this.lastNeed -= buf.length;
        return undefined;
    }

    // Checks the end of buf for an incomplete UTF-8 character, setting
    // lastNeed to the bytes it still needs; returns its total length.
    private utf8CheckIncomplete(buf: Buffer, i: number): number {
        let j = buf.length - 1;
        if (j < i) return 0;
        let nb = utf8CheckByte(buf[j]);
        if (nb >= 0) {
            if (nb > 0) this.lastNeed = nb - 1;
            return nb;
        }
        if (--j < i || nb === -2) return 0;
        nb = utf8CheckByte(buf[j]);
        if (nb >= 0) {
            if (nb > 0) this.lastNeed = nb - 2;
            return nb;
        }
        if (--j < i || nb === -2) return 0;
        nb = utf8CheckByte(buf[j]);
        if (nb >= 0) {
            if (nb > 0) {
                if (nb === 2) nb = 0;
                else this.lastNeed = nb - 3;
            }
            return nb;
        }
        return 0;
    }

    // The continuation bytes the held-back character expects: U+FFFD when
    // one is not a continuation byte.
    private utf8CheckExtraBytes(buf: Buffer): string | undefined {
        if ((buf[0] & 0xC0) !== 0x80) {
            this.lastNeed = 0;
            return '�';
        }
        if (this.lastNeed > 1 && buf.length > 1) {
            if ((buf[1] & 0xC0) !== 0x80) {
                this.lastNeed = 1;
                return '�';
            }
            if (this.lastNeed > 2 && buf.length > 2) {
                if ((buf[2] & 0xC0) !== 0x80) {
                    this.lastNeed = 2;
                    return '�';
                }
            }
        }
        return undefined;
    }

    private utf16Text(buf: Buffer, i: number): string {
        if ((buf.length - i) % 2 === 0) {
            const r = buf.toString('utf16le', i);
            if (r.length > 0) {
                const c = r.charCodeAt(r.length - 1);
                if (c >= 0xD800 && c <= 0xDBFF) {
                    this.lastNeed = 2;
                    this.lastTotal = 4;
                    this.lastChar[0] = buf[buf.length - 2];
                    this.lastChar[1] = buf[buf.length - 1];
                    return r.slice(0, -1);
                }
            }
            return r;
        }
        this.lastNeed = 1;
        this.lastTotal = 2;
        this.lastChar[0] = buf[buf.length - 1];
        return buf.toString('utf16le', i, buf.length - 1);
    }

    private base64Text(buf: Buffer, i: number): string {
        const n = (buf.length - i) % 3;
        if (n === 0) return buf.toString(this.encoding as BufferEncoding, i);
        this.lastNeed = 3 - n;
        this.lastTotal = 3;
        if (n === 1) {
            this.lastChar[0] = buf[buf.length - 1];
        } else {
            this.lastChar[0] = buf[buf.length - 2];
            this.lastChar[1] = buf[buf.length - 1];
        }
        return buf.toString(this.encoding as BufferEncoding, i, buf.length - n);
    }
}
