// Node's `http2`, ported from Node v24's lib/internal/http2/core.js,
// lib/internal/http2/compat.js and lib/internal/http2/util.js: an
// Http2Session owns one nghttp2 session (http2src/h2node.c, Node's
// node_http2.cc) fed from its socket, an Http2Stream is a Duplex over one of
// its streams, and the servers are a net.Server and a tls.Server (ALPN `h2`)
// whose connections each become a ServerHttp2Session. The compatibility API
// (Http2ServerRequest/Http2ServerResponse, the 'request' event) rides the
// 'stream' event as in Node. Diagnostics channels and async resources are
// not ported; nghttp2 sends WINDOW_UPDATE frames as data arrives.
// kml:default-namespace — `import http2 from 'http2'` reads this module's
// exports.

import { EventEmitter } from 'events';
import { Duplex, Readable, Stream } from 'stream';
import type { DuplexOptions } from 'stream';
import * as net from 'net';
import * as tls from 'tls';
import * as fs from 'fs';
import { _kmlHttpServerCore } from './internal_http';
import { invalidArgType, validateFunction, validateBoolean, validateNumber, validateString } from './internal_dns_utils';
import { NodeError, NodeTypeError, NodeRangeError } from './internal_errors';

// ---- errors (lib/internal/errors.js) ----

// An nghttp2 library error: nghttp2_strerror's text, code ERR_HTTP2_ERROR.
class NghttpError extends Error {
    code: string;
    errno: number;
    constructor(integerCode: number) {
        __kml_native.h2ErrorText(integerCode);
        super(__kml_native.lastString());
        this.code = 'ERR_HTTP2_ERROR';
        this.errno = integerCode;
    }
}

function errInvalidSession(): Error {
    return new NodeError('ERR_HTTP2_INVALID_SESSION', 'The session has been destroyed');
}

function errInvalidStream(): Error {
    return new NodeError('ERR_HTTP2_INVALID_STREAM', 'The stream has been destroyed');
}

function errInvalidSettingValue(name: string, value: any, range: boolean): Error {
    const msg = 'Invalid value for setting "' + name + '": ' + String(value);
    return range ? new NodeRangeError('ERR_HTTP2_INVALID_SETTING_VALUE', msg)
        : new NodeTypeError('ERR_HTTP2_INVALID_SETTING_VALUE', msg);
}

// ---- constants (the binding's) ----

export const constants = {
    NGHTTP2_ERR_FRAME_SIZE_ERROR: -522,
    NGHTTP2_SESSION_SERVER: 0,
    NGHTTP2_SESSION_CLIENT: 1,
    NGHTTP2_STREAM_STATE_IDLE: 1,
    NGHTTP2_STREAM_STATE_OPEN: 2,
    NGHTTP2_STREAM_STATE_RESERVED_LOCAL: 3,
    NGHTTP2_STREAM_STATE_RESERVED_REMOTE: 4,
    NGHTTP2_STREAM_STATE_HALF_CLOSED_LOCAL: 5,
    NGHTTP2_STREAM_STATE_HALF_CLOSED_REMOTE: 6,
    NGHTTP2_STREAM_STATE_CLOSED: 7,
    NGHTTP2_FLAG_NONE: 0,
    NGHTTP2_FLAG_END_STREAM: 1,
    NGHTTP2_FLAG_END_HEADERS: 4,
    NGHTTP2_FLAG_ACK: 1,
    NGHTTP2_FLAG_PADDED: 8,
    NGHTTP2_FLAG_PRIORITY: 32,
    DEFAULT_SETTINGS_HEADER_TABLE_SIZE: 4096,
    DEFAULT_SETTINGS_ENABLE_PUSH: 1,
    DEFAULT_SETTINGS_MAX_CONCURRENT_STREAMS: 4294967295,
    DEFAULT_SETTINGS_INITIAL_WINDOW_SIZE: 65535,
    DEFAULT_SETTINGS_MAX_FRAME_SIZE: 16384,
    DEFAULT_SETTINGS_MAX_HEADER_LIST_SIZE: 65535,
    DEFAULT_SETTINGS_ENABLE_CONNECT_PROTOCOL: 0,
    MAX_MAX_FRAME_SIZE: 16777215,
    MIN_MAX_FRAME_SIZE: 16384,
    MAX_INITIAL_WINDOW_SIZE: 2147483647,
    NGHTTP2_SETTINGS_HEADER_TABLE_SIZE: 1,
    NGHTTP2_SETTINGS_ENABLE_PUSH: 2,
    NGHTTP2_SETTINGS_MAX_CONCURRENT_STREAMS: 3,
    NGHTTP2_SETTINGS_INITIAL_WINDOW_SIZE: 4,
    NGHTTP2_SETTINGS_MAX_FRAME_SIZE: 5,
    NGHTTP2_SETTINGS_MAX_HEADER_LIST_SIZE: 6,
    NGHTTP2_SETTINGS_ENABLE_CONNECT_PROTOCOL: 8,
    PADDING_STRATEGY_NONE: 0,
    PADDING_STRATEGY_ALIGNED: 1,
    PADDING_STRATEGY_MAX: 2,
    PADDING_STRATEGY_CALLBACK: 1,
    NGHTTP2_NO_ERROR: 0,
    NGHTTP2_PROTOCOL_ERROR: 1,
    NGHTTP2_INTERNAL_ERROR: 2,
    NGHTTP2_FLOW_CONTROL_ERROR: 3,
    NGHTTP2_SETTINGS_TIMEOUT: 4,
    NGHTTP2_STREAM_CLOSED: 5,
    NGHTTP2_FRAME_SIZE_ERROR: 6,
    NGHTTP2_REFUSED_STREAM: 7,
    NGHTTP2_CANCEL: 8,
    NGHTTP2_COMPRESSION_ERROR: 9,
    NGHTTP2_CONNECT_ERROR: 10,
    NGHTTP2_ENHANCE_YOUR_CALM: 11,
    NGHTTP2_INADEQUATE_SECURITY: 12,
    NGHTTP2_HTTP_1_1_REQUIRED: 13,
    NGHTTP2_DEFAULT_WEIGHT: 16,
    HTTP2_HEADER_STATUS: ':status',
    HTTP2_HEADER_METHOD: ':method',
    HTTP2_HEADER_AUTHORITY: ':authority',
    HTTP2_HEADER_SCHEME: ':scheme',
    HTTP2_HEADER_PATH: ':path',
    HTTP2_HEADER_PROTOCOL: ':protocol',
    HTTP2_HEADER_ACCEPT_ENCODING: 'accept-encoding',
    HTTP2_HEADER_ACCEPT_LANGUAGE: 'accept-language',
    HTTP2_HEADER_ACCEPT_RANGES: 'accept-ranges',
    HTTP2_HEADER_ACCEPT: 'accept',
    HTTP2_HEADER_ACCESS_CONTROL_ALLOW_CREDENTIALS: 'access-control-allow-credentials',
    HTTP2_HEADER_ACCESS_CONTROL_ALLOW_HEADERS: 'access-control-allow-headers',
    HTTP2_HEADER_ACCESS_CONTROL_ALLOW_METHODS: 'access-control-allow-methods',
    HTTP2_HEADER_ACCESS_CONTROL_ALLOW_ORIGIN: 'access-control-allow-origin',
    HTTP2_HEADER_ACCESS_CONTROL_EXPOSE_HEADERS: 'access-control-expose-headers',
    HTTP2_HEADER_ACCESS_CONTROL_REQUEST_HEADERS: 'access-control-request-headers',
    HTTP2_HEADER_ACCESS_CONTROL_REQUEST_METHOD: 'access-control-request-method',
    HTTP2_HEADER_AGE: 'age',
    HTTP2_HEADER_AUTHORIZATION: 'authorization',
    HTTP2_HEADER_CACHE_CONTROL: 'cache-control',
    HTTP2_HEADER_CONNECTION: 'connection',
    HTTP2_HEADER_CONTENT_DISPOSITION: 'content-disposition',
    HTTP2_HEADER_CONTENT_ENCODING: 'content-encoding',
    HTTP2_HEADER_CONTENT_LENGTH: 'content-length',
    HTTP2_HEADER_CONTENT_TYPE: 'content-type',
    HTTP2_HEADER_COOKIE: 'cookie',
    HTTP2_HEADER_DATE: 'date',
    HTTP2_HEADER_ETAG: 'etag',
    HTTP2_HEADER_FORWARDED: 'forwarded',
    HTTP2_HEADER_HOST: 'host',
    HTTP2_HEADER_IF_MODIFIED_SINCE: 'if-modified-since',
    HTTP2_HEADER_IF_NONE_MATCH: 'if-none-match',
    HTTP2_HEADER_IF_RANGE: 'if-range',
    HTTP2_HEADER_LAST_MODIFIED: 'last-modified',
    HTTP2_HEADER_LINK: 'link',
    HTTP2_HEADER_LOCATION: 'location',
    HTTP2_HEADER_RANGE: 'range',
    HTTP2_HEADER_REFERER: 'referer',
    HTTP2_HEADER_SERVER: 'server',
    HTTP2_HEADER_SET_COOKIE: 'set-cookie',
    HTTP2_HEADER_STRICT_TRANSPORT_SECURITY: 'strict-transport-security',
    HTTP2_HEADER_TRANSFER_ENCODING: 'transfer-encoding',
    HTTP2_HEADER_TE: 'te',
    HTTP2_HEADER_UPGRADE_INSECURE_REQUESTS: 'upgrade-insecure-requests',
    HTTP2_HEADER_UPGRADE: 'upgrade',
    HTTP2_HEADER_USER_AGENT: 'user-agent',
    HTTP2_HEADER_VARY: 'vary',
    HTTP2_HEADER_X_CONTENT_TYPE_OPTIONS: 'x-content-type-options',
    HTTP2_HEADER_X_FRAME_OPTIONS: 'x-frame-options',
    HTTP2_HEADER_KEEP_ALIVE: 'keep-alive',
    HTTP2_HEADER_PROXY_CONNECTION: 'proxy-connection',
    HTTP2_HEADER_X_XSS_PROTECTION: 'x-xss-protection',
    HTTP2_HEADER_ALT_SVC: 'alt-svc',
    HTTP2_HEADER_CONTENT_SECURITY_POLICY: 'content-security-policy',
    HTTP2_HEADER_EARLY_DATA: 'early-data',
    HTTP2_HEADER_EXPECT_CT: 'expect-ct',
    HTTP2_HEADER_ORIGIN: 'origin',
    HTTP2_HEADER_PURPOSE: 'purpose',
    HTTP2_HEADER_TIMING_ALLOW_ORIGIN: 'timing-allow-origin',
    HTTP2_HEADER_X_FORWARDED_FOR: 'x-forwarded-for',
    HTTP2_HEADER_PRIORITY: 'priority',
    HTTP2_HEADER_ACCEPT_CHARSET: 'accept-charset',
    HTTP2_HEADER_ACCESS_CONTROL_MAX_AGE: 'access-control-max-age',
    HTTP2_HEADER_ALLOW: 'allow',
    HTTP2_HEADER_CONTENT_LANGUAGE: 'content-language',
    HTTP2_HEADER_CONTENT_LOCATION: 'content-location',
    HTTP2_HEADER_CONTENT_MD5: 'content-md5',
    HTTP2_HEADER_CONTENT_RANGE: 'content-range',
    HTTP2_HEADER_DNT: 'dnt',
    HTTP2_HEADER_EXPECT: 'expect',
    HTTP2_HEADER_EXPIRES: 'expires',
    HTTP2_HEADER_FROM: 'from',
    HTTP2_HEADER_IF_MATCH: 'if-match',
    HTTP2_HEADER_IF_UNMODIFIED_SINCE: 'if-unmodified-since',
    HTTP2_HEADER_MAX_FORWARDS: 'max-forwards',
    HTTP2_HEADER_PREFER: 'prefer',
    HTTP2_HEADER_PROXY_AUTHENTICATE: 'proxy-authenticate',
    HTTP2_HEADER_PROXY_AUTHORIZATION: 'proxy-authorization',
    HTTP2_HEADER_REFRESH: 'refresh',
    HTTP2_HEADER_RETRY_AFTER: 'retry-after',
    HTTP2_HEADER_TRAILER: 'trailer',
    HTTP2_HEADER_TK: 'tk',
    HTTP2_HEADER_VIA: 'via',
    HTTP2_HEADER_WARNING: 'warning',
    HTTP2_HEADER_WWW_AUTHENTICATE: 'www-authenticate',
    HTTP2_HEADER_HTTP2_SETTINGS: 'http2-settings',
    HTTP2_METHOD_ACL: 'ACL',
    HTTP2_METHOD_BASELINE_CONTROL: 'BASELINE-CONTROL',
    HTTP2_METHOD_BIND: 'BIND',
    HTTP2_METHOD_CHECKIN: 'CHECKIN',
    HTTP2_METHOD_CHECKOUT: 'CHECKOUT',
    HTTP2_METHOD_CONNECT: 'CONNECT',
    HTTP2_METHOD_COPY: 'COPY',
    HTTP2_METHOD_DELETE: 'DELETE',
    HTTP2_METHOD_GET: 'GET',
    HTTP2_METHOD_HEAD: 'HEAD',
    HTTP2_METHOD_LABEL: 'LABEL',
    HTTP2_METHOD_LINK: 'LINK',
    HTTP2_METHOD_LOCK: 'LOCK',
    HTTP2_METHOD_MERGE: 'MERGE',
    HTTP2_METHOD_MKACTIVITY: 'MKACTIVITY',
    HTTP2_METHOD_MKCALENDAR: 'MKCALENDAR',
    HTTP2_METHOD_MKCOL: 'MKCOL',
    HTTP2_METHOD_MKREDIRECTREF: 'MKREDIRECTREF',
    HTTP2_METHOD_MKWORKSPACE: 'MKWORKSPACE',
    HTTP2_METHOD_MOVE: 'MOVE',
    HTTP2_METHOD_OPTIONS: 'OPTIONS',
    HTTP2_METHOD_ORDERPATCH: 'ORDERPATCH',
    HTTP2_METHOD_PATCH: 'PATCH',
    HTTP2_METHOD_POST: 'POST',
    HTTP2_METHOD_PRI: 'PRI',
    HTTP2_METHOD_PROPFIND: 'PROPFIND',
    HTTP2_METHOD_PROPPATCH: 'PROPPATCH',
    HTTP2_METHOD_PUT: 'PUT',
    HTTP2_METHOD_REBIND: 'REBIND',
    HTTP2_METHOD_REPORT: 'REPORT',
    HTTP2_METHOD_SEARCH: 'SEARCH',
    HTTP2_METHOD_TRACE: 'TRACE',
    HTTP2_METHOD_UNBIND: 'UNBIND',
    HTTP2_METHOD_UNCHECKOUT: 'UNCHECKOUT',
    HTTP2_METHOD_UNLINK: 'UNLINK',
    HTTP2_METHOD_UNLOCK: 'UNLOCK',
    HTTP2_METHOD_UPDATE: 'UPDATE',
    HTTP2_METHOD_UPDATEREDIRECTREF: 'UPDATEREDIRECTREF',
    HTTP2_METHOD_VERSION_CONTROL: 'VERSION-CONTROL',
    HTTP_STATUS_CONTINUE: 100,
    HTTP_STATUS_SWITCHING_PROTOCOLS: 101,
    HTTP_STATUS_PROCESSING: 102,
    HTTP_STATUS_EARLY_HINTS: 103,
    HTTP_STATUS_OK: 200,
    HTTP_STATUS_CREATED: 201,
    HTTP_STATUS_ACCEPTED: 202,
    HTTP_STATUS_NON_AUTHORITATIVE_INFORMATION: 203,
    HTTP_STATUS_NO_CONTENT: 204,
    HTTP_STATUS_RESET_CONTENT: 205,
    HTTP_STATUS_PARTIAL_CONTENT: 206,
    HTTP_STATUS_MULTI_STATUS: 207,
    HTTP_STATUS_ALREADY_REPORTED: 208,
    HTTP_STATUS_IM_USED: 226,
    HTTP_STATUS_MULTIPLE_CHOICES: 300,
    HTTP_STATUS_MOVED_PERMANENTLY: 301,
    HTTP_STATUS_FOUND: 302,
    HTTP_STATUS_SEE_OTHER: 303,
    HTTP_STATUS_NOT_MODIFIED: 304,
    HTTP_STATUS_USE_PROXY: 305,
    HTTP_STATUS_TEMPORARY_REDIRECT: 307,
    HTTP_STATUS_PERMANENT_REDIRECT: 308,
    HTTP_STATUS_BAD_REQUEST: 400,
    HTTP_STATUS_UNAUTHORIZED: 401,
    HTTP_STATUS_PAYMENT_REQUIRED: 402,
    HTTP_STATUS_FORBIDDEN: 403,
    HTTP_STATUS_NOT_FOUND: 404,
    HTTP_STATUS_METHOD_NOT_ALLOWED: 405,
    HTTP_STATUS_NOT_ACCEPTABLE: 406,
    HTTP_STATUS_PROXY_AUTHENTICATION_REQUIRED: 407,
    HTTP_STATUS_REQUEST_TIMEOUT: 408,
    HTTP_STATUS_CONFLICT: 409,
    HTTP_STATUS_GONE: 410,
    HTTP_STATUS_LENGTH_REQUIRED: 411,
    HTTP_STATUS_PRECONDITION_FAILED: 412,
    HTTP_STATUS_PAYLOAD_TOO_LARGE: 413,
    HTTP_STATUS_URI_TOO_LONG: 414,
    HTTP_STATUS_UNSUPPORTED_MEDIA_TYPE: 415,
    HTTP_STATUS_RANGE_NOT_SATISFIABLE: 416,
    HTTP_STATUS_EXPECTATION_FAILED: 417,
    HTTP_STATUS_TEAPOT: 418,
    HTTP_STATUS_MISDIRECTED_REQUEST: 421,
    HTTP_STATUS_UNPROCESSABLE_ENTITY: 422,
    HTTP_STATUS_LOCKED: 423,
    HTTP_STATUS_FAILED_DEPENDENCY: 424,
    HTTP_STATUS_TOO_EARLY: 425,
    HTTP_STATUS_UPGRADE_REQUIRED: 426,
    HTTP_STATUS_PRECONDITION_REQUIRED: 428,
    HTTP_STATUS_TOO_MANY_REQUESTS: 429,
    HTTP_STATUS_REQUEST_HEADER_FIELDS_TOO_LARGE: 431,
    HTTP_STATUS_UNAVAILABLE_FOR_LEGAL_REASONS: 451,
    HTTP_STATUS_INTERNAL_SERVER_ERROR: 500,
    HTTP_STATUS_NOT_IMPLEMENTED: 501,
    HTTP_STATUS_BAD_GATEWAY: 502,
    HTTP_STATUS_SERVICE_UNAVAILABLE: 503,
    HTTP_STATUS_GATEWAY_TIMEOUT: 504,
    HTTP_STATUS_HTTP_VERSION_NOT_SUPPORTED: 505,
    HTTP_STATUS_VARIANT_ALSO_NEGOTIATES: 506,
    HTTP_STATUS_INSUFFICIENT_STORAGE: 507,
    HTTP_STATUS_LOOP_DETECTED: 508,
    HTTP_STATUS_BANDWIDTH_LIMIT_EXCEEDED: 509,
    HTTP_STATUS_NOT_EXTENDED: 510,
    HTTP_STATUS_NETWORK_AUTHENTICATION_REQUIRED: 511,
};

