// The native primitives this compiler's builtin modules written in TypeScript
// (lib/node/*.ts) call: each method's `@lower` names its runtime entry point.
// An asynchronous one runs on the thread pool and calls its callback on the
// loop thread with (errno or 0, result), then runs the tick queue and the
// promise jobs, as Node's MakeCallback does. Only a builtin module may name
// `__kml_native`.

interface KmlNative {
    // One libuv fs request (klainfs.c): the op (lib/node/fs.ts's FsOp), its
    // paths, its numbers and its byte region. The synchronous form returns
    // the result or the negated errno; the other calls back with (errno or
    // 0, result). A string result is lastString().
    /** @lower __kml_native_fs_call @link fs */
    fsCall(op: number, path: string, dest: string, a: number, b: number, c: number, d: number, data: Uint8Array): number;
    /** @lower __kml_native_fs_call_async @link fs */
    fsCallAsync(op: number, path: string, dest: string, a: number, b: number, c: number, d: number, data: Uint8Array, callback: (errno: number, result: number) => void): void;
    // A file watcher (inotify, kqueue, ReadDirectoryChangesW): each event
    // calls back with (1 for a rename, else 0; 0), the file name as
    // lastString(). The result is the watcher's handle.
    /** @lower __kml_native_fs_watch */
    fsWatch(path: string, onEvent: (isRename: number, unused: number) => void): number;
    /** @lower __kml_native_fs_watch_close */
    fsWatchClose(handle: number): void;
    /** @lower __kml_native_fs_flags @link pool */
    fsFlags(flags: string): number;
    /** @lower __kml_native_fs_error */
    fsError(errno: number, syscall: string, path?: string, dest?: string): Error;

    // The string result of the callback being run (a resolved address), or
    // the address the last tcpAddress read.
    /** @lower __kml_native_last_string @link pool */
    lastString(): string;
    /** @lower __kml_native_dns_lookup @link pool */
    dnsLookup(host: string, family: number, callback: (status: number, family: number) => void): void;
    // dns (klaindns.c): each completes with (status, 0) and leaves its
    // result, JSON, as lastString(). getaddrinfo/getnameinfo's status is
    // libuv's (0, UV_EAI_*, -errno); a query's is a c-ares status.
    /** @lower __kml_native_dns_getaddrinfo @link pool */
    dnsGetaddrinfo(host: string, family: number, flags: number, order: number, callback: (status: number, unused: number) => void): void;
    /** @lower __kml_native_dns_getnameinfo @link pool */
    dnsGetnameinfo(address: string, port: number, callback: (status: number, unused: number) => void): void;
    // The host's getaddrinfo flags: 0 AI_ADDRCONFIG, 1 AI_ALL, 2 AI_V4MAPPED.
    /** @lower __kml_native_dns_ai_flag @link pool */
    dnsAiFlag(which: number): number;
    // A resolver's channel: its servers and retry policy.
    /** @lower __kml_native_dns_channel_new @link pool */
    dnsChannelNew(timeout: number, tries: number, maxTimeout: number): number;
    /** @lower __kml_native_dns_channel_servers @link pool */
    dnsChannelServers(channel: number): string;
    // spec: "address port" lines; 0, or a c-ares status.
    /** @lower __kml_native_dns_channel_set_servers @link pool */
    dnsChannelSetServers(channel: number, spec: string): number;
    // setLocalAddress: 0, or 1 an invalid address, 2 two IPv4, 3 two IPv6.
    /** @lower __kml_native_dns_channel_set_local @link pool */
    dnsChannelSetLocal(channel: number, first: string, second: string): number;
    /** @lower __kml_native_dns_channel_cancel @link pool */
    dnsChannelCancel(channel: number): void;
    // A query of the record type; the answer: {"rcode":n,"an":[records]}.
    /** @lower __kml_native_dns_query @link pool */
    dnsQuery(channel: number, type: number, name: string, callback: (status: number, unused: number) => void): void;
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

    // Datagram sockets (Node's udp_wrap) in the TCP handle table: read with
    // tcpReadStart/tcpTake (one datagram per onRead, its sender from
    // udpSender: port + 65536 * family, address by lastString), closed with
    // tcpClose. Options: 0 broadcast, 1 TTL, 2 multicast TTL, 3 multicast
    // loopback, 4 receive buffer, 5 send buffer (a value < 0 reads it).
    /** @lower __kml_native_udp_socket @link pool */
    udpSocket(family: number): number;
    /** @lower __kml_native_udp_bind @link pool */
    udpBind(handle: number, ip: string, port: number, flags: number): number;
    /** @lower __kml_native_udp_connect @link pool */
    udpConnect(handle: number, ip: string, port: number): number;
    /** @lower __kml_native_udp_send @link pool */
    udpSend(handle: number, buffer: Uint8Array, offset: number, length: number, ip: string, port: number): number;
    /** @lower __kml_native_udp_sender @link pool */
    udpSender(handle: number): number;
    /** @lower __kml_native_udp_option @link pool */
    udpOption(handle: number, which: number, value: number): number;
    /** @lower __kml_native_udp_membership @link pool */
    udpMembership(handle: number, group: string, iface: string, add: boolean): number;
    /** @lower __kml_native_udp_multicast_interface @link pool */
    udpMulticastInterface(handle: number, iface: string): number;

