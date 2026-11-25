// Node's `punycode` module: the punycode.js 2.1.0 library Node vendors as
// lib/punycode.js. Loading it emits Node's DEP0040 deprecation warning.
// kml:default-namespace — `import punycode from 'punycode'` reads this
// module's exports, as Node's CommonJS module.exports is the namespace.

process.emitWarning(
    'The `punycode` module is deprecated. Please use a userland alternative instead.',
    'DeprecationWarning', 'DEP0040');

const maxInt = 2147483647; // aka. 0x7FFFFFFF or 2^31-1

// Bootstring parameters
const base = 36;
const tMin = 1;
const tMax = 26;
const skew = 38;
const damp = 700;
const initialBias = 72;
const initialN = 128; // 0x80
const delimiter = '-'; // '\x2D'

const regexPunycode = /^xn--/;
const regexNonASCII = /[^\0-\x7F]/; // Note: U+007F DEL is excluded too.
const regexSeparators = /[\x2E。．｡]/g; // RFC 3490 separators

const errors: { [key: string]: string } = {
    'overflow': 'Overflow: input needs wider integers to process',
    'not-basic': 'Illegal input >= 0x80 (not a basic code point)',
    'invalid-input': 'Invalid input',
};

const baseMinusTMin = base - tMin;

function error(type: string): never {
    throw new RangeError(errors[type]);
}

function map(array: string[], callback: (s: string) => string): string[] {
    const result: string[] = [];
    let length = array.length;
    while (length--) {
        result[length] = callback(array[length]);
    }
    return result;
}

// A domain name or an email address: the callback applied to each label of
// the domain part, the local part kept.
function mapDomain(domain: string, callback: (s: string) => string): string {
    const parts = domain.split('@');
    let result = '';
    if (parts.length > 1) {
        result = parts[0] + '@';
        domain = parts[1];
    }
    domain = domain.replace(regexSeparators, '\x2E');
    const labels = domain.split('.');
    const encoded = map(labels, callback).join('.');
    return result + encoded;
}

// A string's code points (UCS-2 decoding: surrogate pairs combined).
function ucs2decode(string: string): number[] {
    const output: number[] = [];
    let counter = 0;
    const length = string.length;
    while (counter < length) {
        const value = string.charCodeAt(counter++);
        if (value >= 0xD800 && value <= 0xDBFF && counter < length) {
            const extra = string.charCodeAt(counter++);
            if ((extra & 0xFC00) == 0xDC00) {
                output.push(((value & 0x3FF) << 10) + (extra & 0x3FF) + 0x10000);
            } else {
                output.push(value);
                counter--;
            }
        } else {
            output.push(value);
        }
    }
    return output;
}

// The string of code points.
function ucs2encode(codePoints: number[]): string {
    return String.fromCodePoint(...codePoints);
}

function basicToDigit(codePoint: number): number {
    if (codePoint >= 0x30 && codePoint < 0x3A) {
        return 26 + (codePoint - 0x30);
    }
    if (codePoint >= 0x41 && codePoint < 0x5B) {
        return codePoint - 0x41;
    }
    if (codePoint >= 0x61 && codePoint < 0x7B) {
        return codePoint - 0x61;
    }
    return base;
}

function digitToBasic(digit: number, flag: number): number {
    //  0..25 map to ASCII a..z or A..Z
    // 26..35 map to ASCII 0..9
    return digit + 22 + 75 * (digit < 26 ? 1 : 0) - ((flag != 0 ? 1 : 0) << 5);
}

function adapt(delta: number, numPoints: number, firstTime: boolean): number {
    let k = 0;
    delta = firstTime ? Math.floor(delta / damp) : delta >> 1;
    delta += Math.floor(delta / numPoints);
    for (; delta > baseMinusTMin * tMax >> 1; k += base) {
        delta = Math.floor(delta / baseMinusTMin);
    }
    return Math.floor(k + (baseMinusTMin + 1) * delta / (delta + skew));
}

