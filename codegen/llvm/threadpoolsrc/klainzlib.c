// klainzlib.c — the zlib module's natives, in the pool's translation unit:
// node_zlib.cc's ZlibContext over the system libz (deflate/inflate, gzip
// and raw forms, UNZIP's header detection, dictionaries, parameters), a
// write that runs on a pool worker or synchronously, and crc32.
//
// A handle is an id into a table. A write fills the handle's write state
// (avail_out, avail_in after the call) and, on failure, its error (message,
// errno, Node's code), which the TS side reads after the call.
//
// Compiled in only for a program that uses zlib (-DKLAINPOOL_ZLIB, with
// -lz), so every other pool user keeps no libz dependency. Brotli (libbrotli)
// and Zstd (libzstd) contexts are the same handles (BrotliEncoderContext,
// BrotliDecoderContext, ZstdCompressContext, ZstdDecompressContext).

#ifdef KLAINPOOL_ZLIB
#include <zlib.h>
#ifndef _WIN32
#include <dlfcn.h>
#endif

// libbrotlienc, libbrotlidec and libzstd are loaded when a Brotli or Zstd
// stream is first made, so a program using zlib needs neither to build or
// run; the prototypes are the libraries' stable C ABI.
typedef struct BrotliEncoderState BrotliEncoderState;
typedef struct BrotliDecoderState BrotliDecoderState;
typedef struct BrotliEncoderPreparedDictionary BrotliEncoderPreparedDictionary;
typedef struct ZSTD_CCtx_s ZSTD_CCtx;
typedef struct ZSTD_DCtx_s ZSTD_DCtx;
typedef struct { const void *src; size_t size; size_t pos; } ZSTD_inBuffer;
typedef struct { void *dst; size_t size; size_t pos; } ZSTD_outBuffer;
#define BROTLI_OPERATION_FINISH 2
#define BROTLI_DECODER_RESULT_ERROR 0
#define BROTLI_DECODER_RESULT_NEEDS_MORE_INPUT 2
#define BROTLI_SHARED_DICTIONARY_RAW 0
#define BROTLI_MAX_QUALITY 11
#define ZSTD_reset_session_only 1

static struct {
    int tried, brotli, zstd;
    BrotliEncoderState *(*enc_create)(void *, void *, void *);
    void (*enc_destroy)(BrotliEncoderState *);
    int (*enc_set)(BrotliEncoderState *, int, uint32_t);
    int (*enc_stream)(BrotliEncoderState *, int, size_t *, const uint8_t **, size_t *, uint8_t **, size_t *);
    BrotliEncoderPreparedDictionary *(*enc_prepare)(int, size_t, const uint8_t *, int, void *, void *, void *);
    void (*enc_destroy_dict)(BrotliEncoderPreparedDictionary *);
    int (*enc_attach)(BrotliEncoderState *, const BrotliEncoderPreparedDictionary *);
    BrotliDecoderState *(*dec_create)(void *, void *, void *);
    void (*dec_destroy)(BrotliDecoderState *);
    int (*dec_set)(BrotliDecoderState *, int, uint32_t);
    int (*dec_stream)(BrotliDecoderState *, size_t *, const uint8_t **, size_t *, uint8_t **, size_t *);
    int (*dec_error)(const BrotliDecoderState *);
    const char *(*dec_error_string)(int);
    int (*dec_attach)(BrotliDecoderState *, int, size_t, const uint8_t *);
    ZSTD_CCtx *(*zc_create)(void);
    size_t (*zc_free)(ZSTD_CCtx *);
    size_t (*zc_set)(ZSTD_CCtx *, int, int);
    size_t (*zc_stream)(ZSTD_CCtx *, ZSTD_outBuffer *, ZSTD_inBuffer *, int);
    size_t (*zc_reset)(ZSTD_CCtx *, int);
    size_t (*zc_dict)(ZSTD_CCtx *, const void *, size_t);
    size_t (*zc_pledge)(ZSTD_CCtx *, unsigned long long);
    ZSTD_DCtx *(*zd_create)(void);
    size_t (*zd_free)(ZSTD_DCtx *);
    size_t (*zd_set)(ZSTD_DCtx *, int, int);
    size_t (*zd_stream)(ZSTD_DCtx *, ZSTD_outBuffer *, ZSTD_inBuffer *);
    size_t (*zd_reset)(ZSTD_DCtx *, int);
    size_t (*zd_dict)(ZSTD_DCtx *, const void *, size_t);
    unsigned (*z_is_error)(size_t);
    int (*z_error_code)(size_t);
    const char *(*z_error_string)(int);
} kz_lib;
static pthread_mutex_t kz_lib_lock = PTHREAD_MUTEX_INITIALIZER;

