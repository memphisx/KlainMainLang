/* buffill.c — Buffer.prototype.fill and Buffer.alloc(size, fill, encoding)
 * over boxed arguments, as Node's _fill reads and validates them (in C per
 * TDD-00240). Linked with dynjson.c (inspection, view bytes) and the Buffer
 * codecs when a program fills a Buffer (emitBufferFillBoxes). */

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

extern double __kml_any_tonum(long long v);
extern long long __kml_any_view_bytes(long long word, char **data);
extern char *__kml_inspect_opts(long long word, double depth, double compact, double sorted, double breakLength, double maxArrayLength, double customInspect);
extern char *__kml_received(long long word);
extern long long __kml_buf_decode_enc(const char *s, const char *enc, unsigned char **out);
extern int __kml_buf_is_encoding(const char *enc);

/* mirrors __kml_nb_tag / __kml_nb_pay (runtime_nanbox.go), as dynjson.c's */
static void nb_decode(long long word, long long *tag, long long *pay) {
    unsigned long long v = (unsigned long long)word;
    if (v >= (1ULL << 49)) {
        *tag = 1;
        *pay = (long long)(v - (1ULL << 49));
        return;
    }
    if (v < 65536) {
        switch (v) {
        case 2: *tag = 4; *pay = 0; return;
        case 6: *tag = 3; *pay = 0; return;
        case 7: *tag = 3; *pay = 1; return;
        default: *tag = 5; *pay = 0; return;
        }
    }
    static const long long kindTag[8] = {2, 6, 7, 8, 9, 10, 11, 12};
    *tag = kindTag[v & 7];
    *pay = (long long)((v & 7) == 0 ? v : v & ~7ULL);
}

/* A header string of the text in buf. */
static char *hdr_str(const char *buf) {
    long long n = (long long)strlen(buf);
    char *base = (char *)kml_alloc(KML_HEAP_RAW, (size_t)n + 9);
    *(long long *)base = n;
    memcpy(base + 8, buf, (size_t)n + 1);
    return base + 8;
}


/* Node's validateOffset(word, name, 0, len) into *out; 0, or the error kind
   with the message in *msg. len < 0 is the offset's own bound,
   Number.MAX_SAFE_INTEGER. */
static long long fill_offset(long long word, const char *name, long long len, long long *out, char **msg) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    char buf[256];
    if (tag != 0 && tag != 1) {
        char *r = __kml_received(word);
        snprintf(buf, sizeof buf, "The \"%s\" argument must be of type number.%s", name, r);
        kml_free(KML_HEAP_RAW, r - 8);
        *msg = hdr_str(buf);
        return 1; /* TypeError ERR_INVALID_ARG_TYPE */
    }
    double d = __kml_any_tonum(word);
    if (d != floor(d) || isinf(d)) {
        char *shown = __kml_inspect_opts(word, 2, 3, 0, 80, 100, 1);
        snprintf(buf, sizeof buf, "The value of \"%s\" is out of range. It must be an integer. Received %s", name, shown);
        kml_free(KML_HEAP_RAW, shown - 8);
        *msg = hdr_str(buf);
        return 2; /* RangeError ERR_OUT_OF_RANGE */
    }
    if (len < 0) len = 9007199254740991LL;
    if (d < 0 || d > (double)len) {
        char *shown = __kml_inspect_opts(word, 2, 3, 0, 80, 100, 1);
        snprintf(buf, sizeof buf, "The value of \"%s\" is out of range. It must be >= 0 && <= %lld. Received %s", name, len, shown);
        kml_free(KML_HEAP_RAW, shown - 8);
        *msg = hdr_str(buf);
        return 2;
    }
    *out = (long long)d;
    return 0;
}

