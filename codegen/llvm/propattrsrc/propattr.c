/* propattr.c — the attributes Object.defineProperty gives a static
 * object's own property (TDD-00230 P3.3): writable, enumerable and
 * configurable. A static layout has no room for them; an object whose
 * attributes were ever defined has a record in a side map keyed by its
 * address, listing (name, attrs) for each property that is not plain (a
 * plain property is writable, enumerable and configurable). Programs that
 * never define a property never reach this file.
 *
 * attrs bits: 1 not writable, 2 not enumerable, 4 not configurable. */

#include <stdint.h>
#include <string.h>

typedef long long i64;

extern void *__kml_pattr_map_get(void);
extern i64 __kml_map_num_get(void *m, i64 key);
extern void __kml_map_num_set(void *m, i64 key, i64 val);

#define PA_NW 1
#define PA_NE 2
#define PA_NC 4

typedef struct {
    i64 n, cap;
    const char **names;
    unsigned char *attrs;
} PRec;

static PRec *rec_of(void *obj, int create) {
    void *m = __kml_pattr_map_get();
    PRec *r = (PRec *)(intptr_t)__kml_map_num_get(m, (i64)(intptr_t)obj);
    if (r || !create) return r;
    r = (PRec *)kml_alloc(KML_HEAP_RAW, sizeof(PRec));
    r->n = 0;
    r->cap = 0;
    r->names = 0;
    r->attrs = 0;
    __kml_map_num_set(m, (i64)(intptr_t)obj, (i64)(intptr_t)r);
    return r;
}

static i64 find(PRec *r, const char *name) {
    for (i64 i = 0; i < r->n; i++)
        if (r->names[i] == name || strcmp(r->names[i], name) == 0) return i;
    return -1;
}

/* The attrs of obj's property name (0 for a plain one). */
i64 __kml_pattr_attrs(void *obj, const char *name) {
    PRec *r = rec_of(obj, 0);
    if (!r) return 0;
    i64 i = find(r, name);
    return i < 0 ? 0 : r->attrs[i];
}

static void set_attrs(void *obj, const char *name, int a) {
    PRec *r = rec_of(obj, a != 0);
    if (!r) return;
    i64 i = find(r, name);
    if (i < 0) {
        if (!a) return;
        if (r->n == r->cap) {
            i64 cap = r->cap ? r->cap * 2 : 4;
            const char **nn = (const char **)kml_alloc(KML_HEAP_RAW, (size_t)cap * sizeof(char *));
            unsigned char *na = (unsigned char *)kml_alloc(KML_HEAP_RAW, (size_t)cap);
            if (r->n) {
                memcpy(nn, r->names, (size_t)r->n * sizeof(char *));
                memcpy(na, r->attrs, (size_t)r->n);
            }
            r->names = nn;
            r->attrs = na;
            r->cap = cap;
        }
        size_t len = strlen(name);
        char *copy = (char *)kml_alloc(KML_HEAP_RAW, len + 1);
        memcpy(copy, name, len + 1);
        r->names[r->n] = copy;
        r->attrs[r->n] = (unsigned char)a;
        r->n++;
        return;
    }
    r->attrs[i] = (unsigned char)a;
}

/* Object.defineProperty of a data property: has marks the attributes the
 * descriptor gives (1 writable, 2 enumerable, 4 configurable), val their
 * values (same bits); existed says the key was there, same that a given
 * value is SameValue to the current one (1 when none is given). As
 * ValidateAndApplyPropertyDescriptor: a new property takes false for each
 * attribute left out; a non-configurable one may not become configurable,
 * change its enumerability, become writable again, or change its value
 * once not writable. Returns 0, or 1 to reject (a TypeError). */
i64 __kml_pattr_define(void *obj, const char *name, i64 has, i64 val, i64 existed, i64 same) {
    int cur = existed ? (int)__kml_pattr_attrs(obj, name) : (PA_NW | PA_NE | PA_NC);
    if (existed && (cur & PA_NC)) {
        if ((has & 4) && (val & 4)) return 1;
        if ((has & 2) && (!(val & 2)) != !!(cur & PA_NE)) return 1;
        if (cur & PA_NW) {
            if ((has & 1) && (val & 1)) return 1;
            if (!same) return 1;
        }
    }
    int a = cur;
    if (has & 1) a = (val & 1) ? (a & ~PA_NW) : (a | PA_NW);
    if (has & 2) a = (val & 2) ? (a & ~PA_NE) : (a | PA_NE);
    if (has & 4) a = (val & 4) ? (a & ~PA_NC) : (a | PA_NC);
    set_attrs(obj, name, a);
    return 0;
}

/* The property was deleted: a key created again is a plain property. */
void __kml_pattr_forget(void *obj, const char *name) {
    set_attrs(obj, name, 0);
}