static void *kz_open(const char *const *names) {
#ifdef _WIN32
    (void)names;
    return NULL;
#else
    static const char *const dirs[] = { "", "/opt/homebrew/lib/", "/usr/local/lib/", NULL };
    for (int d = 0; dirs[d]; d++) {
        for (int i = 0; names[i]; i++) {
            char path[256];
            snprintf(path, sizeof path, "%s%s", dirs[d], names[i]);
            void *h = dlopen(path, RTLD_NOW | RTLD_LOCAL);
            if (h) return h;
        }
    }
    return NULL;
#endif
}

#ifdef _WIN32
#define KZ_SYM(h, n) NULL
#else
#define KZ_SYM(h, n) dlsym((h), (n))
#endif

static void kz_load(void) {
    pthread_mutex_lock(&kz_lib_lock);
    if (!kz_lib.tried) {
        kz_lib.tried = 1;
        static const char *const enc_names[] = { "libbrotlienc.dylib", "libbrotlienc.1.dylib", "libbrotlienc.so.1", "libbrotlienc.so", NULL };
        static const char *const dec_names[] = { "libbrotlidec.dylib", "libbrotlidec.1.dylib", "libbrotlidec.so.1", "libbrotlidec.so", NULL };
        static const char *const zstd_names[] = { "libzstd.dylib", "libzstd.1.dylib", "libzstd.so.1", "libzstd.so", NULL };
        void *e = kz_open(enc_names), *d = kz_open(dec_names), *z = kz_open(zstd_names);
        if (e && d) {
            *(void **)&kz_lib.enc_create = KZ_SYM(e, "BrotliEncoderCreateInstance");
            *(void **)&kz_lib.enc_destroy = KZ_SYM(e, "BrotliEncoderDestroyInstance");
            *(void **)&kz_lib.enc_set = KZ_SYM(e, "BrotliEncoderSetParameter");
            *(void **)&kz_lib.enc_stream = KZ_SYM(e, "BrotliEncoderCompressStream");
            *(void **)&kz_lib.enc_prepare = KZ_SYM(e, "BrotliEncoderPrepareDictionary");
            *(void **)&kz_lib.enc_destroy_dict = KZ_SYM(e, "BrotliEncoderDestroyPreparedDictionary");
            *(void **)&kz_lib.enc_attach = KZ_SYM(e, "BrotliEncoderAttachPreparedDictionary");
            *(void **)&kz_lib.dec_create = KZ_SYM(d, "BrotliDecoderCreateInstance");
            *(void **)&kz_lib.dec_destroy = KZ_SYM(d, "BrotliDecoderDestroyInstance");
            *(void **)&kz_lib.dec_set = KZ_SYM(d, "BrotliDecoderSetParameter");
            *(void **)&kz_lib.dec_stream = KZ_SYM(d, "BrotliDecoderDecompressStream");
            *(void **)&kz_lib.dec_error = KZ_SYM(d, "BrotliDecoderGetErrorCode");
            *(void **)&kz_lib.dec_error_string = KZ_SYM(d, "BrotliDecoderErrorString");
            *(void **)&kz_lib.dec_attach = KZ_SYM(d, "BrotliDecoderAttachDictionary");
            kz_lib.brotli = kz_lib.enc_create && kz_lib.enc_stream && kz_lib.dec_create && kz_lib.dec_stream;
        }
        if (z) {
            *(void **)&kz_lib.zc_create = KZ_SYM(z, "ZSTD_createCCtx");
            *(void **)&kz_lib.zc_free = KZ_SYM(z, "ZSTD_freeCCtx");
            *(void **)&kz_lib.zc_set = KZ_SYM(z, "ZSTD_CCtx_setParameter");
            *(void **)&kz_lib.zc_stream = KZ_SYM(z, "ZSTD_compressStream2");
            *(void **)&kz_lib.zc_reset = KZ_SYM(z, "ZSTD_CCtx_reset");
            *(void **)&kz_lib.zc_dict = KZ_SYM(z, "ZSTD_CCtx_loadDictionary");
            *(void **)&kz_lib.zc_pledge = KZ_SYM(z, "ZSTD_CCtx_setPledgedSrcSize");
            *(void **)&kz_lib.zd_create = KZ_SYM(z, "ZSTD_createDCtx");
            *(void **)&kz_lib.zd_free = KZ_SYM(z, "ZSTD_freeDCtx");
            *(void **)&kz_lib.zd_set = KZ_SYM(z, "ZSTD_DCtx_setParameter");
            *(void **)&kz_lib.zd_stream = KZ_SYM(z, "ZSTD_decompressStream");
            *(void **)&kz_lib.zd_reset = KZ_SYM(z, "ZSTD_DCtx_reset");
            *(void **)&kz_lib.zd_dict = KZ_SYM(z, "ZSTD_DCtx_loadDictionary");
            *(void **)&kz_lib.z_is_error = KZ_SYM(z, "ZSTD_isError");
            *(void **)&kz_lib.z_error_code = KZ_SYM(z, "ZSTD_getErrorCode");
            *(void **)&kz_lib.z_error_string = KZ_SYM(z, "ZSTD_getErrorString");
            kz_lib.zstd = kz_lib.zc_create && kz_lib.zc_stream && kz_lib.zd_create && kz_lib.zd_stream && kz_lib.z_is_error;
        }
    }
    pthread_mutex_unlock(&kz_lib_lock);
}

