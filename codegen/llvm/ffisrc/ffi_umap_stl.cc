// ffi_umap_stl.cc — node:ffi's per-library name tables on POSIX targets.
//
// Node keeps DynamicLibrary's `symbols` / `functions` records in a
// std::unordered_map<std::string, …>, and `library.symbols` /
// `library.functions` / `getSymbols()` / no-arg `getFunctions()` enumerate
// that map's iteration order. The order is not specified by the language —
// it is whatever the platform's C++ standard library produces. Node's
// macOS build uses libc++ and its Linux build libstdc++; so does clang++ on
// those hosts. Backing the table with the very same container therefore
// reproduces Node's enumeration order by construction (hash function,
// bucket growth policy, and insertion/rehash splicing all included), rather
// than by re-deriving three hash-table implementations by hand. Windows
// (Node is built against the MSVC STL, this toolchain is mingw/libstdc++)
// uses the hand port in ffi_umap_msvc.c behind the same C API.

#include <cstddef>
#include <cstdint>
#include <string>
#include <unordered_map>

struct kml_umap {
  std::unordered_map<std::string, void*> m;
};

extern "C" {

kml_umap* kml_umap_new(void) { return new kml_umap(); }

// Inserts key → val unless key is present (std::unordered_map::emplace
// semantics, which is what Node uses). Returns 1 when inserted.
int kml_umap_emplace(kml_umap* u, const char* key, size_t len, void* val) {
  return u->m.emplace(std::string(key, len), val).second ? 1 : 0;
}

// Looks key up; returns 1 and stores the value in *out when present.
int kml_umap_find(kml_umap* u, const char* key, size_t len, void** out) {
  auto it = u->m.find(std::string(key, len));
  if (it == u->m.end()) return 0;
  *out = it->second;
  return 1;
}

size_t kml_umap_size(kml_umap* u) { return u->m.size(); }

// Visits every entry in the container's iteration order.
void kml_umap_each(kml_umap* u, void (*cb)(void* ctx, const char* key, size_t len, void* val), void* ctx) {
  for (auto& kv : u->m) cb(ctx, kv.first.data(), kv.first.size(), kv.second);
}

void kml_umap_clear(kml_umap* u) { u->m.clear(); }

}  // extern "C"
