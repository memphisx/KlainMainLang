package llvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Layout contracts between the IR this package emits and the C sidecars it
// links (ADR-01060). dynjson.c mirrors, by hand, the any-array box
// (anyArrayBoxTy), the array header (arrayHeaderTy) and the NaN-box encoding
// (runtime_nanbox.go). Drift between the two used to be found at runtime, by
// a wrong value; these tests find it at `go test`.

// llvmStructOffsets computes field offsets and the size of a literal LLVM
// struct type such as "{ ptr, i8, i8 }" under the usual C-ABI rules (each
// field naturally aligned, size rounded up to the largest alignment). Only
// the scalar field types the sidecar contracts use are supported.
func llvmStructOffsets(t *testing.T, ty string) (offsets []int, size int) {
	t.Helper()
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(ty), "{"), "}"))
	maxAlign := 1
	off := 0
	for _, f := range strings.Split(inner, ",") {
		var sz int
		switch strings.TrimSpace(f) {
		case "ptr", "i64", "double":
			sz = 8
		case "i32":
			sz = 4
		case "i16":
			sz = 2
		case "i8", "i1":
			sz = 1
		default:
			t.Fatalf("llvmStructOffsets: unsupported field %q in %q", f, ty)
		}
		if sz > maxAlign {
			maxAlign = sz
		}
		if off%sz != 0 {
			off += sz - off%sz
		}
		offsets = append(offsets, off)
		off += sz
	}
	if off%maxAlign != 0 {
		off += maxAlign - off%maxAlign
	}
	return offsets, off
}

// TestDynjsonBoxLayoutMatchesEmitter compiles a probe that includes dynjson.c
// and prints the C side's KjBox offsets/size, the array-header reads, and the
// NaN-box encoder's answers, and checks each against this package's constants.
func TestDynjsonBoxLayoutMatchesEmitter(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not found in PATH")
	}
	src, err := filepath.Abs(filepath.Join("dynjsonsrc", "dynjson.c"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe.c")
	// The sidecar's externs are satisfied by stubs: the probe never calls them.
	probeSrc := `#include <stddef.h>
#include <stdio.h>
#include "` + filepath.ToSlash(src) + `"
void __kml_dtoa(char *buf, double v) { (void)buf; (void)v; }
long long __kml_toprimitive(long long v, _Bool h) { (void)h; return v; }
double __kml_any_tonum(long long v) { (void)v; return 0; }
long long __kml_dynobj_get(char *o, const char *k) { (void)o; (void)k; return 10; }
void *__kml_inspect_begin(long long d, long long n) { (void)d; (void)n; return 0; }
void __kml_inspect_push(void *l, char *e) { (void)l; (void)e; }
void __kml_inspect_push_more(void *l, long long r) { (void)l; (void)r; }
char *__kml_inspect_end(void *l, const char *o, const char *c, long long i, long long d, long long a, long long n) { (void)l; (void)o; (void)c; (void)i; (void)d; (void)a; (void)n; return 0; }
char *__kml_inspect_quote(const char *s) { (void)s; return 0; }
char *__kml_fn_inspect_dyn(void **r, long long d) { (void)r; (void)d; return 0; }
char *__kml_boxed_bigint_str(void *c) { (void)c; return 0; }
int main(void) {
    printf("kjbox %zu %zu %zu %zu %zu %zu\n", sizeof(KjBox), offsetof(KjBox, hdr), offsetof(KjBox, kind),
           offsetof(KjBox, typed), offsetof(KjBox, ikind), offsetof(KjBox, ityped));
    struct { void *data; long long len; } hdr = { (void *)0x10, 7 };
    KjBox b = { (char *)&hdr, 0, 0, -1, 0 };
    printf("hdr %lld %p\n", kj_len(&b), (void *)kj_data(&b));
    printf("nb %lld %lld %lld %lld %lld\n", nb_pack(5, 0), nb_pack(4, 0), nb_pack(3, 0), nb_pack(3, 1), nb_double(1.5));
    printf("kinds %d %d %d %d %d\n", KJ_F64, KJ_STRING, KJ_ANY, KJ_ARRAY, KJ_I32);
    return 0;
}
`
	if err := os.WriteFile(probe, []byte(probeSrc), 0644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "probe")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command(clang, "-w", "-o", exe, probe).CombinedOutput(); err != nil {
		t.Fatalf("clang: %v\n%s", err, out)
	}
	out, err := exec.Command(exe).Output()
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	got := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		got[f[0]] = f[1:]
	}

	// anyArrayBoxTy ↔ KjBox: same size, same field offsets, in order.
	offs, size := llvmStructOffsets(t, anyArrayBoxTy)
	want := []string{strconv.Itoa(size)}
	for _, o := range offs {
		want = append(want, strconv.Itoa(o))
	}
	if strings.Join(got["kjbox"], " ") != strings.Join(want, " ") {
		t.Errorf("KjBox layout: C says %v, anyArrayBoxTy %q gives %v", got["kjbox"], anyArrayBoxTy, want)
	}
	// The 16-byte malloc in boxAnyArray must hold the whole box.
	if size > 16 {
		t.Errorf("anyArrayBoxTy is %d bytes; boxAnyArray mallocs 16", size)
	}
	// arrayHeaderTy ↔ kj_data/kj_len: data at field 0, len at field 1.
	hOffs, _ := llvmStructOffsets(t, arrayHeaderTy)
	if hOffs[0] != 0 || hOffs[1] != 8 || got["hdr"][0] != "7" || got["hdr"][1] != "0x10" && got["hdr"][1] != "0000000000000010" {
		t.Errorf("array header: C read len=%s data=%s; arrayHeaderTy %q offsets %v", got["hdr"][0], got["hdr"][1], arrayHeaderTy, hOffs)
	}
	// NaN-box immediates and the double encoding.
	one5 := int64(0x3FF8000000000000) + nbDoubleOffset
	wantNb := []string{strconv.Itoa(nbUndefined), strconv.Itoa(nbNull), strconv.Itoa(nbFalse), strconv.Itoa(nbTrue), strconv.FormatInt(one5, 10)}
	if strings.Join(got["nb"], " ") != strings.Join(wantNb, " ") {
		t.Errorf("nb_pack: C says %v, runtime_nanbox.go says %v", got["nb"], wantNb)
	}
	// Element kinds: arrayElemKind's numbers are the C enum's.
	f64, _ := arrayElemKind(TypeF64)
	str, _ := arrayElemKind(TypePtr)
	anyK, _ := arrayElemKind(TypeAny)
	i32, _ := arrayElemKind(TypeI32)
	wantKinds := []string{strconv.Itoa(f64), strconv.Itoa(str), strconv.Itoa(anyK), "13", strconv.Itoa(i32)}
	if strings.Join(got["kinds"], " ") != strings.Join(wantKinds, " ") {
		t.Errorf("KJ_* kinds: C says %v, arrayElemKind says %v", got["kinds"], wantKinds)
	}
}

