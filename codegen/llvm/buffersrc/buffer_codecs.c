/* buffer_codecs.c — the Node-Buffer string codec runtime (TDD-00103):
 * hex, base64/base64url, and latin1. Self-contained, libc only — embedded
 * by the compiler and built alongside the generated .ll only when a program
 * actually uses a Buffer codec (the same shape as the JSON parse-tree
 * runtime). utf8 needs nothing here: the language's strings are already
 * UTF-8 bytes.
 *
 * Allocation goes through plain malloc, so under -mm=gc it is transparently
 * routed to the collector by the shim's global malloc override.
 */
#include <stdlib.h>
#include <string.h>

/* TDD-00120: length-prefixed heap string — [i64 len][bytes][\0], value ptr at
 * base+8, so KML string ops read the true length via ptr-8. The codec encoders
 * (hex/base64/latin1 -> string) return one of these; the decoders (-> byte
 * buffer) keep plain malloc. */
static char *kml_str_hdr_alloc(long len) {
	char *base = (char *)malloc((size_t)(8 + len + 1));
	if (!base) return base;
	*(long *)base = len;
	return base + 8;
}

/* ---- hex ---- */

char *__kml_buf_hex_enc(const unsigned char *src, long long n) {
	static const char digits[] = "0123456789abcdef";
	char *out = kml_str_hdr_alloc(n * 2);
	for (long long i = 0; i < n; i++) {
		out[i * 2] = digits[src[i] >> 4];
		out[i * 2 + 1] = digits[src[i] & 0xF];
	}
	out[n * 2] = 0;
	return out;
}

static int hexval(char c) {
	if (c >= '0' && c <= '9') return c - '0';
	if (c >= 'a' && c <= 'f') return c - 'a' + 10;
	if (c >= 'A' && c <= 'F') return c - 'A' + 10;
	return -1;
}

/* Node semantics: decoding stops at the first non-hex pair (and a trailing
 * lone digit is dropped). Returns the byte count; *out receives the buffer. */
long long __kml_buf_hex_dec(const char *s, unsigned char **out) {
	size_t sl = strlen(s);
	unsigned char *buf = (unsigned char *)malloc(sl / 2 + 1);
	long long n = 0;
	for (size_t i = 0; i + 1 < sl; i += 2) {
		int hi = hexval(s[i]), lo = hexval(s[i + 1]);
		if (hi < 0 || lo < 0) break;
		buf[n++] = (unsigned char)((hi << 4) | lo);
	}
	*out = buf;
	return n;
}

/* ---- base64 / base64url ---- */

char *__kml_buf_b64_enc(const unsigned char *src, long long n, int urlsafe) {
	static const char std_al[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	static const char url_al[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
	const char *al = urlsafe ? url_al : std_al;
	long long groups = (n + 2) / 3;
	char *out = kml_str_hdr_alloc(groups * 4);
	long long o = 0;
	for (long long i = 0; i < n; i += 3) {
		unsigned v = (unsigned)src[i] << 16;
		if (i + 1 < n) v |= (unsigned)src[i + 1] << 8;
		if (i + 2 < n) v |= (unsigned)src[i + 2];
		out[o++] = al[(v >> 18) & 63];
		out[o++] = al[(v >> 12) & 63];
		if (i + 1 < n) out[o++] = al[(v >> 6) & 63];
		else if (!urlsafe) out[o++] = '=';
		if (i + 2 < n) out[o++] = al[v & 63];
		else if (!urlsafe) out[o++] = '=';
	}
	out[o] = 0;
	return out;
}

static int b64val(char c) {
	if (c >= 'A' && c <= 'Z') return c - 'A';
	if (c >= 'a' && c <= 'z') return c - 'a' + 26;
	if (c >= '0' && c <= '9') return c - '0' + 52;
	if (c == '+' || c == '-') return 62;
	if (c == '/' || c == '_') return 63;
	return -1;
}

/* Lenient like Node: non-alphabet bytes (whitespace, '=') are skipped;
 * both the standard and url-safe alphabets are accepted. */
long long __kml_buf_b64_dec(const char *s, unsigned char **out) {
	size_t sl = strlen(s);
	unsigned char *buf = (unsigned char *)malloc(sl / 4 * 3 + 3);
	long long n = 0;
	unsigned acc = 0;
	int bits = 0;
	for (size_t i = 0; i < sl; i++) {
		int v = b64val(s[i]);
		if (v < 0) continue;
		acc = (acc << 6) | (unsigned)v;
		bits += 6;
		if (bits >= 8) {
			bits -= 8;
			buf[n++] = (unsigned char)((acc >> bits) & 0xFF);
		}
	}
	*out = buf;
	return n;
}

/* ---- latin1 ---- */

/* bytes -> UTF-8 string: 0x00–0x7F pass through, 0x80–0xFF become the
 * 2-byte UTF-8 encoding of U+0080–U+00FF. */
char *__kml_buf_latin1_str(const unsigned char *src, long long n) {
	char *out = kml_str_hdr_alloc(n * 2); /* max; actual length set below */
	long long o = 0;
	for (long long i = 0; i < n; i++) {
		unsigned char b = src[i];
		if (b < 0x80) {
			out[o++] = (char)b;
		} else {
			out[o++] = (char)(0xC0 | (b >> 6));
			out[o++] = (char)(0x80 | (b & 0x3F));
		}
	}
	out[o] = 0;
	*(long *)(out - 8) = o; /* TDD-00120: actual UTF-8 length */
	return out;
}

/* UTF-8 string -> bytes: each codepoint keeps its low 8 bits (Node's
 * latin1-write masking). Invalid sequences fall back byte-wise. */
long long __kml_buf_latin1_bytes(const char *s, unsigned char **out) {
	size_t sl = strlen(s);
	unsigned char *buf = (unsigned char *)malloc(sl + 1);
	long long n = 0;
	for (size_t i = 0; i < sl;) {
		unsigned char c = (unsigned char)s[i];
		unsigned cp = c;
		int adv = 1;
		if ((c & 0xE0) == 0xC0 && i + 1 < sl && ((unsigned char)s[i + 1] & 0xC0) == 0x80) {
			cp = ((unsigned)(c & 0x1F) << 6) | ((unsigned char)s[i + 1] & 0x3F);
			adv = 2;
		} else if ((c & 0xF0) == 0xE0 && i + 2 < sl && ((unsigned char)s[i + 1] & 0xC0) == 0x80 && ((unsigned char)s[i + 2] & 0xC0) == 0x80) {
			cp = ((unsigned)(c & 0x0F) << 12) | (((unsigned char)s[i + 1] & 0x3F) << 6) | ((unsigned char)s[i + 2] & 0x3F);
			adv = 3;
		} else if ((c & 0xF8) == 0xF0 && i + 3 < sl && ((unsigned char)s[i + 1] & 0xC0) == 0x80 && ((unsigned char)s[i + 2] & 0xC0) == 0x80 && ((unsigned char)s[i + 3] & 0xC0) == 0x80) {
			cp = ((unsigned)(c & 0x07) << 18) | (((unsigned char)s[i + 1] & 0x3F) << 12) | (((unsigned char)s[i + 2] & 0x3F) << 6) | ((unsigned char)s[i + 3] & 0x3F);
			adv = 4;
		}
		buf[n++] = (unsigned char)(cp & 0xFF);
		i += (size_t)adv;
	}
	*out = buf;
	return n;
}
