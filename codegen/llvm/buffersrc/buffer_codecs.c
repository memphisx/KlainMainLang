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
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* TDD-00120: length-prefixed heap string — [i64 len][bytes][\0], value ptr at
 * base+8, so KML string ops read the true length via ptr-8. The codec encoders
 * (hex/base64/latin1 -> string) return one of these; the decoders (-> byte
 * buffer) keep plain malloc. */
static char *kml_str_hdr_alloc(int64_t len) {
	char *base = (char *)malloc((size_t)(8 + len + 1));
	if (!base) return base;
	*(int64_t *)base = len;
	return base + 8;
}

/* ---- utf8 ----
 * Bytes -> string by the WHATWG UTF-8 decoder, as Node's buf.toString():
 * every maximal invalid subsequence becomes one U+FFFD. Valid input is a
 * plain copy. */
char *__kml_buf_utf8_str(const unsigned char *src, int64_t n) {
	int64_t i = 0;
	int valid = 1;
	/* Validation pass (the common case copies straight through). */
	while (i < n) {
		unsigned char c = src[i];
		if (c < 0x80) { i++; continue; }
		int need; unsigned char lo = 0x80, hi = 0xBF;
		if (c >= 0xC2 && c <= 0xDF) need = 1;
		else if (c >= 0xE0 && c <= 0xEF) { need = 2; if (c == 0xE0) lo = 0xA0; if (c == 0xED) hi = 0x9F; }
		else if (c >= 0xF0 && c <= 0xF4) { need = 3; if (c == 0xF0) lo = 0x90; if (c == 0xF4) hi = 0x8F; }
		else { valid = 0; break; }
		if (i + need >= n) { valid = 0; break; }
		if (src[i + 1] < lo || src[i + 1] > hi) { valid = 0; break; }
		int k;
		for (k = 2; k <= need; k++) if ((src[i + k] & 0xC0) != 0x80) break;
		if (k <= need) { valid = 0; break; }
		i += need + 1;
	}
	if (valid) {
		char *out = kml_str_hdr_alloc(n);
		if (!out) return out;
		memcpy(out, src, (size_t)n);
		out[n] = 0;
		return out;
	}
	/* Each invalid byte may grow to three. */
	char *tmp = (char *)malloc((size_t)(n * 3 + 1));
	int64_t o = 0;
	i = 0;
	while (i < n) {
		unsigned char c = src[i];
		if (c < 0x80) { tmp[o++] = (char)c; i++; continue; }
		int need; unsigned char lo = 0x80, hi = 0xBF;
		if (c >= 0xC2 && c <= 0xDF) need = 1;
		else if (c >= 0xE0 && c <= 0xEF) { need = 2; if (c == 0xE0) lo = 0xA0; if (c == 0xED) hi = 0x9F; }
		else if (c >= 0xF0 && c <= 0xF4) { need = 3; if (c == 0xF0) lo = 0x90; if (c == 0xF4) hi = 0x8F; }
		else { tmp[o++] = (char)0xEF; tmp[o++] = (char)0xBF; tmp[o++] = (char)0xBD; i++; continue; }
		/* The maximal subpart: the lead and each continuation in range. */
		int64_t j = i + 1;
		int seen = 0;
		while (seen < need && j < n) {
			unsigned char b = src[j];
			unsigned char l = seen == 0 ? lo : 0x80, h = seen == 0 ? hi : 0xBF;
			if (b < l || b > h) break;
			j++; seen++;
		}
		if (seen == need) {
			memcpy(tmp + o, src + i, (size_t)(need + 1));
			o += need + 1;
		} else {
			tmp[o++] = (char)0xEF; tmp[o++] = (char)0xBF; tmp[o++] = (char)0xBD;
		}
		i = j;
	}
	char *out = kml_str_hdr_alloc(o);
	if (out) {
		memcpy(out, tmp, (size_t)o);
		out[o] = 0;
	}
	free(tmp);
	return out;
}

/* ---- ascii ----
 * Bytes -> string as Node's 'ascii' decoding: each byte's high bit cleared,
 * then latin1 (so every character is below U+0080). */
char *__kml_buf_ascii_str(const unsigned char *src, int64_t n) {
	char *out = kml_str_hdr_alloc(n);
	if (!out) return out;
	for (int64_t i = 0; i < n; i++) out[i] = (char)(src[i] & 0x7F);
	out[n] = 0;
	return out;
}

/* ---- hex ---- */