enum {
    KZ_NONE = 0, KZ_DEFLATE = 1, KZ_INFLATE = 2, KZ_GZIP = 3, KZ_GUNZIP = 4,
    KZ_DEFLATERAW = 5, KZ_INFLATERAW = 6, KZ_UNZIP = 7,
    KZ_BROTLI_DECODE = 8, KZ_BROTLI_ENCODE = 9, KZ_ZSTD_COMPRESS = 10, KZ_ZSTD_DECOMPRESS = 11,
};

#define KZ_IS_BROTLI(m) ((m) == KZ_BROTLI_DECODE || (m) == KZ_BROTLI_ENCODE)
#define KZ_IS_ZSTD(m) ((m) == KZ_ZSTD_COMPRESS || (m) == KZ_ZSTD_DECOMPRESS)

typedef struct {
    int used;
    int mode;
    z_stream strm;
    int level, window_bits, mem_level, strategy;
    int reject_garbage_after_end;
    int flush;
    int err;
    int init_done;
    int gzip_id_bytes_read;
    unsigned char *dict;
    size_t dict_len;
    // After a write: what is left of the input and output.
    int64_t avail_in, avail_out;
    // The last failure: its message (zlib's own when it has one) and errno.
    char msg[256];
    int err_errno;
    int has_error;
    int busy;          // a pooled write in flight
    int close_pending; // closed during that write: freed when it ends
    // Brotli and Zstd: the state, the stream's positions, the last result
    // and the failure's code name.
    BrotliEncoderState *benc;
    BrotliDecoderState *bdec;
    BrotliEncoderPreparedDictionary *bdict;
    ZSTD_CCtx *zc;
    ZSTD_DCtx *zd;
    const uint8_t *next_in;
    uint8_t *next_out;
    size_t in_left, out_left;
    int last_result;
    long long pledged;
    char code[96];
} kml_zctx;

// The handle table grows as streams are made; a closed handle's slot is
// reused.
static kml_zctx **zctx_tab;
static int zctx_cap;
static pthread_mutex_t zctx_lock = PTHREAD_MUTEX_INITIALIZER;

static kml_zctx *zctx_get(double id) {
    int i = (int)id;
    kml_zctx *c = NULL;
    pthread_mutex_lock(&zctx_lock);
    if (i >= 0 && i < zctx_cap) c = zctx_tab[i];
    pthread_mutex_unlock(&zctx_lock);
    return c;
}

static void zctx_free(kml_zctx *c) {
    if (c->benc) kz_lib.enc_destroy(c->benc);
    if (c->bdec) kz_lib.dec_destroy(c->bdec);
    if (c->bdict) kz_lib.enc_destroy_dict(c->bdict);
    if (c->zc) kz_lib.zc_free(c->zc);
    if (c->zd) kz_lib.zd_free(c->zd);
    if (c->init_done && !KZ_IS_BROTLI(c->mode) && !KZ_IS_ZSTD(c->mode)) {
        if (c->mode == KZ_DEFLATE || c->mode == KZ_GZIP || c->mode == KZ_DEFLATERAW) deflateEnd(&c->strm);
        else if (c->mode != KZ_NONE) inflateEnd(&c->strm);
    }
    free(c->dict);
    free(c);
}

// A new handle of the mode (Node's node_zlib_mode): its id.
double __kml_native_zlib_new(double mode) {
    kml_zctx *c = (kml_zctx *)calloc(1, sizeof *c);
    c->used = 1;
    c->mode = (int)mode;
    c->pledged = -1;
    if (KZ_IS_BROTLI(c->mode) || KZ_IS_ZSTD(c->mode)) kz_load();
    if (KZ_IS_BROTLI(c->mode) && !kz_lib.brotli) c->mode = KZ_NONE;
    if (KZ_IS_ZSTD(c->mode) && !kz_lib.zstd) c->mode = KZ_NONE;
    if (c->mode == KZ_BROTLI_ENCODE) c->benc = kz_lib.enc_create(NULL, NULL, NULL);
    if (c->mode == KZ_BROTLI_DECODE) c->bdec = kz_lib.dec_create(NULL, NULL, NULL);
    if (c->mode == KZ_ZSTD_COMPRESS) c->zc = kz_lib.zc_create();
    if (c->mode == KZ_ZSTD_DECOMPRESS) c->zd = kz_lib.zd_create();
    pthread_mutex_lock(&zctx_lock);
    int id = -1;
    for (int i = 0; i < zctx_cap; i++)
        if (!zctx_tab[i]) { id = i; break; }
    if (id < 0) {
        int ncap = zctx_cap ? zctx_cap * 2 : 16;
        zctx_tab = (kml_zctx **)realloc(zctx_tab, (size_t)ncap * sizeof *zctx_tab);
        for (int i = zctx_cap; i < ncap; i++) zctx_tab[i] = NULL;
        id = zctx_cap;
        zctx_cap = ncap;
    }
    zctx_tab[id] = c;
    pthread_mutex_unlock(&zctx_lock);
    return id;
}

