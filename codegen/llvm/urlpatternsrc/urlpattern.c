/* urlpattern.c — the __kml_urlpattern_* ABI (TDD-00100).
 *
 * Compiles each URLPattern component pattern to an anchored PCRE2 regex once
 * at construction, and matches input URLs parsed through libcurl's URL API —
 * the same two external libraries RegExp and URL/fetch already link.
 *
 * PCRE2 and libcurl symbols are declared by hand below (no pcre2.h or
 * curl/curl.h include), the same header-less approach the emitted IR takes
 * for every pcre2/curl call — so compiling this file needs no include
 * path for either library. Constants are copied from the headers and
 * verified against them (pcre2.h: PCRE2_ANCHORED 0x80000000u,
 * PCRE2_ENDANCHORED 0x20000000u, PCRE2_ZERO_TERMINATED/PCRE2_UNSET
 * ~(PCRE2_SIZE)0; curl/urlapi.h CURLUPart: URL 0, SCHEME 1, HOST 5, PORT 6,
 * PATH 7, QUERY 8, FRAGMENT 9 — the same values emit_url.go documents).
 */

#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>

/* ---- PCRE2 (8-bit) ---- */
extern void  *pcre2_compile_8(const char *, size_t, unsigned int, int *, size_t *, void *);
extern void  *pcre2_match_data_create_from_pattern_8(const void *, void *);
extern int    pcre2_match_8(const void *, const char *, size_t, size_t, unsigned int, void *, void *);
extern size_t *pcre2_get_ovector_pointer_8(void *);
extern void   pcre2_match_data_free_8(void *);

#define KML_PCRE2_ANCHORED        0x80000000u
#define KML_PCRE2_ENDANCHORED     0x20000000u
#define KML_PCRE2_ZERO_TERMINATED (~(size_t)0)
#define KML_PCRE2_UNSET           (~(size_t)0)

/* ---- libcurl URL API ---- */
extern void *curl_url(void);
extern int   curl_url_set(void *, int, const char *, unsigned int);
extern int   curl_url_get(void *, int, char **, unsigned int);
extern void  curl_url_cleanup(void *);
extern void  curl_free(void *);

#define KML_CURLUPART_URL      0
#define KML_CURLUPART_SCHEME   1
#define KML_CURLUPART_USER     2
#define KML_CURLUPART_PASSWORD 3
#define KML_CURLUPART_HOST     5
#define KML_CURLUPART_PORT     6
#define KML_CURLUPART_PATH     7
#define KML_CURLUPART_QUERY    8
#define KML_CURLUPART_FRAGMENT 9

/* ---- Map<string,string> helpers, defined in the emitted IR ---- */
extern void *__kml_map_str_create(void);
extern void  __kml_map_str_set(void *, char *, long long);

/* Component indices — must match emit_urlpattern.go's componentOrder. */
#define KML_UP_NCOMP 8 /* 0 protocol, 1 hostname, 2 port, 3 pathname, 4 search, 5 hash, 6 username, 7 password */

typedef struct {
	void  *re[KML_UP_NCOMP];      /* compiled pcre2 code, always non-NULL after construction */
	char **names[KML_UP_NCOMP];   /* group name per capture group, in group order (NULL = unnamed) */
	int    ngroups[KML_UP_NCOMP];
} kml_urlpattern;

void *__kml_urlpattern_create(void) {
	return calloc(1, sizeof(kml_urlpattern));
}

static int regex_special(char c) {
	return strchr(".\\+*?()[]{}|^$", c) != NULL;
}

typedef struct {
	char  *buf;
	size_t len, cap;
} sbuf;

static void sb_putc(sbuf *b, char c) {
	if (b->len + 2 > b->cap) {
		b->cap = b->cap ? b->cap * 2 : 64;
		b->buf = realloc(b->buf, b->cap);
	}
	b->buf[b->len++] = c;
	b->buf[b->len] = 0;
}

static void sb_puts(sbuf *b, const char *s) {
	while (*s)
		sb_putc(b, *s++);
}

/* translate converts one component pattern to PCRE2 regex source, recording
 * capture-group names positionally. Supported grammar (TDD-00100): literal
 * text, backslash escapes, `*` full wildcards (non-capturing here — the
 * spec's numbered "0" groups are not exposed), `:name` named groups, and the
 * `?` optional modifier on a named group (with the spec's full-segment
 * folding of a preceding `/` for the pathname component). Returns NULL on
 * success or a malloc'd error message for unsupported syntax. */
