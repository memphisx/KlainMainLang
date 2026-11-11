/* promisebox.c — a promise boxed as `any` (emit_anyprom.go): recognizing its
 * wrapper object, and console.log's view of its state. The wrapper's header
 * word varies with the program, so its constructor stores it into hooks.c's
 * __kml_promise_box_hdr; it names __kml_promise_box_on, which links this
 * file, whenever it boxes a promise. Without one no object is a wrapper:
 * hooks.c's defaults answer. kml_layout.h is prepended by the compiler. */
typedef long long i64;

extern i64 __kml_promise_box_hdr;
extern unsigned char __kml_nb_tag(i64 w);
extern i64 __kml_nb_pay(i64 w);
extern i64 __kml_nb_pack(unsigned char tag, i64 pay);

const char __kml_promise_box_on = 1;

/* The wrapper: header, the promise, the routine boxing its value. */
typedef struct {
    i64 hdr;
    i64 *prom;
    i64 (*boxfn)(i64 *prom);
} PromBox;

/* The promise struct's state (0 pending, 1 fulfilled, 2 rejected), its
 * rejection payload and the tag word beside it. */
enum { P_STATE = 0, P_PAY = 2, P_TAG = 3 };

/* v's wrapper when v boxes a promise, else null. */
void *__kml_promise_wrapper_of(i64 v) {
    if (__kml_nb_tag(v) != KML_TAG_OBJECT) return 0;
    const PromBox *b = (const PromBox *)__kml_nb_pay(v);
    return b && b->hdr == __kml_promise_box_hdr ? (void *)b : 0;
}

/* For a wrapper o: its state, with the value or reason boxed into *out;
 * -1 for any other object. */
i64 __kml_promise_inspect_parts(void *o, i64 *out) {
    const PromBox *b = (const PromBox *)o;
    if (b->hdr != __kml_promise_box_hdr) return -1;
    i64 *p = b->prom;
    switch (p[P_STATE]) {
    case 1:
        *out = b->boxfn(p);
        return 1;
    case 2: {
        unsigned char tag = (unsigned char)p[P_TAG];
        i64 pay = p[P_PAY];
        *out = tag == KML_TAG_ERROR ? (pay | 1) : __kml_nb_pack(tag, pay);
        return 2;
    }
    default:
        return 0;
    }
}

