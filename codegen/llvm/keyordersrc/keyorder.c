/* keyorder.c — an object's own-key order when it is not its layout's
 * (TDD-00230 P3.3). A static layout lists its fields in one order; a
 * JavaScript object lists its string keys in the order they were created:
 * a literal written in another order, a key a JSON input gave first, a key
 * deleted and assigned again. Such an object's order slot points at an
 * order block; a null slot is the layout's own order.
 *
 * Fields are named by their presence bit (0..63). A block is
 * [count, bit, bit, ...], capacity 64. A layout's sequence `seq` has the
 * same shape: its bits in its own order (integer-like keys first, as ES
 * orders them). Integer-like keys always list first, ascending, whatever
 * their creation order, so a reader takes them from `seq` and only the
 * string keys from the block (`ints` marks the integer-like bits). */

#include <stdint.h>
#include <string.h>

typedef unsigned char u8;
typedef long long i64;

static u8 *block_new(void) {
    u8 *o = (u8 *)kml_alloc(KML_HEAP_RAW, 65);
    o[0] = 0;
    return o;
}

static void block_remove(u8 *o, int bit) {
    int n = o[0], w = 1;
    for (int r = 1; r <= n; r++)
        if (o[r] != bit) o[w++] = o[r];
    o[0] = (u8)(w - 1);
}

/* The keys present in word, in the layout's order. */
static u8 *block_from_word(i64 word, const u8 *seq) {
    u8 *o = block_new();
    for (int i = 1; i <= seq[0]; i++)
        if ((unsigned long long)word >> seq[i] & 1) o[1 + o[0]++] = seq[i];
    return o;
}

/* Key `bit` was created on an object whose presence word was `word`: it
 * lists last. */
void __kml_keyorder_add(u8 **slot, i64 word, i64 bit, const u8 *seq) {
    u8 *o = *slot;
    if (!o) {
        int last = 1;
        /* Every present key after it in the layout: the layout's order
         * already ends with it, and no block is needed. */
        for (int i = 1; i <= seq[0]; i++) {
            int b = seq[i];
            if (b == bit) {
                for (int j = i + 1; j <= seq[0]; j++)
                    if ((unsigned long long)word >> seq[j] & 1) last = 0;
                break;
            }
        }
        if (last) return;
        o = block_from_word(word, seq);
        *slot = o;
    }
    block_remove(o, (int)bit);
    if (o[0] < 64) o[1 + o[0]++] = (u8)bit;
}

/* Key `bit` was deleted. */
void __kml_keyorder_del(u8 **slot, i64 bit) {
    if (*slot) block_remove(*slot, (int)bit);
}

/* A new object created with its keys in the order `tmpl` lists (a block):
 * its own copy, which later additions change. */
u8 *__kml_keyorder_copy(const u8 *tmpl) {
    if (!tmpl) return 0;
    u8 *o = block_new();
    memcpy(o, tmpl, (size_t)tmpl[0] + 1);
    return o;
}

/* The order a reader emits an object's present keys in: out gets their
 * bits, and the count is returned. present marks the keys the reader found
 * (by bit). Integer-like keys (ints) list first, in the layout's order; the
 * string keys follow in the block's order, or the layout's when order is
 * null; a present key the block lacks lists after them, in the layout's
 * order. */
i64 __kml_keyorder_perm(const u8 *order, const u8 *seq, i64 ints, i64 present, u8 *out) {
    int k = 0;
    unsigned long long p = (unsigned long long)present, im = (unsigned long long)ints, done = 0;
    for (int i = 1; i <= seq[0]; i++) {
        int b = seq[i];
        if ((im >> b & 1) && (p >> b & 1)) out[k++] = (u8)b, done |= 1ULL << b;
    }
    if (order)
        for (int i = 1; i <= order[0]; i++) {
            int b = order[i];
            if ((p >> b & 1) && !(done >> b & 1)) out[k++] = (u8)b, done |= 1ULL << b;
        }
    for (int i = 1; i <= seq[0]; i++) {
        int b = seq[i];
        if ((p >> b & 1) && !(done >> b & 1)) out[k++] = (u8)b, done |= 1ULL << b;
    }
    return k;
}

/* __kml_keyorder_perm as a position per key: rank[bit] for each present
 * key; the count is returned. */
i64 __kml_keyorder_ranks(const u8 *order, const u8 *seq, i64 ints, i64 present, u8 *rank) {
    u8 out[64];
    i64 n = __kml_keyorder_perm(order, seq, ints, present, out);
    for (i64 i = 0; i < n; i++) rank[out[i]] = (u8)i;
    return n;
}

/* A literal with a spread builds its order at run time: a fresh block, each
 * key pushed as the literal creates it (a key already there keeps its
 * place). */
u8 *__kml_keyorder_new(void) { return block_new(); }

void __kml_keyorder_push(u8 *o, i64 bit) {
    for (int i = 1; i <= o[0]; i++)
        if (o[i] == bit) return;
    if (o[0] < 64) o[1 + o[0]++] = (u8)bit;
}

/* A spread's keys, in the source object's order (its block src, or its
 * layout's seq; present marks the keys it has): map[b] is the target's bit
 * for source bit b, 255 for none. */
void __kml_keyorder_push_src(u8 *o, const u8 *src, const u8 *seq, i64 ints, i64 present, const u8 *map) {
    u8 out[64];
    i64 n = __kml_keyorder_perm(src, seq, ints, present, out);
    for (i64 i = 0; i < n; i++)
        if (map[out[i]] != 255) __kml_keyorder_push(o, map[out[i]]);
}
