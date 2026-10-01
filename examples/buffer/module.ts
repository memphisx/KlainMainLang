// The `buffer` module: the Buffer global plus the module's own helpers —
// UTF-8/ASCII validation, transcoding, and the size limits.
import buffer, { isUtf8, isAscii, transcode, constants } from 'buffer'

const upload = Buffer.from('Θεσσαλονίκη')
console.log('valid UTF-8:', isUtf8(upload), 'pure ASCII:', isAscii(upload))

const broken = Buffer.from([0x54, 0x68, 0xc3])
console.log('truncated sequence is valid UTF-8:', isUtf8(broken))

const latin1 = transcode(Buffer.from('café'), 'utf8', 'latin1')
console.log('latin1 bytes:', latin1.length, latin1.toString('hex'))

console.log('largest buffer:', constants.MAX_LENGTH === buffer.kMaxLength)
