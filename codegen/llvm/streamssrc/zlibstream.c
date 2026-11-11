/* zlibstream.c — CompressionStream / DecompressionStream (TDD-00097 Stage
 * 6; in C per TDD-00240). Each is an ordinary TransformStream context
 * whose transform/flush closures are these functions over a zlib z_stream.
 * -lz is linked only when one is constructed. kml_layout.h is prepended by
 * the compiler.
 *
 * zctx (KML_ZCTX_SIZE): z_stream*, readable rstream, mode (0 deflate, 1
 * inflate), finished flag.
 *
 * z_stream stays an opaque calloc (its size is re-verified at runtime by
 * passing it to the Init_ entry points, which reject a mismatch); the
 * fields the streaming loop needs are poked at their ABI offsets: next_in
 * +0, avail_in +8 (u32), then next_out/avail_out. zlib's uLong is 32-bit on
 * Windows (LLP64), which moves them from 24/32 to 16/24 and shrinks the
 * struct from 112 to 88 bytes (mingw-w64 zlib.h, measured). */
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

typedef long long i64;
typedef unsigned int u32;
typedef void *ptr;

#if defined(_WIN32)
#define Z_NEXT_OUT 16
#define Z_AVAIL_OUT 24
#define Z_STREAM_SIZE 88
#else
#define Z_NEXT_OUT 24
#define Z_AVAIL_OUT 32
#define Z_STREAM_SIZE 112
#endif
#define Z_NEXT_IN 0
#define Z_AVAIL_IN 8
#define Z_BLOCK 65536

extern const char *zlibVersion(void);
extern int deflateInit2_(ptr, int, int, int, int, int, const char *, int);
extern int inflateInit2_(ptr, int, const char *, int);
extern int deflate(ptr, int);
extern int inflate(ptr, int);
extern int deflateEnd(ptr);
extern int inflateEnd(ptr);

extern void __kml_rs_error(ptr rs, i64 err);
extern i64 __kml_rs_enqueue(ptr rs, i64 v0, i64 v1);
extern i64 __kml_rs_chunk_hdr(ptr data, i64 n);
extern long long __kml_any_view_bytes(long long word, char **data);
extern i64 __kml_obj_get(void *o, const char *k);
extern const char *__kml_obj_name(void *o);
extern double __kml_any_number_conv(long long w);
/* The constructor-name constant "Error" (one symbol per name, compared by
 * address): { i64 len, bytes }; a string value points at the bytes. Weak:
 * the program's linkonce copy, when it has one, is the same symbol. */
__attribute__((weak, aligned(8))) const struct { i64 len; char s[6]; }
    kml_ctorname_Error_def __asm__("__kml_ctorname.Error") = {KML_STR_ASCII_HDR(5), "Error"};
#define kml_ctorname_Error ((const char *)&kml_ctorname_Error_def)

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

typedef struct {
    ptr strm, readable;
    i64 mode, finished;
} zctx;
_Static_assert(sizeof(zctx) == KML_ZCTX_SIZE, "zctx layout");

static const struct {
    i64 len;
    char s[18];
} zmsg = {KML_STR_ASCII_HDR(17), "zlib stream error"};

/* The error object { type-id flag, message, name }. */
typedef struct {
    i64 kind;
    const char *msg, *name;
} errobj;

/* (mode, windowBits) -> zctx, the readable patched in by the construction
 * site. Null when zlib rejects the init. */
ptr __kml_zs_init(i64 mode, i64 wbits) {
    ptr strm = calloc(1, Z_STREAM_SIZE); /* scratch */
    int rc = mode != 0 ? inflateInit2_(strm, (int)wbits, zlibVersion(), Z_STREAM_SIZE)
                       : deflateInit2_(strm, 6, 8, (int)wbits, 8, 0, zlibVersion(), Z_STREAM_SIZE);
    if (rc != 0) return NULL;
    zctx *c = (zctx *)kml_alloc(KML_HEAP_WORDS, sizeof(zctx));
    c->strm = strm;
    c->readable = NULL;
    c->mode = mode;
    c->finished = 0;
    return c;
}

/* The zlib windowBits of a CompressionFormat (fmt, a string's characters),
 * 0 for a value that is none: deflate's for compression, inflate's (15+32
 * header auto-detection, raw unchanged) for decompression. */
i64 __kml_zs_wbits(const char *fmt, i64 decompress) {
    if (!fmt) return 0;
    if (strcmp(fmt, "gzip") == 0) return decompress ? 47 : 31;
    if (strcmp(fmt, "deflate") == 0) return decompress ? 47 : 15;
    if (strcmp(fmt, "deflate-raw") == 0) return -15;
    return 0;
}

/* Node's message for a format that is not a CompressionFormat. */
char *__kml_zs_format_error(const char *fmt, i64 decompress) {
    const char *ctor = decompress ? "DecompressionStream" : "CompressionStream";
    const char *f = fmt ? fmt : "";
    size_t n = strlen(ctor) + strlen(f) + 96;
    char *base = (char *)kml_alloc(KML_HEAP_RAW, n + 9);
    int len = snprintf(base + 8, n + 1,
                       "Failed to construct '%s': 1st argument '%s' is not a valid enum value of type CompressionFormat.",
                       ctor, f);
    *(long long *)base = len;
    return base + 8;
}

/* Run deflate/inflate over the current input until it is consumed,
 * enqueuing every produced output block. */