static const char *zlib_code_name(int err) {
    switch (err) {
    case Z_OK: return "Z_OK";
    case Z_STREAM_END: return "Z_STREAM_END";
    case Z_NEED_DICT: return "Z_NEED_DICT";
    case Z_ERRNO: return "Z_ERRNO";
    case Z_STREAM_ERROR: return "Z_STREAM_ERROR";
    case Z_DATA_ERROR: return "Z_DATA_ERROR";
    case Z_MEM_ERROR: return "Z_MEM_ERROR";
    case Z_BUF_ERROR: return "Z_BUF_ERROR";
    case Z_VERSION_ERROR: return "Z_VERSION_ERROR";
    }
    return "Z_UNKNOWN_ERROR";
}

// ErrorForMessage: zlib's own message wins over the fallback.
static void zctx_error(kml_zctx *c, const char *message) {
    const char *m = c->strm.msg ? c->strm.msg : message;
    snprintf(c->msg, sizeof c->msg, "%s", m);
    c->err_errno = c->err;
    c->has_error = 1;
}

static int zctx_set_dictionary(kml_zctx *c) {
    if (!c->dict_len) return 0;
    c->err = Z_OK;
    switch (c->mode) {
    case KZ_DEFLATE:
    case KZ_DEFLATERAW:
        c->err = deflateSetDictionary(&c->strm, c->dict, (uInt)c->dict_len);
        break;
    case KZ_INFLATERAW:
        c->err = inflateSetDictionary(&c->strm, c->dict, (uInt)c->dict_len);
        break;
    }
    if (c->err != Z_OK) {
        zctx_error(c, "Failed to set dictionary");
        return -1;
    }
    return 0;
}

// InitZlib: the first write (or reset/params) sets the stream up. 1 when
// this call initialized it.
static int zctx_init_zlib(kml_zctx *c) {
    if (c->init_done) return 0;
    c->strm.msg = NULL;
    switch (c->mode) {
    case KZ_DEFLATE:
    case KZ_GZIP:
    case KZ_DEFLATERAW:
        c->err = deflateInit2(&c->strm, c->level, Z_DEFLATED, c->window_bits, c->mem_level, c->strategy);
        break;
    case KZ_INFLATE:
    case KZ_GUNZIP:
    case KZ_INFLATERAW:
    case KZ_UNZIP:
        c->err = inflateInit2(&c->strm, c->window_bits);
        break;
    default:
        c->err = Z_STREAM_ERROR;
    }
    if (c->err != Z_OK) {
        free(c->dict);
        c->dict = NULL;
        c->dict_len = 0;
        c->mode = KZ_NONE;
        return 1;
    }
    zctx_set_dictionary(c);
    c->init_done = 1;
    return 1;
}

// Init(level, windowBits, memLevel, strategy, rejectGarbageAfterEnd,
// dictionary): the parameters, applied at the first write.
void __kml_native_zlib_init(double id, double level, double window_bits, double mem_level, double strategy,
                            double reject_garbage, const unsigned char *dict, int64_t dict_len) {
    kml_zctx *c = zctx_get(id);
    if (!c) return;
    c->level = (int)level;
    c->window_bits = (int)window_bits;
    c->mem_level = (int)mem_level;
    c->strategy = (int)strategy;
    c->reject_garbage_after_end = reject_garbage != 0;
    c->flush = Z_NO_FLUSH;
    c->err = Z_OK;
    if (c->mode == KZ_GZIP || c->mode == KZ_GUNZIP) c->window_bits += 16;
    if (c->mode == KZ_UNZIP) c->window_bits += 32;
    if (c->mode == KZ_DEFLATERAW || c->mode == KZ_INFLATERAW) c->window_bits *= -1;
    free(c->dict);
    c->dict = NULL;
    c->dict_len = 0;
    if (dict && dict_len > 0) {
        c->dict = (unsigned char *)malloc((size_t)dict_len);
        memcpy(c->dict, dict, (size_t)dict_len);
        c->dict_len = (size_t)dict_len;
    }
}

