// A WebSocket/upgrade-capable primary server alongside a plain additional
// server (TDD-00191 Stage 3). The first http.createServer is the primary: it
// can handle the Node `'upgrade'` event (here a trivial raw-protocol echo). An
// additional server on its own port is plain HTTP/1.1 — an app that speaks
// WebSockets on its main port while exposing a plain health/metrics port.
//
// The additional server also demonstrates Stage 2's close()/listen() relisten:
// it is closed and re-listened on a fresh port before the demo shuts down. A
// self-closing sequence keeps the example runnable to completion.
import http from 'http';

const app = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("app: " + req.url);
});
app.on('upgrade', (req, socket, head) => {
  socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n');
  socket.on('data', (chunk) => { socket.write('echo:' + chunk.toString()); });
});

const health = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/json" });
  res.end('{"status":"ok"}');
});

app.listen(0, () => {
  health.listen(0, () => {
    console.log("app (ws-capable) on port", app.address().port > 0);
    console.log("health on port", health.address().port > 0);
    // Stage 2 relisten: close the additional server and bring it back up on a
    // fresh OS-assigned port.
    health.close();
    health.listen(0, () => {
      console.log("health relistened on port", health.address().port > 0);
      app.close();
      health.close();
    });
  });
});

console.log("ws-capable primary + plain additional server listening");
