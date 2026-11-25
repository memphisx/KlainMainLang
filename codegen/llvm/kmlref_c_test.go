package llvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestKmlRefTable compiles the handle table with a driver that creates,
// reads, rewrites and frees refs, from several threads at once, and checks
// that a freed slot is reused and none stays live.
func TestKmlRefTable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the driver uses pthreads")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not on PATH")
	}
	dir := t.TempDir()
	driver := `#include <pthread.h>
#include <stdio.h>
static int objs[4][1000];
static void *worker(void *a) {
    int k = (int)(long)a;
    kml_ref refs[1000];
    for (int i = 0; i < 1000; i++) refs[i] = kml_ref_new(&objs[k][i]);
    for (int i = 0; i < 1000; i++)
        if (kml_ref_get(refs[i]) != &objs[k][i]) return (void *)1;
    for (int i = 0; i < 1000; i++) kml_ref_free(refs[i]);
    return 0;
}
int main(void) {
    int a = 1, b = 2;
    kml_ref r = kml_ref_new(&a);
    if (r == 0 || kml_ref_get(r) != &a) return 1;
    kml_ref_set(r, &b);
    if (kml_ref_get(r) != &b) return 2;
    kml_ref_free(r);
    if (kml_ref_get(r) != 0) return 3;
    kml_ref r2 = kml_ref_new(&a);
    if (r2 != r) return 4; /* the freed slot is reused */
    kml_ref_free(r2);
    kml_ref_free(r2); /* a second free is a no-op */
    if (kml_ref_get(0) != 0) return 5;
    pthread_t t[4];
    for (long k = 0; k < 4; k++) pthread_create(&t[k], 0, worker, (void *)k);
    for (int k = 0; k < 4; k++) {
        void *res;
        pthread_join(t[k], &res);
        if (res) return 6;
    }
    if (kml_ref_live() != 0) return 7;
    puts("ok");
    return 0;
}
`
	src := kmlHeapHeader + kmlRefSource + "\n" + driver
	cFile := filepath.Join(dir, "ref.c")
	if err := os.WriteFile(cFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "ref")
	if out, err := exec.Command("clang", "-O2", "-pthread", "-o", bin, cFile).CombinedOutput(); err != nil {
		t.Fatalf("clang: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("driver: %v\n%s", err, out)
	}
}