/* __kml_buf_fill_any: Buffer.prototype.fill(value, offset, end, encoding)
   over the buffer's len bytes at data, with argc boxed arguments in args
   (the value first), as Node's _fill reads them: a string value takes a
   string offset or end as its encoding, and is its bytes in it; a typed
   array or Buffer is its bytes; anything else is ToNumber's low byte.
   Returns 0, or the error to throw: 1 TypeError ERR_INVALID_ARG_TYPE,
   2 RangeError ERR_OUT_OF_RANGE, 3 TypeError ERR_UNKNOWN_ENCODING, 4 TypeError
   ERR_INVALID_ARG_VALUE, with its message in *msg. */
long long __kml_buf_fill_any(unsigned char *data, long long len, long long argc, long long *args, char **msg) {
    long long undef = 10; /* the undefined box */
    long long value = args[0];
    long long offset = argc > 1 ? args[1] : undef, end = argc > 2 ? args[2] : undef, encoding = argc > 3 ? args[3] : undef;
    long long vtag, vpay, t, p;
    nb_decode(value, &vtag, &vpay);
    const char *enc = NULL;
    char buf[256];
    if (vtag == 2) {
        nb_decode(offset, &t, &p);
        long long et, ep;
        nb_decode(end, &et, &ep);
        if (t == 5 || t == 2) {
            encoding = offset;
            offset = undef;
            end = undef;
        } else if (et == 2) {
            encoding = end;
            end = undef;
        }
        nb_decode(encoding, &t, &p);
        if (t == 2) {
            enc = (const char *)p;
            if (!__kml_buf_is_encoding(enc)) {
                snprintf(buf, sizeof buf, "Unknown encoding: %s", enc);
                *msg = hdr_str(buf);
                return 3;
            }
        } else if (t != 5) {
            char *r = __kml_received(encoding);
            snprintf(buf, sizeof buf, "The \"encoding\" argument must be of type string.%s", r);
            kml_free(KML_HEAP_RAW, r - 8);
            *msg = hdr_str(buf);
            return 1;
        }
    }
    long long start = 0, stop = len;
    nb_decode(offset, &t, &p);
    if (t != 5) {
        long long k = fill_offset(offset, "offset", -1, &start, msg);
        if (k) return k;
        nb_decode(end, &t, &p);
        if (t != 5 && (k = fill_offset(end, "end", len, &stop, msg))) return k;
        if (start >= stop) return 0;
    }
    if (vtag == 2 && KML_STR_LEN(vpay) > 0) {
        unsigned char *bytes = NULL;
        long long n = __kml_buf_decode_enc((const char *)vpay, enc, &bytes);
        if (n <= 0) {
            kml_free(KML_HEAP_RAW, bytes);
            char *shown = __kml_inspect_opts(value, 2, 3, 0, 80, 100, 1);
            snprintf(buf, sizeof buf, "The argument 'value' is invalid. Received %s", shown);
            kml_free(KML_HEAP_RAW, shown - 8);
            *msg = hdr_str(buf);
            return 4;
        }
        for (long long i = start, k = 0; i < stop; i++) {
            data[i] = bytes[k];
            if (++k == n) k = 0;
        }
        kml_free(KML_HEAP_RAW, bytes);
        return 0;
    }
    if (vtag != 2) {
        char *src = NULL;
        long long n = __kml_any_view_bytes(value, &src);
        if (n == 0) {
            char *shown = __kml_inspect_opts(value, 2, 3, 0, 80, 100, 1);
            snprintf(buf, sizeof buf, "The argument 'value' is invalid. Received %s", shown);
            kml_free(KML_HEAP_RAW, shown - 8);
            *msg = hdr_str(buf);
            return 4;
        }
        if (n > 0) {
            for (long long i = start, k = 0; i < stop; i++) {
                data[i] = (unsigned char)src[k];
                if (++k == n) k = 0;
            }
            return 0;
        }
    }
    /* A number (an empty string is 0): its ToUint8. */
    double d = vtag == 2 ? 0 : __kml_any_tonum(value);
    unsigned char b = 0;
    if (isfinite(d)) {
        double m = fmod(trunc(d), 256.0);
        if (m < 0) m += 256.0;
        b = (unsigned char)m;
    }
    memset(data + start, b, (size_t)(stop - start));
    return 0;
}