const NGHTTP2_NO_ERROR = 0;
const NGHTTP2_INTERNAL_ERROR = 2;
const NGHTTP2_CANCEL = 8;
const NGHTTP2_SESSION_SERVER = 0;
const NGHTTP2_SESSION_CLIENT = 1;
const NGHTTP2_FLAG_END_STREAM = 1;
const NGHTTP2_HCAT_RESPONSE = 1;
const NGHTTP2_HCAT_PUSH_RESPONSE = 2;
const NGHTTP2_ERR_STREAM_ID_NOT_AVAILABLE = -509;
const NGHTTP2_ERR_STREAM_CLOSED = -510;

const nameForErrorCode: string[] = [
    'NGHTTP2_NO_ERROR', 'NGHTTP2_PROTOCOL_ERROR', 'NGHTTP2_INTERNAL_ERROR', 'NGHTTP2_FLOW_CONTROL_ERROR',
    'NGHTTP2_SETTINGS_TIMEOUT', 'NGHTTP2_STREAM_CLOSED', 'NGHTTP2_FRAME_SIZE_ERROR', 'NGHTTP2_REFUSED_STREAM',
    'NGHTTP2_CANCEL', 'NGHTTP2_COMPRESSION_ERROR', 'NGHTTP2_CONNECT_ERROR', 'NGHTTP2_ENHANCE_YOUR_CALM',
    'NGHTTP2_INADEQUATE_SECURITY', 'NGHTTP2_HTTP_1_1_REQUIRED',
];

function codeName(code: number): string {
    return code >= 0 && code < nameForErrorCode.length ? nameForErrorCode[code] : String(code);
}

// The native event types (h2node.c).
const H2E_HEADERS = 1;
const H2E_DATA = 2;
const H2E_STREAM_END = 3;
const H2E_STREAM_CLOSE = 4;
const H2E_SETTINGS = 5;
const H2E_PING = 6;
const H2E_GOAWAY = 7;
const H2E_PUSH = 8;
const H2E_FRAME_ERROR = 9;
const H2E_WANT_TRAILERS = 10;

const kMaxFrameSize = 16777215;
const kMaxInt = 4294967295;
const kMaxInitialWindowSize = 2147483647;
const kMaxStreams = 4294967295;
const MAX_ADDITIONAL_SETTINGS = 10;

// ---- headers (lib/internal/http2/util.js) ----

// http2.sensitiveHeaders: a headers object's list of names never indexed.
export const sensitiveHeaders: unique symbol = Symbol('sensitiveHeaders');

const kValidPseudoHeaders = new Set([':status', ':method', ':authority', ':scheme', ':path', ':protocol']);

const kSingleValueFields = new Set([
    ':status', ':method', ':authority', ':scheme', ':path', ':protocol',
    'access-control-allow-credentials', 'access-control-max-age', 'access-control-request-method',
    'age', 'authorization', 'content-encoding', 'content-language', 'content-length', 'content-location',
    'content-md5', 'content-range', 'content-type', 'date', 'dnt', 'etag', 'expires', 'from', 'host',
    'if-match', 'if-modified-since', 'if-none-match', 'if-range', 'if-unmodified-since', 'last-modified',
    'location', 'max-forwards', 'proxy-authorization', 'range', 'referer', 'retry-after', 'tk',
    'upgrade-insecure-requests', 'user-agent', 'x-content-type-options',
]);

const kNoPayloadMethods = new Set(['DELETE', 'GET', 'HEAD']);

