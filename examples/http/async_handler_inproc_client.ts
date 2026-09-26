// A Node http.createServer async handler that awaits a *task-shaped* promise
// (here a setTimeout-backed delay — a may-suspend await, distinct from awaiting
// fetch directly), driven by an in-process http.get client on the *same* event
// loop, with the program exiting from the client's own 'end' reaction (ADR-00986).
//
// This is the natural self-contained shape of examples/http/client_server.ts,
// but with an async handler that yields its connection fiber mid-request. Before
// ADR-00986 two things went wrong: several such requests serialized (the fiber
// busy-waited itself), and — as here — once the handler yielded and then wrote
// its response the loop blocked in select() with nothing left to wake it, so the
// in-process client's completion reaction never fired and the process hung.
// Runs unchanged under Node.js.
import http from 'http'

async function delay(ms: number): Promise<void> {
  return await new Promise<void>((r) => setTimeout(() => r(), ms))
}

http.createServer(async (req: http.IncomingMessage, res: http.ServerResponse) => {
  await delay(20) // yield the connection fiber to the loop, then respond
  res.writeHead(200, { "Content-Type": "text/plain" })
  res.end("kalimera from the server")
}).listen(18537, () => {
  http.get("http://127.0.0.1:18537/", (res) => {
    let body = ""
    res.on('data', (chunk: string) => { body = body + chunk })
    res.on('end', () => {
      console.log("status:", res.statusCode)
      console.log("body:", body)
      process.exit(0)
    })
  })
})