static char *translate(const char *pat, int comp, char **out_re, char ***out_names, int *out_n) {
	sbuf re = {0};
	char **names = NULL;
	int nnames = 0;
	const char *segclass = (comp == 3) ? "[^/]+" : (comp == 1) ? "[^.]+" : ".+";
	re.cap = 64; /* pre-allocate so an all-empty pattern still yields "" (matches only an empty component), not a NULL compile */
	re.buf = malloc(re.cap);
	re.buf[0] = 0;

	for (size_t i = 0; pat[i]; i++) {
		char c = pat[i];
		if (c == '*') {
			sb_puts(&re, ".*");
			continue;
		}
		if (c == '\\') {
			if (!pat[i + 1])
				goto unsupported;
			i++;
			if (regex_special(pat[i]))
				sb_putc(&re, '\\');
			sb_putc(&re, pat[i]);
			continue;
		}
		if (c == ':') {
			size_t j = i + 1, start = j;
			while ((pat[j] >= 'a' && pat[j] <= 'z') || (pat[j] >= 'A' && pat[j] <= 'Z') ||
			       (pat[j] >= '0' && pat[j] <= '9') || pat[j] == '_')
				j++;
			if (j == start)
				goto unsupported; /* bare ':' with no name */
			int optional = (pat[j] == '?');
			char *name = strndup(pat + start, j - start);
			names = realloc(names, sizeof(char *) * (nnames + 1));
			names[nnames++] = name;
			if (optional && comp == 3 && re.len >= 1 && re.buf[re.len - 1] == '/' &&
			    (re.len < 2 || re.buf[re.len - 2] != '\\')) {
				/* fold "/:name?" into one optional segment group */
				re.len--;
				re.buf[re.len] = 0;
				sb_puts(&re, "(?:/(");
				sb_puts(&re, segclass);
				sb_puts(&re, "))?");
			} else {
				sb_putc(&re, '(');
				sb_puts(&re, segclass);
				sb_putc(&re, ')');
				if (optional)
					sb_putc(&re, '?');
			}
			i = j - 1 + (optional ? 1 : 0);
			continue;
		}
		if (c == '{' || c == '}' || c == '(' || c == ')' || c == '+' || c == '?')
			goto unsupported;
		if (regex_special(c))
			sb_putc(&re, '\\');
		sb_putc(&re, c);
	}
	*out_re = re.buf;
	*out_names = names;
	*out_n = nnames;
	return NULL;

unsupported:;
	char *msg = malloc(strlen(pat) + 96);
	sprintf(msg, "Invalid URLPattern component '%s': only literals, '*', ':name' and ':name?' are supported", pat);
	free(re.buf);
	for (int k = 0; k < nnames; k++)
		free(names[k]);
	free(names);
	return msg;
}

/* __kml_urlpattern_set compiles component idx's pattern. Returns NULL on
 * success or a malloc'd error message (the emitter throws it as a TypeError,
 * the spec's error type for an invalid pattern). */
char *__kml_urlpattern_set(void *uph, long long idx, const char *pat) {
	kml_urlpattern *up = uph;
	char *resrc = NULL, **names = NULL;
	int n = 0;
	char *err = translate(pat, (int)idx, &resrc, &names, &n);
	if (err)
		return err;
	int errcode = 0;
	size_t erroff = 0;
	void *code = pcre2_compile_8(resrc, KML_PCRE2_ZERO_TERMINATED,
	                             KML_PCRE2_ANCHORED | KML_PCRE2_ENDANCHORED,
	                             &errcode, &erroff, NULL);
	free(resrc);
	if (!code) {
		for (int k = 0; k < n; k++)
			free(names[k]);
		free(names);
		char *msg = malloc(strlen(pat) + 64);
		sprintf(msg, "Invalid URLPattern component '%s'", pat);
		return msg;
	}
	up->re[idx] = code;
	up->names[idx] = names;
	up->ngroups[idx] = n;
	return NULL;
}

/* parse_input splits an absolute URL into the six matchable component
 * strings (scheme without ':', hostname, port or "", path defaulting to "/",
 * query and fragment without their '?'/'#' prefixes). Returns 0 on success,
 * -1 for an unparseable URL. Each part is malloc'd. */
