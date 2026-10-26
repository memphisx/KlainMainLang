package tests

import "testing"

// TestE2EKlainHTTPListenPortZero: listen(0) binds a port the system picks,
// and onListening receives it once bound, before the loop serves.
func TestE2EKlainHTTPListenPortZero(t *testing.T) {
	assertOutputImports(t, `
import http from 'klain:http'
http.listen(0, (req: HttpRequest) => ({ status: 200, body: 'hi ' + req.path }), {
  host: '127.0.0.1',
  onListening: (port: number) => {
    console.log('bound', port > 0 && port < 65536)
    fetch('http://127.0.0.1:' + port + '/x').then((r) => r.text()).then((t) => {
      console.log(t)
      http.close()
    })
  },
})
console.log('closed')
`, "bound true\nhi /x\nclosed")
}
