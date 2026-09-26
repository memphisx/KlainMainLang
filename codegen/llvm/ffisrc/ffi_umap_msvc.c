// ffi_umap_msvc.c — node:ffi's per-library name tables on Windows.
//
// Same C API as ffi_umap_stl.cc (see there for why the container's
// iteration order matters). Node's Windows build uses the MSVC STL, while
// this toolchain links mingw's libstdc++, so the real container would
// enumerate differently. This file ports the order-relevant parts of the
// MSVC STL's std::unordered_map (<xhash>) for unique keys:
//
//   - std::hash<std::string>: 64-bit FNV-1a over the bytes;
//   - a doubly-linked element list plus a bucket vector of inclusive
//     [lo, hi] list ranges, bucket = hash & (bucket_count - 1);
//   - 8 initial buckets, max_load_factor 1.0; on overflow the bucket count
//     grows 8x while below 512, else to the next power of two >= size;
//   - a new key is linked at the front of its bucket's range, or at the
//     list end when its bucket is empty;
//   - a rehash walks the list in order, leaving the first element of each
//     new bucket in place and splicing every later one to its bucket front.

#include <math.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct kml_umap_node {
  struct kml_umap_node* prev;
  struct kml_umap_node* next;
  uint64_t hash;
  char* key;
  size_t len;
  void* val;
} kml_umap_node;

typedef struct kml_umap {
  kml_umap_node head;   // list sentinel; head.next is begin()
  kml_umap_node** vec;  // 2 * nbuckets entries: [lo, hi] per bucket
  size_t nbuckets;
  size_t size;
} kml_umap;

#define KML_UMAP_MIN_BUCKETS 8

static uint64_t kml_umap_fnv1a(const char* key, size_t len) {
  uint64_t h = 14695981039346656037ULL;
  for (size_t i = 0; i < len; i++) {
    h ^= (unsigned char)key[i];
    h *= 1099511628211ULL;
  }
  return h;
}

static int kml_umap_eq(const kml_umap_node* n, const char* key, size_t len) {
  return n->len == len && memcmp(n->key, key, len) == 0;
}

static void kml_umap_reset_vec(kml_umap* u, size_t nb) {
  free(u->vec);
  u->vec = (kml_umap_node**)malloc(2 * nb * sizeof(kml_umap_node*));
  for (size_t i = 0; i < 2 * nb; i++) u->vec[i] = &u->head;
  u->nbuckets = nb;
}

kml_umap* kml_umap_new(void) {
  kml_umap* u = (kml_umap*)calloc(1, sizeof(kml_umap));
  u->head.prev = u->head.next = &u->head;
  kml_umap_reset_vec(u, KML_UMAP_MIN_BUCKETS);
  return u;
}

// Unlinks n and relinks it immediately before `before`.
static void kml_umap_splice_before(kml_umap_node* before, kml_umap_node* n) {
  n->prev->next = n->next;
  n->next->prev = n->prev;
  n->prev = before->prev;
  n->next = before;
  before->prev->next = n;
  before->prev = n;
}

// _Find_last for unique keys: the duplicate if present, else the node the
// new key is linked before.
static kml_umap_node* kml_umap_find_last(kml_umap* u, const char* key, size_t len, uint64_t hash,
                                         kml_umap_node** dup) {
  size_t b = (size_t)(hash & (uint64_t)(u->nbuckets - 1));
  kml_umap_node* where = u->vec[2 * b + 1];
  *dup = NULL;
  if (where == &u->head) return &u->head;
  kml_umap_node* lo = u->vec[2 * b];
  for (;;) {
    if (kml_umap_eq(where, key, len)) {
      *dup = where;
      return where->next;
    }
    if (where == lo) return where;
    where = where->prev;
  }
}

static size_t kml_umap_ceil_pow2(size_t n) {
  size_t p = 1;
  while (p < n) p <<= 1;
  return p;
}

// _Forced_rehash for unique keys (no two list elements compare equal).
static void kml_umap_forced_rehash(kml_umap* u, size_t buckets) {
  buckets = kml_umap_ceil_pow2(buckets);
  kml_umap_reset_vec(u, buckets);
  kml_umap_node* end = &u->head;
  kml_umap_node* inserted = u->head.next;
  while (inserted != end) {
    kml_umap_node* next = inserted->next;
    size_t b = (size_t)(inserted->hash & (uint64_t)(buckets - 1));
    kml_umap_node** lo = &u->vec[2 * b];
    kml_umap_node** hi = &u->vec[2 * b + 1];
    if (*lo == end) {
      *lo = inserted;
      *hi = inserted;
    } else {
      kml_umap_splice_before(*lo, inserted);
      *lo = inserted;
    }
    inserted = next;
  }
}

static size_t kml_umap_desired_grow(kml_umap* u, size_t for_size) {
  size_t old = u->nbuckets;
  size_t req = (size_t)ceilf((float)for_size / 1.0f);
  if (req < KML_UMAP_MIN_BUCKETS) req = KML_UMAP_MIN_BUCKETS;
  if (old >= req) return old;
  if (old < 512 && old * 8 >= req) return old * 8;
  return req;
}

int kml_umap_emplace(kml_umap* u, const char* key, size_t len, void* val) {
  uint64_t hash = kml_umap_fnv1a(key, len);
  kml_umap_node* dup;
  kml_umap_node* before = kml_umap_find_last(u, key, len, hash, &dup);
  if (dup) return 0;
  kml_umap_node* n = (kml_umap_node*)malloc(sizeof(kml_umap_node));
  n->hash = hash;
  n->key = (char*)malloc(len ? len : 1);
  memcpy(n->key, key, len);
  n->len = len;
  n->val = val;
  if (1.0f < (float)(u->size + 1) / (float)u->nbuckets) {
    kml_umap_forced_rehash(u, kml_umap_desired_grow(u, u->size + 1));
    before = kml_umap_find_last(u, key, len, hash, &dup);
  }
  // _Insert_new_node_before
  kml_umap_node* after = before->prev;
  u->size++;
  n->next = before;
  n->prev = after;
  after->next = n;
  before->prev = n;
  size_t b = (size_t)(hash & (uint64_t)(u->nbuckets - 1));
  kml_umap_node** lo = &u->vec[2 * b];
  kml_umap_node** hi = &u->vec[2 * b + 1];
  if (*lo == &u->head) {
    *lo = n;
    *hi = n;
  } else if (*lo == before) {
    *lo = n;
  } else if (*hi == after) {
    *hi = n;
  }
  return 1;
}

int kml_umap_find(kml_umap* u, const char* key, size_t len, void** out) {
  kml_umap_node* dup;
  kml_umap_find_last(u, key, len, kml_umap_fnv1a(key, len), &dup);
  if (!dup) return 0;
  *out = dup->val;
  return 1;
}

size_t kml_umap_size(kml_umap* u) { return u->size; }

void kml_umap_each(kml_umap* u, void (*cb)(void* ctx, const char* key, size_t len, void* val), void* ctx) {
  for (kml_umap_node* n = u->head.next; n != &u->head; n = n->next) cb(ctx, n->key, n->len, n->val);
}

void kml_umap_clear(kml_umap* u) {
  kml_umap_node* n = u->head.next;
  while (n != &u->head) {
    kml_umap_node* next = n->next;
    free(n->key);
    free(n);
    n = next;
  }
  u->head.prev = u->head.next = &u->head;
  u->size = 0;
  kml_umap_reset_vec(u, KML_UMAP_MIN_BUCKETS);
}
