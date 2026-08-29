// http2.createSecureServer (TDD-00111 Stage 3b): HTTP/2 over TLS. Each accepted
// connection is TLS-handshaked, ALPN negotiates `h2`, and the request is served
// over the same nghttp2 driver as h2c but through the TLS shims. It is h2-only —
// a client that does not negotiate h2 is dropped after the handshake, matching
// Node's `allowHTTP1: false` default. Try it with:
//   curl -k --http2 https://127.0.0.1:8630/hello
//
// { cert, key } are PEM strings (as Node's are). A real service reads them with
// fs.readFileSync; the throwaway self-signed pair below is inline so the example
// is self-contained (it listens, prints, and exits — no peer needed).
import http2 from 'http2';

const cert = "-----BEGIN CERTIFICATE-----\nMIIDCTCCAfGgAwIBAgIUb0EjRVgpEh2tYhIu6ZuQdLQQmAcwDQYJKoZIhvcNAQEL\nBQAwFDESMBAGA1UEAwwJbG9jYWxob3N0MB4XDTI2MDgyOTIwMjY0OFoXDTM2MDgy\nNjIwMjY0OFowFDESMBAGA1UEAwwJbG9jYWxob3N0MIIBIjANBgkqhkiG9w0BAQEF\nAAOCAQ8AMIIBCgKCAQEA905/S9av/wNvyMT6u/OZYReIVL00tX2jQbrhcF26dajP\nwPvekIAMVYoYTTVin0o/bLxwv5PSLVH/CUHXAHzOHCD4UExm3fsYDmnEqsNuw0P0\n12seWdMuNyUXd54otJxAlqKvU5wwbqznqJHGoJ2DxYUnrFrf1ClUPBdWLR6CD5Zd\nJP8qI+ltwWoAOBqpNvlNkwxs9zG7rr7qWXdpNz3q/Om6B5Ee1zyUZ5gd6e+L9ngO\n/9f5N2LR5fPVHZ2qY2GiIb2sWdQf248j0CP5UI7p6HiuRqlWBTpmxQtIjx487nxv\nZPH2uu9rrURoz/8jk4jadKpCLa+1O/JUrXsPQRJZqQIDAQABo1MwUTAdBgNVHQ4E\nFgQUUi+IwWmZvgYocGW4K69QC+ZLuUgwHwYDVR0jBBgwFoAUUi+IwWmZvgYocGW4\nK69QC+ZLuUgwDwYDVR0TAQH/BAUwAwEB/zANBgkqhkiG9w0BAQsFAAOCAQEAn/Jc\nKTR8GqpL/iyteSE6+agOB/1tngobP4sZ6zm4Fb8SKznFnZDWPZZDFVZJ/4BwzKwP\ncCrdB4fvZpUTid6425NHayazNgKMAY2fzDQHPHtkU9r8aoaB8CE+wTqEvRUTiYz2\nF0tqVm9Dzzf5GEBt81v+qRk8eXwVI7Fzms+SHCKgvJvy7AVxiUMVBY37wRBV4BW8\nqF7PCzDdcSHjHUXwqBZrYwsk7xjHo1oj/4IPWPuL4B5YfRFUft5ejYIc/IzWURkS\nhvPPL4HMOhoNmVVoF/44SJG0eNYItUjAiFoq3pu/lf+BvsW2SINaFybW2FcT0niv\nEuAn3aVIZbbELbBRww==\n-----END CERTIFICATE-----\n";
const key = "-----BEGIN PRIVATE KEY-----\nMIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQD3Tn9L1q//A2/I\nxPq785lhF4hUvTS1faNBuuFwXbp1qM/A+96QgAxVihhNNWKfSj9svHC/k9ItUf8J\nQdcAfM4cIPhQTGbd+xgOacSqw27DQ/TXax5Z0y43JRd3nii0nECWoq9TnDBurOeo\nkcagnYPFhSesWt/UKVQ8F1YtHoIPll0k/yoj6W3BagA4Gqk2+U2TDGz3MbuuvupZ\nd2k3Per86boHkR7XPJRnmB3p74v2eA7/1/k3YtHl89UdnapjYaIhvaxZ1B/bjyPQ\nI/lQjunoeK5GqVYFOmbFC0iPHjzufG9k8fa672utRGjP/yOTiNp0qkItr7U78lSt\new9BElmpAgMBAAECggEABmf90NdxMa2Yx15K5x7VT02Oa4Yx3QMHJ/G7sJnM9bVM\nxGwB2kO8OLmzYsn05w7DpoEZzpdOr3vbRmsdMwGzTnPlhXb6hy+KR6f3vztWyC90\nzTZqJTDc6/LFsS84pg0SIz94Q+CHj1ENTdw7hUzvQpOYxn8yzcp68x+LokQ9tuzv\ngt94BtYzbCCr1r9RH5LsiadAZICQoHW1MaJWhkz5cCiWEvZkrbikT0ZdYrEU19GS\nyVye3dxQWm/2qC4+oaIl33w5J9z6G8YYptn7eAjTNeX5BP20Tz9cbBXWEFi1BW35\nACiP/caURY5ydgrCPzmHHl0nEuuY1LJhaibuC1neMQKBgQD8qAiGdLCWzmycR6yI\nfSmy5Hlpel4t62pIjGebMjfAg79mciUzpFlHJq3FWEpjWJ3v5M9ss53b1vKf5ewB\npWZgr5habtF0CoDwJZbeRn00ET5o6RLjpoK6hjmV4P1jKUfbIUNoG8zMi+Xj/th3\njiKpWvgs6SNqhTs7pv3/sbVtpQKBgQD6lFb2X+txUF5BmGR/Fv7JSoc+ou8HgFO6\nrlHhH+QTR/PA+m8jus58xEHCBDq3leXK0iNOQ6d5PNlVXY70TxRpj/0cn75dfa9j\nU0vDUwJ+Sg5AS1U1J4adGTtu5TP/GL8yE7HAYiDBMIqZQFrA1VRYoANlbfc+QQNI\nZS+sWDxEtQKBgBKHKAjkKccFYEWdo/NmalZqFtU7Wgi4CNVFJpvk9N2zS6fxmvTM\nipeDKJ8eOGZMq1haSTPJgDwM6UH8lHASdw2EEwIeulFuK8Jwnz2xoaDd2tvKq83x\n+gg/q51oIGzTLCfPqqfJ0hz17Wfo2mr6C2Sr/SMd/bDkEFHxjxLfL1TZAoGBAK7j\ny9JHXj+HNVIY98NQHGIHd197PtOAeG/p7NHwfTIL3RAKenl4j1e7bp3ob8bkgy7M\n/cFJLOFMW+/dzcGsU/Xdfm50+9uqtjff0hgwnqPgMhQjwAPKY4TQMJAUvvbDoeZk\nooJAutW7eHC/3teJzUXR4KzxVEgJ/i2QGfby2pWlAoGBAJCCRjwt+aPJ3z7KkXhM\nG3maFt/i3ZCyDGjQPythjVt1SWKoXE6yUPEJUb9k5w3fUhWcvBXZUSx94rANP8cA\nI9Xb0EYtlOuZfYqj/Ib/kk1IwE+t8xgUXeZahJVpeNocVGnqW7MTya3VRXM/G3WM\n4YE1nJLFlTbbvxexv4mGXDIT\n-----END PRIVATE KEY-----\n";

const server = http2.createSecureServer({ cert: cert, key: key }, (req, res) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("h2-over-TLS: " + req.method + " " + req.path);
});

server.listen(8630, () => {
  console.log("http2 secure server on", server.address().port);
  // Exit shortly after for the example runner; a real service would stay up.
  setTimeout(() => { server.close(); }, 150);
});
