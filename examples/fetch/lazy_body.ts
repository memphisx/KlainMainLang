// Lazy Response body promises (TDD-00186): text()/json()/arrayBuffer() return a
// real Promise settled off the fetch reactor's completion. Two consequences a
// program can observe: the download overlaps intervening work (the accessor
// returns a pending promise instead of driving to done), and a JSON parse error
// is a promise *rejection* — reachable via .then's onRejected / .catch — not a
// throw that escapes before a reaction can attach.
//
// Run against the httpbin-lite fixture on 127.0.0.1:8765 (started by the
// examples runner): /get returns JSON, /status/200 returns a text/plain body.

async function main(): Promise<void> {
  // Well-formed JSON body fulfills through the lazy path.
  const r: Response = await fetch('http://127.0.0.1:8765/get');
  const p = r.text();               // pending promise — not yet driven
  const body: string = await p;
  console.log('got body: ' + (body.length > 0));

  // A non-JSON body makes .json() reject; the rejection reaches .then/.catch.
  const r2: Response = await fetch('http://127.0.0.1:8765/status/200');
  const outcome: string = await r2.json().then(
    () => 'parsed',
    (e) => 'rejected: ' + (e as Error).name,
  );
  console.log(outcome);
}

main();