// sidecarExterns is the declared runtime dependency of each embedded C
// sidecar: every `extern` it references must be DEFINED in the IR by the
// ensure* calls of the Go function that links it (the C object references
// them unconditionally, so a missing one is a link error in any program
// that pulls the sidecar in). The test below keeps this list equal to what
// the C file actually declares, so adding an extern forces updating the
// ensure wiring named here.
var sidecarExterns = map[string]struct {
	provider string   // the Go ensure function that links the file
	externs  []string // symbols the C file declares extern, sorted
}{
	"dynjsonsrc/dynjson.c": {
		provider: "ensureDynJSONC (dynjson_c.go): ensureDtoa, ensureAnyOps, ensureAnyToPrimitive, ensureDynObj, ensureInspectReduce, ensureFnMeta, ensureBoxedBigIntHooks",
		externs:  []string{"__kml_any_tonum", "__kml_boxed_bigint_str", "__kml_dtoa", "__kml_dynobj_get", "__kml_fn_inspect_dyn", "__kml_inspect_begin", "__kml_inspect_end", "__kml_inspect_push", "__kml_inspect_push_more", "__kml_inspect_quote", "__kml_toprimitive"},
	},
}

var externDeclRe = regexp.MustCompile(`(?m)^extern\s+[^;]*?\b(__kml_\w+)\s*\(`)

func TestSidecarExternsDeclared(t *testing.T) {
	for file, want := range sidecarExterns {
		data, err := os.ReadFile(filepath.FromSlash(file))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var got []string
		for _, m := range externDeclRe.FindAllStringSubmatch(string(data), -1) {
			got = append(got, m[1])
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want.externs, ",") {
			t.Errorf("%s declares externs %v but sidecarExterns lists %v — update the list AND the provider (%s)", file, got, want.externs, want.provider)
		}
	}
}