void __kml_zs_pump(ptr cp, int flush) {
    zctx *c = (zctx *)cp;
    ptr strm = c->strm;
    for (;;) {
        char *out = (char *)kml_alloc(KML_HEAP_RAW, Z_BLOCK);
        F(strm, Z_NEXT_OUT, ptr) = out;
        F(strm, Z_AVAIL_OUT, u32) = Z_BLOCK;
        int rc = c->mode != 0 ? inflate(strm, 0) : deflate(strm, flush);
        u32 ao = F(strm, Z_AVAIL_OUT, u32);
        i64 produced = Z_BLOCK - (i64)ao;
        if (produced > 0) __kml_rs_enqueue(c->readable, __kml_rs_chunk_hdr(out, produced), produced);
        if (rc == 1) {
            c->finished = 1;
            return;
        }
        if (rc < 0) {
            /* Z_BUF_ERROR (-5) just means "no progress possible right
             * now": stop without erroring; any other negative code errors
             * the readable side. */
            if (rc == -5) return;
            errobj *eo = (errobj *)kml_alloc(KML_HEAP_WORDS, sizeof(errobj));
            eo->kind = KML_ERROR_TYPE_FLAG;
            eo->msg = zmsg.s;
            eo->name = kml_ctorname_Error + 8;
            __kml_rs_error(c->readable, (i64)(size_t)eo | 1); /* the Error, boxed */
            return;
        }
        /* Keep looping while input remains or the output block filled up
         * (avail_out == 0 means there may be more to produce). */
        if (!(F(strm, Z_AVAIL_IN, u32) != 0 || ao == 0)) return;
    }
}

/* The bytes of an ArrayBuffer or SharedArrayBuffer held in `any` (an
 * object word at its host header, the {byteLength, data} header after it),
 * their count returned; -1 for any other value. */
static i64 ab_bytes(i64 w, char **data) {
    unsigned long long v = (unsigned long long)w;
    if ((v & 7) != 1 || v < 65536 || v >= (1ULL << 49)) return -1;
    i64 *cell = (i64 *)(v & ~7ULL);
    i64 h = cell[0] & ~KML_HDR_STATE_MASK;
    if (h != KML_AB_HDR && h != KML_SAB_HDR) return -1;
    *data = (char *)cell[2];
    return cell[1];
}

/* The bytes a DataView held in `any` covers: its buffer's, from its
 * byteOffset for its byteLength; -1 for any other value. */
static const struct { i64 len; char s[8]; } k_buffer = {KML_STR_ASCII_HDR(6), "buffer"};
static const struct { i64 len; char s[16]; } k_offset = {KML_STR_ASCII_HDR(10), "byteOffset"};
static const struct { i64 len; char s[16]; } k_length = {KML_STR_ASCII_HDR(10), "byteLength"};
static i64 dv_bytes(i64 w, char **data) {
    unsigned long long v = (unsigned long long)w;
    if ((v & 7) != 1 || v < 65536 || v >= (1ULL << 49)) return -1;
    void *o = (void *)(v & ~7ULL);
    i64 h = *(i64 *)o;
    if ((h & (0x7FFFLL << 32)) != (0x4B4DLL << 32) || (h & ((1LL << 49) | (1LL << 48)))) return -1;
    const char *name = __kml_obj_name(o);
    if (!name || strcmp(name, "DataView") != 0) return -1;
    char *base = NULL;
    if (ab_bytes(__kml_obj_get(o, k_buffer.s), &base) < 0) return -1;
    i64 off = (i64)__kml_any_number_conv(__kml_obj_get(o, k_offset.s));
    i64 len = (i64)__kml_any_number_conv(__kml_obj_get(o, k_length.s));
    *data = base + off;
    return len;
}

/* The UTF-8 bytes of a string held in `any`; -1 for any other value. */
static i64 str_bytes(i64 w, char **data) {
    unsigned long long v = (unsigned long long)w;
    if ((v & 7) != 0 || v < 65536 || v >= (1ULL << 49)) return -1;
    *data = (char *)v;
    return KML_STR_LEN(*data);
}

/* The TransformStream-compatible transform / flush closures. */
ptr __kml_zs_transform(ptr cp, i64 v0, i64 v1) {
    zctx *c = (zctx *)cp;
    if (c->finished) return NULL;
    /* A Uint8Array chunk is its header and length; one that came through
     * an untyped stream or writer is a boxed `any` and a zero length (an
     * empty typed chunk is zero bytes either way). */
    char *data = NULL;
    i64 n = v1;
    if (n > 0) {
        data = *(char **)(size_t)v0;
    } else {
        n = __kml_any_view_bytes(v0, &data);
        if (n < 0) n = ab_bytes(v0, &data);
        if (n < 0) n = dv_bytes(v0, &data);
        if (n < 0) n = str_bytes(v0, &data);
        if (n < 0) n = 0;
    }
    F(c->strm, Z_NEXT_IN, ptr) = data;
    F(c->strm, Z_AVAIL_IN, u32) = (u32)n;
    __kml_zs_pump(c, 0);
    return NULL;
}

ptr __kml_zs_flush(ptr cp) {
    zctx *c = (zctx *)cp;
    F(c->strm, Z_NEXT_IN, ptr) = NULL;
    F(c->strm, Z_AVAIL_IN, u32) = 0;
    if (c->mode != 0) {
        inflateEnd(c->strm);
    } else {
        /* Z_FINISH drains the deflate state; pump loops until Z_STREAM_END. */
        __kml_zs_pump(c, 4);
        deflateEnd(c->strm);
    }
    return NULL;
}