    // Child processes (Node's process_wrap): argv and env are NUL-terminated
    // strings back to back (an empty env inherits ours); stdio is one entry
    // per descriptor, comma-separated ("p" pipe, "c" IPC channel, "i"
    // ignored, or an fd). A process handle, or -errno.
    /** @lower __kml_native_process_spawn @link pool */
    processSpawn(file: string, argv: Uint8Array, env: Uint8Array, cwd: string, flags: number, stdio: string, uid: number, gid: number, onExit: (exitCode: number, signal: number) => void): number;
    // The stream handle of the last spawn's descriptor i, or -1.
    /** @lower __kml_native_process_stdio @link pool */
    processStdio(i: number): number;
    /** @lower __kml_native_process_pid @link pool */
    processPid(handle: number): number;
    /** @lower __kml_native_process_kill @link pool */
    processKill(handle: number, signal: number): number;
    /** @lower __kml_native_process_ref @link pool */
    processRef(handle: number, on: boolean): void;
    /** @lower __kml_native_process_close @link pool */
    processClose(handle: number): void;
    // Blocks until the child exits: 0, or -errno; spawnSyncResult reads the
    // rest (0 pid, 1 status, 2 signal, 3 error, 4 + fd the bytes collected).
    /** @lower __kml_native_spawn_sync @link pool */
    spawnSync(file: string, argv: Uint8Array, env: Uint8Array, cwd: string, flags: number, stdio: string, uid: number, gid: number, input: Uint8Array, timeout: number, maxBuffer: number, killSignal: number): number;
    /** @lower __kml_native_spawn_sync_result @link pool */
    spawnSyncResult(what: number): number;
    /** @lower __kml_native_spawn_sync_take @link pool */
    spawnSyncTake(fd: number, buffer: Uint8Array): number;
    /** @lower __kml_native_signal_number @link pool */
    signalNumber(name: string): number;
    /** @lower __kml_native_signal_name @link pool */
    signalName(signal: number): string;

