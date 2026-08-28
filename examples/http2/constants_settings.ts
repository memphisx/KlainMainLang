// http2.constants and the settings helpers (TDD-00139 Stage 4). Constants
// resolve at compile time — header/method names, RFC 7540 error codes,
// default-settings values; the settings helpers speak nghttp2's 6-byte wire
// format (identifier + big-endian value, in identifier order).
import http2 from 'http2';

console.log("path header:", http2.constants.HTTP2_HEADER_PATH);
console.log("cancel code:", http2.constants.NGHTTP2_CANCEL);

const defaults = http2.getDefaultSettings();
console.log("default frame size:", defaults.maxFrameSize);

const packed = http2.getPackedSettings({ headerTableSize: 4096, enablePush: false });
console.log("packed bytes:", packed.length);

const unpacked = http2.getUnpackedSettings(packed);
console.log("round trip:", unpacked.headerTableSize, unpacked.enablePush);
