// The native primitives this compiler's builtin modules written in TypeScript
// (lib/node/*.ts) call: each method's `@lower` names its runtime entry point.
// An asynchronous one runs on the thread pool and calls its callback on the
// loop thread with (errno or 0, result), then runs the tick queue and the
// promise jobs, as Node's MakeCallback does. Only a builtin module may name
// `__kml_native`.

interface KmlNative {
    /** @lower __kml_native_fs_open @link pool */
    fsOpen(path: string, flags: number, mode: number, callback: (errno: number, fd: number) => void): void;
    /** @lower __kml_native_fs_close @link pool */
    fsClose(fd: number, callback: (errno: number, result: number) => void): void;
    /** @lower __kml_native_fs_fsync @link pool */
    fsFsync(fd: number, callback: (errno: number, result: number) => void): void;
    /** @lower __kml_native_fs_read @link pool */
    fsRead(fd: number, buffer: Uint8Array, offset: number, length: number, position: number, callback: (errno: number, bytesRead: number) => void): void;
    /** @lower __kml_native_fs_write @link pool */
    fsWrite(fd: number, buffer: Uint8Array, offset: number, length: number, position: number, callback: (errno: number, bytesWritten: number) => void): void;
    /** @lower __kml_native_fs_flags @link pool */
    fsFlags(flags: string): number;
    /** @lower __kml_native_fs_error */
    fsError(errno: number, syscall: string, path?: string): Error;

    // The string result of the callback being run (a resolved address), or
    // the address the last tcpAddress read.
    /** @lower __kml_native_last_string @link pool */
    lastString(): string;
    /** @lower __kml_native_dns_lookup @link pool */
    dnsLookup(host: string, family: number, callback: (status: number, family: number) => void): void;
    /** @lower __kml_native_eai_code @link pool */
    eaiCode(code: number): string;
    /** @lower __kml_native_errno_name */
    errnoName(errno: number): string;
    /** @lower __kml_native_errno_desc */
    errnoDesc(errno: number): string;
    /** @lower __kml_native_uv_errno */
    uvErrno(errno: number): number;

    // TCP handles: an id, or -errno.
    /** @lower __kml_native_tcp_listen @link pool */
    tcpListen(host: string, port: number, backlog: number, onConnection: (status: number, handle: number) => void): number;
    /** @lower __kml_native_tcp_connect @link pool */
    tcpConnect(ip: string, port: number, onConnect: (status: number, unused: number) => void): number;
    /** @lower __kml_native_pipe_listen @link pool */
    pipeListen(path: string, backlog: number, onConnection: (status: number, handle: number) => void): number;
    /** @lower __kml_native_pipe_connect @link pool */
    pipeConnect(path: string, onConnect: (status: number, unused: number) => void): number;
    // The HTTP/1 parser (0 request, 1 response): execute() returns its next
    // event (see klainhttp.c) and the getters read the message.
    /** @lower __kml_native_http_parser_new @link pool */
    httpParserNew(type: number, maxHeaderSize: number): number;
    /** @lower __kml_native_http_parser_init @link pool */
    httpParserInit(parser: number, type: number, maxHeaderSize: number): void;
    /** @lower __kml_native_http_parser_execute @link pool */
    httpParserExecute(parser: number, data: Uint8Array, offset: number, length: number): number;
    /** @lower __kml_native_http_parser_finish @link pool */
    httpParserFinish(parser: number): number;
    /** @lower __kml_native_http_parser_info @link pool */
    httpParserInfo(parser: number, which: number): number;
    /** @lower __kml_native_http_parser_set @link pool */
    httpParserSet(parser: number, flag: number): void;
    /** @lower __kml_native_http_parser_string @link pool */
    httpParserString(parser: number, which: number): string;
    /** @lower __kml_native_http_parser_header @link pool */
    httpParserHeader(parser: number, index: number): string;
    // TLS on a TCP handle: a secure context (an id, -1 with tlsLastError),
    // a session on the handle whose handshake reports onSecure(0 | 1, 0),
    // and its details (see tls.c).
    /** @lower __kml_native_tls_context @link tls */
    tlsContext(server: boolean, cert: string, key: string, ca: string, alpn: string): number;
    /** @lower __kml_native_tls_last_error @link tls */
    tlsLastError(): string;
    /** @lower __kml_native_tls_start @link tls */
    tlsStart(handle: number, context: number, server: boolean, servername: string, onSecure: (status: number, unused: number) => void): number;
    /** @lower __kml_native_tls_info @link tls */
    tlsInfo(handle: number, which: number): number;
    /** @lower __kml_native_tcp_read_start @link pool */
    tcpReadStart(handle: number, onRead: (status: number, nread: number) => void): void;
    /** @lower __kml_native_tcp_read_stop @link pool */
    tcpReadStop(handle: number): void;
    /** @lower __kml_native_tcp_take @link pool */
    tcpTake(handle: number, buffer: Uint8Array): number;
    /** @lower __kml_native_tcp_write @link pool */
    tcpWrite(handle: number, buffer: Uint8Array, offset: number, length: number, onWritten: (status: number, unused: number) => void): number;
    /** @lower __kml_native_tcp_shutdown @link pool */
    tcpShutdown(handle: number, onShutdown: (status: number, unused: number) => void): void;
    /** @lower __kml_native_tcp_close @link pool */
    tcpClose(handle: number, onClose: (status: number, unused: number) => void): void;
    /** @lower __kml_native_tcp_set_no_delay @link pool */
    tcpSetNoDelay(handle: number, on: boolean): void;
    /** @lower __kml_native_tcp_set_keep_alive @link pool */
    tcpSetKeepAlive(handle: number, on: boolean, delaySecs: number): void;
    /** @lower __kml_native_tcp_ref @link pool */
    tcpRef(handle: number, on: boolean): void;
    /** @lower __kml_native_tcp_address @link pool */
    tcpAddress(handle: number, peer: boolean): number;
}

declare var __kml_native: KmlNative;
