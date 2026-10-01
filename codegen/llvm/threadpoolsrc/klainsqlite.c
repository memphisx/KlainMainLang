// node:sqlite's natives (lib/node/sqlite.ts): libsqlite3 behind handle ids,
// as Node's src/node_sqlite.cc drives it. Compiled into the pool's unit only
// with -DKLAINPOOL_SQLITE (ThreadPoolCFlags). Databases and statements are
// per-thread tables, as a Node object belongs to its isolate. Integers cross
// as decimal strings where they may exceed 2^53 (the module decides between
// a number, a bigint and ERR_OUT_OF_RANGE). The session, extension and
// serialization entry points are looked up at run time: a system libsqlite3
// may be built without them.
#ifdef KLAINPOOL_SQLITE
#include <sqlite3.h>
#ifndef _WIN32
#include <dlfcn.h>
#endif

typedef struct kml_sqlite_fn {
    void *inv, *clo;   // the module's callback: (kind, call key)
} kml_sqlite_fn;

static __thread sqlite3 **sq_db = NULL;
static __thread int sq_db_n = 0, sq_db_cap = 0;
static __thread sqlite3_stmt **sq_st = NULL;
static __thread int sq_st_n = 0, sq_st_cap = 0;
static __thread char *sq_open_err = NULL;
static __thread int sq_open_code = 0;

static sqlite3 *sq_db_get(double id) {
    int i = (int)id;
    return (i >= 0 && i < sq_db_n) ? sq_db[i] : NULL;
}

static sqlite3_stmt *sq_st_get(double id) {
    int i = (int)id;
    return (i >= 0 && i < sq_st_n) ? sq_st[i] : NULL;
}

static char *sq_string(const char *src, int64_t n) {
    if (!src) { src = ""; n = 0; }
    if (n < 0) n = (int64_t)strlen(src);
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, src, (size_t)n);
    out[n] = 0;
    __kml_str_finalize(out);
    return out;
}

static void *sq_sym(const char *name) {
#ifdef _WIN32
    (void)name;
    return NULL;
#else
    return dlsym(RTLD_DEFAULT, name);
#endif
}

// ---- databases ----

// Open path with the SQLITE_OPEN_* flags: its id, or -1 (the error is
// sqliteOpenError / sqliteOpenCode).
double __kml_native_sqlite_open(const char *path, double flags, double timeout, _Bool foreignKeys, _Bool dqs) {
    sqlite3 *db = NULL;
    int rc = sqlite3_open_v2(path, &db, (int)flags | SQLITE_OPEN_URI, NULL);
    if (rc != SQLITE_OK) {
        free(sq_open_err);
        sq_open_err = strdup(db ? sqlite3_errmsg(db) : sqlite3_errstr(rc));
        sq_open_code = db ? sqlite3_extended_errcode(db) : rc;
        if (db) sqlite3_close_v2(db);
        return -1;
    }
    sqlite3_extended_result_codes(db, 1);
    int on = 0;
    sqlite3_db_config(db, SQLITE_DBCONFIG_ENABLE_FKEY, foreignKeys ? 1 : 0, &on);
    sqlite3_db_config(db, SQLITE_DBCONFIG_DQS_DML, dqs ? 1 : 0, &on);
    sqlite3_db_config(db, SQLITE_DBCONFIG_DQS_DDL, dqs ? 1 : 0, &on);
    if (timeout > 0) sqlite3_busy_timeout(db, (int)timeout);
    if (sq_db_n == sq_db_cap) {
        int nc = sq_db_cap ? sq_db_cap * 2 : 8;
        sqlite3 **nt = (sqlite3 **)calloc((size_t)nc, sizeof *nt);
        if (sq_db) memcpy(nt, sq_db, (size_t)sq_db_cap * sizeof *nt);
        sq_db = nt;
        sq_db_cap = nc;
    }
    sq_db[sq_db_n] = db;
    return sq_db_n++;
}

char *__kml_native_sqlite_open_error(void) { return sq_string(sq_open_err, -1); }
double __kml_native_sqlite_open_code(void) { return sq_open_code; }

double __kml_native_sqlite_close(double id) {
    sqlite3 *db = sq_db_get(id);
    if (!db) return SQLITE_MISUSE;
    int rc = sqlite3_close_v2(db);
    sq_db[(int)id] = NULL;
    return rc;
}

