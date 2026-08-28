// https.get — Node's https client module. get/request share the libcurl
// backend with http.get and fetch, so the TLS handshake and certificate
// verification come from the URL scheme at curl's layer; there is no separate
// https-specific machinery to configure.
//
// Needs real network access to run (a TLS endpoint can't come from the local
// plain-HTTP fixture server), same caveat as examples/fetch/wttr_weather.ts.
import https from 'https';

https.get("https://example.com/", (res) => {
  console.log("status:", res.statusCode);
  let bytes = 0;
  res.on('data', (chunk: string) => { bytes = bytes + chunk.length; });
  res.on('end', () => {
    if (bytes > 0) { console.log("body received over TLS"); }
  });
});