    // A descriptor's kind, as Node's guessHandleType: "TTY", "FILE", "PIPE",
    // "TCP", "UDP" or "UNKNOWN".
    /** @lower __kml_native_guess_handle_type @link pool */
    guessHandleType(fd: number): string;
    // An existing descriptor as a stream handle (a readable terminal
    // reopened by its path): its id, or -errno.
    /** @lower __kml_native_stream_open @link pool */
    streamOpen(fd: number, readable: boolean): number;
    // An IPC pipe (uv_pipe_init with ipc set): its reads collect the
    // descriptors sent with them.
    // A bound, listening socket not accepted on here (a cluster primary's
    // shared handle): its id, or -errno.
    /** @lower __kml_native_tcp_bind @link pool */
    tcpBind(host: string, port: number, backlog: number): number;
    // A listening socket's descriptor as a server handle: its id, or -errno.
    /** @lower __kml_native_tcp_listen_open @link pool */
    tcpListenOpen(fd: number, onConnection: (status: number, client: number) => void): number;
    // The descriptor under a stream handle, or -1 (uv_fileno).
    /** @lower __kml_native_tcp_fileno @link pool */
    tcpFileno(handle: number): number;
    /** @lower __kml_native_ipc_open @link pool */
    ipcOpen(handle: number): void;
    // The oldest descriptor received on the IPC pipe, or -1.
    /** @lower __kml_native_ipc_take_fd @link pool */
    ipcTakeFd(handle: number): number;
    // tcpWrite carrying stream handle sendHandle's descriptor (uv_write2);
    // -ENOTSUP on Windows.
    /** @lower __kml_native_ipc_write_handle @link pool */
    ipcWriteHandle(handle: number, buffer: Uint8Array, offset: number, length: number, sendHandle: number, onWritten: (status: number, unused: number) => void): number;
    // A blocking write of bytes [offset, offset + length) to fd: the count
    // written, or -errno.
    /** @lower __kml_native_write_sync @link pool */
    writeSync(fd: number, buffer: Uint8Array, offset: number, length: number): number;
    // MessagePorts (worker_threads): an entangled pair's first id (the
    // second is the next), a port's delivery on this thread's loop
    // (onEvent(0 messages | 1 closed, 0), draining with mportHas/mportTake),
    // and posting a cloned value to the other end (0, or -1 once closed).
    /** @lower __kml_native_mport_pair @link pool */
    mportPair(): number;
    /** @lower __kml_native_mport_start @link pool */
    mportStart(port: number, onEvent: (kind: number, unused: number) => void): void;
    /** @lower __kml_native_mport_stop @link pool */
    mportStop(port: number): void;
    /** @lower __kml_native_mport_ref @link pool */
    mportRef(port: number, on: boolean): void;
    /** @lower __kml_native_mport_post @link pool */
    mportPost(port: number, value: any): number;
    /** @lower __kml_native_mport_has @link pool */
    mportHas(port: number): boolean;
    /** @lower __kml_native_mport_take @link pool */
    mportTake(port: number): any;
    /** @lower __kml_native_mport_close @link pool */
    mportClose(port: number): void;
    // BroadcastChannel's process-wide registry: each open channel of a name
    // as the port the others post into.
    /** @lower __kml_native_bc_join @link pool */
    bcJoin(name: string, port: number): void;
    /** @lower __kml_native_bc_leave @link pool */
    bcLeave(name: string, port: number): void;
    /** @lower __kml_native_bc_count @link pool */
    bcCount(name: string): number;
    /** @lower __kml_native_bc_member @link pool */
    bcMember(name: string, index: number): number;
    // Worker threads: a thread running the worker module registered by
    // path, talking over the worker's ends of two port pairs; the parent
    // hears onEvent(0 online | 1 exit, exit code). The id, or -1 when no
    // worker module has the path, -errno when the thread fails to start.
    /** @lower __kml_native_worker_spawn @link pool */
    workerSpawn(path: string, port: number, internalPort: number, data: any, onEvent: (kind: number, code: number) => void): number;
    /** @lower __kml_native_worker_thread_id @link pool */
    workerThreadId(worker: number): number;
    /** @lower __kml_native_worker_ref @link pool */
    workerRef(worker: number, on: boolean): void;
    /** @lower __kml_native_worker_terminate @link pool */
    workerTerminate(worker: number): void;
    /** @lower __kml_native_worker_exited @link pool */
    workerExited(worker: number): void;
    // This thread as a worker: 0 whether it is one (1), 1 its public port,
    // 2 its internal port; its workerData and its file.
    /** @lower __kml_native_worker_self @link pool */
    workerSelf(which: number): number;
    /** @lower __kml_native_worker_self_data @link pool */
    workerSelfData(): any;
    /** @lower __kml_native_worker_self_filename @link pool */
    workerSelfFilename(): string;
    // node:sqlite (klainsqlite.c): databases and statements as ids of this
    // thread; SQLite result codes; integers that may exceed 2^53 as decimal
    // strings. See lib/node/sqlite.ts.
    /** @lower __kml_native_sqlite_open @link sqlite */
    sqliteOpen(path: string, flags: number, timeout: number, foreignKeys: boolean, doubleQuotedStrings: boolean): number;
    /** @lower __kml_native_sqlite_open_error @link sqlite */
    sqliteOpenError(): string;
    /** @lower __kml_native_sqlite_open_code @link sqlite */
    sqliteOpenCode(): number;
    /** @lower __kml_native_sqlite_close @link sqlite */
    sqliteClose(db: number): number;
    /** @lower __kml_native_sqlite_exec @link sqlite */
    sqliteExec(db: number, sql: string): number;
    /** @lower __kml_native_sqlite_errmsg @link sqlite */
    sqliteErrmsg(db: number): string;
    /** @lower __kml_native_sqlite_errcode @link sqlite */
    sqliteErrcode(db: number): number;
    /** @lower __kml_native_sqlite_errstr @link sqlite */
    sqliteErrstr(code: number): string;
    /** @lower __kml_native_sqlite_in_transaction @link sqlite */
    sqliteInTransaction(db: number): boolean;
    /** @lower __kml_native_sqlite_filename @link sqlite */
    sqliteFilename(db: number, name: string): string;
    /** @lower __kml_native_sqlite_has_db @link sqlite */
    sqliteHasDb(db: number, name: string): boolean;
    /** @lower __kml_native_sqlite_changes_str @link sqlite */
    sqliteChanges(db: number): string;
    /** @lower __kml_native_sqlite_last_rowid @link sqlite */
    sqliteLastRowid(db: number): string;
    /** @lower __kml_native_sqlite_db_config @link sqlite */
    sqliteDbConfig(db: number, op: number, value: number): number;
    /** @lower __kml_native_sqlite_prepare @link sqlite */
    sqlitePrepare(db: number, sql: string): number;
    /** @lower __kml_native_sqlite_finalize @link sqlite */
    sqliteFinalize(stmt: number): void;
    /** @lower __kml_native_sqlite_reset @link sqlite */
    sqliteReset(stmt: number, clearBindings: boolean): number;
    /** @lower __kml_native_sqlite_step @link sqlite */
    sqliteStep(stmt: number): number;
    /** @lower __kml_native_sqlite_param_count @link sqlite */
    sqliteParamCount(stmt: number): number;
    /** @lower __kml_native_sqlite_param_name @link sqlite */
    sqliteParamName(stmt: number, index: number): string;
    /** @lower __kml_native_sqlite_param_index @link sqlite */
    sqliteParamIndex(stmt: number, name: string): number;
    /** @lower __kml_native_sqlite_bind_null @link sqlite */
    sqliteBindNull(stmt: number, index: number): number;
    /** @lower __kml_native_sqlite_bind_double @link sqlite */
    sqliteBindDouble(stmt: number, index: number, value: number): number;
    /** @lower __kml_native_sqlite_bind_int @link sqlite */
    sqliteBindInt(stmt: number, index: number, decimal: string): number;
    /** @lower __kml_native_sqlite_bind_text @link sqlite */
    sqliteBindText(stmt: number, index: number, value: string): number;
    /** @lower __kml_native_sqlite_bind_blob @link sqlite */
    sqliteBindBlob(stmt: number, index: number, value: Uint8Array): number;
    /** @lower __kml_native_sqlite_column_count @link sqlite */
    sqliteColumnCount(stmt: number): number;
    // Column metadata: 0 name, 1 declared type, 2 database, 3 table, 4
    // origin column.
    /** @lower __kml_native_sqlite_column_meta @link sqlite */
    sqliteColumnMeta(stmt: number, index: number, which: number): string;
    /** @lower __kml_native_sqlite_column_has @link sqlite */
    sqliteColumnHas(stmt: number, index: number, which: number): boolean;
    // The current row's value: its storage class (1 integer, 2 float, 3
    // text, 4 blob, 5 null), then the value in it.
    /** @lower __kml_native_sqlite_column_type @link sqlite */
    sqliteColumnType(stmt: number, index: number): number;
    /** @lower __kml_native_sqlite_column_double @link sqlite */
    sqliteColumnDouble(stmt: number, index: number): number;
    /** @lower __kml_native_sqlite_column_int @link sqlite */
    sqliteColumnInt(stmt: number, index: number): string;
    /** @lower __kml_native_sqlite_column_text @link sqlite */
    sqliteColumnText(stmt: number, index: number): string;
    /** @lower __kml_native_sqlite_column_bytes @link sqlite */
    sqliteColumnBytes(stmt: number, index: number): number;
    /** @lower __kml_native_sqlite_column_blob @link sqlite */
    sqliteColumnBlob(stmt: number, index: number, out: Uint8Array): void;
    /** @lower __kml_native_sqlite_sql @link sqlite */
    sqliteSql(stmt: number, expanded: boolean): string;
    // A user function (kind 0) or aggregate (1, 2 with inverse): onCall(0
    // call | 1 step | 2 final | 3 value | 4 inverse, the group's key) runs
    // during a step, reading sqliteArg* and setting sqliteResult*.
    /** @lower __kml_native_sqlite_create_function @link sqlite */
    sqliteCreateFunction(db: number, name: string, argc: number, flags: number, kind: number, onCall: (kind: number, key: number) => void): number;
    /** @lower __kml_native_sqlite_arg_count @link sqlite */
    sqliteArgCount(): number;
    /** @lower __kml_native_sqlite_arg_type @link sqlite */
    sqliteArgType(index: number): number;
    /** @lower __kml_native_sqlite_arg_double @link sqlite */
    sqliteArgDouble(index: number): number;
    /** @lower __kml_native_sqlite_arg_int @link sqlite */
    sqliteArgInt(index: number): string;
    /** @lower __kml_native_sqlite_arg_text @link sqlite */
    sqliteArgText(index: number): string;
    /** @lower __kml_native_sqlite_arg_bytes @link sqlite */
    sqliteArgBytes(index: number): number;
    /** @lower __kml_native_sqlite_arg_blob @link sqlite */
    sqliteArgBlob(index: number, out: Uint8Array): void;
    /** @lower __kml_native_sqlite_result_null @link sqlite */
    sqliteResultNull(): void;
    /** @lower __kml_native_sqlite_result_double @link sqlite */
    sqliteResultDouble(value: number): void;
    /** @lower __kml_native_sqlite_result_int @link sqlite */
    sqliteResultInt(decimal: string): void;
    /** @lower __kml_native_sqlite_result_text @link sqlite */
    sqliteResultText(value: string): void;
    /** @lower __kml_native_sqlite_result_blob @link sqlite */
    sqliteResultBlob(value: Uint8Array): void;
    /** @lower __kml_native_sqlite_result_error @link sqlite */
    sqliteResultError(message: string): void;
    // A callback's answer (authorizer, changeset filter or conflict).
    /** @lower __kml_native_sqlite_cb_return @link sqlite */
    sqliteCallbackReturn(value: number): void;
    /** @lower __kml_native_sqlite_set_authorizer @link sqlite */
    sqliteSetAuthorizer(db: number, onAction: (action: number) => void): number;
    /** @lower __kml_native_sqlite_clear_authorizer @link sqlite */
    sqliteClearAuthorizer(db: number): number;
    /** @lower __kml_native_sqlite_auth_has @link sqlite */
    sqliteAuthHas(index: number): boolean;
    /** @lower __kml_native_sqlite_auth_arg @link sqlite */
    sqliteAuthArg(index: number): string;
    // -1: the library lacks the entry point.
    /** @lower __kml_native_sqlite_enable_load_extension @link sqlite */
    sqliteEnableLoadExtension(db: number, on: boolean): number;
    /** @lower __kml_native_sqlite_load_extension @link sqlite */
    sqliteLoadExtension(db: number, path: string, entryPoint: string): number;
    /** @lower __kml_native_sqlite_session_create @link sqlite */
    sqliteSessionCreate(db: number, dbName: string, table: string): number;
    /** @lower __kml_native_sqlite_session_set @link sqlite */
    sqliteSessionSet(session: number, which: number): number;
    /** @lower __kml_native_sqlite_session_bytes @link sqlite */
    sqliteSessionBytes(out: Uint8Array): void;
    /** @lower __kml_native_sqlite_session_delete @link sqlite */
    sqliteSessionDelete(session: number): void;
    /** @lower __kml_native_sqlite_apply_changeset @link sqlite */
    sqliteApplyChangeset(db: number, changeset: Uint8Array, filter: boolean, onEvent: (kind: number, conflict: number) => void): number;
    /** @lower __kml_native_sqlite_serialize @link sqlite */
    sqliteSerialize(db: number, dbName: string): number;
    /** @lower __kml_native_sqlite_serialized_bytes @link sqlite */
    sqliteSerializedBytes(out: Uint8Array): void;
    /** @lower __kml_native_sqlite_deserialize @link sqlite */
    sqliteDeserialize(db: number, dbName: string, image: Uint8Array): number;
    /** @lower __kml_native_sqlite_backup_init @link sqlite */
    sqliteBackupInit(db: number, source: string, path: string, target: string): number;
    /** @lower __kml_native_sqlite_backup_step @link sqlite */
    sqliteBackupStep(backup: number, pages: number): number;
    /** @lower __kml_native_sqlite_backup_progress @link sqlite */
    sqliteBackupProgress(backup: number, which: number): number;
    /** @lower __kml_native_sqlite_backup_finish @link sqlite */
    sqliteBackupFinish(backup: number): number;
    // node:ffi (ffi_native.c): libraries as ids over the registry
    // (ffi_registry.c, Node's unordered_map order), calls and callbacks
    // through libffi, and raw memory. Values cross boxed and are validated
    // with Node's per-type rules (0 ok, 1 not a value of the kind, 2 a
    // pointer bigint out of range); addresses cross as numbers.
    /** @lower __kml_native_ffi_open @link ffi */
    ffiOpen(path: string, isNull: boolean): number;
    /** @lower __kml_native_ffi_error @link ffi */
    ffiError(): string;
    /** @lower __kml_native_ffi_closed @link ffi */
    ffiClosed(lib: number): boolean;
    /** @lower __kml_native_ffi_close @link ffi */
    ffiClose(lib: number): void;
    /** @lower __kml_native_ffi_symbol @link ffi */
    ffiSymbol(lib: number, name: string): number;
    /** @lower __kml_native_ffi_last_addr @link ffi */
    ffiLastAddr(): number;
    /** @lower __kml_native_ffi_prepare @link ffi */
    ffiPrepare(lib: number, name: string, signature: string): number;
    /** @lower __kml_native_ffi_last_fresh @link ffi */
    ffiLastFresh(): boolean;
    /** @lower __kml_native_ffi_commit @link ffi */
    ffiCommit(lib: number): void;
    /** @lower __kml_native_ffi_count @link ffi */
    ffiCount(lib: number, functions: boolean): number;
    /** @lower __kml_native_ffi_list_name @link ffi */
    ffiListName(index: number): string;
    /** @lower __kml_native_ffi_list_addr @link ffi */
    ffiListAddr(index: number, functions: boolean): number;
    // A call interface for "ret:arg,arg" kinds, arguments, the call.
    /** @lower __kml_native_ffi_cif @link ffi */
    ffiCif(kinds: string): number;
    /** @lower __kml_native_ffi_arg @link ffi */
    ffiArg(index: number, kind: number, value: any): number;
    /** @lower __kml_native_ffi_call @link ffi */
    ffiCall(cif: number, fn: number): any;
    /** @lower __kml_native_ffi_closure @link ffi */
    ffiClosure(cif: number, onCall: (unused: number, unused2: number) => void): number;
    /** @lower __kml_native_ffi_closure_free @link ffi */
    ffiClosureFree(code: number): void;
    /** @lower __kml_native_ffi_cb_argc @link ffi */
    ffiCallbackArgc(): number;
    /** @lower __kml_native_ffi_cb_arg @link ffi */
    ffiCallbackArg(index: number): any;
    /** @lower __kml_native_ffi_cb_return @link ffi */
    ffiCallbackReturn(value: any): number;
    // A pointer argument's address: -1 not a pointer value, -2 out of range.
    /** @lower __kml_native_ffi_ptr @link ffi */
    ffiPtr(value: any): number;
    /** @lower __kml_native_ffi_get @link ffi */
    ffiGet(kind: number, addr: number, offset: number): any;
    /** @lower __kml_native_ffi_set @link ffi */
    ffiSet(kind: number, addr: number, offset: number, value: any): number;
    /** @lower __kml_native_ffi_cstring @link ffi */
    ffiCString(addr: number): string;
    /** @lower __kml_native_ffi_copy_in @link ffi */
    ffiCopyIn(addr: number, out: Uint8Array): void;
    /** @lower __kml_native_ffi_copy_out @link ffi */
    ffiCopyOut(src: Uint8Array, addr: number): void;
    // len bytes at addr as a Buffer (or an ArrayBuffer) sharing them.
    /** @lower __kml_native_ffi_view */
    ffiView(addr: number, len: number, arrayBuffer: boolean): any;
    // A terminal handle's raw (true) or normal mode: 0, or -errno.
    /** @lower __kml_native_tty_set_raw_mode @link pool */
    ttySetRawMode(handle: number, raw: boolean): number;
    // A terminal's size as cols * 65536 + rows, or -errno.
    /** @lower __kml_native_tty_window_size @link pool */
    ttyWindowSize(fd: number): number;
    // A signal watcher (Node's signal_wrap): onSignal runs on the loop when
    // the signal arrives; it never holds the loop open. 0, or -errno.
    /** @lower __kml_native_signal_start */
    signalStart(signal: number, onSignal: (signal: number) => void): number;
    /** @lower __kml_native_signal_stop */
    signalStop(signal: number): void;
    // The runtime's hook for a process event it raises: 0 'exit' (code), 1
    // 'uncaughtException' (error, 1 when from a rejection), 2
    // 'unhandledRejection' (reason, promise), 3 a worker's uncaught error
    // (error), which ends the worker.
    /** @lower __kml_native_process_hook */
    processHook(which: number, hook: (a: any, b: any) => void): void;
    /** @lower __kml_native_process_unhook */
    processUnhook(which: number): void;
    // The current async context frame (async_hooks): undefined, or the Map
    // AsyncLocalStorage keeps; the runtime carries it into spawned tasks and
    // scheduled timers.
    // util.inspect(value, { depth, compact, sorted, breakLength,
    // maxArrayLength }) (compact 0 is false; Infinity is unlimited).
    /** @lower __kml_inspect_opts @link inspect */
    inspectWith(value: any, depth: number, compact: number, sorted: number, breakLength: number, maxArrayLength: number): string;
    // A key naming value's prototype: two objects have one prototype exactly
    // when their keys are equal ("" for a primitive).
    /** @lower __kml_proto_key @link inspect */
    protoKey(value: any): string;
    // A TypedArray's bytes, copied into out (as many as fit): the count
    // copied, 0 for any other value.
    /** @lower __kml_typed_copy_bytes @link inspect */
    typedBytes(value: any, out: Uint8Array): number;
    // in's bytes copied into a TypedArray held in `any` (as many as fit).
    /** @lower __kml_typed_set_bytes @link inspect */
    typedBytesBack(bytes: Uint8Array, value: any): number;