double __kml_native_sqlite_exec(double id, const char *sql) {
    sqlite3 *db = sq_db_get(id);
    if (!db) return SQLITE_MISUSE;
    return sqlite3_exec(db, sql, NULL, NULL, NULL);
}

// The database's last error: 0 its message, 1 the extended code, 2 the
// code's description (errstr).
char *__kml_native_sqlite_errmsg(double id) {
    sqlite3 *db = sq_db_get(id);
    return sq_string(db ? sqlite3_errmsg(db) : "database is not open", -1);
}

double __kml_native_sqlite_errcode(double id) {
    sqlite3 *db = sq_db_get(id);
    return db ? sqlite3_extended_errcode(db) : SQLITE_MISUSE;
}

char *__kml_native_sqlite_errstr(double code) { return sq_string(sqlite3_errstr((int)code), -1); }

_Bool __kml_native_sqlite_in_transaction(double id) {
    sqlite3 *db = sq_db_get(id);
    return db && !sqlite3_get_autocommit(db);
}

// The file of the attached database name, or "" for an in-memory one; -1 as
// the result's length is null (no such database): see sqliteHasDb.
char *__kml_native_sqlite_filename(double id, const char *name) {
    sqlite3 *db = sq_db_get(id);
    const char *f = db ? sqlite3_db_filename(db, name) : NULL;
    return sq_string(f ? f : "", -1);
}

_Bool __kml_native_sqlite_has_db(double id, const char *name) {
    sqlite3 *db = sq_db_get(id);
    return db && sqlite3_db_filename(db, name) != NULL;
}

double __kml_native_sqlite_changes(double id) {
    sqlite3 *db = sq_db_get(id);
    return db ? (double)sqlite3_changes64(db) : 0;
}

char *__kml_native_sqlite_last_rowid(double id) {
    sqlite3 *db = sq_db_get(id);
    char buf[32];
    snprintf(buf, sizeof buf, "%lld", db ? (long long)sqlite3_last_insert_rowid(db) : 0LL);
    return sq_string(buf, -1);
}

char *__kml_native_sqlite_changes_str(double id) {
    sqlite3 *db = sq_db_get(id);
    char buf[32];
    snprintf(buf, sizeof buf, "%lld", db ? (long long)sqlite3_changes64(db) : 0LL);
    return sq_string(buf, -1);
}

double __kml_native_sqlite_db_config(double id, double op, double value) {
    sqlite3 *db = sq_db_get(id);
    if (!db) return SQLITE_MISUSE;
    int out = 0;
    return sqlite3_db_config(db, (int)op, (int)value, &out);
}

// ---- statements ----

// A prepared statement's id, or -1 (the database's error says why); -2 when
// sql held no statement.
double __kml_native_sqlite_prepare(double id, const char *sql) {
    sqlite3 *db = sq_db_get(id);
    if (!db) return -1;
    sqlite3_stmt *st = NULL;
    int rc = sqlite3_prepare_v2(db, sql, -1, &st, NULL);
    if (rc != SQLITE_OK) return -1;
    if (!st) return -2;
    if (sq_st_n == sq_st_cap) {
        int nc = sq_st_cap ? sq_st_cap * 2 : 16;
        sqlite3_stmt **nt = (sqlite3_stmt **)calloc((size_t)nc, sizeof *nt);
        if (sq_st) memcpy(nt, sq_st, (size_t)sq_st_cap * sizeof *nt);
        sq_st = nt;
        sq_st_cap = nc;
    }
    sq_st[sq_st_n] = st;
    return sq_st_n++;
}

void __kml_native_sqlite_finalize(double sid) {
    sqlite3_stmt *st = sq_st_get(sid);
    if (!st) return;
    sqlite3_finalize(st);
    sq_st[(int)sid] = NULL;
}

double __kml_native_sqlite_reset(double sid, _Bool clearBindings) {
    sqlite3_stmt *st = sq_st_get(sid);
    if (!st) return SQLITE_MISUSE;
    if (clearBindings) sqlite3_clear_bindings(st);
    return sqlite3_reset(st);
}

double __kml_native_sqlite_step(double sid) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st ? sqlite3_step(st) : SQLITE_MISUSE;
}

double __kml_native_sqlite_param_count(double sid) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st ? sqlite3_bind_parameter_count(st) : 0;
}