static int parse_input(const char *url, char *parts[KML_UP_NCOMP]) {
	void *h = curl_url();
	if (!h)
		return -1;
	if (curl_url_set(h, KML_CURLUPART_URL, url, 0) != 0) {
		curl_url_cleanup(h);
		return -1;
	}
	static const int partIDs[KML_UP_NCOMP] = {
	    KML_CURLUPART_SCHEME, KML_CURLUPART_HOST, KML_CURLUPART_PORT,
	    KML_CURLUPART_PATH, KML_CURLUPART_QUERY, KML_CURLUPART_FRAGMENT,
	    KML_CURLUPART_USER, KML_CURLUPART_PASSWORD};
	for (int i = 0; i < KML_UP_NCOMP; i++) {
		char *raw = NULL;
		if (curl_url_get(h, partIDs[i], &raw, 0) == 0 && raw) {
			parts[i] = strdup(raw);
			curl_free(raw);
		} else {
			parts[i] = strdup(i == 3 ? "/" : ""); /* absent path serializes as "/" */
		}
	}
	curl_url_cleanup(h);
	return 0;
}

static void free_parts(char *parts[KML_UP_NCOMP]) {
	for (int i = 0; i < KML_UP_NCOMP; i++)
		free(parts[i]);
}

/* match_component runs component i's regex over its input part. Returns the
 * pcre2 match data (caller frees) on a match, NULL on no match. out_rc (may
 * be NULL) receives pcre2_match's return — one more than the highest-
 * numbered ovector pair that was set, so callers can skip pairs the match
 * never touched rather than trusting them to be PCRE2_UNSET. */
static void *match_component(kml_urlpattern *up, int i, const char *part, int *out_rc) {
	void *md = pcre2_match_data_create_from_pattern_8(up->re[i], NULL);
	int rc = pcre2_match_8(up->re[i], part, KML_PCRE2_ZERO_TERMINATED, 0, 0, md, NULL);
	if (rc < 0) {
		pcre2_match_data_free_8(md);
		return NULL;
	}
	if (out_rc)
		*out_rc = rc;
	return md;
}

_Bool __kml_urlpattern_test(void *uph, const char *url) {
	kml_urlpattern *up = uph;
	char *parts[KML_UP_NCOMP];
	if (parse_input(url, parts) != 0)
		return 0;
	_Bool ok = 1;
	for (int i = 0; i < KML_UP_NCOMP && ok; i++) {
		void *md = match_component(up, i, parts[i], NULL);
		if (md)
			pcre2_match_data_free_8(md);
		else
			ok = 0;
	}
	free_parts(parts);
	return ok;
}

/* __kml_urlpattern_exec returns a Map<string,string> of every named group
 * across all components (an unset optional group is simply absent), or NULL
 * on no match / unparseable input. */
void *__kml_urlpattern_exec(void *uph, const char *url) {
	kml_urlpattern *up = uph;
	char *parts[KML_UP_NCOMP];
	if (parse_input(url, parts) != 0)
		return NULL;

	void *mds[KML_UP_NCOMP] = {0};
	int rcs[KML_UP_NCOMP] = {0};
	for (int i = 0; i < KML_UP_NCOMP; i++) {
		mds[i] = match_component(up, i, parts[i], &rcs[i]);
		if (!mds[i]) {
			for (int k = 0; k < i; k++)
				pcre2_match_data_free_8(mds[k]);
			free_parts(parts);
			return NULL;
		}
	}

	void *map = __kml_map_str_create();
	for (int i = 0; i < KML_UP_NCOMP; i++) {
		size_t *ovec = pcre2_get_ovector_pointer_8(mds[i]);
		for (int g = 1; g <= up->ngroups[i]; g++) {
			if (g >= rcs[i])
				break; /* pairs past pcre2_match's return were never set */
			size_t s = ovec[2 * g], e = ovec[2 * g + 1];
			if (s == KML_PCRE2_UNSET)
				continue; /* unset optional group — absent from the map */
			char *val = strndup(parts[i] + s, e - s);
			__kml_map_str_set(map, up->names[i][g - 1], (long long)(intptr_t)val);
		}
		pcre2_match_data_free_8(mds[i]);
	}
	free_parts(parts);
	return map;
}