static int zctx_reset_stream(kml_zctx *c) {
    if (zctx_init_zlib(c) && c->err != Z_OK) {
        zctx_error(c, "Failed to init stream before reset");
        return -1;
    }
    c->err = Z_OK;
    switch (c->mode) {
    case KZ_DEFLATE:
    case KZ_DEFLATERAW:
    case KZ_GZIP:
        c->err = deflateReset(&c->strm);
        break;
    case KZ_INFLATE:
    case KZ_INFLATERAW:
    case KZ_GUNZIP:
        c->err = inflateReset(&c->strm);
        break;
    }
    if (c->err != Z_OK) {
        zctx_error(c, "Failed to reset stream");
        return -1;
    }
    return zctx_set_dictionary(c);
}

static const char *zstd_code_name(int e) {
    static const struct { int code; const char *name; } names[] = {
        {0, "ZSTD_error_no_error"}, {1, "ZSTD_error_GENERIC"}, {10, "ZSTD_error_prefix_unknown"},
        {12, "ZSTD_error_version_unsupported"}, {14, "ZSTD_error_frameParameter_unsupported"},
        {16, "ZSTD_error_frameParameter_windowTooLarge"}, {20, "ZSTD_error_corruption_detected"},
        {22, "ZSTD_error_checksum_wrong"}, {24, "ZSTD_error_literals_headerWrong"},
        {30, "ZSTD_error_dictionary_corrupted"}, {32, "ZSTD_error_dictionary_wrong"},
        {34, "ZSTD_error_dictionaryCreation_failed"}, {40, "ZSTD_error_parameter_unsupported"},
        {41, "ZSTD_error_parameter_combination_unsupported"}, {42, "ZSTD_error_parameter_outOfBound"},
        {44, "ZSTD_error_tableLog_tooLarge"}, {46, "ZSTD_error_maxSymbolValue_tooLarge"},
        {48, "ZSTD_error_maxSymbolValue_tooSmall"}, {50, "ZSTD_error_stabilityCondition_notRespected"},
        {60, "ZSTD_error_stage_wrong"}, {62, "ZSTD_error_init_missing"}, {64, "ZSTD_error_memory_allocation"},
        {66, "ZSTD_error_workSpace_tooSmall"}, {70, "ZSTD_error_dstSize_tooSmall"},
        {72, "ZSTD_error_srcSize_wrong"}, {74, "ZSTD_error_dstBuffer_null"},
        {80, "ZSTD_error_noForwardProgress_destFull"}, {82, "ZSTD_error_noForwardProgress_inputEmpty"},
    };
    for (size_t i = 0; i < sizeof names / sizeof names[0]; i++)
        if (names[i].code == e) return names[i].name;
    return "ZSTD_error_GENERIC";
}

// A Brotli or Zstd step (their DoThreadPoolWork), and its GetErrorInfo.
static void zctx_work_other(kml_zctx *c) {
    c->has_error = 0;
    if (c->mode == KZ_BROTLI_ENCODE) {
        c->last_result = kz_lib.enc_stream(c->benc, c->flush, &c->in_left, &c->next_in,
                                                     &c->out_left, &c->next_out, NULL);
        if (!c->last_result) {
            snprintf(c->msg, sizeof c->msg, "Compression failed");
            snprintf(c->code, sizeof c->code, "ERR_BROTLI_COMPRESSION_FAILED");
            c->err_errno = -1;
            c->has_error = 1;
        }
    } else if (c->mode == KZ_BROTLI_DECODE) {
        c->last_result = kz_lib.dec_stream(c->bdec, &c->in_left, &c->next_in, &c->out_left, &c->next_out, NULL);
        if (c->last_result == BROTLI_DECODER_RESULT_ERROR) {
            int e = kz_lib.dec_error(c->bdec);
            snprintf(c->msg, sizeof c->msg, "Decompression failed");
            snprintf(c->code, sizeof c->code, "ERR_%s", kz_lib.dec_error_string(e));
            c->err_errno = (int)e;
            c->has_error = 1;
        } else if (c->flush == BROTLI_OPERATION_FINISH && c->last_result == BROTLI_DECODER_RESULT_NEEDS_MORE_INPUT) {
            snprintf(c->msg, sizeof c->msg, "unexpected end of file");
            snprintf(c->code, sizeof c->code, "Z_BUF_ERROR");
            c->err_errno = Z_BUF_ERROR;
            c->has_error = 1;
        }
    } else {
        ZSTD_inBuffer in = { c->next_in, c->in_left, 0 };
        ZSTD_outBuffer out = { c->next_out, c->out_left, 0 };
        size_t r = c->mode == KZ_ZSTD_COMPRESS
            ? kz_lib.zc_stream(c->zc, &out, &in, c->flush)
            : kz_lib.zd_stream(c->zd, &out, &in);
        c->next_in += in.pos;
        c->in_left -= in.pos;
        c->next_out += out.pos;
        c->out_left -= out.pos;
        if (kz_lib.z_is_error(r)) {
            int e = kz_lib.z_error_code(r);
            snprintf(c->msg, sizeof c->msg, "%s", kz_lib.z_error_string(e));
            snprintf(c->code, sizeof c->code, "%s", zstd_code_name(e));
            c->err_errno = (int)e;
            c->has_error = 1;
        }
    }
}