// The i-th (1-based) parameter's name with its prefix (":x"), or "" for an
// anonymous one.
char *__kml_native_sqlite_param_name(double sid, double i) {
    sqlite3_stmt *st = sq_st_get(sid);
    const char *n = st ? sqlite3_bind_parameter_name(st, (int)i) : NULL;
    return sq_string(n ? n : "", -1);
}

double __kml_native_sqlite_param_index(double sid, const char *name) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st ? sqlite3_bind_parameter_index(st, name) : 0;
}

// Binding a value: the SQLite result code.
double __kml_native_sqlite_bind_null(double sid, double i) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st ? sqlite3_bind_null(st, (int)i) : SQLITE_MISUSE;
}

double __kml_native_sqlite_bind_double(double sid, double i, double v) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st ? sqlite3_bind_double(st, (int)i, v) : SQLITE_MISUSE;
}

// An integer in decimal (a bigint's, or a whole number's).
double __kml_native_sqlite_bind_int(double sid, double i, const char *dec) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st ? sqlite3_bind_int64(st, (int)i, (sqlite3_int64)strtoll(dec, NULL, 10)) : SQLITE_MISUSE;
}

double __kml_native_sqlite_bind_text(double sid, double i, const char *s) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st ? sqlite3_bind_text(st, (int)i, s, -1, SQLITE_TRANSIENT) : SQLITE_MISUSE;
}

double __kml_native_sqlite_bind_blob(double sid, double i, const uint8_t *data, int64_t len) {
    sqlite3_stmt *st = sq_st_get(sid);
    if (!st) return SQLITE_MISUSE;
    return sqlite3_bind_blob64(st, (int)i, len ? (const void *)data : (const void *)"", (sqlite3_uint64)len, SQLITE_TRANSIENT);
}

double __kml_native_sqlite_column_count(double sid) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st ? sqlite3_column_count(st) : 0;
}

// Column i's name (0), declared type (1), database (2), table (3), origin
// column (4); "" when there is none (see sqliteColumnHas).
static const char *sq_column_meta(sqlite3_stmt *st, int i, int which) {
    switch (which) {
    case 0: return sqlite3_column_name(st, i);
    case 1: return sqlite3_column_decltype(st, i);
#ifdef SQLITE_ENABLE_COLUMN_METADATA
    case 2: return sqlite3_column_database_name(st, i);
    case 3: return sqlite3_column_table_name(st, i);
    case 4: return sqlite3_column_origin_name(st, i);
#else
    case 2: { const char *(*f)(sqlite3_stmt *, int) = (const char *(*)(sqlite3_stmt *, int))sq_sym("sqlite3_column_database_name"); return f ? f(st, i) : NULL; }
    case 3: { const char *(*f)(sqlite3_stmt *, int) = (const char *(*)(sqlite3_stmt *, int))sq_sym("sqlite3_column_table_name"); return f ? f(st, i) : NULL; }
    case 4: { const char *(*f)(sqlite3_stmt *, int) = (const char *(*)(sqlite3_stmt *, int))sq_sym("sqlite3_column_origin_name"); return f ? f(st, i) : NULL; }
#endif
    }
    return NULL;
}

char *__kml_native_sqlite_column_meta(double sid, double i, double which) {
    sqlite3_stmt *st = sq_st_get(sid);
    const char *s = st ? sq_column_meta(st, (int)i, (int)which) : NULL;
    return sq_string(s ? s : "", -1);
}

_Bool __kml_native_sqlite_column_has(double sid, double i, double which) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st && sq_column_meta(st, (int)i, (int)which) != NULL;
}

// The current row's column i: its storage class (1 integer, 2 float, 3 text,
// 4 blob, 5 null), and its value in that class.
double __kml_native_sqlite_column_type(double sid, double i) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st ? sqlite3_column_type(st, (int)i) : SQLITE_NULL;
}

double __kml_native_sqlite_column_double(double sid, double i) {
    sqlite3_stmt *st = sq_st_get(sid);
    return st ? sqlite3_column_double(st, (int)i) : 0;
}

char *__kml_native_sqlite_column_int(double sid, double i) {
    sqlite3_stmt *st = sq_st_get(sid);
    char buf[32];
    snprintf(buf, sizeof buf, "%lld", st ? (long long)sqlite3_column_int64(st, (int)i) : 0LL);
    return sq_string(buf, -1);
}

