package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestE2EHTTPGCParkedFiberDataSurvives is the regression test for ADR-01028:
// data reachable only from a *parked* connection fiber must survive collections
// caused by other connections.
//
// The upstream holds the request for 150ms, so the fiber is genuinely parked
// while the neighbours run.
//
// A connection fiber's stack is a GC_malloc'd block under ucontext, so the
// collector scans it like any other object; a Win32 fiber runs on a stack the
// OS allocated, which the collector cannot see at all. This handler builds a
// string, awaits an upstream fetch (parking the fiber with that string live
// only in its frame), and then answers with it — while five other connections
// allocate hard enough to collect underneath it. A parked stack that is not
// scanned loses the string and the response comes back wrong or the server
// dies; ADR-01028 registers the parked range as a root for exactly as long as
// the fiber is parked.
func TestE2EHTTPGCParkedFiberDataSurvives(t *testing.T) {
	upstream := newDelayedUpstreamServer(t, 150*time.Millisecond)
	src := fmt.Sprintf(`
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8979, async (req: HttpRequest): Promise<Res> => {
  if (req.path === "/churn") {
    let total = 0;
    for (let i = 0; i < 60000; i++) {
      let s: string = "abcdefghijklmnopqrstuvwxyz0123456789" + "abcdefghijklmnopqrstuvwxyz0123456789";
      total = total + s.length;
    }
    return { status: 200, body: total.toString() };
  }
  // Built before the await, used after it: live only on this fiber's stack
  // for the whole time the fiber is parked.
  let held: string = "";
  for (let i = 0; i < 64; i++) {
    held = held + "park" + req.path;
  }
  const r: Response = await fetch("%s" + req.path)
  const up: string = await r.text()
  return { status: 200, body: held + "|" + up }
})
`, upstream.URL)
	port := startHTTPServerGC(t, src, 8979)

	want := strings.Repeat("park/slow", 64) + "|upstream /slow"

	var wg sync.WaitGroup
	var parked string
	var parkedErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/slow", port))
		if err != nil {
			parkedErr = err
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		parked = string(b)
	}()
	// Allocation pressure from other connections while the fiber above is parked.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/churn", port))
			if err != nil {
				return
			}
			io.ReadAll(resp.Body)
			resp.Body.Close()
		}()
	}
	wg.Wait()

	if parkedErr != nil {
		t.Fatalf("parked request failed (the server died mid-request, or never answered): %v", parkedErr)
	}
	if parked != want {
		t.Errorf("the parked fiber's own data did not survive the collections its neighbours caused:\n got %q\nwant %q", parked, want)
	}
}
