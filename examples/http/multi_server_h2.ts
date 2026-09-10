// Two HTTP/2 (h2c, cleartext prior-knowledge) servers in one program, on
// different ports (TDD-00191 Stage 4). Each http2.createServer runs its own
// dispatcher and its own h2 bridge vtable, so the C nghttp2 driver routes each
// connection to that server's handler. Try either port with:
//   curl --http2-prior-knowledge http://127.0.0.1:<port>/hello
//
// A self-closing sequence keeps the example runnable to completion.
import http2 from 'http2';

const api = http2.createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("api h2: " + req.path);
});

const admin = http2.createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("admin h2: " + req.path);
});

api.listen(0, () => {
  admin.listen(0, () => {
    console.log("api (h2c) on port", api.address().port > 0);
    console.log("admin (h2c) on port", admin.address().port > 0);
    api.close();
    admin.close();
  });
});

console.log("two h2c servers on separate ports");