char *__kml_native_sqlite_column_text(double sid, double i) {
    sqlite3_stmt *st = sq_st_get(sid);
    if (!st) return sq_string("", 0);
    const unsigned char *t = sqlite3_column_text(st, (int)i);
    return sq_string((const char *)t, sqlite3_column_bytes(st, (int)i));
}

double __kml_native_sqlite_column_bytes(double sid, double i) {
    sqlite3_stmt *st = sq_st_get(sid);
    if (!st) return 0;
    sqlite3_column_blob(st, (int)i);
    return sqlite3_column_bytes(st, (int)i);
}

// Copy column i's blob into out (sized by sqliteColumnBytes).
void __kml_native_sqlite_column_blob(double sid, double i, uint8_t *out, int64_t len) {
    sqlite3_stmt *st = sq_st_get(sid);
    if (!st) return;
    const void *b = sqlite3_column_blob(st, (int)i);
    int n = sqlite3_column_bytes(st, (int)i);
    if (b && n > 0) memcpy(out, b, (size_t)(n < len ? n : len));
}

char *__kml_native_sqlite_sql(double sid, _Bool expanded) {
    sqlite3_stmt *st = sq_st_get(sid);
    if (!st) return sq_string("", 0);
    if (!expanded) return sq_string(sqlite3_sql(st), -1);
    char *e = sqlite3_expanded_sql(st);
    char *out = sq_string(e ? e : "", -1);
    sqlite3_free(e);
    return out;
}

// ---- user functions and aggregates ----
// The module's callback runs during sqlite3_step: (0 call | 1 step | 2 final
// | 3 value | 4 inverse, the call's aggregate key). It reads the arguments
// with sqliteArg*, sets the result with sqliteResult*.

static __thread sqlite3_context *sq_ctx = NULL;
static __thread sqlite3_value **sq_argv = NULL;
static __thread int sq_argc = 0;

static void sq_call(sqlite3_context *ctx, int kind, int argc, sqlite3_value **argv) {
    kml_sqlite_fn *fn = (kml_sqlite_fn *)sqlite3_user_data(ctx);
    sqlite3_context *pc = sq_ctx;
    sqlite3_value **pv = sq_argv;
    int pn = sq_argc;
    sq_ctx = ctx;
    sq_argv = argv;
    sq_argc = argc;
    double key = 0;
    if (kind != 0) {
        // One key per aggregate group, stable for its calls.
        int64_t *slot = (int64_t *)sqlite3_aggregate_context(ctx, (int)sizeof(int64_t));
        static __thread int64_t next_key = 0;
        if (slot) {
            if (*slot == 0) *slot = ++next_key;
            key = (double)*slot;
        }
    }
    ((void (*)(void *, double, double))fn->inv)(fn->clo, kind, key);
    sq_ctx = pc;
    sq_argv = pv;
    sq_argc = pn;
}

static void sq_x_func(sqlite3_context *c, int n, sqlite3_value **v) { sq_call(c, 0, n, v); }
static void sq_x_step(sqlite3_context *c, int n, sqlite3_value **v) { sq_call(c, 1, n, v); }
static void sq_x_final(sqlite3_context *c) { sq_call(c, 2, 0, NULL); }
static void sq_x_value(sqlite3_context *c) { sq_call(c, 3, 0, NULL); }
static void sq_x_inverse(sqlite3_context *c, int n, sqlite3_value **v) { sq_call(c, 4, n, v); }
static void sq_x_destroy(void *p) { free(p); }

// flags: 1 deterministic, 2 direct only. kind: 0 a scalar function, 1 an
// aggregate, 2 an aggregate window function (with inverse).
double __kml_native_sqlite_create_function(double id, const char *name, double argc, double flags, double kind, void *inv, void *clo) {
    sqlite3 *db = sq_db_get(id);
    if (!db) return SQLITE_MISUSE;
    kml_sqlite_fn *fn = (kml_sqlite_fn *)calloc(1, sizeof *fn);
    fn->inv = inv;
    fn->clo = clo;
    int tf = SQLITE_UTF8;
    if ((int)flags & 1) tf |= SQLITE_DETERMINISTIC;
    if ((int)flags & 2) tf |= SQLITE_DIRECTONLY;
    if ((int)kind == 0)
        return sqlite3_create_function_v2(db, name, (int)argc, tf, fn, sq_x_func, NULL, NULL, sq_x_destroy);
    if ((int)kind == 1)
        return sqlite3_create_window_function(db, name, (int)argc, tf, fn, sq_x_step, sq_x_final, NULL, NULL, sq_x_destroy);
    return sqlite3_create_window_function(db, name, (int)argc, tf, fn, sq_x_step, sq_x_final, sq_x_value, sq_x_inverse, sq_x_destroy);
}