const tokenRegExp = /^[\^_`a-zA-Z\-0-9!#$%&'*+.|~]+$/;
function checkIsHttpToken(val: string): boolean {
    return tokenRegExp.test(val);
}

function utcDate(): string {
    return new Date().toUTCString();
}

function isIllegalConnectionSpecificHeader(name: string, value: string): boolean {
    switch (name) {
        case 'connection':
        case 'upgrade':
        case 'http2-settings':
        case 'keep-alive':
        case 'proxy-connection':
        case 'transfer-encoding':
            return true;
        case 'te':
            return value !== 'trailers';
        default:
            return false;
    }
}

type PseudoCheck = (key: string) => void;

function assertValidPseudoHeader(key: string): void {
    if (!kValidPseudoHeaders.has(key)) {
        throw new NodeTypeError('ERR_HTTP2_INVALID_PSEUDOHEADER', '"' + key + '" is an invalid pseudoheader or is used incorrectly');
    }
}

function assertValidPseudoHeaderResponse(key: string): void {
    if (key !== ':status') {
        throw new NodeTypeError('ERR_HTTP2_INVALID_PSEUDOHEADER', '"' + key + '" is an invalid pseudoheader or is used incorrectly');
    }
}

function assertValidPseudoHeaderTrailer(key: string): void {
    throw new NodeTypeError('ERR_HTTP2_INVALID_PSEUDOHEADER', '"' + key + '" is an invalid pseudoheader or is used incorrectly');
}

function assertIsObject(value: any, name: string, types?: string): void {
    if (value !== undefined && (value === null || typeof value !== 'object' || Array.isArray(value))) {
        throw invalidArgType(name, 'of type ' + (types ?? 'object'), value);
    }
}

// buildNgHeaderString: the headers as the native list ("name\nvalue\n…",
// a sensitive header's name followed by \x01), pseudo-headers first.
function buildNgHeaderString(arrayOrMap: any, validatePseudo: PseudoCheck, strictSingleValueFields: boolean): string {
    let headers = '';
    let pseudoHeaders = '';
    const singles = new Set<string>();
    const sensitive: any[] = arrayOrMap[sensitiveHeaders] || [];
    const neverIndex = sensitive.map((v: any) => String(v).toLowerCase());

    const processHeader = (keyIn: string, valueIn: any): void => {
        const key = keyIn.toLowerCase();
        const isStrictSingleValueField = strictSingleValueFields && kSingleValueFields.has(key);
        let value: any = valueIn;
        let isArray = Array.isArray(value);
        if (isArray) {
            switch (value.length) {
                case 0:
                    return;
                case 1:
                    value = String(value[0]);
                    isArray = false;
                    break;
                default:
                    if (isStrictSingleValueField) {
                        throw new NodeTypeError('ERR_HTTP2_HEADER_SINGLE_VALUE', 'Header field "' + key + '" must only have a single value');
                    }
            }
        } else {
            value = String(value);
        }
        if (isStrictSingleValueField) {
            if (singles.has(key)) {
                throw new NodeTypeError('ERR_HTTP2_HEADER_SINGLE_VALUE', 'Header field "' + key + '" must only have a single value');
            }
            singles.add(key);
        }
        const name = neverIndex.includes(key) ? key + '\x01' : key;
        if (key[0] === ':') {
            validatePseudo(key);
            pseudoHeaders += name + '\n' + value + '\n';
            return;
        }
        if (!checkIsHttpToken(key)) {
            throw new NodeTypeError('ERR_INVALID_HTTP_TOKEN', 'Header name must be a valid HTTP token ["' + key + '"]');
        }
        if (isIllegalConnectionSpecificHeader(key, value)) {
            throw new NodeTypeError('ERR_HTTP2_INVALID_CONNECTION_HEADERS', 'HTTP/1 Connection specific headers are forbidden: "' + key + '"');
        }
        if (isArray) {
            for (let j = 0; j < value.length; ++j) headers += name + '\n' + String(value[j]) + '\n';
            return;
        }
        headers += name + '\n' + value + '\n';
    };

    if (Array.isArray(arrayOrMap)) {
        for (let i = 0; i < arrayOrMap.length; i += 2) {
            const key = arrayOrMap[i];
            const value = arrayOrMap[i + 1];
            if (value === undefined || key === '') continue;
            processHeader(key, value);
        }
    } else {
        const keys = Object.keys(arrayOrMap);
        for (let i = 0; i < keys.length; ++i) {
            const key = keys[i];
            const value = arrayOrMap[key];
            if (value === undefined || key === '') continue;
            processHeader(key, value);
        }
    }
    return pseudoHeaders + headers;
}

// The native header list ("name\nvalue\n…") as a flat [name, value, …].
function parseHeaderList(text: string): string[] {
    const parts = text.split('\n');
    parts.pop();
    return parts;
}

function toHeaderObject(headers: string[], sensitive: string[]): any {
    const obj: any = { __proto__: null };
    for (let n = 0; n < headers.length; n += 2) {
        const name = headers[n];
        let value: any = headers[n + 1];
        if (name === ':status') value = value | 0;
        const existing = obj[name];
        if (existing === undefined) {
            obj[name] = name === 'set-cookie' ? [value] : value;
        } else if (!kSingleValueFields.has(name)) {
            switch (name) {
                case 'cookie':
                    obj[name] = existing + '; ' + value;
                    break;
                case 'set-cookie':
                    existing.push(value);
                    break;
                default:
                    obj[name] = existing + ', ' + value;
                    break;
            }
        }
    }
    obj[sensitiveHeaders] = sensitive;
    return obj;
}

function getAuthority(headers: any): any {
    if (headers[':authority'] !== undefined) return headers[':authority'];
    if (headers['host'] !== undefined) return headers['host'];
    return undefined;
}

// ---- settings ----

// @types/node's Settings, with the maxHeaderSize alias Node's settings
// objects carry, in the order Node lists them.
export interface Settings {
    headerTableSize?: number | undefined;
    enablePush?: boolean | undefined;
    initialWindowSize?: number | undefined;
    maxFrameSize?: number | undefined;
    maxConcurrentStreams?: number | undefined;
    maxHeaderListSize?: number | undefined;
    maxHeaderSize?: number | undefined;
    enableConnectProtocol?: boolean | undefined;
}

function assertWithinRange(name: string, value: any, min: number, max: number): void {
    if (value !== undefined && (typeof value !== 'number' || value < min || value > max)) {
        throw errInvalidSettingValue(name, value, true);
    }
}

function validateSettings(settings: any): void {
    if (settings === undefined) return;
    assertIsObject(settings.customSettings, 'customSettings', 'Number');
    if (settings.customSettings) {
        const keys = Object.keys(settings.customSettings);
        if (keys.length > MAX_ADDITIONAL_SETTINGS) {
            throw new NodeRangeError('ERR_HTTP2_TOO_MANY_CUSTOM_SETTINGS', 'Number of custom settings exceeds MAX_ADDITIONAL_SETTINGS');
        }
        for (const key of keys) {
            assertWithinRange('customSettings:id', Number(key), 0, 0xffff);
            assertWithinRange('customSettings:value', Number(settings.customSettings[key]), 0, kMaxInt);
        }
    }
    assertWithinRange('headerTableSize', settings.headerTableSize, 0, kMaxInt);
    assertWithinRange('initialWindowSize', settings.initialWindowSize, 0, kMaxInitialWindowSize);
    assertWithinRange('maxFrameSize', settings.maxFrameSize, 16384, kMaxFrameSize);
    assertWithinRange('maxConcurrentStreams', settings.maxConcurrentStreams, 0, kMaxStreams);
    assertWithinRange('maxHeaderListSize', settings.maxHeaderListSize, 0, kMaxInt);
    assertWithinRange('maxHeaderSize', settings.maxHeaderSize, 0, kMaxInt);
    if (settings.enablePush !== undefined && typeof settings.enablePush !== 'boolean') {
        throw errInvalidSettingValue('enablePush', settings.enablePush, false);
    }
    if (settings.enableConnectProtocol !== undefined && typeof settings.enableConnectProtocol !== 'boolean') {
        throw errInvalidSettingValue('enableConnectProtocol', settings.enableConnectProtocol, false);
    }
}

let maxHeaderSizeWarned = false;

// The settings as SETTINGS records (id, value), Node's PackSettings order.
function packSettings(settings: any): Buffer {
    const entries: number[][] = [];
    const add = (id: number, v: number): void => { entries.push([id, v]); };
    if (settings.headerTableSize !== undefined) add(1, settings.headerTableSize);
    if (settings.enablePush !== undefined) add(2, settings.enablePush ? 1 : 0);
    if (settings.maxConcurrentStreams !== undefined) add(3, settings.maxConcurrentStreams);
    if (settings.initialWindowSize !== undefined) add(4, settings.initialWindowSize);
    if (settings.maxFrameSize !== undefined) add(5, settings.maxFrameSize);
    let headerList: any = settings.maxHeaderListSize;
    if (settings.maxHeaderSize !== undefined) {
        if (headerList !== undefined && !maxHeaderSizeWarned) {
            maxHeaderSizeWarned = true;
            process.emitWarning('settings.maxHeaderSize overwrite settings.maxHeaderListSize');
        }
        headerList = settings.maxHeaderSize;
    }
    if (headerList !== undefined) add(6, headerList);
    if (settings.enableConnectProtocol !== undefined) add(8, settings.enableConnectProtocol ? 1 : 0);
    if (typeof settings.customSettings === 'object' && settings.customSettings !== null) {
        for (const key of Object.keys(settings.customSettings)) {
            const val = settings.customSettings[key];
            if (typeof val !== 'number') continue;
            const id = Number(key);
            if (Number.isNaN(id) || id <= 0 || id > 0xffff) {
                throw new NodeRangeError('ERR_HTTP2_INVALID_SETTING_VALUE', 'Invalid value for setting "Range Error": ' + id);
            }
            if (Number.isNaN(val) || val <= 0 || val > 0xffffffff) {
                throw new NodeRangeError('ERR_HTTP2_INVALID_SETTING_VALUE', 'Invalid value for setting "Range Error": ' + val);
            }
            add(id, val);
        }
    }
    const buf = Buffer.alloc(entries.length * 6);
    for (let i = 0; i < entries.length; i++) {
        buf.writeUInt16BE(entries[i][0], i * 6);
        buf.writeUInt32BE(entries[i][1], i * 6 + 2);
    }
    return buf;
}

export function getDefaultSettings(): Settings;
export function getDefaultSettings(): any {
    const holder: any = { __proto__: null };
    holder.headerTableSize = 4096;
    holder.enablePush = true;
    holder.initialWindowSize = 65535;
    holder.maxFrameSize = 16384;
    holder.maxConcurrentStreams = 4294967295;
    holder.maxHeaderSize = 65535;
    holder.maxHeaderListSize = 65535;
    holder.enableConnectProtocol = false;
    return holder;
}

export function getPackedSettings(settings?: Settings): Buffer;
export function getPackedSettings(settings?: any): Buffer {
    assertIsObject(settings, 'settings');
    validateSettings(settings);
    return packSettings({ ...settings });
}

export function getUnpackedSettings(buf: Uint8Array, options?: { validate?: boolean }): Settings;
export function getUnpackedSettings(buf: any, options?: any): any {
    if (!ArrayBuffer.isView(buf) || (buf as any).length === undefined) {
        throw invalidArgType('buf', 'an instance of Buffer or TypedArray', buf);
    }
    if (buf.length % 6 !== 0) {
        throw new NodeRangeError('ERR_HTTP2_INVALID_PACKED_SETTINGS_LENGTH', 'Packed settings length must be a multiple of six');
    }
    const settings: any = {};
    let offset = 0;
    while (offset < buf.length) {
        const id = (buf[offset] << 8) | buf[offset + 1];
        offset += 2;
        const value = buf[offset] * 0x1000000 + ((buf[offset + 1] << 16) | (buf[offset + 2] << 8) | buf[offset + 3]);
        switch (id) {
            case 1: settings.headerTableSize = value; break;
            case 2: settings.enablePush = value !== 0; break;
            case 3: settings.maxConcurrentStreams = value; break;
            case 4: settings.initialWindowSize = value; break;
            case 5: settings.maxFrameSize = value; break;
            case 6:
                settings.maxHeaderSize = value;
                settings.maxHeaderListSize = value;
                break;
            case 8: settings.enableConnectProtocol = value !== 0; break;
            default:
                if (settings.customSettings === undefined) settings.customSettings = {};
                settings.customSettings[id] = value;
        }
        offset += 4;
    }
    if (options != null && options.validate) validateSettings(settings);
    return settings;
}

// A session's local (40 + id) or remote (20 + id) settings, from nghttp2.
function readSettings(handle: number, base: number): Settings {
    const s: any = {};
    s.headerTableSize = __kml_native.h2State(handle, base + 1);
    s.enablePush = __kml_native.h2State(handle, base + 2) === 1;
    s.initialWindowSize = __kml_native.h2State(handle, base + 4);
    s.maxFrameSize = __kml_native.h2State(handle, base + 5);
    s.maxConcurrentStreams = __kml_native.h2State(handle, base + 3);
    s.maxHeaderListSize = __kml_native.h2State(handle, base + 6);
    s.maxHeaderSize = s.maxHeaderListSize;
    s.enableConnectProtocol = __kml_native.h2State(handle, base + 8) === 1;
    return s;
}

// ---- sessions ----

const SESSION_FLAGS_PENDING = 0x0;
const SESSION_FLAGS_READY = 0x1;
const SESSION_FLAGS_CLOSED = 0x2;
const SESSION_FLAGS_DESTROYED = 0x4;

export interface SessionState {
    effectiveLocalWindowSize?: number;
    effectiveRecvDataLength?: number;
    nextStreamID?: number;
    localWindowSize?: number;
    lastProcStreamID?: number;
    remoteWindowSize?: number;
    outboundQueueSize?: number;
    deflateDynamicTableSize?: number;
    inflateDynamicTableSize?: number;
}

export interface ClientSessionOptions {
    settings?: Settings;
    maxHeaderListPairs?: number;
    maxReservedRemoteStreams?: number;
    peerMaxConcurrentStreams?: number;
    strictSingleValueFields?: boolean;
    protocol?: 'http:' | 'https:';
    [key: string]: any;
}

export interface ServerOptions {
    settings?: Settings;
    maxHeaderListPairs?: number;
    peerMaxConcurrentStreams?: number;
    strictSingleValueFields?: boolean;
    unknownProtocolTimeout?: number;
    Http2ServerRequest?: typeof Http2ServerRequest;
    Http2ServerResponse?: typeof Http2ServerResponse;
    [key: string]: any;
}

export interface SecureServerOptions extends ServerOptions {
    allowHTTP1?: boolean;
    origins?: string[];
    cert?: any;
    key?: any;
    ca?: any;
}

export interface SecureClientSessionOptions extends ClientSessionOptions {
    ca?: any;
    rejectUnauthorized?: boolean;
    servername?: string;
}

// kBoundSession: the session a socket is bound to.
const boundSessions = new WeakMap<object, Http2Session>();

export class Http2Session extends EventEmitter {
    _kmlHandle = 0;
    _kmlType: number;
    _kmlSocket: any;
    _kmlOptions: any;
    _kmlFlags = SESSION_FLAGS_PENDING;
    _kmlDestroyCode = NGHTTP2_NO_ERROR;
    _kmlGoawayCode: number | null = null;
    _kmlGoawayLastStreamID: number | null = null;
    _kmlStreams = new Map<number, Http2Stream>();
    _kmlPendingStreams = new Set<Http2Stream>();
    _kmlPendingAck = 0;
    _kmlSettingsCallbacks: any[] = [];
    _kmlPings: any[] = [];
    _kmlLocalSettings: Settings | undefined = undefined;
    _kmlEncrypted: boolean | undefined = undefined;
    _kmlAlpnProtocol: string | undefined = undefined;
    _kmlTimeout: any = null;
    _kmlTimeoutMs = 0;
    _kmlStrictSingleValueFields: boolean;
    _kmlAuthority = '';
    _kmlProtocol = '';
    _kmlServer: any = undefined;

    constructor(type: number, options: any, socket: any) {
        super();
        if (boundSessions.has(socket)) {
            throw new NodeError('ERR_HTTP2_SOCKET_BOUND', 'The socket is already bound to an Http2Session');
        }
        boundSessions.set(socket, this);
        this._kmlType = type;
        this._kmlSocket = socket;
        this._kmlOptions = options;
        this._kmlStrictSingleValueFields = options.strictSingleValueFields !== false;
        socket.on('error', (error: any) => { this._kmlSocketError(error); });
        socket.on('close', () => { this._kmlSocketClose(); });
        if (typeof socket.setNoDelay === 'function') socket.setNoDelay();
        const setup = (): void => {
            try {
                this._kmlSetupHandle();
            } catch (error) {
                socket.destroy(error);
            }
        };
        const connecting = socket instanceof tls.TLSSocket ? !(socket as any)._secureEstablished : socket.connecting;
        if (connecting) {
            socket.once(socket instanceof tls.TLSSocket ? 'secureConnect' : 'connect', setup);
        } else {
            setup();
        }
    }

    // setupHandle
    private _kmlSetupHandle(): void {
        if (this.destroyed) {
            process.nextTick(() => { this.emit('connect', this, this._kmlSocket); });
            return;
        }
        this._kmlFlags |= SESSION_FLAGS_READY;
        const o = this._kmlOptions;
        this._kmlHandle = __kml_native.h2New(this._kmlType, o.maxHeaderListPairs ?? -1,
            o.maxReservedRemoteStreams ?? -1, o.peerMaxConcurrentStreams ?? -1);
        if (this._kmlSocket instanceof tls.TLSSocket) {
            this._kmlAlpnProtocol = (this._kmlSocket as tls.TLSSocket).alpnProtocol as any;
            this._kmlEncrypted = true;
        } else {
            this._kmlAlpnProtocol = 'h2c';
            this._kmlEncrypted = false;
        }
        this._kmlSocket.on('data', (chunk: Buffer) => { this._kmlReceive(chunk); });
        const settings = typeof o.settings === 'object' && o.settings !== null ? o.settings : {};
        this.settings(settings);
        process.nextTick(() => { this.emit('connect', this, this._kmlSocket); });
    }

    // The bytes the socket read: into nghttp2, then its events, then what
    // it has to send.
    _kmlReceive(chunk: Buffer): void {
        if (this.destroyed || this._kmlHandle <= 0) return;
        this._kmlUpdateTimer();
        const n = __kml_native.h2Feed(this._kmlHandle, chunk, 0, chunk.length);
        this._kmlProcessEvents();
        if (n < 0) {
            this.destroy(new NghttpError(n));
            return;
        }
        this._kmlScheduleFlush();
    }

    // MaybeScheduleWrite: the frames go out once per turn of the event loop,
    // so nghttp2 sees every queued frame together and orders them by stream
    // priority (a pushed stream's DATA after its parent's).
    _kmlFlushScheduled = false;

    _kmlScheduleFlush(): void {
        if (this._kmlFlushScheduled) return;
        this._kmlFlushScheduled = true;
        setImmediate(() => {
            this._kmlFlushScheduled = false;
            this._kmlFlush();
        });
    }

    // Writes out every frame nghttp2 has ready; a closing session whose last
    // frames went out completes its graceful close.
    _kmlFlush(): void {
        if (this._kmlHandle <= 0) return;
        this._kmlFlushOnce();
        if (this.closed && !this.destroyed) this._kmlMaybeDestroy();
    }

    private _kmlFlushOnce(): void {
        const n = __kml_native.h2Flush(this._kmlHandle);
        if (n > 0) {
            const out = Buffer.alloc(n);
            __kml_native.h2Take(this._kmlHandle, out);
            const socket = this._kmlSocket;
            if (socket !== undefined && !socket.destroyed) socket.write(out);
        }
        this._kmlProcessEvents();
    }

    private _kmlProcessEvents(): void {
        const h = this._kmlHandle;
        if (h <= 0) return;
        for (;;) {
            const type = __kml_native.h2Next(h);
            if (type === 0) break;
            const a = __kml_native.h2Event(h, 0);
            const b = __kml_native.h2Event(h, 1);
            const c = __kml_native.h2Event(h, 2);
            switch (type) {
                case H2E_HEADERS:
                    this._kmlOnHeaders(a, b, c, __kml_native.lastString());
                    break;
                case H2E_DATA: {
                    const bytes = Buffer.alloc(__kml_native.h2Event(h, 3));
                    __kml_native.h2EventBytes(h, bytes);
                    const stream = this._kmlStreams.get(a);
                    if (stream !== undefined && !stream.destroyed) {
                        stream._kmlUpdateTimer();
                        stream.push(bytes);
                    }
                    break;
                }
                case H2E_STREAM_END: {
                    const stream = this._kmlStreams.get(a);
                    if (stream !== undefined) stream.push(null);
                    break;
                }
                case H2E_STREAM_CLOSE: {
                    const stream = this._kmlStreams.get(a);
                    if (stream !== undefined) stream._kmlOnStreamClose(b);
                    break;
                }
                case H2E_SETTINGS:
                    if (b === 1) this._kmlSettingsAcked();
                    else this.emit('remoteSettings', this.remoteSettings);
                    break;
                case H2E_PING: {
                    const payload = Buffer.alloc(8);
                    __kml_native.h2EventBytes(h, payload);
                    if (b === 1) {
                        const p = this._kmlPings.shift();
                        if (p !== undefined) p.cb(null, Date.now() - p.start, payload);
                    } else {
                        this.emit('ping', payload);
                    }
                    break;
                }
                case H2E_GOAWAY: {
                    const opaque = Buffer.alloc(__kml_native.h2Event(h, 3));
                    __kml_native.h2EventBytes(h, opaque);
                    this._kmlOnGoaway(b, a, opaque.length > 0 ? opaque : undefined);
                    break;
                }
                case H2E_PUSH:
                    this._kmlOnPush(a, b, __kml_native.lastString());
                    break;
                case H2E_FRAME_ERROR: {
                    const stream = this._kmlStreams.get(a);
                    const emitter: EventEmitter = stream !== undefined ? stream : this;
                    emitter.emit('frameError', b, c, a);
                    setImmediate(() => {
                        if (stream !== undefined) stream.close(c);
                        this.close();
                    });
                    break;
                }
                case H2E_WANT_TRAILERS: {
                    const stream = this._kmlStreams.get(a);
                    if (stream !== undefined) stream._kmlOnWantTrailers();
                    break;
                }
            }
            // Each frame is its own native callback in Node, which runs the
            // tick queue before the next (MakeCallback): a 'response' emitted
            // on the next tick precedes the DATA frame after it.
            __kml_native.runMicrotasks();
            if (this._kmlHandle !== h) return;
        }
    }

    // onSessionHeaders
    private _kmlOnHeaders(id: number, cat: number, flags: number, text: string): void {
        if (this.destroyed) return;
        this._kmlUpdateTimer();
        const headers = parseHeaderList(text);
        const endOfStream = (flags & NGHTTP2_FLAG_END_STREAM) !== 0;
        const obj = toHeaderObject(headers, []);
        let stream = this._kmlStreams.get(id);
        if (stream === undefined) {
            if (this.closed) {
                __kml_native.h2Rst(this._kmlHandle, id, 7);
                return;
            }
            if (this._kmlType === NGHTTP2_SESSION_SERVER) {
                const s = new ServerHttp2Stream(this, id, {}, obj);
                stream = s;
                if (endOfStream) s.push(null);
                if (obj[':method'] === 'HEAD') {
                    s.end();
                    s._kmlHeadRequest = true;
                }
            } else {
                const s = new ClientHttp2Stream(this, id, {});
                stream = s;
                if (endOfStream) s.push(null);
                s.end();
            }
            if (endOfStream) stream._kmlEndAfterHeaders = true;
            const st = stream;
            process.nextTick(() => { this.emit('stream', st, obj, flags, headers); });
        } else {
            let event: string;
            const status = obj[':status'];
            if (cat === NGHTTP2_HCAT_RESPONSE) {
                event = !endOfStream && status !== undefined && status >= 100 && status < 200 ? 'headers' : 'response';
            } else if (cat === NGHTTP2_HCAT_PUSH_RESPONSE) {
                event = 'push';
            } else if (status !== undefined && status >= 200) {
                event = 'response';
            } else {
                event = endOfStream ? 'trailers' : 'headers';
            }
            const st = stream;
            process.nextTick(() => { st.emit(event, obj, flags, headers); });
        }
        if (endOfStream) stream.push(null);
    }

    // A PUSH_PROMISE (client): the promised stream, announced on the session.
    private _kmlOnPush(parentId: number, id: number, text: string): void {
        const headers = parseHeaderList(text);
        const obj = toHeaderObject(headers, []);
        const s = new ClientHttp2Stream(this, id, {});
        s.end();
        process.nextTick(() => { this.emit('stream', s, obj, 0, headers); });
    }

    // onGoawayData
    private _kmlOnGoaway(code: number, lastStreamID: number, buf: Buffer | undefined): void {
        if (this.destroyed) return;
        this._kmlGoawayCode = code;
        this._kmlGoawayLastStreamID = lastStreamID;
        this.emit('goaway', code, lastStreamID, buf);
        if (code === NGHTTP2_NO_ERROR) {
            this.close();
        } else {
            this.destroy(new NodeError('ERR_HTTP2_SESSION_ERROR', 'Session closed with error code ' + code), NGHTTP2_NO_ERROR);
        }
    }

    // settingsCallback, for the SETTINGS frame acknowledged.
    private _kmlSettingsAcked(): void {
        this._kmlPendingAck--;
        this._kmlLocalSettings = undefined;
        const entry = this._kmlSettingsCallbacks.shift();
        const settings = this.localSettings;
        if (entry !== undefined && typeof entry.cb === 'function') entry.cb(null, settings, Date.now() - entry.start);
        this.emit('localSettings', settings);
    }

    _kmlSocketError(error: any): void {
        if (error.code === 'ECONNRESET' && this._kmlGoawayCode !== null) {
            this.destroy();
            return;
        }
        this.destroy(error);
    }

    // socketOnClose
    private _kmlSocketClose(): void {
        if (this.destroyed) return;
        const err = this.connecting ? new NodeError('ERR_SOCKET_CLOSED', 'Socket is closed') : null;
        for (const s of Array.from(this._kmlStreams.values())) s.close(NGHTTP2_CANCEL);
        for (const s of Array.from(this._kmlPendingStreams)) s.close(NGHTTP2_CANCEL);
        this.close();
        this._kmlCloseSession(NGHTTP2_NO_ERROR, err);
    }

    get encrypted(): boolean | undefined {
        return this._kmlEncrypted;
    }

    get alpnProtocol(): string | undefined {
        return this._kmlAlpnProtocol;
    }

    get originSet(): string[] | undefined {
        if (!this.encrypted || this.destroyed) return undefined;
        return ['https://' + this._kmlAuthority];
    }

    get connecting(): boolean {
        return (this._kmlFlags & SESSION_FLAGS_READY) === 0;
    }

    get closed(): boolean {
        return (this._kmlFlags & SESSION_FLAGS_CLOSED) !== 0;
    }

    get destroyed(): boolean {
        return (this._kmlFlags & SESSION_FLAGS_DESTROYED) !== 0;
    }

    get socket(): any {
        return this._kmlSocket;
    }

    get type(): number {
        return this._kmlType;
    }

    get goawayCode(): number {
        return this._kmlGoawayCode || NGHTTP2_NO_ERROR;
    }

    get goawayLastStreamID(): number {
        return this._kmlGoawayLastStreamID || 0;
    }

    get pendingSettingsAck(): boolean {
        return this._kmlPendingAck > 0;
    }

    get state(): SessionState {
        if (this.connecting || this.destroyed) return {};
        const h = this._kmlHandle;
        return {
            effectiveLocalWindowSize: __kml_native.h2State(h, 0),
            effectiveRecvDataLength: __kml_native.h2State(h, 1),
            nextStreamID: __kml_native.h2State(h, 2),
            localWindowSize: __kml_native.h2State(h, 3),
            lastProcStreamID: __kml_native.h2State(h, 4),
            remoteWindowSize: __kml_native.h2State(h, 5),
            outboundQueueSize: __kml_native.h2State(h, 6),
            deflateDynamicTableSize: __kml_native.h2State(h, 7),
            inflateDynamicTableSize: __kml_native.h2State(h, 8),
        };
    }

    get localSettings(): Settings {
        if (this._kmlLocalSettings !== undefined) return this._kmlLocalSettings;
        if (this.destroyed || this.connecting) return {};
        this._kmlLocalSettings = readSettings(this._kmlHandle, 40);
        return this._kmlLocalSettings;
    }

    get remoteSettings(): Settings {
        if (this.destroyed || this.connecting) return {};
        return readSettings(this._kmlHandle, 20);
    }

    // Node's timeout.refresh(): the timer re-armed for its full duration.
    _kmlArmTimer(): void {
        const t = setTimeout(() => { this.emit('timeout'); }, this._kmlTimeoutMs);
        t.unref();
        this._kmlTimeout = t;
    }

    _kmlUpdateTimer(): void {
        if (this.destroyed) return;
        if (this._kmlTimeout !== null) {
            clearTimeout(this._kmlTimeout);
            this._kmlArmTimer();
        }
    }

    setTimeout(msecs: number, callback?: () => void): void {
        if (this._kmlTimeout !== null) {
            clearTimeout(this._kmlTimeout);
            this._kmlTimeout = null;
        }
        if (callback !== undefined) {
            validateFunction(callback, 'callback');
            this.once('timeout', callback);
        }
        if (msecs > 0) {
            this._kmlTimeoutMs = msecs;
            this._kmlArmTimer();
        }
    }

    setNextStreamID(id: number): void {
        if (this.destroyed) throw errInvalidSession();
        validateNumber(id, 'id');
    }

    setLocalWindowSize(windowSize: number): void {
        if (this.destroyed) throw errInvalidSession();
        validateNumber(windowSize, 'windowSize');
    }

    ping(callback: (err: Error | null, duration: number, payload: Buffer) => void): boolean;
    ping(payload: NodeJS.ArrayBufferView, callback: (err: Error | null, duration: number, payload: Buffer) => void): boolean;
    ping(payload: any, callback?: any): boolean {
        if (this.destroyed) throw errInvalidSession();
        if (typeof payload === 'function') {
            callback = payload;
            payload = undefined;
        }
        if (payload) {
            if (!ArrayBuffer.isView(payload)) throw invalidArgType('payload', 'an instance of Buffer, TypedArray, or DataView', payload);
            if (payload.byteLength !== 8) throw new NodeRangeError('ERR_HTTP2_PING_LENGTH', 'HTTP2 ping payload must be 8 bytes');
        }
        validateFunction(callback, 'callback');
        const body = Buffer.alloc(8);
        if (payload) body.set((payload as Uint8Array).subarray(0, 8));
        if (this.connecting || this.closed) {
            process.nextTick(() => { callback(new NodeError('ERR_HTTP2_PING_CANCEL', 'HTTP2 ping cancelled')); });
            return false;
        }
        this._kmlPings.push({ cb: callback, start: Date.now() });
        __kml_native.h2Ping(this._kmlHandle, body);
        this._kmlScheduleFlush();
        return true;
    }

    settings(settings: Settings, callback?: (err: Error | null, settings: Settings, duration: number) => void): void {
        if (this.destroyed) throw errInvalidSession();
        assertIsObject(settings, 'settings');
        validateSettings(settings);
        if (callback) validateFunction(callback, 'callback');
        this._kmlPendingAck++;
        const submit = (): void => {
            if (this.destroyed) return;
            this._kmlUpdateTimer();
            this._kmlSettingsCallbacks.push({ cb: callback, start: Date.now() });
            __kml_native.h2Settings(this._kmlHandle, packSettings({ ...settings }));
            this._kmlScheduleFlush();
        };
        if (this.connecting) {
            this.once('connect', submit);
            return;
        }
        submit();
    }

    goaway(code?: number, lastStreamID?: number, opaqueData?: NodeJS.ArrayBufferView): void {
        if (this.destroyed) throw errInvalidSession();
        const c = code ?? NGHTTP2_NO_ERROR;
        const last = lastStreamID ?? 0;
        if (opaqueData !== undefined && !ArrayBuffer.isView(opaqueData)) {
            throw invalidArgType('opaqueData', 'an instance of Buffer, TypedArray, or DataView', opaqueData);
        }
        validateNumber(c, 'code');
        validateNumber(last, 'lastStreamID');
        const submit = (): void => {
            if (this.destroyed) return;
            const data = Buffer.alloc(opaqueData !== undefined ? opaqueData.byteLength : 0);
            if (opaqueData !== undefined) data.set(opaqueData as Uint8Array);
            __kml_native.h2Goaway(this._kmlHandle, c, last <= 0 ? -1 : last, data);
            this._kmlScheduleFlush();
        };
        if (this.connecting) {
            this.once('connect', submit);
            return;
        }
        submit();
    }

    destroy(error?: any, code?: number): void {
        if (this.destroyed) return;
        let err: any = error;
        let c: any = code;
        if (typeof error === 'number') {
            c = error;
            err = c !== NGHTTP2_NO_ERROR ? new NodeError('ERR_HTTP2_SESSION_ERROR', 'Session closed with error code ' + c) : undefined;
        }
        if (err === NGHTTP2_NO_ERROR) err = undefined;
        if (c === undefined && err != null) c = NGHTTP2_INTERNAL_ERROR;
        this._kmlCloseSession(c ?? NGHTTP2_NO_ERROR, err);
    }

    close(callback?: () => void): void {
        if (this.closed || this.destroyed) return;
        this._kmlFlags |= SESSION_FLAGS_CLOSED;
        if (typeof callback === 'function') this.once('close', callback);
        this.goaway();
        this._kmlMaybeDestroy();
    }

    // closeSession
    _kmlCloseSession(code: number, error: any): void {
        this._kmlFlags |= SESSION_FLAGS_DESTROYED;
        this._kmlDestroyCode = code;
        this.setTimeout(0);
        this.removeAllListeners('timeout');
        const socket = this._kmlSocket;
        if (this._kmlPendingStreams.size > 0 || this._kmlStreams.size > 0) {
            const cancel = new NodeError('ERR_HTTP2_STREAM_CANCEL', 'The pending stream has been canceled' +
                (error ? ' (caused by: ' + error.message + ')' : ''));
            for (const s of Array.from(this._kmlPendingStreams)) s.destroy(cancel);
            for (const s of Array.from(this._kmlStreams.values())) s.destroy(error);
        }
        const h = this._kmlHandle;
        if (h > 0) {
            if (socket !== undefined && !socket.destroyed && code !== NGHTTP2_NO_ERROR) {
                __kml_native.h2Goaway(h, code, -1, Buffer.alloc(0));
            }
            if (socket !== undefined && !socket.destroyed) this._kmlFlush();
            this._kmlHandle = 0;
            __kml_native.h2Free(h);
        }
        this._kmlFinishSessionClose(error);
    }

    // finishSessionClose
    private _kmlFinishSessionClose(error: any): void {
        const socket = this._kmlSocket;
        if (this._kmlServer !== undefined) this._kmlServer._kmlSessions.delete(this);
        if (socket !== undefined && !socket.destroyed) {
            socket.on('close', () => { this._kmlEmitClose(error); });
            if (this.closed) socket.resume();
            socket.end(() => {
                if (!this.closed) setImmediate(() => { socket.destroy(error); });
            });
        } else {
            process.nextTick(() => { this._kmlEmitClose(error); });
        }
    }

    private _kmlEmitClose(error: any): void {
        if (error) this.emit('error', error);
        this.emit('close');
    }

    // kMaybeDestroy: a closed session with no streams and nothing left to
    // send (its GOAWAY included) destroys.
    _kmlMaybeDestroy(error?: any): void {
        if (error == null) {
            const h = this._kmlHandle;
            const pending = h > 0 && (this._kmlFlushScheduled || __kml_native.h2State(h, 10) !== 0);
            if (!this.closed || this._kmlStreams.size > 0 || this._kmlPendingStreams.size > 0 || pending) return;
        }
        this.destroy(error);
    }

    ref(): void {
        if (this._kmlSocket) this._kmlSocket.ref();
    }

    unref(): void {
        if (this._kmlSocket) this._kmlSocket.unref();
    }
}

export class ServerHttp2Session extends Http2Session {
    constructor(options: any, socket: any, server: any) {
        super(NGHTTP2_SESSION_SERVER, options, socket);
        this._kmlServer = server;
        if (server) server._kmlSessions.add(this);
    }

    get server(): any {
        return this._kmlServer;
    }

    altsvc(alt: string, originOrStream: any): void {
        if (this.destroyed) throw errInvalidSession();
        validateString(alt, 'alt');
    }

    origin(...origins: any[]): void {
        if (this.destroyed) throw errInvalidSession();
    }
}

export interface ClientSessionRequestOptions {
    endStream?: boolean;
    exclusive?: boolean;
    parent?: number;
    waitForTrailers?: boolean;
    signal?: AbortSignal;
}

export class ClientHttp2Session extends Http2Session {
    private _kmlPendingRequestCalls: (() => void)[] | null = null;

    constructor(options: any, socket: any) {
        super(NGHTTP2_SESSION_CLIENT, options, socket);
    }

    request(headersParam?: OutgoingHttpHeaders | string[], options?: ClientSessionRequestOptions): ClientHttp2Stream {
        let requestError: Error | undefined = undefined;
        if (this.destroyed) {
            requestError = errInvalidSession();
        } else if (this.closed) {
            requestError = new NodeError('ERR_HTTP2_GOAWAY_SESSION', 'New streams cannot be created after receiving a GOAWAY');
        }
        this._kmlUpdateTimer();
        let headersObject: any = undefined;
        let headersList: string;
        let method: string;
        let scheme: any;
        let authority: any;
        if (Array.isArray(headersParam)) {
            const r = prepareRequestHeadersArray(headersParam, this);
            headersList = r.headersList;
            method = r.method;
            scheme = r.scheme;
            authority = r.authority;
        } else if (!!headersParam && typeof headersParam === 'object') {
            const r = prepareRequestHeadersObject(headersParam, this);
            headersObject = r.headersObject;
            headersList = r.headersList;
            method = r.method;
            scheme = r.scheme;
            authority = r.authority;
        } else if (headersParam === undefined) {
            const r = prepareRequestHeadersObject({}, this);
            headersObject = r.headersObject;
            headersList = r.headersList;
            method = r.method;
            scheme = r.scheme;
            authority = r.authority;
        } else {
            throw invalidArgType('headers', 'of type object or an instance of Array', headersParam);
        }
        assertIsObject(options, 'options');
        const opts: any = { ...options };
        if (opts.parent !== undefined) validateNumber(opts.parent, 'options.parent');
        if (opts.exclusive !== undefined) validateBoolean(opts.exclusive, 'options.exclusive');
        if (opts.endStream === undefined) {
            opts.endStream = kNoPayloadMethods.has(method);
        } else {
            validateBoolean(opts.endStream, 'options.endStream');
        }
        const stream = new ClientHttp2Stream(this, undefined, {});
        stream._kmlSentHeaders = headersObject;
        stream._kmlOrigin = String(scheme) + '://' + String(authority);
        if (opts.endStream) stream.end();
        if (opts.waitForTrailers) stream._kmlHasTrailers = true;
        const signal: any = opts.signal;
        if (signal) {
            const aborter = (): void => {
                const err: any = new Error('The operation was aborted');
                err.name = 'AbortError';
                err.code = 'ABORT_ERR';
                err.cause = signal.reason;
                stream.destroy(err);
            };
            if (signal.aborted) aborter();
            else {
                signal.addEventListener('abort', aborter, { once: true });
                stream.once('close', () => { signal.removeEventListener('abort', aborter); });
            }
        }
        if (requestError !== undefined) {
            const e = requestError;
            process.nextTick(() => { stream.destroy(e); });
        } else {
            const onConnect = (): void => { stream._kmlRequestOnConnect(headersList, opts); };
            if (this.connecting) {
                if (this._kmlPendingRequestCalls !== null) {
                    this._kmlPendingRequestCalls.push(onConnect);
                } else {
                    this._kmlPendingRequestCalls = [onConnect];
                    this.once('connect', () => {
                        const calls = this._kmlPendingRequestCalls ?? [];
                        this._kmlPendingRequestCalls = null;
                        for (const f of calls) f();
                    });
                }
            } else {
                onConnect();
            }
        }
        return stream;
    }
}

export interface OutgoingHttpHeaders {
    [header: string]: number | string | string[] | undefined;
}

export interface IncomingHttpHeaders {
    [header: string]: any;
}

function prepareRequestHeadersArray(headers: any[], session: Http2Session): any {
    let method: any;
    let scheme: any;
    let authority: any;
    let path: any;
    let protocol: any;
    for (let i = 0; i < headers.length; i += 2) {
        if (headers[i][0] !== ':') continue;
        const header = String(headers[i]).toLowerCase();
        const value = headers[i + 1];
        if (header === ':method') method = value;
        else if (header === ':scheme') scheme = value;
        else if (header === ':authority') authority = value;
        else if (header === ':path') path = value;
        else if (header === ':protocol') protocol = value;
    }
    const additional: any[] = [];
    if (method === undefined) {
        method = 'GET';
        additional.push(':method', method);
    }
    const connect = method === 'CONNECT';
    if (!connect || protocol !== undefined) {
        if (authority === undefined) {
            authority = session._kmlAuthority;
            additional.push(':authority', authority);
        }
        if (scheme === undefined) {
            scheme = session._kmlProtocol.slice(0, -1);
            additional.push(':scheme', scheme);
        }
        if (path === undefined) additional.push(':path', '/');
    } else {
        if (authority === undefined) throw new NodeError('ERR_HTTP2_CONNECT_AUTHORITY', ':authority header is required for CONNECT requests');
        if (scheme !== undefined) throw new NodeError('ERR_HTTP2_CONNECT_SCHEME', 'The :scheme header is forbidden for CONNECT requests');
        if (path !== undefined) throw new NodeError('ERR_HTTP2_CONNECT_PATH', 'The :path header is forbidden for CONNECT requests');
    }
    const rawHeaders = additional.length ? additional.concat(headers) : headers;
    const headersList = buildNgHeaderString(rawHeaders, assertValidPseudoHeader, session._kmlStrictSingleValueFields);
    return { headersList, scheme, authority, method };
}

function prepareRequestHeadersObject(headers: any, session: Http2Session): any {
    const headersObject: any = Object.assign({ __proto__: null }, headers);
    if (headers[sensitiveHeaders] !== undefined) headersObject[sensitiveHeaders] = headers[sensitiveHeaders];
    if (headersObject[':method'] === undefined) headersObject[':method'] = 'GET';
    const connect = headersObject[':method'] === 'CONNECT';
    if (!connect || headersObject[':protocol'] !== undefined) {
        if (getAuthority(headersObject) === undefined) headersObject[':authority'] = session._kmlAuthority;
        if (headersObject[':scheme'] === undefined) headersObject[':scheme'] = session._kmlProtocol.slice(0, -1);
        if (headersObject[':path'] === undefined) headersObject[':path'] = '/';
    } else {
        if (headersObject[':authority'] === undefined) throw new NodeError('ERR_HTTP2_CONNECT_AUTHORITY', ':authority header is required for CONNECT requests');
        if (headersObject[':scheme'] !== undefined) throw new NodeError('ERR_HTTP2_CONNECT_SCHEME', 'The :scheme header is forbidden for CONNECT requests');
        if (headersObject[':path'] !== undefined) throw new NodeError('ERR_HTTP2_CONNECT_PATH', 'The :path header is forbidden for CONNECT requests');
    }
    const headersList = buildNgHeaderString(headersObject, assertValidPseudoHeader, session._kmlStrictSingleValueFields);
    return {
        headersObject,
        headersList,
        scheme: headersObject[':scheme'],
        authority: getAuthority(headersObject),
        method: headersObject[':method'],
    };
}

// ---- streams ----

export interface StreamState {
    localWindowSize?: number;
    state?: number;
    localClose?: number;
    remoteClose?: number;
    sumDependencyWeight?: number;
    weight?: number;
}

export class Http2Stream extends Duplex {
    _kmlSession: any;
    _kmlId: number | undefined = undefined;
    _kmlClosed = false;
    _kmlAborted = false;
    _kmlHeadersSent = false;
    _kmlHeadRequest = false;
    _kmlHasTrailers = false;
    _kmlTrailersReady = false;
    _kmlDidRead = false;
    _kmlRstCode = NGHTTP2_NO_ERROR;
    _kmlEndAfterHeaders = false;
    _kmlShutdown = false;
    _kmlSentHeaders: any = undefined;
    _kmlSentTrailers: any = undefined;
    _kmlInfoHeaders: any[] | undefined = undefined;
    _kmlTimeoutMs = 0;
    _kmlTimeout: any = null;
    _kmlOrigin = '';
    _kmlRequest: any = undefined;
    _kmlResponse: any = undefined;

    constructor(session: Http2Session, options: DuplexOptions) {
        super({ ...options, allowHalfOpen: true, decodeStrings: false, autoDestroy: false });
        this._kmlSession = session;
        session._kmlPendingStreams.add(this);
    }

    // kInit: the stream's id, once nghttp2 assigned it.
    _kmlInit(id: number): void {
        const session = this._kmlSession;
        session._kmlPendingStreams.delete(this);
        session._kmlStreams.set(id, this);
        this._kmlId = id;
        this.emit('ready');
    }

    // Node's timeout.refresh(): the timer re-armed for its full duration.
    _kmlArmTimer(): void {
        const t = setTimeout(() => { this.emit('timeout'); }, this._kmlTimeoutMs);
        t.unref();
        this._kmlTimeout = t;
    }

    _kmlUpdateTimer(): void {
        if (this.destroyed) return;
        if (this._kmlTimeout !== null) {
            clearTimeout(this._kmlTimeout);
            this._kmlArmTimer();
        }
        if (this._kmlSession) this._kmlSession._kmlUpdateTimer();
    }

    get bufferSize(): number {
        return this.writableLength;
    }

    get endAfterHeaders(): boolean {
        return this._kmlEndAfterHeaders;
    }

    get sentHeaders(): OutgoingHttpHeaders | undefined {
        return this._kmlSentHeaders;
    }

    get sentTrailers(): OutgoingHttpHeaders | undefined {
        return this._kmlSentTrailers;
    }

    get sentInfoHeaders(): OutgoingHttpHeaders[] | undefined {
        return this._kmlInfoHeaders;
    }

    get pending(): boolean {
        return this._kmlId === undefined;
    }

    get id(): number | undefined {
        return this._kmlId;
    }

    get session(): Http2Session | undefined {
        return this._kmlSession;
    }

    get headersSent(): boolean {
        return this._kmlHeadersSent;
    }

    get aborted(): boolean {
        return this._kmlAborted;
    }

    get headRequest(): boolean {
        return this._kmlHeadRequest;
    }

    get rstCode(): number {
        return this._kmlRstCode;
    }

    get closed(): boolean {
        return this._kmlClosed;
    }

    get state(): StreamState {
        const id = this._kmlId;
        if (this.destroyed || id === undefined) return {};
        const h = this._kmlSession._kmlHandle;
        return {
            state: __kml_native.h2StreamState(h, id, 0),
            weight: __kml_native.h2StreamState(h, id, 1),
            sumDependencyWeight: __kml_native.h2StreamState(h, id, 2),
            localClose: __kml_native.h2StreamState(h, id, 3),
            remoteClose: __kml_native.h2StreamState(h, id, 4),
            localWindowSize: __kml_native.h2StreamState(h, id, 5),
        };
    }

    setTimeout(msecs: number, callback?: () => void): void {
        if (this.destroyed) return;
        if (this._kmlTimeout !== null) {
            clearTimeout(this._kmlTimeout);
            this._kmlTimeout = null;
        }
        if (callback !== undefined) {
            validateFunction(callback, 'callback');
            this.once('timeout', callback);
        }
        if (msecs > 0) {
            this._kmlTimeoutMs = msecs;
            this._kmlArmTimer();
        }
    }

    priority(options: any): void {
        if (this.destroyed) throw errInvalidStream();
    }

    // Before the first body byte a server stream sends its response headers.
    _kmlProceed(): void {
    }

    _write(chunk: any, encoding: BufferEncoding, cb: (error?: Error | null) => void): void {
        if (this.pending) {
            this.once('ready', () => { this._write(chunk, encoding, cb); });
            return;
        }
        if (this.destroyed) return;
        this._kmlUpdateTimer();
        if (!this.headersSent) this._kmlProceed();
        const data: Buffer = typeof chunk === 'string' ? Buffer.from(chunk, encoding) : chunk;
        const session = this._kmlSession;
        const rv = __kml_native.h2Write(session._kmlHandle, this._kmlId as number, data, 0, data.length, 0);
        session._kmlScheduleFlush();
        // Shut the writable side down right after the last chunk, so the final
        // DATA frame carries END_STREAM.
        process.nextTick(() => {
            if (!this._writableState!.ending || this._writableState!.buffered.length > 0 || this._kmlHasTrailers) return;
            this._kmlShutdownWritable();
        });
        if (rv < 0) {
            const err = new NghttpError(rv);
            this.destroy(err);
            cb(err);
            return;
        }
        cb();
    }

    _final(cb: (error?: Error | null) => void): void {
        if (this.pending) {
            this.once('ready', () => { this._final(cb); });
            return;
        }
        this._kmlShutdownWritable();
        cb();
    }

    // shutdownWritable
    _kmlShutdownWritable(): void {
        if (this._kmlShutdown) return;
        this._kmlShutdown = true;
        const session = this._kmlSession;
        if (session === undefined || session._kmlHandle <= 0 || this._kmlId === undefined) return;
        if (this.headersSent || this instanceof ClientHttp2Stream) {
            __kml_native.h2Write(session._kmlHandle, this._kmlId, Buffer.alloc(0), 0, 0, 1);
            session._kmlScheduleFlush();
        }
        this.once('finish', () => { this._kmlMaybeDestroy(); });
    }

    _read(size: number): void {
        if (this.destroyed) {
            this.push(null);
            return;
        }
        if (!this._kmlDidRead) this._kmlDidRead = true;
    }

    sendTrailers(headers: OutgoingHttpHeaders): void {
        if (this.destroyed || this.closed) throw errInvalidStream();
        if (this._kmlSentTrailers) throw new NodeError('ERR_HTTP2_TRAILERS_ALREADY_SENT', 'Trailing headers have already been sent');
        if (!this._kmlTrailersReady) {
            throw new NodeError('ERR_HTTP2_TRAILERS_NOT_READY', 'Trailing headers cannot be sent until after the wantTrailers event is emitted');
        }
        assertIsObject(headers, 'headers');
        const h: any = Object.assign({ __proto__: null }, headers);
        this._kmlUpdateTimer();
        const headersList = buildNgHeaderString(h, assertValidPseudoHeaderTrailer, this._kmlSession._kmlStrictSingleValueFields);
        this._kmlSentTrailers = h;
        setImmediate(() => {
            if (this.destroyed) return;
            this._kmlHasTrailers = false;
            const session = this._kmlSession;
            const ret = __kml_native.h2Headers(session._kmlHandle, this._kmlId as number, headersList, 1);
            session._kmlScheduleFlush();
            if (ret < 0) this.destroy(new NghttpError(ret));
            else this._kmlMaybeDestroy();
        });
    }

    _kmlOnWantTrailers(): void {
        this._kmlTrailersReady = true;
        if (this.destroyed || this.closed) return;
        if (!this.emit('wantTrailers')) {
            // No listener: no trailers.
            this.sendTrailers({});
        }
    }

    close(code?: number, callback?: () => void): void {
        const c = code ?? NGHTTP2_NO_ERROR;
        if (typeof c !== 'number' || c < 0 || c > kMaxInt || Math.floor(c) !== c) {
            throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "code" is out of range. It must be >= 0 && <= ' + kMaxInt + '. Received ' + String(c));
        }
        if (callback !== undefined) validateFunction(callback, 'callback');
        if (this.closed) return;
        if (callback !== undefined) this.once('close', callback);
        this._kmlCloseStream(c, 1);
    }

    // closeStream (rst: 0 none, 1 submit, 2 force)
    _kmlCloseStream(code: number, rst: number): void {
        this._kmlClosed = true;
        this._kmlRstCode = code;
        this.setTimeout(0);
        this.removeAllListeners('timeout');
        const ending = this._writableState!.ending;
        if (!ending) {
            if (!this.aborted) {
                this._kmlAborted = true;
                this.emit('aborted');
            }
            this.end();
        }
        if (rst !== 0) {
            const finish = (): void => {
                if (this.pending) {
                    this.push(null);
                    this.once('ready', () => { this._kmlSubmitRst(code); });
                    return;
                }
                this._kmlSubmitRst(code);
            };
            if (!ending || this.writableFinished || code !== NGHTTP2_NO_ERROR || rst === 2) finish();
            else this.once('finish', finish);
        }
    }

    private _kmlSubmitRst(code: number): void {
        const session = this._kmlSession;
        if (session === undefined || session._kmlHandle <= 0 || this._kmlId === undefined) return;
        __kml_native.h2Rst(session._kmlHandle, this._kmlId, code);
        session._kmlScheduleFlush();
    }

    // onStreamClose
    _kmlOnStreamClose(code: number): void {
        if (this.destroyed) return;
        if (!this.closed) this._kmlCloseStream(code, 0);
        if (!this.readable || code !== NGHTTP2_NO_ERROR) {
            this.destroy();
        } else {
            this.on('end', () => { this._kmlMaybeDestroy(); });
            this.push(null);
            if (this._kmlSession._kmlType === NGHTTP2_SESSION_SERVER && !this._kmlDidRead && this.readableFlowing === null) {
                this.resume();
            } else {
                this.read(0);
            }
        }
    }

    _destroy(err: Error | null, callback: (error?: Error | null) => void): void {
        const session = this._kmlSession;
        const id = this._kmlId;
        if (session === undefined) {
            callback(err);
            return;
        }
        const sessionCode = session._kmlGoawayCode || session._kmlDestroyCode;
        let code = this.closed ? this._kmlRstCode : sessionCode;
        let error: any = err;
        if (error != null) {
            if (sessionCode) code = sessionCode;
            else if (error.name === 'AbortError') code = NGHTTP2_CANCEL;
            else code = NGHTTP2_INTERNAL_ERROR;
        }
        const hasHandle = id !== undefined;
        if (!this.closed) this._kmlCloseStream(code, hasHandle ? 2 : 0);
        this.push(null);
        if (hasHandle) session._kmlStreams.delete(id);
        else session._kmlPendingStreams.delete(this);
        if (error == null && code !== NGHTTP2_NO_ERROR && code !== NGHTTP2_CANCEL) {
            error = new NodeError('ERR_HTTP2_STREAM_ERROR', 'Stream closed with error code ' + codeName(code));
        }
        this._kmlSession = undefined;
        setImmediate(() => { session._kmlMaybeDestroy(); });
        callback(error);
    }

    // kMaybeDestroy
    _kmlMaybeDestroy(code?: number): void {
        if (code !== undefined && code !== NGHTTP2_NO_ERROR) {
            this.destroy();
            return;
        }
        if (this.writableFinished) {
            if (!this.readable && this.closed) {
                this.destroy();
                return;
            }
            const session = this._kmlSession;
            if (this.headersSent && session && session._kmlType === NGHTTP2_SESSION_SERVER &&
                !this._kmlHasTrailers && !this._kmlDidRead && this.readableFlowing === null) {
                setImmediate(() => { this.close(); });
            }
        }
    }
}

export interface ServerStreamResponseOptions {
    endStream?: boolean;
    waitForTrailers?: boolean;
    sendDate?: boolean;
}

export class ServerHttp2Stream extends Http2Stream {
    _kmlProtocol: any;
    _kmlAuthority: any;

    constructor(session: Http2Session, id: number, options: DuplexOptions, headers: any) {
        super(session, options);
        this._kmlInit(id);
        this._kmlProtocol = headers[':scheme'];
        this._kmlAuthority = getAuthority(headers);
    }

    _kmlProceed(): void {
        this.respond();
    }

    get pushAllowed(): boolean {
        const session = this._kmlSession;
        return !this.destroyed && !this.closed && !session.closed && !session.destroyed &&
            session.remoteSettings.enablePush === true;
    }

    pushStream(headers: OutgoingHttpHeaders, callback: (err: Error | null, pushStream: ServerHttp2Stream, headers: OutgoingHttpHeaders) => void): void;
    pushStream(headers: OutgoingHttpHeaders, options: { exclusive?: boolean; parent?: number }, callback: (err: Error | null, pushStream: ServerHttp2Stream, headers: OutgoingHttpHeaders) => void): void;
    pushStream(headersIn: any, options: any, callback?: any): void {
        if (!this.pushAllowed) throw new NodeError('ERR_HTTP2_PUSH_DISABLED', 'HTTP/2 client has disabled push streams');
        if ((this._kmlId as number) % 2 === 0) throw new NodeError('ERR_HTTP2_NESTED_PUSH', 'A push stream cannot initiate another push stream.');
        const session = this._kmlSession;
        this._kmlUpdateTimer();
        if (typeof options === 'function') {
            callback = options;
            options = undefined;
        }
        validateFunction(callback, 'callback');
        assertIsObject(options, 'options');
        const opts: any = { ...options };
        opts.endStream = !!opts.endStream;
        assertIsObject(headersIn, 'headers');
        const headers: any = Object.assign({ __proto__: null }, headersIn);
        if (headers[':method'] === undefined) headers[':method'] = 'GET';
        if (getAuthority(headers) === undefined) headers[':authority'] = this._kmlAuthority;
        if (headers[':scheme'] === undefined) headers[':scheme'] = this._kmlProtocol;
        if (headers[':path'] === undefined) headers[':path'] = '/';
        let headRequest = false;
        if (headers[':method'] === 'HEAD') {
            headRequest = true;
            opts.endStream = true;
        }
        const headersList = buildNgHeaderString(headers, assertValidPseudoHeader, session._kmlStrictSingleValueFields);
        const ret = __kml_native.h2Push(session._kmlHandle, this._kmlId as number, headersList);
        if (ret < 0) {
            let err: Error;
            if (ret === NGHTTP2_ERR_STREAM_ID_NOT_AVAILABLE) err = new NodeError('ERR_HTTP2_OUT_OF_STREAMS', 'No stream ID is available because maximum stream ID has been reached');
            else if (ret === NGHTTP2_ERR_STREAM_CLOSED) err = errInvalidStream();
            else err = new NghttpError(ret);
            process.nextTick(() => { callback(err); });
            return;
        }
        const stream = new ServerHttp2Stream(session, ret, opts, headers);
        stream._kmlSentHeaders = headers;
        stream.push(null);
        if (opts.endStream) stream.end();
        if (headRequest) stream._kmlHeadRequest = true;
        session._kmlScheduleFlush();
        process.nextTick(() => { callback(null, stream, headers, 0); });
    }

    respond(headersParam?: OutgoingHttpHeaders | string[], options?: ServerStreamResponseOptions): void {
        if (this.destroyed || this.closed) throw errInvalidStream();
        if (this.headersSent) throw new NodeError('ERR_HTTP2_HEADERS_SENT', 'Response has already been initiated.');
        assertIsObject(options, 'options');
        const opts: any = { ...options };
        this._kmlUpdateTimer();
        opts.endStream = !!opts.endStream;
        if (opts.waitForTrailers) this._kmlHasTrailers = true;
        const prepared = prepareResponseHeaders(this, headersParam, opts);
        this._kmlHeadersSent = true;
        const statusCode = prepared.statusCode;
        if (opts.endStream || statusCode === 204 || statusCode === 205 || statusCode === 304 || this.headRequest === true) {
            opts.endStream = true;
            this.end();
        }
        const session = this._kmlSession;
        const ret = __kml_native.h2Respond(session._kmlHandle, this._kmlId as number, prepared.headersList,
            opts.endStream ? 1 : 0, opts.waitForTrailers ? 1 : 0);
        session._kmlScheduleFlush();
        if (ret < 0) this.destroy(new NghttpError(ret));
    }

    respondWithFD(fd: number, headers?: OutgoingHttpHeaders, options?: any): void {
        const data: Buffer = fs.readFileSync(fd);
        this.respond(headers);
        this.end(data);
    }

    respondWithFile(path: string, headers?: OutgoingHttpHeaders, options?: any): void {
        const data: Buffer = fs.readFileSync(path);
        this.respond(headers);
        this.end(data);
    }

    additionalHeaders(headersIn: OutgoingHttpHeaders): void {
        if (this.destroyed || this.closed) throw errInvalidStream();
        if (this.headersSent) throw new NodeError('ERR_HTTP2_HEADERS_AFTER_RESPOND', 'Cannot specify additional headers after response initiated');
        assertIsObject(headersIn, 'headers');
        const headers: any = Object.assign({ __proto__: null }, headersIn);
        if (headers[':status'] != null) {
            const statusCode = headers[':status'] | 0;
            headers[':status'] = statusCode;
            if (statusCode === 101) throw new NodeError('ERR_HTTP2_STATUS_101', 'HTTP status code 101 (Switching Protocols) is forbidden in HTTP/2');
            if (statusCode < 100 || statusCode >= 200) {
                throw new NodeRangeError('ERR_HTTP2_INVALID_INFO_STATUS', 'Invalid informational status code: ' + String(headersIn[':status']));
            }
        }
        this._kmlUpdateTimer();
        const headersList = buildNgHeaderString(headers, assertValidPseudoHeaderResponse, this._kmlSession._kmlStrictSingleValueFields);
        if (!this._kmlInfoHeaders) this._kmlInfoHeaders = [headers];
        else this._kmlInfoHeaders.push(headers);
        const session = this._kmlSession;
        const ret = __kml_native.h2Headers(session._kmlHandle, this._kmlId as number, headersList, 0);
        session._kmlScheduleFlush();
        if (ret < 0) this.destroy(new NghttpError(ret));
    }
}

function prepareResponseHeaders(stream: Http2Stream, headersParam: any, options: any): any {
    let headers: any;
    if (Array.isArray(headersParam)) {
        let statusCode = 0;
        let isDateSet = false;
        for (let i = 0; i < headersParam.length; i += 2) {
            const header = String(headersParam[i]).toLowerCase();
            if (header === ':status') statusCode = headersParam[i + 1] | 0;
            else if (header === 'date') isDateSet = true;
        }
        if (!statusCode) {
            statusCode = 200;
            headersParam.unshift(':status', statusCode);
        }
        if (!isDateSet && (options.sendDate == null || options.sendDate)) headersParam.push('date', utcDate());
        if (statusCode < 200 || statusCode > 599) {
            throw new NodeRangeError('ERR_HTTP2_STATUS_INVALID', 'Invalid status code: ' + statusCode);
        }
        const headersList = buildNgHeaderString(headersParam, assertValidPseudoHeaderResponse, stream._kmlSession._kmlStrictSingleValueFields);
        return { headersList, statusCode };
    }
    assertIsObject(headersParam, 'headers', 'Object or Array');
    headers = { __proto__: null };
    if (headersParam !== null && headersParam !== undefined) {
        for (const key of Object.keys(headersParam)) headers[key] = headersParam[key];
        if (headersParam[sensitiveHeaders] !== undefined) headers[sensitiveHeaders] = headersParam[sensitiveHeaders];
    }
    const statusCode = (headers[':status'] | 0) || 200;
    headers[':status'] = statusCode;
    if (options.sendDate == null || options.sendDate) {
        if (headers['date'] === undefined || headers['date'] === null) headers['date'] = utcDate();
    }
    if (statusCode < 200 || statusCode > 599) {
        throw new NodeRangeError('ERR_HTTP2_STATUS_INVALID', 'Invalid status code: ' + statusCode);
    }
    const neverIndex = headers[sensitiveHeaders];
    if (neverIndex !== undefined && !Array.isArray(neverIndex)) {
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The property 'headers[http2.neverIndex]' is invalid. Received " + String(neverIndex));
    }
    stream._kmlSentHeaders = headers;
    const headersList = buildNgHeaderString(headers, assertValidPseudoHeaderResponse, stream._kmlSession._kmlStrictSingleValueFields);
    return { headersList, statusCode };
}

export class ClientHttp2Stream extends Http2Stream {
    constructor(session: Http2Session, id: number | undefined, options: DuplexOptions) {
        super(session, options);
        this._kmlHeadersSent = true;
        if (id !== undefined) this._kmlInit(id);
        this.on('headers', (headers: any) => {
            if (headers[':status'] === 100) this.emit('continue');
        });
    }

    // requestOnConnect
    _kmlRequestOnConnect(headersList: string, options: any): void {
        const session = this._kmlSession;
        if (session === undefined || session.destroyed) return;
        if (session.closed) {
            this.destroy(new NodeError('ERR_HTTP2_GOAWAY_SESSION', 'New streams cannot be created after receiving a GOAWAY'));
            return;
        }
        const ret = __kml_native.h2Request(session._kmlHandle, headersList, options.endStream ? 1 : 0, options.waitForTrailers ? 1 : 0);
        if (ret < 0) {
            if (ret === NGHTTP2_ERR_STREAM_ID_NOT_AVAILABLE) {
                this.destroy(new NodeError('ERR_HTTP2_OUT_OF_STREAMS', 'No stream ID is available because maximum stream ID has been reached'));
            } else {
                session.destroy(new NghttpError(ret));
            }
            return;
        }
        this._kmlInit(ret);
        if (options.endStream) this._kmlShutdown = true;
        session._kmlScheduleFlush();
    }
}

// ---- the compatibility API (lib/internal/http2/compat.js) ----

let statusMessageWarned = false;

function statusMessageWarn(): void {
    if (!statusMessageWarned) {
        process.emitWarning('Status message is not supported by HTTP/2 (RFC7540 8.1.2.4)', 'UnsupportedWarning');
        statusMessageWarned = true;
    }
}

let statusConnectionHeaderWarned = false;

function connectionHeaderMessageWarn(): void {
    if (!statusConnectionHeaderWarned) {
        process.emitWarning('The provided connection header is not valid, the value will be dropped from the header and will never be in use.',
            'UnsupportedWarning');
        statusConnectionHeaderWarned = true;
    }
}

function assertValidHeader(name: string, value: any): void {
    if (name === '' || typeof name !== 'string' || name.indexOf(' ') >= 0) {
        throw new NodeTypeError('ERR_INVALID_HTTP_TOKEN', 'Header name must be a valid HTTP token ["' + name + '"]');
    }
    if (name === ':status' || name === ':method' || name === ':path' || name === ':authority' || name === ':scheme') {
        throw new NodeError('ERR_HTTP2_PSEUDOHEADER_NOT_ALLOWED', 'Cannot set HTTP/2 pseudo-headers');
    }
    if (value === undefined || value === null) {
        throw new NodeTypeError('ERR_HTTP2_INVALID_HEADER_VALUE', 'Invalid value "' + String(value) + '" for header "' + name + '"');
    }
    if (name === 'connection' && value !== 'trailers') connectionHeaderMessageWarn();
}

export class Http2ServerRequest extends Readable {
    _kmlStream: ServerHttp2Stream;
    _kmlHeaders: any;
    _kmlRawHeaders: string[];
    _kmlTrailers: any = {};
    _kmlRawTrailers: string[] = [];
    _kmlAbortedFlag = false;
    _kmlReqClosed = false;
    _kmlReqDidRead = false;

    constructor(stream: ServerHttp2Stream, headers: IncomingHttpHeaders, options: any, rawHeaders: string[]) {
        super({ autoDestroy: false, ...options });
        this._kmlHeaders = headers;
        this._kmlRawHeaders = rawHeaders;
        this._kmlStream = stream;
        stream._kmlRequest = this;
        stream.on('trailers', (trailers: any, flags: number, rawTrailers: string[]) => {
            Object.assign(this._kmlTrailers, trailers);
            for (const r of rawTrailers) this._kmlRawTrailers.push(r);
        });
        stream.on('end', () => { this.push(null); });
        stream.on('error', (error: Error) => {});
        stream.on('aborted', () => {
            if (!this._kmlReqClosed) {
                this._kmlAbortedFlag = true;
                this.emit('aborted');
            }
        });
        stream.on('close', () => { this._kmlOnStreamClose(); });
        stream.on('timeout', () => { this.emit('timeout'); });
        this.on('pause', () => { this._kmlStream.pause(); });
        this.on('resume', () => { this._kmlStream.resume(); });
    }

    // onStreamCloseRequest
    private _kmlOnStreamClose(): void {
        if (this._kmlStream._kmlRequest === undefined) return;
        this._kmlReqClosed = true;
        this.push(null);
        if (!this._kmlReqDidRead && !this._readableState!.resumeScheduled) this.resume();
        this._kmlStream._kmlRequest = undefined;
        this.emit('close');
    }

    get aborted(): boolean {
        return this._kmlAbortedFlag;
    }

    get complete(): boolean {
        return this._kmlAbortedFlag || this.readableEnded || this._kmlReqClosed || this._kmlStream.destroyed;
    }

    get stream(): ServerHttp2Stream {
        return this._kmlStream;
    }

    get headers(): IncomingHttpHeaders {
        return this._kmlHeaders;
    }

    get rawHeaders(): string[] {
        return this._kmlRawHeaders;
    }

    get trailers(): IncomingHttpHeaders {
        return this._kmlTrailers;
    }

    get rawTrailers(): string[] {
        return this._kmlRawTrailers;
    }

    get httpVersionMajor(): number {
        return 2;
    }

    get httpVersionMinor(): number {
        return 0;
    }

    get httpVersion(): string {
        return '2.0';
    }

    get socket(): any {
        const session = this._kmlStream.session;
        return session !== undefined ? session.socket : undefined;
    }

    get connection(): any {
        return this.socket;
    }

    _read(size: number): void {
        if (!this._kmlReqDidRead) {
            this._kmlReqDidRead = true;
            this._kmlStream.on('data', (chunk: Buffer) => {
                if (this._kmlStream._kmlRequest !== undefined && !this.push(chunk)) this._kmlStream.pause();
            });
        } else {
            process.nextTick(() => { this._kmlStream.resume(); });
        }
    }

    get method(): string {
        return this._kmlHeaders[':method'];
    }

    set method(method: string) {
        validateString(method, 'method');
        if (method.trim() === '') {
            throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'method' is invalid. Received '" + method + "'");
        }
        this._kmlHeaders[':method'] = method;
    }

    get authority(): string {
        return getAuthority(this._kmlHeaders);
    }

    get scheme(): string {
        return this._kmlHeaders[':scheme'];
    }

    get url(): string {
        return this._kmlHeaders[':path'];
    }

    set url(url: string) {
        this._kmlHeaders[':path'] = url;
    }

    setTimeout(msecs: number, callback?: () => void): this {
        if (!this._kmlReqClosed) this._kmlStream.setTimeout(msecs, callback);
        return this;
    }
}

export class Http2ServerResponse extends Stream {
    _kmlStream: ServerHttp2Stream;
    _kmlResHeaders: any = { __proto__: null };
    _kmlResTrailers: any = { __proto__: null };
    _kmlResClosed = false;
    _kmlEnding = false;
    _kmlResDestroyed = false;
    _kmlResHeadRequest = false;
    _kmlSendDate = true;
    _kmlStatusCode = 200;
    writable = true;
    req: Http2ServerRequest;

    constructor(stream: ServerHttp2Stream, options?: any) {
        super(options);
        this._kmlStream = stream;
        stream._kmlResponse = this;
        this.req = stream._kmlRequest;
        stream.on('drain', () => { this.emit('drain'); });
        stream.on('aborted', () => {});
        stream.on('close', () => { this._kmlOnStreamClose(); });
        stream.on('wantTrailers', () => { stream.sendTrailers(this._kmlResTrailers); });
        stream.on('timeout', () => { this.emit('timeout'); });
    }

    // onStreamCloseResponse
    _kmlOnStreamClose(): void {
        const stream = this._kmlStream;
        if (stream._kmlResponse === undefined) return;
        if (stream.headRequest !== this._kmlResHeadRequest) return;
        this._kmlResClosed = true;
        stream._kmlResponse = undefined;
        this.emit('finish');
        this.emit('close');
    }

    get _header(): boolean {
        return this.headersSent;
    }

    get writableEnded(): boolean {
        return this._kmlEnding;
    }

    get finished(): boolean {
        return this._kmlEnding;
    }

    get socket(): any {
        if (this._kmlResClosed) return undefined;
        const session = this._kmlStream.session;
        return session !== undefined ? session.socket : undefined;
    }

    get connection(): any {
        return this.socket;
    }

    get stream(): ServerHttp2Stream {
        return this._kmlStream;
    }

    get headersSent(): boolean {
        return this._kmlStream.headersSent;
    }

    get sendDate(): boolean {
        return this._kmlSendDate;
    }

    set sendDate(bool: boolean) {
        this._kmlSendDate = Boolean(bool);
    }

    get statusCode(): number {
        return this._kmlStatusCode;
    }

    set statusCode(codeIn: number) {
        const code = codeIn | 0;
        if (code >= 100 && code < 200) throw new NodeRangeError('ERR_HTTP2_INFO_STATUS_NOT_ALLOWED', 'Informational status codes cannot be used');
        if (code < 100 || code > 599) throw new NodeRangeError('ERR_HTTP2_STATUS_INVALID', 'Invalid status code: ' + code);
        this._kmlStatusCode = code;
    }

    get writableCorked(): number {
        return this._kmlStream.writableCorked;
    }

    get writableHighWaterMark(): number {
        return this._kmlStream.writableHighWaterMark;
    }

    get writableFinished(): boolean {
        return this._kmlStream.writableFinished;
    }

    get writableLength(): number {
        return this._kmlStream.writableLength;
    }

    get writableNeedDrain(): boolean {
        return this._kmlStream.writableNeedDrain;
    }

    setTrailer(nameIn: string, value: any): void {
        validateString(nameIn, 'name');
        const name = nameIn.trim().toLowerCase();
        assertValidHeader(name, value);
        this._kmlResTrailers[name] = value;
    }

    addTrailers(headers: OutgoingHttpHeaders): void {
        for (const key of Object.keys(headers)) this.setTrailer(key, headers[key]);
    }

    getHeader(name: string): any {
        validateString(name, 'name');
        return this._kmlResHeaders[name.trim().toLowerCase()];
    }

    getHeaderNames(): string[] {
        return Object.keys(this._kmlResHeaders);
    }

    getHeaders(): OutgoingHttpHeaders {
        const headers: any = { __proto__: null };
        return Object.assign(headers, this._kmlResHeaders);
    }

    hasHeader(name: string): boolean {
        validateString(name, 'name');
        return Object.hasOwn(this._kmlResHeaders, name.trim().toLowerCase());
    }

    removeHeader(nameIn: string): void {
        validateString(nameIn, 'name');
        if (this._kmlStream.headersSent) throw new NodeError('ERR_HTTP2_HEADERS_SENT', 'Response has already been initiated.');
        const name = nameIn.trim().toLowerCase();
        if (name === 'date') {
            this._kmlSendDate = false;
            return;
        }
        delete this._kmlResHeaders[name];
    }

    setHeader(name: string, value: number | string | readonly string[]): this {
        validateString(name, 'name');
        if (this._kmlStream.headersSent) throw new NodeError('ERR_HTTP2_HEADERS_SENT', 'Response has already been initiated.');
        this._kmlSetHeader(name, value);
        return this;
    }

    private _kmlSetHeader(nameIn: string, value: any): void {
        const name = nameIn.trim().toLowerCase();
        assertValidHeader(name, value);
        if (name === 'connection' && value !== 'trailers') return;
        if (name[0] === ':') assertValidPseudoHeader(name);
        else if (!checkIsHttpToken(name)) this.destroy(new NodeTypeError('ERR_INVALID_HTTP_TOKEN', 'Header name must be a valid HTTP token ["' + name + '"]'));
        this._kmlResHeaders[name] = value;
    }

    appendHeader(name: string, value: string | string[]): this {
        validateString(name, 'name');
        if (this._kmlStream.headersSent) throw new NodeError('ERR_HTTP2_HEADERS_SENT', 'Response has already been initiated.');
        this._kmlAppendHeader(name, value);
        return this;
    }

    private _kmlAppendHeader(nameIn: string, value: any): void {
        const name = nameIn.trim().toLowerCase();
        assertValidHeader(name, value);
        if (name === 'connection' && value !== 'trailers') return;
        if (name[0] === ':') assertValidPseudoHeader(name);
        else if (!checkIsHttpToken(name)) this.destroy(new NodeTypeError('ERR_INVALID_HTTP_TOKEN', 'Header name must be a valid HTTP token ["' + name + '"]'));
        const headers = this._kmlResHeaders;
        if (!headers[name]) {
            this.setHeader(name, value);
            return;
        }
        if (!Array.isArray(headers[name])) headers[name] = [headers[name]];
        const existing: any[] = headers[name];
        if (Array.isArray(value)) {
            for (const v of value) existing.push(v);
        } else {
            existing.push(value);
        }
    }

    get statusMessage(): string {
        statusMessageWarn();
        return '';
    }

    set statusMessage(msg: string) {
        statusMessageWarn();
    }

    flushHeaders(): void {
        if (!this._kmlResClosed && !this._kmlStream.headersSent) this.writeHead(this._kmlStatusCode);
    }

    writeHead(statusCode: number, headers?: OutgoingHttpHeaders | string[]): this;
    writeHead(statusCode: number, statusMessage: string, headers?: OutgoingHttpHeaders | string[]): this;
    writeHead(statusCode: number, statusMessage?: any, headersIn?: any): this {
        if (this._kmlResClosed || this.stream.destroyed || this.stream.closed) return this;
        if (this._kmlStream.headersSent) throw new NodeError('ERR_HTTP2_HEADERS_SENT', 'Response has already been initiated.');
        if (typeof statusMessage === 'string') statusMessageWarn();
        let headers: any = headersIn;
        if (headers === undefined && typeof statusMessage === 'object') headers = statusMessage;
        if (Array.isArray(headers)) {
            if (headers.length && Array.isArray(headers[0])) {
                for (const h of headers) this.removeHeader(h[0]);
                for (const h of headers) this._kmlAppendHeader(h[0], h[1]);
            } else {
                if (headers.length % 2 !== 0) {
                    throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'headers' is invalid. Received " + String(headers));
                }
                for (let n = 0; n < headers.length; n += 2) this.removeHeader(headers[n]);
                for (let i = 0; i < headers.length; i += 2) this._kmlAppendHeader(headers[i], headers[i + 1]);
            }
        } else if (typeof headers === 'object' && headers !== null) {
            for (const key of Object.keys(headers)) this._kmlSetHeader(key, headers[key]);
        }
        this._kmlStatusCode = statusCode;
        this._kmlBeginSend();
        return this;
    }

    cork(): void {
        this._kmlStream.cork();
    }

    uncork(): void {
        this._kmlStream.uncork();
    }

    write(chunk: string | Uint8Array, callback?: (err?: Error) => void): boolean;
    write(chunk: string | Uint8Array, encoding: BufferEncoding, callback?: (err?: Error) => void): boolean;
    write(chunk: any, encoding?: any, cb?: any): boolean {
        if (typeof encoding === 'function') {
            cb = encoding;
            encoding = 'utf8';
        }
        let err: Error | undefined = undefined;
        if (this._kmlEnding) err = new NodeError('ERR_STREAM_WRITE_AFTER_END', 'write after end');
        else if (this._kmlResClosed) err = errInvalidStream();
        else if (this._kmlResDestroyed) return false;
        if (err !== undefined) {
            const e = err;
            if (typeof cb === 'function') process.nextTick(() => { cb(e); });
            this.destroy(e);
            return false;
        }
        const stream = this._kmlStream;
        if (!stream.headersSent) this.writeHead(this._kmlStatusCode);
        return stream.write(chunk, encoding, cb);
    }

    end(cb?: () => void): this;
    end(chunk: string | Uint8Array, cb?: () => void): this;
    end(chunk: string | Uint8Array, encoding: BufferEncoding, cb?: () => void): this;
    end(chunk?: any, encoding?: any, cb?: any): this {
        const stream = this._kmlStream;
        if (typeof chunk === 'function') {
            cb = chunk;
            chunk = null;
        } else if (typeof encoding === 'function') {
            cb = encoding;
            encoding = 'utf8';
        }
        if ((this._kmlResClosed || this._kmlEnding) && this._kmlResHeadRequest === stream.headRequest) {
            if (typeof cb === 'function') process.nextTick(cb);
            return this;
        }
        if (chunk !== null && chunk !== undefined) this.write(chunk, encoding);
        this._kmlResHeadRequest = stream.headRequest;
        this._kmlEnding = true;
        if (typeof cb === 'function') {
            if (stream.writableEnded) this.once('finish', cb);
            else stream.once('finish', cb);
        }
        if (!stream.headersSent) this.writeHead(this._kmlStatusCode);
        if (this._kmlResClosed || stream.destroyed) this._kmlOnStreamClose();
        else stream.end();
        return this;
    }

    destroy(err?: Error): this {
        if (this._kmlResDestroyed) return this;
        this._kmlResDestroyed = true;
        this._kmlStream.destroy(err);
        return this;
    }

    setTimeout(msecs: number, callback?: () => void): void {
        if (this._kmlResClosed) return;
        this._kmlStream.setTimeout(msecs, callback);
    }

    createPushResponse(headers: OutgoingHttpHeaders, callback: (err: Error | null, res: Http2ServerResponse) => void): void {
        validateFunction(callback, 'callback');
        if (this._kmlResClosed) {
            process.nextTick(() => { callback(errInvalidStream(), undefined as any); });
            return;
        }
        this._kmlStream.pushStream(headers, {}, (err: Error | null, stream: ServerHttp2Stream) => {
            if (err) {
                callback(err, undefined as any);
                return;
            }
            callback(null, new Http2ServerResponse(stream));
        });
    }

    // kBeginSend
    private _kmlBeginSend(): void {
        const headers = this._kmlResHeaders;
        headers[':status'] = this._kmlStatusCode;
        this._kmlStream.respond(headers, { endStream: this._kmlEnding, waitForTrailers: true, sendDate: this._kmlSendDate });
    }

    writeInformation(statusCode: number, headers?: any): boolean {
        if (typeof statusCode !== 'number' || statusCode < 100 || statusCode > 199 || statusCode === 101) {
            throw new NodeRangeError('ERR_HTTP2_STATUS_INVALID', 'Invalid status code: ' + String(statusCode));
        }
        const stream = this._kmlStream;
        if (stream.headersSent || this._kmlResClosed) return false;
        const outHeaders: any = { __proto__: null };
        if (headers !== undefined && headers !== null) {
            for (const key of Object.keys(headers)) outHeaders[key] = headers[key];
        }
        outHeaders[':status'] = statusCode;
        stream.additionalHeaders(outHeaders);
        return true;
    }

    writeContinue(): boolean {
        return this.writeInformation(100);
    }

    writeEarlyHints(hints: Record<string, string | string[]>): boolean {
        const headers: any = { __proto__: null };
        const link = hints.link;
        const linkValue = Array.isArray(link) ? link.join(', ') : (link ?? '');
        for (const key of Object.keys(hints)) {
            if (key !== 'link') {
                const name = key.trim().toLowerCase();
                assertValidHeader(name, hints[key]);
                headers[name] = hints[key];
            }
        }
        if (linkValue.length === 0) return false;
        headers.Link = linkValue;
        return this.writeInformation(103, headers);
    }
}

// onServerStream: the 'request' event for each stream.
function onServerStream(server: any, stream: ServerHttp2Stream, headers: any, flags: number, rawHeaders: string[]): void {
    const request = new Http2ServerRequest(stream, headers, undefined, rawHeaders);
    const response = new Http2ServerResponse(stream);
    const method = headers[':method'];
    if (method === 'CONNECT') {
        if (!server.emit('connect', request, response)) {
            response.statusCode = 405;
            response.end();
        }
        return;
    }
    if (headers.expect !== undefined) {
        if (headers.expect === '100-continue') {
            if (server.listenerCount('checkContinue')) {
                server.emit('checkContinue', request, response);
            } else {
                response.writeContinue();
                server.emit('request', request, response);
            }
        } else if (server.listenerCount('checkExpectation')) {
            server.emit('checkExpectation', request, response);
        } else {
            response.statusCode = 417;
            response.end();
        }
        return;
    }
    server.emit('request', request, response);
}

// ---- servers ----

function initializeOptions(options: any): any {
    assertIsObject(options, 'options');
    const opts: any = { ...options };
    assertIsObject(opts.settings, 'options.settings');
    opts.settings = { ...opts.settings };
    if (opts.unknownProtocolTimeout === undefined) opts.unknownProtocolTimeout = 10000;
    if (opts.strictSingleValueFields !== undefined) validateBoolean(opts.strictSingleValueFields, 'options.strictSingleValueFields');
    else opts.strictSingleValueFields = true;
    return opts;
}

// connectionListener
function connectionListener(server: any, socket: any): void {
    const options = server._kmlOptions || {};
    const alpn: any = socket instanceof tls.TLSSocket ? (socket as tls.TLSSocket).alpnProtocol : undefined;
    if (alpn === false || alpn === 'http/1.1') {
        if (options.allowHTTP1 === true) {
            server._kmlHttpCore.connectionListener(socket);
            return;
        }
        if (!server.emit('unknownProtocol', socket)) {
            const timer = setTimeout(() => {
                if (!socket.destroyed) socket.destroy();
            }, options.unknownProtocolTimeout);
            timer.unref();
            socket.once('close', () => { clearTimeout(timer); });
            socket.end('HTTP/1.0 403 Forbidden\r\n' +
                'Content-Type: text/plain\r\n\r\n' +
                'Missing ALPN Protocol, expected `h2` to be available.\n' +
                'If this is a HTTP request: The server was not ' +
                'configured with the `allowHTTP1` option or a ' +
                'listener for the `unknownProtocol` event.\n');
        }
        return;
    }
    const session = new ServerHttp2Session(options, socket, server);
    session.on('stream', (stream: any, headers: any, flags: number, rawHeaders: string[]) => {
        server.emit('stream', stream, headers, flags, rawHeaders);
    });
    session.on('error', (error: Error) => { server.emit('sessionError', error, session); });
    if (server.timeout) {
        session.setTimeout(server.timeout, () => {
            if (session.destroyed || session.closed) return;
            if (!server.emit('timeout', session)) session.destroy();
        });
    }
    server.emit('session', session);
}

// setupCompat: a 'request' listener turns on the compatibility API.
function setupCompat(server: any): void {
    const onNew = (ev: any): void => {
        if (ev === 'request') {
            server.removeListener('newListener', onNew);
            server.on('stream', (stream: any, headers: any, flags: number, rawHeaders: string[]) => {
                onServerStream(server, stream, headers, flags, rawHeaders);
            });
        }
    };
    server.on('newListener', onNew);
}

// kml:callable Http2Server
export class Http2Server extends net.Server {
    _kmlOptions: any;
    _kmlSessions = new Set<Http2Session>();
    timeout = 0;

    constructor(options: ServerOptions, requestListener?: (request: Http2ServerRequest, response: Http2ServerResponse) => void) {
        const opts = initializeOptions(options);
        super(opts);
        this._kmlOptions = opts;
        this.on('connection', (socket: net.Socket) => { connectionListener(this, socket); });
        setupCompat(this);
        if (typeof requestListener === 'function') this.on('request', requestListener);
    }

    setTimeout(msecs?: number, callback?: () => void): this {
        this.timeout = msecs ?? 0;
        if (callback !== undefined) {
            validateFunction(callback, 'callback');
            this.on('timeout', callback);
        }
        return this;
    }

    updateSettings(settings: Settings): void {
        assertIsObject(settings, 'settings');
        validateSettings(settings);
        this._kmlOptions.settings = { ...this._kmlOptions.settings, ...settings };
    }

    close(callback?: (err?: Error) => void): this {
        super.close(callback);
        for (const session of Array.from(this._kmlSessions)) session.close();
        return this;
    }
}

// kml:callable Http2SecureServer
export class Http2SecureServer extends tls.Server {
    _kmlOptions: any;
    _kmlSessions = new Set<Http2Session>();
    _kmlHttpCore: any = null;
    timeout = 0;

    constructor(options: SecureServerOptions, requestListener?: (request: Http2ServerRequest, response: Http2ServerResponse) => void) {
        const opts = initializeOptions(options);
        opts.ALPNProtocols = ['h2'];
        if (opts.allowHTTP1 === true) opts.ALPNProtocols.push('http/1.1');
        super(opts);
        this._kmlOptions = opts;
        this.on('secureConnection', (socket: tls.TLSSocket) => { connectionListener(this, socket); });
        setupCompat(this);
        if (opts.allowHTTP1 === true) {
            const core = new _kmlHttpServerCore(this, { ...opts, ...opts.http1Options });
            this._kmlHttpCore = core;
            this.on('listening', () => { core.setupConnectionsTracking(); });
        }
        if (typeof requestListener === 'function') this.on('request', requestListener);
        this.on('tlsClientError', (err: Error, socket: any) => {
            if (!this.emit('clientError', err, socket)) socket.destroy(err);
        });
    }

    setTimeout(msecs?: number, callback?: () => void): this {
        this.timeout = msecs ?? 0;
        if (callback !== undefined) {
            validateFunction(callback, 'callback');
            this.on('timeout', callback);
        }
        return this;
    }

    updateSettings(settings: Settings): void {
        assertIsObject(settings, 'settings');
        validateSettings(settings);
        this._kmlOptions.settings = { ...this._kmlOptions.settings, ...settings };
    }

    close(callback?: (err?: Error) => void): this {
        super.close(callback);
        if (this._kmlHttpCore !== null) {
            // httpServerPreClose: the HTTP/1.1 side's idle connections.
            this._kmlHttpCore.stopConnectionsTracking();
            this._kmlHttpCore.closeIdleConnections();
        }
        for (const session of Array.from(this._kmlSessions)) session.close();
        return this;
    }

    closeIdleConnections(): void {
        if (this._kmlHttpCore !== null) this._kmlHttpCore.closeIdleConnections();
    }
}

export function createServer(onRequestHandler?: (request: Http2ServerRequest, response: Http2ServerResponse) => void): Http2Server;
export function createServer(options: ServerOptions, onRequestHandler?: (request: Http2ServerRequest, response: Http2ServerResponse) => void): Http2Server;
export function createServer(options?: any, handler?: any): Http2Server {
    if (typeof options === 'function') {
        handler = options;
        options = {};
    }
    return new Http2Server(options ?? {}, handler);
}

export function createSecureServer(onRequestHandler?: (request: Http2ServerRequest, response: Http2ServerResponse) => void): Http2SecureServer;
export function createSecureServer(options: SecureServerOptions, onRequestHandler?: (request: Http2ServerRequest, response: Http2ServerResponse) => void): Http2SecureServer;
export function createSecureServer(options?: any, handler?: any): Http2SecureServer {
    if (typeof options === 'function') {
        handler = options;
        options = {};
    }
    return new Http2SecureServer(options ?? {}, handler);
}

export function connect(authority: string | URL, listener?: (session: ClientHttp2Session, socket: any) => void): ClientHttp2Session;
export function connect(authority: string | URL, options?: ClientSessionOptions | SecureClientSessionOptions, listener?: (session: ClientHttp2Session, socket: any) => void): ClientHttp2Session;
export function connect(authorityIn: any, options?: any, listener?: any): ClientHttp2Session {
    if (typeof options === 'function') {
        listener = options;
        options = undefined;
    }
    assertIsObject(options, 'options');
    const opts: any = { ...options };
    if (opts.strictSingleValueFields !== undefined) validateBoolean(opts.strictSingleValueFields, 'options.strictSingleValueFields');
    else opts.strictSingleValueFields = true;
    const authority: any = typeof authorityIn === 'string' ? new URL(authorityIn) : authorityIn;
    assertIsObject(authority, 'authority', 'string, Object, or URL');
    const protocol: string = authority.protocol || opts.protocol || 'https:';
    const port = '' + (authority.port !== '' && authority.port !== undefined ? authority.port : (authority.protocol === 'http:' ? 80 : 443));
    let host = 'localhost';
    if (authority.hostname) {
        host = authority.hostname;
        if (host[0] === '[') host = host.slice(1, -1);
    } else if (authority.host) {
        host = authority.host;
    }
    let socket: any;
    if (typeof opts.createConnection === 'function') {
        socket = opts.createConnection(authority, opts);
    } else if (protocol === 'http:') {
        socket = net.connect({ ...opts, port: Number(port), host: host });
    } else if (protocol === 'https:') {
        const tlsOpts: any = { ...opts, port: Number(port), host: host, ALPNProtocols: ['h2'] };
        if (net.isIP(host) === 0 && !tlsOpts.servername) tlsOpts.servername = host;
        socket = tls.connect(tlsOpts);
    } else {
        throw new NodeError('ERR_HTTP2_UNSUPPORTED_PROTOCOL', 'protocol "' + protocol + '" is unsupported.');
    }
    const session = new ClientHttp2Session(opts, socket);
    session._kmlAuthority = (opts.servername || host) + ':' + port;
    session._kmlProtocol = protocol;
    if (typeof listener === 'function') session.once('connect', listener);
    return session;
}

export function performServerHandshake(socket: any, options?: ServerOptions): ServerHttp2Session {
    return new ServerHttp2Session(initializeOptions(options ?? {}), socket, undefined);
}
