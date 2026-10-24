package llvm

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// heapCoveredC are the C runtime files whose program-value allocations all
// go through kml_heap.h (TDD-00230 P3.3a). The rest are converted file by
// file and join the list.
var heapCoveredC = []string{
	"dynjsonsrc/dynjson.c",
	"buffillsrc/buffill.c",
	"keyordersrc/keyorder.c",
	"buffersrc/buffer_codecs.c",
	"stringsrc/regexp.c",
	"stringsrc/strheader.c",
	"stringsrc/strings.c",
	"stringsrc/string.c",
	"coresrc/core.c",
	"jsonsrc/json_parse.c",
	"dynobjsrc/dynarr.c",
	"dynobjsrc/dynobj.c",
	"dynobjsrc/dynjsonnode.c",
	"shapesrc/shape.c",
	"inspectsrc/inspect_reduce.c",
	"fnmetasrc/fnmeta.c",
	"collectionssrc/collections.c",
	"collectionssrc/collections_engine.h",
	"collectionssrc/arrayguard.c",
	"casemapsrc/casemap.c",
	"encodingsrc/encoding.c",
	"numbersrc/number.c",
	"boxsrc/toprim.c",
	"exceptionssrc/exceptions.c",
	"bigintsrc/bigint_tommath.c",
	"bigintsrc/bigint_gmp.c",
	"unitregsrc/unitreg.c",
	"viewsrc/views.c",
	"weaksrc/weak.c",
	"pathsrc/path.c",
	"asynchookssrc/asynchooks.c",
	"cryptosrc/crypto_cng.c",
	"cryptosrc/crypto_commoncrypto.c",
	"cryptosrc/crypto_openssl.c",
	"dynimportsrc/dynimport.c",
	"dynimportsrc/dynimportwatch.c",
	"dynimportsrc/dynwhen.c",
	"embedassetssrc/embed_assets.c",
	"ffisrc/ffi_native.c",
	"ffisrc/ffi_registry.c",
	"ffisrc/ffi_umap_msvc.c",
	"ffisrc/umap_order.c",
	"finalizationsrc/finalization.c",
	"gcsrc/gcshim.c",
	"hookssrc/hooks.c",
	"jmpstacksrc/jmpstack.c",
	"klainsyncsrc/klainsync.c",
	"microtasksrc/microtask.c",
	"promiseadoptsrc/adopt.c",
	"promiseadoptsrc/fetchslot.c",
	"promisesrc/anyprom.c",
	"promisesrc/defer.c",
	"promisesrc/fetchbodyprom.c",
	"promisesrc/fetchdrive.c",
	"promisesrc/finally.c",
	"promisesrc/module.c",
	"promisesrc/settle.c",
	"tasksrc/promise.c",
	"tasksrc/task.c",
	"webviewsrc/natives.c",
	"win32src/win32fs.c",
	"win32src/win32io.c",
	"win32src/win32proc.c",
	"win32src/win32shim.c",
	"pathsrc/path_win32.c",
	"threadpoolsrc/klaindns.c",
	"threadpoolsrc/klainfs.c",
	"threadpoolsrc/klainhttp.c",
	"threadpoolsrc/klainpool.c",
	"threadpoolsrc/klainsqlite.c",
	"threadpoolsrc/klainkhttp.c",
	"threadpoolsrc/klainzlib.c",
	"fssrc/errno.c",
	"fssrc/fserror.c",
	"fssrc/fswatch.c",
	"fetchsrc/fetch.c",
	"fetchsrc/fetch_stream.c",
	"http2src/h2node.c",
	"streamssrc/streams.c",
	"streamssrc/zlibstream.c",
	"tlssrc/tlshandle.c",
	"caressrc/klaincares.c",
	"processsrc/process.c",
	"ipcsrc/ipc.c",
	"ipcsrc/ipcc.c",
	"osinfosrc/osinfo.c",
	"loopsrc/loop.c",
}

var libcAllocRe = regexp.MustCompile(`(^|[^_\w])(malloc|calloc|realloc|free)\(`)

// TestCRuntimeAllocatesThroughHeapInterface: in a covered file, a libc
// allocation call is scratch memory the runtime keeps to itself, and says
// so on its line; every other allocation is a kml_alloc/kml_calloc/
// kml_realloc/kml_free with its block's kind.
func TestCRuntimeAllocatesThroughHeapInterface(t *testing.T) {
	for _, f := range heapCoveredC {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, ln := range strings.Split(string(data), "\n") {
			if libcAllocRe.MatchString(ln) && !strings.Contains(ln, "scratch") {
				t.Errorf("%s:%d: a libc allocation outside kml_heap.h, not marked scratch: %s", f, i+1, strings.TrimSpace(ln))
			}
		}
	}
}