// DoThreadPoolWork: one deflate/inflate step over the set buffers.
static void zctx_work(kml_zctx *c) {
    if (KZ_IS_BROTLI(c->mode) || KZ_IS_ZSTD(c->mode)) {
        zctx_work_other(c);
        return;
    }
    if (zctx_init_zlib(c) && c->err != Z_OK) return;
    const Bytef *next = NULL;
    switch (c->mode) {
    case KZ_DEFLATE:
    case KZ_GZIP:
    case KZ_DEFLATERAW:
        c->err = deflate(&c->strm, c->flush);
        break;
    case KZ_UNZIP:
        if (c->strm.avail_in > 0) next = c->strm.next_in;
        if (c->gzip_id_bytes_read == 0) {
            if (next == NULL) goto inflate_step;
            if (*next == 0x1f) {
                c->gzip_id_bytes_read = 1;
                next++;
                if (c->strm.avail_in == 1) goto inflate_step;
            } else {
                c->mode = KZ_INFLATE;
                goto inflate_step;
            }
        }
        if (c->gzip_id_bytes_read == 1) {
            if (next == NULL) goto inflate_step;
            if (*next == 0x8b) {
                c->gzip_id_bytes_read = 2;
                c->mode = KZ_GUNZIP;
            } else {
                c->mode = KZ_INFLATE;
            }
        }
        /* fallthrough */
    case KZ_INFLATE:
    case KZ_GUNZIP:
    case KZ_INFLATERAW:
    inflate_step:
        c->err = inflate(&c->strm, c->flush);
        if (c->mode != KZ_INFLATERAW && c->err == Z_NEED_DICT && c->dict_len) {
            c->err = inflateSetDictionary(&c->strm, c->dict, (uInt)c->dict_len);
            if (c->err == Z_OK) c->err = inflate(&c->strm, c->flush);
            else if (c->err == Z_DATA_ERROR) c->err = Z_NEED_DICT;
        }
        // Another gzip member after this one's end: decode it too.
        while (c->strm.avail_in > 0 && c->mode == KZ_GUNZIP && c->err == Z_STREAM_END &&
               !c->reject_garbage_after_end && c->strm.next_in[0] != 0x00) {
            zctx_reset_stream(c);
            c->err = inflate(&c->strm, c->flush);
        }
        break;
    default:
        c->err = Z_STREAM_ERROR;
    }
}

// GetErrorInfo: whether the step failed, and why.
static void zctx_check(kml_zctx *c) {
    c->has_error = 0;
    switch (c->err) {
    case Z_OK:
    case Z_BUF_ERROR:
        if (c->strm.avail_out != 0 && c->flush == Z_FINISH) zctx_error(c, "unexpected end of file");
        return;
    case Z_STREAM_END:
        return;
    case Z_NEED_DICT:
        zctx_error(c, c->dict_len ? "Bad dictionary" : "Missing dictionary");
        return;
    default:
        zctx_error(c, "Zlib error");
    }
}

static void zctx_set_buffers(kml_zctx *c, double flush, const unsigned char *in, int64_t in_off, int64_t in_len,
                             unsigned char *out, int64_t out_off, int64_t out_len) {
    if (KZ_IS_BROTLI(c->mode) || KZ_IS_ZSTD(c->mode)) {
        c->next_in = in ? in + in_off : NULL;
        c->in_left = (size_t)in_len;
        c->next_out = out + out_off;
        c->out_left = (size_t)out_len;
        c->flush = (int)flush;
        return;
    }
    c->strm.next_in = (Bytef *)(in ? in + in_off : NULL);
    c->strm.avail_in = (uInt)in_len;
    c->strm.next_out = (Bytef *)(out + out_off);
    c->strm.avail_out = (uInt)out_len;
    c->flush = (int)flush;
}

static void zctx_after(kml_zctx *c) {
    if (KZ_IS_BROTLI(c->mode) || KZ_IS_ZSTD(c->mode)) {
        c->avail_in = (int64_t)c->in_left;
        c->avail_out = (int64_t)c->out_left;
        return;
    }
    c->avail_in = c->strm.avail_in;
    c->avail_out = c->strm.avail_out;
    zctx_check(c);
}