double __kml_native_sqlite_arg_count(void) { return sq_argc; }

double __kml_native_sqlite_arg_type(double i) {
    return (i >= 0 && i < sq_argc) ? sqlite3_value_type(sq_argv[(int)i]) : SQLITE_NULL;
}

double __kml_native_sqlite_arg_double(double i) {
    return (i >= 0 && i < sq_argc) ? sqlite3_value_double(sq_argv[(int)i]) : 0;
}

char *__kml_native_sqlite_arg_int(double i) {
    char buf[32];
    snprintf(buf, sizeof buf, "%lld", (i >= 0 && i < sq_argc) ? (long long)sqlite3_value_int64(sq_argv[(int)i]) : 0LL);
    return sq_string(buf, -1);
}

char *__kml_native_sqlite_arg_text(double i) {
    if (!(i >= 0 && i < sq_argc)) return sq_string("", 0);
    const unsigned char *t = sqlite3_value_text(sq_argv[(int)i]);
    return sq_string((const char *)t, sqlite3_value_bytes(sq_argv[(int)i]));
}

double __kml_native_sqlite_arg_bytes(double i) {
    if (!(i >= 0 && i < sq_argc)) return 0;
    sqlite3_value_blob(sq_argv[(int)i]);
    return sqlite3_value_bytes(sq_argv[(int)i]);
}

void __kml_native_sqlite_arg_blob(double i, uint8_t *out, int64_t len) {
    if (!(i >= 0 && i < sq_argc)) return;
    const void *b = sqlite3_value_blob(sq_argv[(int)i]);
    int n = sqlite3_value_bytes(sq_argv[(int)i]);
    if (b && n > 0) memcpy(out, b, (size_t)(n < len ? n : len));
}

void __kml_native_sqlite_result_null(void) { if (sq_ctx) sqlite3_result_null(sq_ctx); }
void __kml_native_sqlite_result_double(double v) { if (sq_ctx) sqlite3_result_double(sq_ctx, v); }
void __kml_native_sqlite_result_int(const char *dec) {
    if (sq_ctx) sqlite3_result_int64(sq_ctx, (sqlite3_int64)strtoll(dec, NULL, 10));
}
void __kml_native_sqlite_result_text(const char *s) {
    if (sq_ctx) sqlite3_result_text(sq_ctx, s, -1, SQLITE_TRANSIENT);
}
void __kml_native_sqlite_result_blob(const uint8_t *data, int64_t len) {
    if (sq_ctx) sqlite3_result_blob64(sq_ctx, len ? (const void *)data : (const void *)"", (sqlite3_uint64)len, SQLITE_TRANSIENT);
}
void __kml_native_sqlite_result_error(const char *msg) {
    if (sq_ctx) sqlite3_result_error(sq_ctx, msg, -1);
}

// ---- authorizer ----

// A callback's answer (the authorizer's, a changeset filter's or conflict
// handler's): callbacks return nothing, so it sets this.
static __thread double sq_cb_ret = 0;
void __kml_native_sqlite_cb_return(double v) { sq_cb_ret = v; }

static __thread void *sq_auth_inv[64];
static __thread void *sq_auth_clo[64];
static __thread const char *sq_auth_args[4];

static int sq_x_auth(void *p, int action, const char *a, const char *b, const char *c, const char *d) {
    int slot = (int)(intptr_t)p;
    sq_auth_args[0] = a;
    sq_auth_args[1] = b;
    sq_auth_args[2] = c;
    sq_auth_args[3] = d;
    sq_cb_ret = SQLITE_OK;
    ((void (*)(void *, double))sq_auth_inv[slot])(sq_auth_clo[slot], action);
    return (int)sq_cb_ret;
}