    // node:crypto over libcrypto (cryptosrc/crypto_openssl.c). A hash, HMAC
    // or cipher is an id; a pooled form calls back (status, result).
    /** @lower __kml_native_crypto_hash_new @link crypto */
    cryptoHashNew(algorithm: string, outputLength: number): number;
    /** @lower __kml_native_crypto_hmac_new @link crypto */
    cryptoHmacNew(algorithm: string, key: Uint8Array): number;
    /** @lower __kml_native_crypto_hash_update @link crypto */
    cryptoHashUpdate(handle: number, data: Uint8Array): number;
    /** @lower __kml_native_crypto_hash_size @link crypto */
    cryptoHashSize(handle: number): number;
    /** @lower __kml_native_crypto_hash_digest @link crypto */
    cryptoHashDigest(handle: number, out: Uint8Array): number;
    /** @lower __kml_native_crypto_hash_copy @link crypto */
    cryptoHashCopy(handle: number): number;
    /** @lower __kml_native_crypto_free @link crypto */
    cryptoFree(handle: number): void;
    /** @lower __kml_native_crypto_names @link crypto */
    cryptoNames(which: number): string;
    /** @lower __kml_native_crypto_random_fill @link crypto */
    cryptoRandomFill(data: Uint8Array, offset: number, length: number): number;
    /** @lower __kml_native_crypto_random_fill_async @link crypto */
    cryptoRandomFillAsync(data: Uint8Array, offset: number, length: number, callback: (status: number, unused: number) => void): void;
    /** @lower __kml_native_crypto_kdf @link crypto */
    cryptoKdf(op: number, a: Uint8Array, b: Uint8Array, c: Uint8Array, n1: number, n2: number, n3: number, n4: number, digest: string, out: Uint8Array): number;
    /** @lower __kml_native_crypto_kdf_async @link crypto */
    cryptoKdfAsync(op: number, a: Uint8Array, b: Uint8Array, c: Uint8Array, n1: number, n2: number, n3: number, n4: number, digest: string, out: Uint8Array, callback: (status: number, unused: number) => void): void;
    /** @lower __kml_native_crypto_cipher_new @link crypto */
    cryptoCipherNew(algorithm: string, key: Uint8Array, iv: Uint8Array, encrypt: number, authTagLength: number): number;
    /** @lower __kml_native_crypto_cipher_block_size @link crypto */
    cryptoCipherBlockSize(handle: number): number;
    /** @lower __kml_native_crypto_cipher_update @link crypto */
    cryptoCipherUpdate(handle: number, data: Uint8Array, out: Uint8Array): number;
    /** @lower __kml_native_crypto_cipher_final @link crypto */
    cryptoCipherFinal(handle: number, out: Uint8Array): number;
    /** @lower __kml_native_crypto_cipher_set_padding @link crypto */
    cryptoCipherSetPadding(handle: number, on: number): number;
    /** @lower __kml_native_crypto_cipher_set_aad @link crypto */
    cryptoCipherSetAAD(handle: number, aad: Uint8Array): number;
    /** @lower __kml_native_crypto_cipher_get_tag @link crypto */
    cryptoCipherGetTag(handle: number, out: Uint8Array): number;
    /** @lower __kml_native_crypto_cipher_set_tag @link crypto */
    cryptoCipherSetTag(handle: number, tag: Uint8Array): number;
    /** @lower __kml_native_crypto_keygen @link crypto */
    cryptoKeygen(kind: number, bits: number, exponent: number, curve: string): string;
    /** @lower __kml_native_crypto_keygen_async @link crypto */
    cryptoKeygenAsync(kind: number, bits: number, exponent: number, curve: string, callback: (status: number, result: number) => void): void;
    /** @lower __kml_native_crypto_keygen_take @link crypto */
    cryptoKeygenTake(result: number): string;
    /** @lower __kml_native_crypto_sign @link crypto */
    cryptoSign(digest: string, pem: string, passphrase: Uint8Array, hasPassphrase: number, data: Uint8Array, out: Uint8Array): number;
    /** @lower __kml_native_crypto_verify @link crypto */
    cryptoVerify(digest: string, pem: string, passphrase: Uint8Array, hasPassphrase: number, data: Uint8Array, signature: Uint8Array): number;
    /** @lower __kml_native_crypto_timing_equal @link crypto */
    cryptoTimingEqual(a: Uint8Array, b: Uint8Array): number;
    /** @lower __kml_native_crypto_last_error @link crypto */
    cryptoLastError(): string;
    // 0 for a value that is no constructor, 2 for an Error constructor, 1
    // for any other.
    /** @lower __kml_ctor_kind */
    ctorKind(value: any): number;
    // The Error subclass an error was made by ("" for a builtin kind).
    /** @lower __kml_error_ctor_name @link inspect */
    errorConstructorName(value: any): string;
    /** @lower __kml_native_async_context_get */
    asyncContextGet(): any;
    /** @lower __kml_native_async_context_set */
    asyncContextSet(frame: any): void;
    // The fork channel's descriptor is the process emitter's to read.
    /** @lower __kml_native_ipc_child_claim */
    ipcChildClaim(): void;
    // performance.now(): ms since the time origin (process start), and the
    // time origin as epoch ms.
    /** @lower __kml_native_perf_now */
    perfNow(): number;
    /** @lower __kml_native_perf_time_origin */
    perfTimeOrigin(): number;
    // process (Node's process binding): a failing call is -errno.
    /** @lower __kml_native_process_cwd */
    processCwd(): string;
    /** @lower __kml_native_process_chdir */
    processChdir(directory: string): number;
    /** @lower __kml_native_process_uptime */
    processUptime(): number;
    // Reads the monotonic clock; processHrtimeRead(0) is its seconds,
    // (1) its nanoseconds.
    /** @lower __kml_native_process_hrtime */
    processHrtime(): void;
    /** @lower __kml_native_process_hrtime_read */
    processHrtimeRead(which: number): number;
    /** @lower __kml_native_kill_pid */
    killPid(pid: number, signal: number): number;
    // 0 rss, 1 heapTotal, 2 heapUsed.
    /** @lower __kml_native_process_memory */
    processMemory(which: number): number;
    // Sets the mask, returning the previous one; a negative mask reads it.
    /** @lower __kml_native_process_umask */
    processUmask(mask: number): number;
    // 0 pid, 1 ppid, 2 uid, 3 euid, 4 gid, 5 egid.
    /** @lower __kml_native_process_id */
    processId(which: number): number;
    // The argument vector, Node-shaped ([execPath, execPath, …args]), and
    // the operating system's argv[0].
    /** @lower __kml_native_process_argc */
    processArgc(): number;
    /** @lower __kml_native_process_argv */
    processArgv(i: number): string;
    /** @lower __kml_native_process_argv0 */
    processArgv0(): string;
    /** @lower __kml_native_process_exec_path */
    processExecPath(): string;
    // The code the program exits with at its end.
    /** @lower __kml_native_process_set_exit_code */
    processSetExitCode(code: number): void;
    /** @lower __kml_native_process_get_exit_code */
    processGetExitCode(): number;
    // The 'exit' hook, then exit(3).
    /** @lower __kml_native_process_really_exit */
    processReallyExit(code: number): void;
    // 0 process.version, 1 node, 2 v8, 3 klain.
    /** @lower __kml_native_process_version */
    processVersion(which: number): string;
    /** @lower __kml_native_env_get */
    envGet(key: string): string | undefined;
    /** @lower __kml_native_env_set */
    envSet(key: string, value: string): void;
    /** @lower __kml_native_env_delete */
    envDelete(key: string): void;
    // The program's entry file (require.main.filename).
    /** @lower __kml_native_entry_path */
    entryPath(): string;