char *__kml_buf_hex_enc(const unsigned char *src, int64_t n) {
	static const char digits[] = "0123456789abcdef";
	char *out = kml_str_hdr_alloc(n * 2);
	for (int64_t i = 0; i < n; i++) {
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
int64_t __kml_buf_hex_dec(const char *s, unsigned char **out) {
	size_t sl = strlen(s);
	unsigned char *buf = (unsigned char *)malloc(sl / 2 + 1);
	int64_t n = 0;
	for (size_t i = 0; i + 1 < sl; i += 2) {
		int hi = hexval(s[i]), lo = hexval(s[i + 1]);
		if (hi < 0 || lo < 0) break;
		buf[n++] = (unsigned char)((hi << 4) | lo);
	}
	*out = buf;
	return n;
}

/* ---- base64 / base64url ---- */

char *__kml_buf_b64_enc(const unsigned char *src, int64_t n, int urlsafe) {
	static const char std_al[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	static const char url_al[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
	const char *al = urlsafe ? url_al : std_al;
	int64_t groups = (n + 2) / 3;
	char *out = kml_str_hdr_alloc(groups * 4);
	int64_t o = 0;
	for (int64_t i = 0; i < n; i += 3) {
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
int64_t __kml_buf_b64_dec(const char *s, unsigned char **out) {
	size_t sl = strlen(s);
	unsigned char *buf = (unsigned char *)malloc(sl / 4 * 3 + 3);
	int64_t n = 0;
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
char *__kml_buf_latin1_str(const unsigned char *src, int64_t n) {
	char *out = kml_str_hdr_alloc(n * 2); /* max; actual length set below */
	int64_t o = 0;
	for (int64_t i = 0; i < n; i++) {
		unsigned char b = src[i];
		if (b < 0x80) {
			out[o++] = (char)b;
		} else {
			out[o++] = (char)(0xC0 | (b >> 6));
			out[o++] = (char)(0x80 | (b & 0x3F));
		}
	}
	out[o] = 0;
	*(int64_t *)(out - 8) = o; /* TDD-00120: actual UTF-8 length */
	return out;
}

/* UTF-8 string -> bytes: each codepoint keeps its low 8 bits (Node's
 * latin1-write masking). Invalid sequences fall back byte-wise. */
int64_t __kml_buf_latin1_bytes(const char *s, unsigned char **out) {
	size_t sl = strlen(s);
	unsigned char *buf = (unsigned char *)malloc(sl + 1);
	int64_t n = 0;
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

/* ---- utf16le / ucs2 (TDD-00229) ----
 * Strings here are UTF-8; Node's 'utf16le' is the string's UTF-16 code units,
 * little-endian — a transcoding, not a representation problem. A code point
 * above the BMP becomes a surrogate pair; an invalid UTF-8 byte, like a lone
 * surrogate decoded back, becomes U+FFFD. */

/* Decodes one UTF-8 code point at s[i] (sl bytes total); *adv = bytes used. */
static unsigned kml_utf8_next(const char *s, size_t sl, size_t i, int *adv) {
	unsigned char c = (unsigned char)s[i];
	*adv = 1;
	if (c < 0x80) return c;
	if ((c & 0xE0) == 0xC0 && i + 1 < sl && ((unsigned char)s[i + 1] & 0xC0) == 0x80) {
		*adv = 2;
		return ((unsigned)(c & 0x1F) << 6) | ((unsigned char)s[i + 1] & 0x3F);
	}
	if ((c & 0xF0) == 0xE0 && i + 2 < sl && ((unsigned char)s[i + 1] & 0xC0) == 0x80 && ((unsigned char)s[i + 2] & 0xC0) == 0x80) {
		*adv = 3;
		return ((unsigned)(c & 0x0F) << 12) | (((unsigned char)s[i + 1] & 0x3F) << 6) | ((unsigned char)s[i + 2] & 0x3F);
	}
	if ((c & 0xF8) == 0xF0 && i + 3 < sl && ((unsigned char)s[i + 1] & 0xC0) == 0x80 && ((unsigned char)s[i + 2] & 0xC0) == 0x80 && ((unsigned char)s[i + 3] & 0xC0) == 0x80) {
		*adv = 4;
		return ((unsigned)(c & 0x07) << 18) | (((unsigned char)s[i + 1] & 0x3F) << 12) | (((unsigned char)s[i + 2] & 0x3F) << 6) | ((unsigned char)s[i + 3] & 0x3F);
	}
	return 0xFFFD;
}

/* UTF-8 string -> UTF-16LE bytes. */
int64_t __kml_buf_utf16le_bytes(const char *s, unsigned char **out) {
	size_t sl = strlen(s);
	unsigned char *buf = (unsigned char *)malloc(sl * 4 + 1);
	int64_t n = 0;
	for (size_t i = 0; i < sl;) {
		int adv;
		unsigned cp = kml_utf8_next(s, sl, i, &adv);
		i += (size_t)adv;
		if (cp >= 0x10000) {
			unsigned v = cp - 0x10000;
			unsigned hi = 0xD800 | (v >> 10), lo = 0xDC00 | (v & 0x3FF);
			buf[n++] = (unsigned char)(hi & 0xFF);
			buf[n++] = (unsigned char)(hi >> 8);
			buf[n++] = (unsigned char)(lo & 0xFF);
			buf[n++] = (unsigned char)(lo >> 8);
		} else {
			buf[n++] = (unsigned char)(cp & 0xFF);
			buf[n++] = (unsigned char)(cp >> 8);
		}
	}
	*out = buf;
	return n;
}

/* UTF-16LE bytes -> UTF-8 string (a trailing odd byte is dropped, as Node
 * does). */
char *__kml_buf_utf16le_str(const unsigned char *src, int64_t n) {
	char *out = kml_str_hdr_alloc(n * 2 + 4); /* max; actual length set below */
	int64_t o = 0;
	for (int64_t i = 0; i + 1 < n; i += 2) {
		unsigned u = (unsigned)src[i] | ((unsigned)src[i + 1] << 8);
		unsigned cp = u;
		if (u >= 0xD800 && u <= 0xDBFF && i + 3 < n) {
			unsigned u2 = (unsigned)src[i + 2] | ((unsigned)src[i + 3] << 8);
			if (u2 >= 0xDC00 && u2 <= 0xDFFF) {
				cp = 0x10000 + (((u - 0xD800) << 10) | (u2 - 0xDC00));
				i += 2;
			} else {
				cp = 0xFFFD;
			}
		} else if (u >= 0xD800 && u <= 0xDFFF) {
			cp = 0xFFFD;
		}
		if (cp < 0x80) {
			out[o++] = (char)cp;
		} else if (cp < 0x800) {
			out[o++] = (char)(0xC0 | (cp >> 6));
			out[o++] = (char)(0x80 | (cp & 0x3F));
		} else if (cp < 0x10000) {
			out[o++] = (char)(0xE0 | (cp >> 12));
			out[o++] = (char)(0x80 | ((cp >> 6) & 0x3F));
			out[o++] = (char)(0x80 | (cp & 0x3F));
		} else {
			out[o++] = (char)(0xF0 | (cp >> 18));
			out[o++] = (char)(0x80 | ((cp >> 12) & 0x3F));
			out[o++] = (char)(0x80 | ((cp >> 6) & 0x3F));
			out[o++] = (char)(0x80 | (cp & 0x3F));
		}
	}
	out[o] = 0;
	*(int64_t *)(out - 8) = o;
	return out;
}

/* ---- PEM assembly (crypto.generateKeyPair, ADR-00434) ----
 * base64 the DER in 64-char lines between -----BEGIN <label>-----/
 * -----END <label>----- with a trailing newline — Node's own PEM shape.
 * Returns a length-prefixed kml string. */
char *__kml_pem_from_der(const unsigned char *der, int64_t n, const char *label) {
	char *b64 = __kml_buf_b64_enc(der, n, 0);
	int64_t bl = *(int64_t *)(b64 - 8);
	int64_t lines = (bl + 63) / 64;
	int64_t ll = (int64_t)strlen(label);
	/* "-----BEGIN -----\n" = 17 + label; "-----END -----\n" = 15 + label */
	int64_t total = 17 + ll + bl + lines + 15 + ll;
	char *out = kml_str_hdr_alloc(total);
	int64_t o = 0;
	o += sprintf(out + o, "-----BEGIN %s-----\n", label);
	for (int64_t i = 0; i < bl; i += 64) {
		int64_t chunk = bl - i < 64 ? bl - i : 64;
		memcpy(out + o, b64 + i, (size_t)chunk);
		o += chunk;
		out[o++] = '\n';
	}
	o += sprintf(out + o, "-----END %s-----\n", label);
	*(int64_t *)(out - 8) = o;
	out[o] = 0;
	return out;
}

/* __kml_buf_concat joins n arrays (each slot a pointer to a {data, len}
   header; a null slot is skipped) into one fresh byte buffer — Buffer.concat
   over a runtime list. total < 0 means the sum of the lengths; a shorter
   total truncates and a longer one zero-fills, as Node's. The length is
   written to *out_len. */
typedef struct { unsigned char *data; int64_t len; } kml_bufhdr;
unsigned char *__kml_buf_concat(kml_bufhdr **list, int64_t n, int64_t total, int64_t *out_len) {
	int64_t sum = 0;
	for (int64_t i = 0; i < n; i++)
		if (list[i]) sum += list[i]->len;
	if (total < 0) total = sum;
	unsigned char *out = calloc(total > 0 ? (size_t)total : 1, 1);
	int64_t off = 0;
	for (int64_t i = 0; i < n && off < total; i++) {
		if (!list[i]) continue;
		int64_t c = list[i]->len;
		if (c > total - off) c = total - off;
		memcpy(out + off, list[i]->data, (size_t)c);
		off += c;
	}
	*out_len = total;
	return out;
}

/* ---- an encoding named at run time ----
 * Node's normalizeEncoding: a case-insensitive name; -1 for one Node rejects
 * (ERR_UNKNOWN_ENCODING). */
enum { KML_ENC_UTF8, KML_ENC_HEX, KML_ENC_B64, KML_ENC_B64URL, KML_ENC_LATIN1, KML_ENC_ASCII, KML_ENC_UTF16LE };

static int kml_enc_id(const char *enc) {
	static const struct { const char *name; int id; } names[] = {
		{"utf8", KML_ENC_UTF8}, {"utf-8", KML_ENC_UTF8}, {"hex", KML_ENC_HEX},
		{"base64", KML_ENC_B64}, {"base64url", KML_ENC_B64URL},
		{"latin1", KML_ENC_LATIN1}, {"binary", KML_ENC_LATIN1}, {"ascii", KML_ENC_ASCII},
		{"utf16le", KML_ENC_UTF16LE}, {"utf-16le", KML_ENC_UTF16LE},
		{"ucs2", KML_ENC_UTF16LE}, {"ucs-2", KML_ENC_UTF16LE},
	};
	if (!enc) return KML_ENC_UTF8;
	for (size_t i = 0; i < sizeof names / sizeof names[0]; i++) {
		const char *a = enc, *b = names[i].name;
		while (*a && *b) {
			char c = *a;
			if (c >= 'A' && c <= 'Z') c = (char)(c - 'A' + 'a');
			if (c != *b) break;
			a++, b++;
		}
		if (!*a && !*b) return names[i].id;
	}
	return -1;
}

/* A string's bytes in the encoding named enc: their count, the buffer in
 * *out; -1 when enc names no encoding. */
int64_t __kml_buf_decode_enc(const char *s, const char *enc, unsigned char **out) {
	switch (kml_enc_id(enc)) {
	case KML_ENC_UTF8: {
		int64_t n = (int64_t)strlen(s);
		unsigned char *b = (unsigned char *)malloc((size_t)n + 1);
		memcpy(b, s, (size_t)n);
		*out = b;
		return n;
	}
	case KML_ENC_HEX: return __kml_buf_hex_dec(s, out);
	case KML_ENC_B64:
	case KML_ENC_B64URL: return __kml_buf_b64_dec(s, out);
	case KML_ENC_LATIN1:
	case KML_ENC_ASCII: return __kml_buf_latin1_bytes(s, out);
	case KML_ENC_UTF16LE: return __kml_buf_utf16le_bytes(s, out);
	}
	*out = NULL;
	return -1;
}

/* Bytes as a string in the encoding named enc; NULL when enc names none. */
char *__kml_buf_encode_enc(const unsigned char *src, int64_t n, const char *enc) {
	switch (kml_enc_id(enc)) {
	case KML_ENC_UTF8: return __kml_buf_utf8_str(src, n);
	case KML_ENC_HEX: return __kml_buf_hex_enc(src, n);
	case KML_ENC_B64: return __kml_buf_b64_enc(src, n, 0);
	case KML_ENC_B64URL: return __kml_buf_b64_enc(src, n, 1);
	case KML_ENC_LATIN1: return __kml_buf_latin1_str(src, n);
	case KML_ENC_ASCII: return __kml_buf_ascii_str(src, n);
	case KML_ENC_UTF16LE: return __kml_buf_utf16le_str(src, n);
	}
	return NULL;
}

/* Node's Buffer.byteLength(string, encoding) (lib/buffer.js): the formula
 * each encoding's ops give, over the string's length in UTF-16 code units —
 * not the bytes a decode yields (hex is length >>> 1 however valid the
 * digits are). An unknown encoding counts as utf8. */
int64_t __kml_buf_byte_length(const char *s, const char *enc) {
	size_t sl = strlen(s);
	int64_t units = 0;
	for (size_t i = 0; i < sl;) {
		int adv = 1;
		unsigned cp = kml_utf8_next(s, sl, i, &adv);
		units += cp > 0xFFFF ? 2 : 1;
		i += (size_t)adv;
	}
	if (units == 0) return 0;
	switch (kml_enc_id(enc)) {
	case KML_ENC_HEX: return units >> 1;
	case KML_ENC_B64:
	case KML_ENC_B64URL: {
		int64_t bytes = units;
		/* Padding: the string is ASCII wherever base64 is well formed. */
		if (sl >= 1 && s[sl - 1] == '=') bytes--;
		if (bytes > 1 && sl >= 2 && s[sl - 2] == '=') bytes--;
		return (bytes * 3) >> 2;
	}
	case KML_ENC_LATIN1:
	case KML_ENC_ASCII: return units;
	case KML_ENC_UTF16LE: return units * 2;
	}
	return (int64_t)sl; /* utf8, or an unknown encoding */
}