// writeSync: 0, or 1 when it failed (zlibError* read the failure).
double __kml_native_zlib_write_sync(double id, double flush, const unsigned char *in, int64_t in_size, double in_off,
                                    double in_len, unsigned char *out, int64_t out_size, double out_off, double out_len) {
    (void)in_size;
    (void)out_size;
    kml_zctx *c = zctx_get(id);
    if (!c) return 1;
    zctx_set_buffers(c, flush, in, (int64_t)in_off, (int64_t)in_len, out, (int64_t)out_off, (int64_t)out_len);
    zctx_work(c);
    zctx_after(c);
    return c->has_error;
}

static void work_zlib(kml_pool_item *it) {
    kml_zctx *c = (kml_zctx *)(intptr_t)it->a[1];
    if (!c) { it->err = 1; return; }
    zctx_work(c);
    zctx_after(c);
    it->err = c->has_error;
    pthread_mutex_lock(&zctx_lock);
    c->busy = 0;
    int pending = c->close_pending;
    pthread_mutex_unlock(&zctx_lock);
    if (pending) zctx_free(c);
}

// write: the same step on a pool worker; the callback gets (failed, 0).
void __kml_native_zlib_write(double id, double flush, const unsigned char *in, int64_t in_size, double in_off,
                             double in_len, unsigned char *out, int64_t out_size, double out_off, double out_len,
                             void *inv, void *clo) {
    (void)in_size;
    (void)out_size;
    kml_zctx *c = zctx_get(id);
    kml_pool_item *it = native_item(work_zlib, inv, clo);
    it->a[0] = (int64_t)id;
    it->a[1] = (int64_t)(intptr_t)c;
    if (c) {
        zctx_set_buffers(c, flush, in, (int64_t)in_off, (int64_t)in_len, out, (int64_t)out_off, (int64_t)out_len);
        pthread_mutex_lock(&zctx_lock);
        c->busy = 1;
        pthread_mutex_unlock(&zctx_lock);
    }
    native_submit(it);
}

// The write state after a write: 0 avail_out, 1 avail_in.
double __kml_native_zlib_state(double id, double which) {
    kml_zctx *c = zctx_get(id);
    if (!c) return 0;
    return (int)which == 0 ? (double)c->avail_out : (double)c->avail_in;
}

// The last failure: its message.
char *__kml_native_zlib_error_message(double id) {
    kml_zctx *c = zctx_get(id);
    const char *m = c ? c->msg : "";
    int64_t n = (int64_t)strlen(m);
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, m, (size_t)n + 1);
    __kml_str_finalize(out);
    return out;
}

// The last failure's errno (zlib's return code) and code name.
double __kml_native_zlib_error_errno(double id) {
    kml_zctx *c = zctx_get(id);
    return c ? c->err_errno : 0;
}

char *__kml_native_zlib_error_code(double id) {
    kml_zctx *c = zctx_get(id);
    const char *m = c && c->code[0] ? c->code : zlib_code_name(c ? c->err_errno : 0);
    int64_t n = (int64_t)strlen(m);
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, m, (size_t)n + 1);
    __kml_str_finalize(out);
    return out;
}

// params(level, strategy): 0, or 1 when it failed.
double __kml_native_zlib_params(double id, double level, double strategy) {
    kml_zctx *c = zctx_get(id);
    if (!c) return 1;
    c->has_error = 0;
    if (zctx_init_zlib(c) && c->err != Z_OK) {
        zctx_error(c, "Failed to init stream before set parameters");
        return 1;
    }
    c->err = Z_OK;
    if (c->mode == KZ_DEFLATE || c->mode == KZ_DEFLATERAW)
        c->err = deflateParams(&c->strm, (int)level, (int)strategy);
    if (c->err != Z_OK && c->err != Z_BUF_ERROR) {
        zctx_error(c, "Failed to set parameters");
        return 1;
    }
    return 0;
}

// reset(): 0, or 1 when it failed.
double __kml_native_zlib_reset(double id) {
    kml_zctx *c = zctx_get(id);
    if (!c) return 1;
    c->has_error = 0;
    c->code[0] = 0;
    if (c->mode == KZ_ZSTD_COMPRESS) return kz_lib.z_is_error(kz_lib.zc_reset(c->zc, ZSTD_reset_session_only)) ? 1 : 0;
    if (c->mode == KZ_ZSTD_DECOMPRESS) return kz_lib.z_is_error(kz_lib.zd_reset(c->zd, ZSTD_reset_session_only)) ? 1 : 0;
    if (c->mode == KZ_BROTLI_ENCODE) {
        kz_lib.enc_destroy(c->benc);
        c->benc = kz_lib.enc_create(NULL, NULL, NULL);
        return c->benc ? 0 : 1;
    }
    if (c->mode == KZ_BROTLI_DECODE) {
        kz_lib.dec_destroy(c->bdec);
        c->bdec = kz_lib.dec_create(NULL, NULL, NULL);
        return c->bdec ? 0 : 1;
    }
    return zctx_reset_stream(c) ? 1 : 0;
}