    // The os module (osinfo.c). A failed read sets osErrno().
    /** @lower __kml_native_os_errno @link osinfo */
    osErrno(): number;
    // 0 type, 1 version, 2 release, 3 machine, 4 hostname, 5 homedir.
    /** @lower __kml_native_os_string @link osinfo */
    osString(which: number): string;
    // 0 uptime, 1 totalmem, 2 freemem, 3 availableParallelism, 4-6 loadavg,
    // 7 whether the host is big-endian.
    /** @lower __kml_native_os_number @link osinfo */
    osNumber(which: number): number;
    // Reads the CPUs; returns their count.
    /** @lower __kml_native_os_cpus @link osinfo */
    osCpus(): number;
    /** @lower __kml_native_os_cpu_model @link osinfo */
    osCpuModel(cpu: number): string;
    // 0 speed, 1-5 user, nice, sys, idle, irq.
    /** @lower __kml_native_os_cpu_value @link osinfo */
    osCpuValue(cpu: number, which: number): number;
    // Reads the interface addresses; returns their count.
    /** @lower __kml_native_os_netifs @link osinfo */
    osNetifs(): number;
    // 0 name, 1 address, 2 netmask, 3 mac.
    /** @lower __kml_native_os_netif_string @link osinfo */
    osNetifString(entry: number, which: number): string;
    // 0 family (4 or 6), 1 internal, 2 scopeid (-1 for IPv4).
    /** @lower __kml_native_os_netif_number @link osinfo */
    osNetifNumber(entry: number, which: number): number;
    // Reads the effective user's passwd entry: 0, or the errno.
    /** @lower __kml_native_os_userinfo @link osinfo */
    osUserInfo(): number;
    // 0 username, 1 homedir, 2 shell.
    /** @lower __kml_native_os_userinfo_string @link osinfo */
    osUserInfoString(which: number): string;
    // 0 uid, 1 gid, 2 whether there is a shell.
    /** @lower __kml_native_os_userinfo_number @link osinfo */
    osUserInfoNumber(which: number): number;
    /** @lower __kml_native_os_get_priority @link osinfo */
    osGetPriority(pid: number): number;
    // 0, or the errno.
    /** @lower __kml_native_os_set_priority @link osinfo */
    osSetPriority(pid: number, priority: number): number;
    // os.constants groups: 0 errno, 1 signals, 2 priority, 3 dlopen.
    /** @lower __kml_native_os_constant_count @link osinfo */
    osConstantCount(group: number): number;
    /** @lower __kml_native_os_constant_name @link osinfo */
    osConstantName(group: number, index: number): string;
    /** @lower __kml_native_os_constant_value @link osinfo */
    osConstantValue(group: number, index: number): number;
    // zlib (klainzlib.c): a handle per stream, Node's ZlibContext. A write
    // fails with 1 (zlibError* read why); the state reads avail_out (0) or
    // avail_in (1) after it.
    /** @lower __kml_native_zlib_new @link zlib */
    zlibNew(mode: number): number;
    /** @lower __kml_native_zlib_init @link zlib */
    zlibInit(id: number, level: number, windowBits: number, memLevel: number, strategy: number, rejectGarbage: number, dictionary: Uint8Array): void;
    /** @lower __kml_native_zlib_write_sync @link zlib */
    zlibWriteSync(id: number, flush: number, input: Uint8Array, inOff: number, inLen: number, out: Uint8Array, outOff: number, outLen: number): number;
    /** @lower __kml_native_zlib_write @link zlib */
    zlibWrite(id: number, flush: number, input: Uint8Array, inOff: number, inLen: number, out: Uint8Array, outOff: number, outLen: number, callback: (failed: number, unused: number) => void): void;
    /** @lower __kml_native_zlib_state @link zlib */
    zlibState(id: number, which: number): number;
    /** @lower __kml_native_zlib_error_message @link zlib */
    zlibErrorMessage(id: number): string;
    /** @lower __kml_native_zlib_error_errno @link zlib */
    zlibErrorErrno(id: number): number;
    /** @lower __kml_native_zlib_error_code @link zlib */
    zlibErrorCode(id: number): string;
    // A Brotli/Zstd handle's parameter, then its dictionary and (Zstd
    // compression) pledged size (-1: none): 0, or 1 when refused.
    /** @lower __kml_native_zlib_set_param @link zlib */
    zlibSetParam(id: number, key: number, value: number): number;
    /** @lower __kml_native_zlib_init_other @link zlib */
    zlibInitOther(id: number, dictionary: Uint8Array, pledgedSrcSize: number): number;
    /** @lower __kml_native_zlib_params @link zlib */
    zlibParams(id: number, level: number, strategy: number): number;
    /** @lower __kml_native_zlib_reset @link zlib */
    zlibReset(id: number): number;
    /** @lower __kml_native_zlib_close @link zlib */
    zlibClose(id: number): void;
    // The linked zlib's ZLIB_VERNUM.
    /** @lower __kml_native_zlib_vernum @link zlib */
    zlibVernum(): number;
    /** @lower __kml_native_zlib_crc32 @link zlib */
    zlibCrc32(data: Uint8Array, value: number): number;

