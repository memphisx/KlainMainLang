/* kml_nanbox.h — the NaN-box encoding (runtime_nanbox.go) for every runtime
 * C file, prepended after kml_layout.h. A number is its double's bits (NaN
 * canonical) plus 2^49; the immediates are null 2, false 6, true 7,
 * undefined 10; any other word is a pointer with its kind in the low 3 bits
 * (0 a string). A tag is the emitter's kind: 0 an integer to box as a
 * number, 1 a number (its double's bits as pay), 2 string, 3 boolean,
 * 4 null, 5 undefined, 6–12 the pointer kinds. */
#ifndef KML_NANBOX_H
#define KML_NANBOX_H

static inline long long kml_nb_double(double d) {
    union { double d; unsigned long long u; } c;
    c.d = d;
    if (d != d) c.u = 0x7FF8000000000000ULL;
    return (long long)(c.u + (unsigned long long)KML_NB_DOUBLE_OFFSET);
}

/* The number a number word holds. */
static inline double kml_nb_number(long long w) {
    union { double d; unsigned long long u; } c;
    c.u = (unsigned long long)w - (unsigned long long)KML_NB_DOUBLE_OFFSET;
    return c.d;
}

static inline long long kml_nb_pack(long long tag, long long pay) {
    if (tag >= 6 && tag <= 12) return pay | (tag - 5);
    switch (tag) {
    case 0: return kml_nb_double((double)pay);
    case 1: {
        union { long long i; double d; } c;
        c.i = pay;
        return kml_nb_double(c.d);
    }
    case 2: return pay;
    case 3: return pay ? 7 : 6;
    case 4: return 2;
    default: return 10;
    }
}

static inline long long kml_nb_tag(long long w) {
    unsigned long long v = (unsigned long long)w;
    if (v >= (unsigned long long)KML_NB_DOUBLE_OFFSET) return 1;
    if (v < 65536) return v == 2 ? 4 : (v == 6 || v == 7) ? 3 : 5;
    return (v & 7) == 0 ? 2 : (long long)(v & 7) + 5;
}

static inline long long kml_nb_pay(long long w) {
    unsigned long long v = (unsigned long long)w;
    if (v >= (unsigned long long)KML_NB_DOUBLE_OFFSET) return (long long)(v - (unsigned long long)KML_NB_DOUBLE_OFFSET);
    if (v < 65536) return v == 7;
    return (long long)((v & 7) == 0 ? v : v & ~7ULL);
}

/* The pointer a pointer word holds. */
static inline void *kml_nb_ptr(long long w) {
    unsigned long long v = (unsigned long long)w;
    return (void *)((v & 7) == 0 ? v : v & ~7ULL);
}

static inline void kml_nb_decode(long long w, long long *tag, long long *pay) {
    *tag = kml_nb_tag(w);
    *pay = kml_nb_pay(w);
}

#endif