// The authorizer: onAction(action) answers SQLITE_OK / DENY / IGNORE
// through sqliteCallbackReturn and reads the action's four strings with
// sqliteAuthArg (null: sqliteAuthHas).
double __kml_native_sqlite_set_authorizer(double id, void *inv, void *clo) {
    sqlite3 *db = sq_db_get(id);
    int slot = (int)id;
    if (!db || slot >= 64) return SQLITE_MISUSE;
    sq_auth_inv[slot] = inv;
    sq_auth_clo[slot] = clo;
    return sqlite3_set_authorizer(db, sq_x_auth, (void *)(intptr_t)slot);
}

double __kml_native_sqlite_clear_authorizer(double id) {
    sqlite3 *db = sq_db_get(id);
    return db ? sqlite3_set_authorizer(db, NULL, NULL) : SQLITE_MISUSE;
}

_Bool __kml_native_sqlite_auth_has(double i) { return i >= 0 && i < 4 && sq_auth_args[(int)i] != NULL; }
char *__kml_native_sqlite_auth_arg(double i) {
    return sq_string(i >= 0 && i < 4 && sq_auth_args[(int)i] ? sq_auth_args[(int)i] : "", -1);
}

// ---- extensions, sessions, serialization (optional in the library) ----

double __kml_native_sqlite_enable_load_extension(double id, _Bool on) {
    sqlite3 *db = sq_db_get(id);
    int (*f)(sqlite3 *, int) = (int (*)(sqlite3 *, int))sq_sym("sqlite3_enable_load_extension");
    if (!db) return SQLITE_MISUSE;
    if (!f) return -1;
    return f(db, on ? 1 : 0);
}

double __kml_native_sqlite_load_extension(double id, const char *path, const char *entry) {
    sqlite3 *db = sq_db_get(id);
    int (*f)(sqlite3 *, const char *, const char *, char **) =
        (int (*)(sqlite3 *, const char *, const char *, char **))sq_sym("sqlite3_load_extension");
    if (!db) return SQLITE_MISUSE;
    if (!f) return -1;
    return f(db, path, entry && *entry ? entry : NULL, NULL);
}

typedef struct sqlite3_session kml_sqlite_session;
static __thread kml_sqlite_session **sq_ss = NULL;
static __thread int sq_ss_n = 0, sq_ss_cap = 0;

// A session on the attached database db (table: "" for all): its id, -1
// when the library has no sessions, or -2 - the SQLite code.
double __kml_native_sqlite_session_create(double id, const char *dbname, const char *table) {
    sqlite3 *db = sq_db_get(id);
    int (*create)(sqlite3 *, const char *, kml_sqlite_session **) =
        (int (*)(sqlite3 *, const char *, kml_sqlite_session **))sq_sym("sqlite3session_create");
    int (*attach)(kml_sqlite_session *, const char *) =
        (int (*)(kml_sqlite_session *, const char *))sq_sym("sqlite3session_attach");
    if (!create || !attach) return -1;
    if (!db) return -2 - SQLITE_MISUSE;
    kml_sqlite_session *s = NULL;
    int rc = create(db, dbname, &s);
    if (rc != SQLITE_OK) return -2 - rc;
    rc = attach(s, table && *table ? table : NULL);
    if (rc != SQLITE_OK) return -2 - rc;
    if (sq_ss_n == sq_ss_cap) {
        int nc = sq_ss_cap ? sq_ss_cap * 2 : 8;
        kml_sqlite_session **nt = (kml_sqlite_session **)calloc((size_t)nc, sizeof *nt);
        if (sq_ss) memcpy(nt, sq_ss, (size_t)sq_ss_cap * sizeof *nt);
        sq_ss = nt;
        sq_ss_cap = nc;
    }
    sq_ss[sq_ss_n] = s;
    return sq_ss_n++;
}

static __thread void *sq_ss_buf = NULL;
static __thread int sq_ss_len = 0;

// A session's changeset (0) or patchset (1): its length (the bytes are
// sqliteSessionBytes), or -1 - the SQLite code.
double __kml_native_sqlite_session_set(double sid, double which) {
    int i = (int)sid;
    if (i < 0 || i >= sq_ss_n || !sq_ss[i]) return -1 - SQLITE_MISUSE;
    int (*f)(kml_sqlite_session *, int *, void **) = (int (*)(kml_sqlite_session *, int *, void **))
        sq_sym(which == 0 ? "sqlite3session_changeset" : "sqlite3session_patchset");
    if (!f) return -1 - SQLITE_MISUSE;
    if (sq_ss_buf) sqlite3_free(sq_ss_buf);
    sq_ss_buf = NULL;
    sq_ss_len = 0;
    int rc = f(sq_ss[i], &sq_ss_len, &sq_ss_buf);
    if (rc != SQLITE_OK) return -1 - rc;
    return sq_ss_len;
}