    // The tick queue and the promise jobs, as Node's MakeCallback runs them
    // after a native callback into JavaScript.
    /** @lower __kml_drain_microtasks */
    runMicrotasks(): void;
    // An nghttp2 session (http2src/h2node.c), Node's node_http2.cc: fed the
    // bytes its socket reads (h2Feed), drained of the bytes to write
    // (h2Flush, then h2Take). The frames it receives queue events h2Next
    // takes one at a time: the type, its fields through h2Event, headers
    // ("name\nvalue\n…") as lastString, bytes through h2EventBytes.
    /** @lower __kml_native_h2_new @link http2 */
    h2New(type: number, maxHeaderListPairs: number, maxReservedRemoteStreams: number, peerMaxConcurrentStreams: number): number;
    /** @lower __kml_native_h2_feed @link http2 */
    h2Feed(id: number, data: Uint8Array, offset: number, length: number): number;
    /** @lower __kml_native_h2_flush @link http2 */
    h2Flush(id: number): number;
    /** @lower __kml_native_h2_take @link http2 */
    h2Take(id: number, out: Uint8Array): number;
    /** @lower __kml_native_h2_next @link http2 */
    h2Next(id: number): number;
    /** @lower __kml_native_h2_event @link http2 */
    h2Event(id: number, which: number): number;
    /** @lower __kml_native_h2_event_bytes @link http2 */
    h2EventBytes(id: number, out: Uint8Array): number;
    /** @lower __kml_native_h2_request @link http2 */
    h2Request(id: number, headers: string, endStream: number, waitForTrailers: number): number;
    /** @lower __kml_native_h2_respond @link http2 */
    h2Respond(id: number, stream: number, headers: string, endStream: number, waitForTrailers: number): number;
    /** @lower __kml_native_h2_headers @link http2 */
    h2Headers(id: number, stream: number, headers: string, trailers: number): number;
    /** @lower __kml_native_h2_push @link http2 */
    h2Push(id: number, stream: number, headers: string): number;
    /** @lower __kml_native_h2_write @link http2 */
    h2Write(id: number, stream: number, data: Uint8Array, offset: number, length: number, end: number): number;
    /** @lower __kml_native_h2_queued @link http2 */
    h2Queued(id: number, stream: number): number;
    /** @lower __kml_native_h2_rst @link http2 */
    h2Rst(id: number, stream: number, code: number): number;
    /** @lower __kml_native_h2_settings @link http2 */
    h2Settings(id: number, settings: Uint8Array): number;
    /** @lower __kml_native_h2_ping @link http2 */
    h2Ping(id: number, payload: Uint8Array): number;
    /** @lower __kml_native_h2_goaway @link http2 */
    h2Goaway(id: number, code: number, lastStreamId: number, opaqueData: Uint8Array): number;
    /** @lower __kml_native_h2_state @link http2 */
    h2State(id: number, which: number): number;
    /** @lower __kml_native_h2_stream_state @link http2 */
    h2StreamState(id: number, stream: number, which: number): number;
    /** @lower __kml_native_h2_error_text @link http2 */
    h2ErrorText(code: number): void;
    /** @lower __kml_native_h2_free @link http2 */
    h2Free(id: number): void;
}

declare var __kml_native: KmlNative;