// close(): the stream ends and the handle is freed (when its pooled write,
// if any, ends).
void __kml_native_zlib_close(double id) {
    int i = (int)id;
    kml_zctx *c = NULL;
    int busy = 0;
    pthread_mutex_lock(&zctx_lock);
    if (i >= 0 && i < zctx_cap) c = zctx_tab[i];
    if (c) {
        zctx_tab[i] = NULL;
        busy = c->busy;
        if (busy) c->close_pending = 1;
    }
    pthread_mutex_unlock(&zctx_lock);
    if (c && !busy) zctx_free(c);
}

// Brotli/Zstd parameters (BrotliEncoderSetParameter, BrotliDecoderSetParameter,
// ZSTD_CCtx_setParameter, ZSTD_DCtx_setParameter): 0, or 1 when refused.
double __kml_native_zlib_set_param(double id, double key, double value) {
    kml_zctx *c = zctx_get(id);
    if (!c) return 1;
    uint32_t v = (uint32_t)(int64_t)value;
    switch (c->mode) {
    case KZ_BROTLI_ENCODE: return kz_lib.enc_set(c->benc, (int)key, v) ? 0 : 1;
    case KZ_BROTLI_DECODE: return kz_lib.dec_set(c->bdec, (int)key, v) ? 0 : 1;
    case KZ_ZSTD_COMPRESS: return kz_lib.z_is_error(kz_lib.zc_set(c->zc, (int)key, (int)(int32_t)v)) ? 1 : 0;
    case KZ_ZSTD_DECOMPRESS: return kz_lib.z_is_error(kz_lib.zd_set(c->zd, (int)key, (int)(int32_t)v)) ? 1 : 0;
    }
    return 1;
}

// A Brotli/Zstd stream's dictionary and (Zstd compression) pledged source
// size (-1: none): 0, or 1 when it failed.
double __kml_native_zlib_init_other(double id, const unsigned char *dict, int64_t dict_len, double pledged) {
    kml_zctx *c = zctx_get(id);
    if (!c) return 1;
    if ((c->mode == KZ_BROTLI_ENCODE && !c->benc) || (c->mode == KZ_BROTLI_DECODE && !c->bdec) ||
        (c->mode == KZ_ZSTD_COMPRESS && !c->zc) || (c->mode == KZ_ZSTD_DECOMPRESS && !c->zd))
        return 1;
    if (dict && dict_len > 0) {
        switch (c->mode) {
        case KZ_BROTLI_ENCODE:
            if (!kz_lib.enc_prepare || !kz_lib.enc_attach) return 1;
            c->bdict = kz_lib.enc_prepare(BROTLI_SHARED_DICTIONARY_RAW, (size_t)dict_len, dict,
                                                      BROTLI_MAX_QUALITY, NULL, NULL, NULL);
            if (!c->bdict || !kz_lib.enc_attach(c->benc, c->bdict)) return 1;
            break;
        case KZ_BROTLI_DECODE:
            c->dict = (unsigned char *)malloc((size_t)dict_len);
            memcpy(c->dict, dict, (size_t)dict_len);
            c->dict_len = (size_t)dict_len;
            if (!kz_lib.dec_attach || !kz_lib.dec_attach(c->bdec, BROTLI_SHARED_DICTIONARY_RAW, c->dict_len, c->dict)) return 1;
            break;
        case KZ_ZSTD_COMPRESS:
            if (kz_lib.z_is_error(kz_lib.zc_dict(c->zc, dict, (size_t)dict_len))) return 1;
            break;
        case KZ_ZSTD_DECOMPRESS:
            if (kz_lib.z_is_error(kz_lib.zd_dict(c->zd, dict, (size_t)dict_len))) return 1;
            break;
        }
    }
    if (c->mode == KZ_ZSTD_COMPRESS && pledged >= 0) {
        c->pledged = (long long)pledged;
        if (kz_lib.z_is_error(kz_lib.zc_pledge(c->zc, (unsigned long long)pledged))) return 1;
    }
    return 0;
}

// The linked zlib's version number (ZLIB_VERNUM).
double __kml_native_zlib_vernum(void) {
    return ZLIB_VERNUM;
}

// crc32(data, value): zlib's CRC-32.
double __kml_native_zlib_crc32(const unsigned char *data, int64_t len, double value) {
    uLong crc = (uLong)(uint32_t)value;
    return (double)crc32(crc, data, (uInt)len);
}
#endif // KLAINPOOL_ZLIB
