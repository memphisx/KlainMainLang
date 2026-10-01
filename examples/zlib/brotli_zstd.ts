// Brotli and Zstandard, the two other codecs in Node's zlib: one-shot sync
// and callback forms, parameters, and streams. libbrotli and libzstd load
// the first time a Brotli or Zstd stream is made.
import * as zlib from 'zlib';

const text = 'Thessaloniki '.repeat(40);

const br = zlib.brotliCompressSync(text, {
  params: { [zlib.constants.BROTLI_PARAM_QUALITY]: 9 },
});
console.log(`brotli: ${text.length} -> ${br.length} bytes`);
console.log(zlib.brotliDecompressSync(br).toString() === text);

const zs = zlib.zstdCompressSync(text, {
  params: { [zlib.constants.ZSTD_c_compressionLevel]: 10 },
});
console.log(`zstd: ${text.length} -> ${zs.length} bytes`);
console.log(zlib.zstdDecompressSync(zs).toString() === text);

try {
  zlib.brotliDecompressSync(Buffer.from('not brotli at all'));
} catch (e: any) {
  console.log(`${e.code}: ${e.message}`);
}

zlib.zstdCompress('over the callback', (err, packed) => {
  zlib.zstdDecompress(packed, (err2, out) => {
    console.log(err, err2, out.toString());
  });
});