void __kml_native_sqlite_session_bytes(uint8_t *out, int64_t len) {
    if (sq_ss_buf && sq_ss_len > 0) memcpy(out, sq_ss_buf, (size_t)(sq_ss_len < len ? sq_ss_len : len));
}

void __kml_native_sqlite_session_delete(double sid) {
    int i = (int)sid;
    void (*f)(kml_sqlite_session *) = (void (*)(kml_sqlite_session *))sq_sym("sqlite3session_delete");
    if (i < 0 || i >= sq_ss_n || !sq_ss[i]) return;
    if (f) f(sq_ss[i]);
    sq_ss[i] = NULL;
}

// Applying a changeset: onEvent(0 filter | 1 conflict, conflict kind) during
// the call, answering through sqliteCallbackReturn: a filter 1 to apply the
// table's changes (its name is sqliteAuthArg(0)), a conflict handler an
// SQLITE_CHANGESET_* action.
static __thread void *sq_ap_inv = NULL, *sq_ap_clo = NULL;
static __thread _Bool sq_ap_filter = 0;

static int sq_x_filter(void *p, const char *table) {
    (void)p;
    if (!sq_ap_filter) return 1;
    sq_auth_args[0] = table;
    sq_cb_ret = 1;
    ((void (*)(void *, double, double))sq_ap_inv)(sq_ap_clo, 0, 0);
    return (int)sq_cb_ret;
}

static int sq_x_conflict(void *p, int kind, void *iter) {
    (void)p;
    (void)iter;
    sq_cb_ret = 2; // SQLITE_CHANGESET_ABORT
    ((void (*)(void *, double, double))sq_ap_inv)(sq_ap_clo, 1, kind);
    return (int)sq_cb_ret;
}

double __kml_native_sqlite_apply_changeset(double id, const uint8_t *data, int64_t len, _Bool filter, void *inv, void *clo) {
    sqlite3 *db = sq_db_get(id);
    int (*f)(sqlite3 *, int, void *, int (*)(void *, const char *), int (*)(void *, int, void *), void *) =
        (int (*)(sqlite3 *, int, void *, int (*)(void *, const char *), int (*)(void *, int, void *), void *))sq_sym("sqlite3changeset_apply");
    if (!f) return -1;
    if (!db) return SQLITE_MISUSE;
    void *pi = sq_ap_inv, *pc = sq_ap_clo;
    _Bool pf = sq_ap_filter;
    sq_ap_inv = inv;
    sq_ap_clo = clo;
    sq_ap_filter = filter;
    int rc = f(db, (int)len, (void *)data, sq_x_filter, sq_x_conflict, NULL);
    sq_ap_inv = pi;
    sq_ap_clo = pc;
    sq_ap_filter = pf;
    return rc;
}

// serialize: the database's image length (bytes: sqliteSerializedBytes), or
// -1 when the library cannot.
static __thread unsigned char *sq_ser = NULL;
static __thread int64_t sq_ser_len = 0;

double __kml_native_sqlite_serialize(double id, const char *dbname) {
    sqlite3 *db = sq_db_get(id);
    unsigned char *(*f)(sqlite3 *, const char *, sqlite3_int64 *, unsigned int) =
        (unsigned char *(*)(sqlite3 *, const char *, sqlite3_int64 *, unsigned int))sq_sym("sqlite3_serialize");
    if (!db || !f) return -1;
    if (sq_ser) sqlite3_free(sq_ser);
    sqlite3_int64 n = 0;
    sq_ser = f(db, dbname, &n, 0);
    sq_ser_len = sq_ser ? n : 0;
    return sq_ser ? (double)n : -1;
}

void __kml_native_sqlite_serialized_bytes(uint8_t *out, int64_t len) {
    if (sq_ser && sq_ser_len > 0) memcpy(out, sq_ser, (size_t)(sq_ser_len < len ? sq_ser_len : len));
}

