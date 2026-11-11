// FormData and File: building a multipart form, sending it as a request body
// and reading it back, as fetch does on the wire.
import assert from 'assert';

const form = new FormData();
form.append('city', 'Thessaloniki');
form.append('report', new File(['{"temp":21}'], 'report.json', { type: 'application/json' }));
form.append('note', new Blob(['plain bytes']));          // a Blob entry becomes a File named "blob"

assert.strictEqual(form.get('city'), 'Thessaloniki');
assert.strictEqual((form.get('note') as File).name, 'blob');
console.log([...form.keys()]);

// The body is multipart/form-data with a generated boundary.
const req = new Request('http://localhost/upload', { method: 'POST', body: form });
assert.ok(req.headers.get('content-type')!.startsWith('multipart/form-data; boundary='));

const parsed = await req.formData();
const report = parsed.get('report') as File;
console.log(parsed.get('city'), report.name, report.type, await report.text());

// A urlencoded body parses too.
const fields = await new Response('a=1&b=two', { headers: { 'content-type': 'application/x-www-form-urlencoded' } }).formData();
console.log([...fields]);
