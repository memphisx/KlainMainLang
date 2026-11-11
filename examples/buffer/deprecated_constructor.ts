// The deprecated Buffer constructor: a number allocates zero-filled bytes,
// anything else is Buffer.from's input. Node warns once (DEP0005).
const zeros = new Buffer(4);
const text = new Buffer("Thessaloniki");
console.log(zeros, text.toString(), text.length);