double __kml_native_sqlite_deserialize(double id, const char *dbname, const uint8_t *data, int64_t len) {
    sqlite3 *db = sq_db_get(id);
    int (*f)(sqlite3 *, const char *, unsigned char *, sqlite3_int64, sqlite3_int64, unsigned) =
        (int (*)(sqlite3 *, const char *, unsigned char *, sqlite3_int64, sqlite3_int64, unsigned))sq_sym("sqlite3_deserialize");
    if (!db || !f) return -1;
    unsigned char *copy = (unsigned char *)sqlite3_malloc64((sqlite3_uint64)(len ? len : 1));
    if (!copy) return SQLITE_NOMEM;
    if (len) memcpy(copy, data, (size_t)len);
    return f(db, dbname, copy, len, len, SQLITE_DESERIALIZE_FREEONCLOSE | SQLITE_DESERIALIZE_RESIZEABLE);
}

// ---- backup (sqlite.backup): one step of pages at a time on the loop ----

static __thread sqlite3_backup **sq_bk = NULL;
static __thread sqlite3 **sq_bk_dest = NULL;
static __thread int sq_bk_n = 0, sq_bk_cap = 0;

// Start copying the attached database source of id into the file path's
// database target: its id, or -1 - the SQLite code (message: sqliteOpenError).
double __kml_native_sqlite_backup_init(double id, const char *source, const char *path, const char *target) {
    sqlite3 *db = sq_db_get(id);
    if (!db) return -1 - SQLITE_MISUSE;
    sqlite3 *dest = NULL;
    int rc = sqlite3_open_v2(path, &dest, SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE | SQLITE_OPEN_URI, NULL);
    if (rc != SQLITE_OK) {
        free(sq_open_err);
        sq_open_err = strdup(dest ? sqlite3_errmsg(dest) : sqlite3_errstr(rc));
        if (dest) sqlite3_close_v2(dest);
        return -1 - rc;
    }
    sqlite3_backup *b = sqlite3_backup_init(dest, target, db, source);
    if (!b) {
        rc = sqlite3_extended_errcode(dest);
        free(sq_open_err);
        sq_open_err = strdup(sqlite3_errmsg(dest));
        sqlite3_close_v2(dest);
        return -1 - rc;
    }
    if (sq_bk_n == sq_bk_cap) {
        int nc = sq_bk_cap ? sq_bk_cap * 2 : 4;
        sqlite3_backup **nb = (sqlite3_backup **)calloc((size_t)nc, sizeof *nb);
        sqlite3 **nd = (sqlite3 **)calloc((size_t)nc, sizeof *nd);
        if (sq_bk) {
            memcpy(nb, sq_bk, (size_t)sq_bk_cap * sizeof *nb);
            memcpy(nd, sq_bk_dest, (size_t)sq_bk_cap * sizeof *nd);
        }
        sq_bk = nb;
        sq_bk_dest = nd;
        sq_bk_cap = nc;
    }
    sq_bk[sq_bk_n] = b;
    sq_bk_dest[sq_bk_n] = dest;
    return sq_bk_n++;
}

// Copy up to pages pages: the step's code (SQLITE_OK more, SQLITE_DONE).
double __kml_native_sqlite_backup_step(double bid, double pages) {
    int i = (int)bid;
    if (i < 0 || i >= sq_bk_n || !sq_bk[i]) return SQLITE_MISUSE;
    return sqlite3_backup_step(sq_bk[i], (int)pages);
}

double __kml_native_sqlite_backup_progress(double bid, double which) {
    int i = (int)bid;
    if (i < 0 || i >= sq_bk_n || !sq_bk[i]) return 0;
    return which == 0 ? sqlite3_backup_pagecount(sq_bk[i]) : sqlite3_backup_remaining(sq_bk[i]);
}

// Finish the backup: the code; the error text is sqliteOpenError.
double __kml_native_sqlite_backup_finish(double bid) {
    int i = (int)bid;
    if (i < 0 || i >= sq_bk_n || !sq_bk[i]) return SQLITE_MISUSE;
    int rc = sqlite3_backup_finish(sq_bk[i]);
    if (rc != SQLITE_OK) {
        free(sq_open_err);
        sq_open_err = strdup(sqlite3_errmsg(sq_bk_dest[i]));
    }
    sqlite3_close_v2(sq_bk_dest[i]);
    sq_bk[i] = NULL;
    sq_bk_dest[i] = NULL;
    return rc;
}
#endif
