// tls.createServer — a TLS-terminating server. Wraps each accepted connection
// in TLS (a blocking SSL_accept), then the socket behaves like a net socket:
// .on('data') / .write() / .end().
//
// { cert, key } are PEM strings (as Node's are). Here they'd come from your own
// certificate — read them with fs.readFileSync, or paste the PEM inline. This
// example illustrates the shape; it needs a real cert + a connecting peer, so
// it's excluded from `make examples`' offline run (see tests/tls_test.go for the
// deterministic coverage, with a self-signed cert generated in the test).

import tls from "tls";
import fs from "fs";

const cert = fs.readFileSync("cert.pem");
const key = fs.readFileSync("key.pem");

const server = tls.createServer({ cert: cert, key: key }, (socket) => {
  socket.on("data", (chunk: string) => {
    // Echo each chunk back, TLS-encrypted.
    socket.write("echo: " + chunk);
  });
});

server.listen(8443, () => {
  console.log("TLS server listening on :8443");
});