// A Punycode string of ASCII-only symbols to a string of Unicode symbols.
export function decode(input: string): string {
    const output: number[] = [];
    const inputLength = input.length;
    let i = 0;
    let n = initialN;
    let bias = initialBias;

    // The basic code points: the ones before the last delimiter.
    let basic = input.lastIndexOf(delimiter);
    if (basic < 0) {
        basic = 0;
    }

    for (let j = 0; j < basic; ++j) {
        if (input.charCodeAt(j) >= 0x80) {
            error('not-basic');
        }
        output.push(input.charCodeAt(j));
    }

    // The main decoding loop: start just after the last delimiter if any
    // basic code points were copied; start at the beginning otherwise.
    for (let index = basic > 0 ? basic + 1 : 0; index < inputLength;) {
        const oldi = i;
        for (let w = 1, k = base; /* no condition */; k += base) {
            if (index >= inputLength) {
                error('invalid-input');
            }
            const digit = basicToDigit(input.charCodeAt(index++));
            if (digit >= base) {
                error('invalid-input');
            }
            if (digit > Math.floor((maxInt - i) / w)) {
                error('overflow');
            }
            i += digit * w;
            const t = k <= bias ? tMin : (k >= bias + tMax ? tMax : k - bias);
            if (digit < t) {
                break;
            }
            const baseMinusT = base - t;
            if (w > Math.floor(maxInt / baseMinusT)) {
                error('overflow');
            }
            w *= baseMinusT;
        }

        const out = output.length + 1;
        bias = adapt(i - oldi, out, oldi == 0);

        if (Math.floor(i / out) > maxInt - n) {
            error('overflow');
        }
        n += Math.floor(i / out);
        i %= out;

        output.splice(i++, 0, n);
    }

    return String.fromCodePoint(...output);
}

// A string of Unicode symbols (e.g. a domain name label) to a Punycode
// string of ASCII-only symbols.
export function encode(inputString: string): string {
    const output: string[] = [];
    const input = ucs2decode(inputString);
    const inputLength = input.length;
    let n = initialN;
    let delta = 0;
    let bias = initialBias;

    for (const currentValue of input) {
        if (currentValue < 0x80) {
            output.push(String.fromCharCode(currentValue));
        }
    }

    const basicLength = output.length;
    let handledCPCount = basicLength;

    if (basicLength) {
        output.push(delimiter);
    }

    while (handledCPCount < inputLength) {
        // The next larger code point.
        let m = maxInt;
        for (const currentValue of input) {
            if (currentValue >= n && currentValue < m) {
                m = currentValue;
            }
        }

        const handledCPCountPlusOne = handledCPCount + 1;
        if (m - n > Math.floor((maxInt - delta) / handledCPCountPlusOne)) {
            error('overflow');
        }

        delta += (m - n) * handledCPCountPlusOne;
        n = m;

        for (const currentValue of input) {
            if (currentValue < n && ++delta > maxInt) {
                error('overflow');
            }
            if (currentValue == n) {
                // Represent delta as a generalized variable-length integer.
                let q = delta;
                for (let k = base; /* no condition */; k += base) {
                    const t = k <= bias ? tMin : (k >= bias + tMax ? tMax : k - bias);
                    if (q < t) {
                        break;
                    }
                    const qMinusT = q - t;
                    const baseMinusT = base - t;
                    output.push(String.fromCharCode(digitToBasic(t + qMinusT % baseMinusT, 0)));
                    q = Math.floor(qMinusT / baseMinusT);
                }

                output.push(String.fromCharCode(digitToBasic(q, 0)));
                bias = adapt(delta, handledCPCountPlusOne, handledCPCount == basicLength);
                delta = 0;
                ++handledCPCount;
            }
        }

        ++delta;
        ++n;
    }
    return output.join('');
}

// A Punycode domain name or email address to Unicode: only the labels that
// are Punycode are converted.
export function toUnicode(input: string): string {
    return mapDomain(input, (string: string) => {
        return regexPunycode.test(string)
            ? decode(string.slice(4).toLowerCase())
            : string;
    });
}

// A Unicode domain name or email address to Punycode: only the labels with
// non-ASCII symbols are converted.
export function toASCII(input: string): string {
    return mapDomain(input, (string: string) => {
        return regexNonASCII.test(string)
            ? 'xn--' + encode(string)
            : string;
    });
}

export const version = '2.1.0';

export const ucs2 = {
    decode: ucs2decode,
    encode: ucs2encode,
};
