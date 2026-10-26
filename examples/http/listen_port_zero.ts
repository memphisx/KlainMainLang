// klain:http on a port the system picks: listen(0) binds a free one, and
// onListening receives it once the server is bound — the way to run a server
// beside others (or in parallel tests) without guessing a port nobody holds.
import http from 'klain:http'

http.listen(0, (req: HttpRequest) => ({ status: 200, body: 'kalimera from ' + req.path }), {
  host: '127.0.0.1',
  onListening: async (port: number) => {
    const res = await fetch('http://127.0.0.1:' + port + '/thessaloniki')
    console.log(res.status)        // 200
    console.log(await res.text())  // kalimera from /thessaloniki
    http.close()
  },
})
console.log('server closed')
