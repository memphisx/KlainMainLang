/* path_win32.c — Node's `path.win32` flavour (TDD-00178), a function-by-
 * function port of lib/path.js's `win32` object. Pure string code on libc,
 * compiled with the program on every host so `path.win32.*` works on
 * Linux/macOS exactly as it does on Windows (where it is also the default
 * `path`). Strings cross the boundary as this compiler's heap strings
 * (TDD-00120): [i64 len][bytes][\0], value pointer at base+8. Inputs are
 * read up to their NUL; outputs are freshly allocated in that layout.
 *
 * Function names and local variable names follow the JS deliberately so the
 * two can be reviewed side by side. Index arithmetic is on bytes; Node's is
 * on UTF-16 code units, but every character these algorithms test for is
 * ASCII, and multi-byte UTF-8 sequences never contain an ASCII byte, so the
 * results agree for well-formed UTF-8 input.
 */

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <ctype.h>

#define CHAR_DOT '.'
#define CHAR_FORWARD_SLASH '/'
#define CHAR_BACKWARD_SLASH '\\'
#define CHAR_COLON ':'

/* ---- heap-string helpers ------------------------------------------------ */

static char *kml_str_alloc(int64_t n) {
    char *base = (char *)malloc(8 + n + 1);
    if (!base) return NULL;
    *(int64_t *)base = n;
    char *p = base + 8;
    p[n] = 0;
    return p;
}

static char *kml_str_from(const char *s, int64_t n) {
    char *p = kml_str_alloc(n);
    if (p && n > 0) memcpy(p, s, (size_t)n);
    return p;
}

static char *kml_str_dup(const char *s) {
    return kml_str_from(s, (int64_t)strlen(s));
}

/* A growable scratch buffer for the JS `res += ...` idiom. */
typedef struct {
    char *buf;
    int64_t len;
    int64_t cap;
} sb_t;

static void sb_init(sb_t *b) { b->buf = NULL; b->len = 0; b->cap = 0; }

static void sb_reserve(sb_t *b, int64_t extra) {
    if (b->len + extra + 1 <= b->cap) return;
    int64_t ncap = b->cap ? b->cap * 2 : 32;
    while (ncap < b->len + extra + 1) ncap *= 2;
    b->buf = (char *)realloc(b->buf, (size_t)ncap);
    b->cap = ncap;
}

static void sb_append_n(sb_t *b, const char *s, int64_t n) {
    if (n <= 0) return;
    sb_reserve(b, n);
    memcpy(b->buf + b->len, s, (size_t)n);
    b->len += n;
    b->buf[b->len] = 0;
}

static void sb_append(sb_t *b, const char *s) { sb_append_n(b, s, (int64_t)strlen(s)); }
static void sb_append_ch(sb_t *b, char c) { sb_append_n(b, &c, 1); }
static void sb_truncate(sb_t *b, int64_t n) { b->len = n; if (b->buf) b->buf[n] = 0; }
static const char *sb_cstr(const sb_t *b) { return b->buf ? b->buf : ""; }

/* Finish: hand the bytes out as a heap string and release the scratch. */
static char *sb_finish(sb_t *b) {
    char *out = kml_str_from(sb_cstr(b), b->len);
    free(b->buf);
    return out;
}

/* ---- Node helpers -------------------------------------------------------- */

static int isPathSeparator(int code) {
    return code == CHAR_FORWARD_SLASH || code == CHAR_BACKWARD_SLASH;
}

static int isPosixPathSeparator(int code) { return code == CHAR_FORWARD_SLASH; }

static int isWindowsDeviceRoot(int code) {
    return (code >= 'A' && code <= 'Z') || (code >= 'a' && code <= 'z');
}

/* WINDOWS_RESERVED_NAMES, compared case-insensitively against the part of
 * the path before `colonIndex`. Node's list also has the superscript-digit
 * variants (COM¹ etc., U+00B9/B2/B3); in UTF-8 those are two bytes, spelled
 * out here as the byte sequences. */
static int isWindowsReservedName(const char *path, int64_t colonIndex) {
    static const char *const names[] = {
        "CON", "PRN", "AUX", "NUL",
        "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
        "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
        "COM\xc2\xb9", "COM\xc2\xb2", "COM\xc2\xb3",
        "LPT\xc2\xb9", "LPT\xc2\xb2", "LPT\xc2\xb3",
        NULL,
    };
    if (colonIndex < 0) return 0;
    for (int i = 0; names[i]; i++) {
        const char *n = names[i];
        int64_t nl = (int64_t)strlen(n);
        if (nl != colonIndex) continue;
        int64_t k = 0;
        for (; k < nl; k++) {
            if (toupper((unsigned char)path[k]) != (unsigned char)n[k]) break;
        }
        if (k == nl) return 1;
    }
    return 0;
}

static int64_t indexOfFrom(const char *s, int64_t len, char c, int64_t from) {
    for (int64_t i = from; i < len; i++) if (s[i] == c) return i;
    return -1;
}

static int64_t lastIndexOf(const char *s, int64_t len, char c) {
    for (int64_t i = len - 1; i >= 0; i--) if (s[i] == c) return i;
    return -1;
}

/* normalizeString(path, allowAboveRoot, '\\', isPathSeparator): resolves .
 * and .. segments of `path` (which has already had its root stripped) and
 * rejoins with `separator`. Result written into `res`. */
static void normalizeString(const char *path, int64_t len, int allowAboveRoot, char separator, sb_t *res) {
    int64_t lastSegmentLength = 0;
    int64_t lastSlash = -1;
    int dots = 0;
    int code = 0;
    for (int64_t i = 0; i <= len; ++i) {
        if (i < len)
            code = (unsigned char)path[i];
        else if (isPathSeparator(code))
            break;
        else
            code = CHAR_FORWARD_SLASH;

        if (isPathSeparator(code)) {
            if (lastSlash == i - 1 || dots == 1) {
                /* NOOP */
            } else if (dots == 2) {
                if (res->len < 2 || lastSegmentLength != 2 ||
                    res->buf[res->len - 1] != CHAR_DOT ||
                    res->buf[res->len - 2] != CHAR_DOT) {
                    if (res->len > 2) {
                        int64_t lastSlashIndex = res->len - lastSegmentLength - 1;
                        if (lastSlashIndex == -1) {
                            sb_truncate(res, 0);
                            lastSegmentLength = 0;
                        } else {
                            sb_truncate(res, lastSlashIndex);
                            lastSegmentLength = res->len - 1 - lastIndexOf(res->buf, res->len, separator);
                        }
                        lastSlash = i;
                        dots = 0;
                        continue;
                    } else if (res->len != 0) {
                        sb_truncate(res, 0);
                        lastSegmentLength = 0;
                        lastSlash = i;
                        dots = 0;
                        continue;
                    }
                }
                if (allowAboveRoot) {
                    if (res->len > 0) sb_append_ch(res, separator);
                    sb_append(res, "..");
                    lastSegmentLength = 2;
                }
            } else {
                if (res->len > 0) {
                    sb_append_ch(res, separator);
                    sb_append_n(res, path + lastSlash + 1, i - (lastSlash + 1));
                } else {
                    sb_truncate(res, 0);
                    sb_append_n(res, path + lastSlash + 1, i - (lastSlash + 1));
                }
                lastSegmentLength = i - lastSlash - 1;
            }
            lastSlash = i;
            dots = 0;
        } else if (code == CHAR_DOT && dots != -1) {
            ++dots;
        } else {
            dots = -1;
        }
    }
}

static int str_ieq(const char *a, int64_t alen, const char *b, int64_t blen) {
    if (alen != blen) return 0;
    for (int64_t i = 0; i < alen; i++)
        if (tolower((unsigned char)a[i]) != tolower((unsigned char)b[i])) return 0;
    return 1;
}

/* ---- win32.normalize ------------------------------------------------------ */

static char *win32_normalize(const char *path) {
    int64_t len = (int64_t)strlen(path);
    if (len == 0) return kml_str_dup(".");
    int64_t rootEnd = 0;
    sb_t device; sb_init(&device);
    int haveDevice = 0;
    int isAbsolute = 0;
    int code = (unsigned char)path[0];

    if (len == 1) {
        return isPosixPathSeparator(code) ? kml_str_dup("\\") : kml_str_dup(path);
    }
    if (isPathSeparator(code)) {
        isAbsolute = 1;
        if (isPathSeparator((unsigned char)path[1])) {
            int64_t j = 2;
            int64_t last = j;
            while (j < len && !isPathSeparator((unsigned char)path[j])) j++;
            if (j < len && j != last) {
                const char *firstPart = path + last;
                int64_t firstPartLen = j - last;
                last = j;
                while (j < len && isPathSeparator((unsigned char)path[j])) j++;
                if (j < len && j != last) {
                    last = j;
                    while (j < len && !isPathSeparator((unsigned char)path[j])) j++;
                    if (j == len || j != last) {
                        if ((firstPartLen == 1 && firstPart[0] == '.') ||
                            (firstPartLen == 1 && firstPart[0] == '?')) {
                            sb_append(&device, "\\\\");
                            sb_append_n(&device, firstPart, firstPartLen);
                            haveDevice = 1;
                            rootEnd = 4;
                            int64_t colonIndex = indexOfFrom(path, len, ':', 0);
                            /* possibleDevice = path.slice(4, colonIndex + 1) */
                            int64_t pdLen = (colonIndex + 1) - 4;
                            if (pdLen < 0) pdLen = 0;
                            if (pdLen > 0 && isWindowsReservedName(path + 4, pdLen - 1)) {
                                sb_truncate(&device, 0);
                                sb_append(&device, "\\\\?\\");
                                sb_append_n(&device, path + 4, pdLen);
                                rootEnd = 4 + pdLen;
                            }
                        } else if (j == len) {
                            /* `\\\\${firstPart}\\${path.slice(last)}\\` */
                            sb_t out; sb_init(&out);
                            sb_append(&out, "\\\\");
                            sb_append_n(&out, firstPart, firstPartLen);
                            sb_append_ch(&out, '\\');
                            sb_append_n(&out, path + last, len - last);
                            sb_append_ch(&out, '\\');
                            free(device.buf);
                            return sb_finish(&out);
                        } else {
                            sb_append(&device, "\\\\");
                            sb_append_n(&device, firstPart, firstPartLen);
                            sb_append_ch(&device, '\\');
                            sb_append_n(&device, path + last, j - last);
                            haveDevice = 1;
                            rootEnd = j;
                        }
                    }
                }
            }
        } else {
            rootEnd = 1;
        }
    } else {
        int64_t colonIndex = indexOfFrom(path, len, ':', 0);
        if (colonIndex > 0) {
            if (isWindowsDeviceRoot(code) && colonIndex == 1) {
                sb_append_n(&device, path, 2);
                haveDevice = 1;
                rootEnd = 2;
                if (len > 2 && isPathSeparator((unsigned char)path[2])) {
                    isAbsolute = 1;
                    rootEnd = 3;
                }
            } else if (isWindowsReservedName(path, colonIndex)) {
                sb_append_n(&device, path, colonIndex + 1);
                haveDevice = 1;
                rootEnd = colonIndex + 1;
            }
        }
    }

    sb_t tail; sb_init(&tail);
    if (rootEnd < len)
        normalizeString(path + rootEnd, len - rootEnd, !isAbsolute, '\\', &tail);
    if (tail.len == 0 && !isAbsolute) sb_append_ch(&tail, '.');
    if (tail.len > 0 && isPathSeparator((unsigned char)path[len - 1])) sb_append_ch(&tail, '\\');

    if (!isAbsolute && !haveDevice && indexOfFrom(path, len, ':', 0) != -1) {
        if (tail.len >= 2 && isWindowsDeviceRoot((unsigned char)tail.buf[0]) && tail.buf[1] == CHAR_COLON) {
            sb_t out; sb_init(&out);
            sb_append(&out, ".\\");
            sb_append_n(&out, sb_cstr(&tail), tail.len);
            free(tail.buf); free(device.buf);
            return sb_finish(&out);
        }
        int64_t index = indexOfFrom(path, len, ':', 0);
        do {
            if (index == len - 1 || isPathSeparator((unsigned char)path[index + 1])) {
                sb_t out; sb_init(&out);
                sb_append(&out, ".\\");
                sb_append_n(&out, sb_cstr(&tail), tail.len);
                free(tail.buf); free(device.buf);
                return sb_finish(&out);
            }
        } while ((index = indexOfFrom(path, len, ':', index + 1)) != -1);
    }

    {
        int64_t colonIndex = indexOfFrom(path, len, ':', 0);
        if (isWindowsReservedName(path, colonIndex)) {
            sb_t out; sb_init(&out);
            sb_append(&out, ".\\");
            if (haveDevice) sb_append_n(&out, sb_cstr(&device), device.len);
            sb_append_n(&out, sb_cstr(&tail), tail.len);
            free(tail.buf); free(device.buf);
            return sb_finish(&out);
        }
    }

    sb_t out; sb_init(&out);
    if (!haveDevice) {
        if (isAbsolute) sb_append_ch(&out, '\\');
        sb_append_n(&out, sb_cstr(&tail), tail.len);
    } else {
        sb_append_n(&out, sb_cstr(&device), device.len);
        if (isAbsolute) sb_append_ch(&out, '\\');
        sb_append_n(&out, sb_cstr(&tail), tail.len);
    }
    free(tail.buf); free(device.buf);
    return sb_finish(&out);
}

/* ---- win32.isAbsolute ----------------------------------------------------- */

int __kml_path_win32_is_absolute(const char *path) {
    int64_t len = (int64_t)strlen(path);
    if (len == 0) return 0;
    int code = (unsigned char)path[0];
    return isPathSeparator(code) ||
           (len > 2 && isWindowsDeviceRoot(code) && path[1] == CHAR_COLON &&
            isPathSeparator((unsigned char)path[2]));
}

/* ---- win32.join ------------------------------------------------------------ */

char *__kml_path_win32_join(int64_t n, char **args) {
    if (n == 0) return kml_str_dup(".");
    /* path = args.filter(nonEmpty); joined = path.join('\\') */
    sb_t joined; sb_init(&joined);
    const char *firstPart = NULL;
    int64_t count = 0;
    for (int64_t i = 0; i < n; ++i) {
        const char *arg = args[i];
        if (arg[0] != 0) {
            if (count == 0) firstPart = arg; else sb_append_ch(&joined, '\\');
            sb_append(&joined, arg);
            count++;
        }
    }
    if (count == 0) { free(joined.buf); return kml_str_dup("."); }

    int needsReplace = 1;
    int64_t slashCount = 0;
    if (isPathSeparator((unsigned char)firstPart[0])) {
        ++slashCount;
        int64_t firstLen = (int64_t)strlen(firstPart);
        if (firstLen > 1 && isPathSeparator((unsigned char)firstPart[1])) {
            ++slashCount;
            if (firstLen > 2) {
                if (isPathSeparator((unsigned char)firstPart[2]))
                    ++slashCount;
                else
                    needsReplace = 0; /* a UNC path: keep it */
            }
        }
    }
    if (needsReplace) {
        while (slashCount < joined.len && isPathSeparator((unsigned char)joined.buf[slashCount])) slashCount++;
        if (slashCount >= 2) {
            /* joined = `\\${joined.slice(slashCount)}` */
            int64_t rest = joined.len - slashCount;
            memmove(joined.buf + 1, joined.buf + slashCount, (size_t)rest);
            joined.buf[0] = '\\';
            sb_truncate(&joined, rest + 1);
        }
    }

    /* Reserved-device check over the '\\'-split parts: any part naming a
     * device (CON, COM1:...) means the joined path is returned with '/'
     * flipped to '\\' but otherwise un-normalized. */
    {
        int64_t i = 0;
        int reserved = 0;
        const char *jb = sb_cstr(&joined);
        while (i < joined.len && !reserved) {
            int64_t start = i;
            while (i < joined.len && jb[i] != '\\') i++;
            int64_t plen = i - start;
            if (plen > 0) {
                int64_t colon = indexOfFrom(jb + start, plen, ':', 0);
                if (colon != -1 && isWindowsReservedName(jb + start, colon)) reserved = 1;
            }
            while (i < joined.len && jb[i] == '\\') i++;
        }
        if (reserved) {
            for (int64_t k = 0; k < joined.len; k++) if (joined.buf[k] == '/') joined.buf[k] = '\\';
            return sb_finish(&joined);
        }
    }

    char *out = win32_normalize(sb_cstr(&joined));
    free(joined.buf);
    return out;
}

/* ---- win32.resolve --------------------------------------------------------- */

/* `cwd` is process.cwd() as the emitter obtained it; `host_is_windows` is
 * Node's `isWindows` (the only host branch in the win32 code). */
char *__kml_path_win32_resolve(int64_t n, char **args, const char *cwd, int host_is_windows) {
    sb_t resolvedDevice; sb_init(&resolvedDevice);
    sb_t resolvedTail; sb_init(&resolvedTail);
    int resolvedAbsolute = 0;
    char *drivecwd = NULL; /* owned copy of process.env['=X:'] when used */

    for (int64_t i = n - 1; i >= -1; i--) {
        const char *path;
        if (i >= 0) {
            path = args[i];
            if (path[0] == 0) continue;
        } else if (resolvedDevice.len == 0) {
            path = cwd;
            if (n == 0 || ((n == 1 && (args[0][0] == 0 || (args[0][0] == '.' && args[0][1] == 0))) &&
                           isPathSeparator((unsigned char)path[0]))) {
                char *out = kml_str_dup(path);
                if (!host_is_windows && out) {
                    for (char *p = out; *p; p++) if (*p == '/') *p = '\\';
                }
                free(resolvedDevice.buf); free(resolvedTail.buf);
                return out;
            }
        } else {
            /* path = process.env[`=${resolvedDevice}`] || process.cwd() */
            char envname[64];
            const char *envval = NULL;
            if (resolvedDevice.len + 2 < (int64_t)sizeof envname) {
                envname[0] = '=';
                memcpy(envname + 1, sb_cstr(&resolvedDevice), (size_t)resolvedDevice.len);
                envname[resolvedDevice.len + 1] = 0;
                envval = getenv(envname);
            }
            if (envval && envval[0]) {
                free(drivecwd);
                drivecwd = strdup(envval);
                path = drivecwd;
            } else {
                path = cwd;
            }
            /* if (path === undefined || (path.slice(0,2).toLowerCase() !== resolvedDevice.toLowerCase() && path[2] === '\\')) path = `${resolvedDevice}\\` */
            int64_t plen = (int64_t)strlen(path);
            int64_t head = plen < 2 ? plen : 2;
            if (!str_ieq(path, head, sb_cstr(&resolvedDevice), resolvedDevice.len) &&
                plen > 2 && path[2] == CHAR_BACKWARD_SLASH) {
                free(drivecwd);
                drivecwd = (char *)malloc((size_t)resolvedDevice.len + 2);
                memcpy(drivecwd, sb_cstr(&resolvedDevice), (size_t)resolvedDevice.len);
                drivecwd[resolvedDevice.len] = '\\';
                drivecwd[resolvedDevice.len + 1] = 0;
                path = drivecwd;
            }
        }

        int64_t len = (int64_t)strlen(path);
        int64_t rootEnd = 0;
        sb_t device; sb_init(&device);
        int isAbsolute = 0;
        int code = (unsigned char)path[0];

        if (len == 1) {
            if (isPathSeparator(code)) {
                rootEnd = 1;
                isAbsolute = 1;
            }
        } else if (isPathSeparator(code)) {
            isAbsolute = 1;
            if (isPathSeparator((unsigned char)path[1])) {
                int64_t j = 2;
                int64_t last = j;
                while (j < len && !isPathSeparator((unsigned char)path[j])) j++;
                if (j < len && j != last) {
                    const char *firstPart = path + last;
                    int64_t firstPartLen = j - last;
                    last = j;
                    while (j < len && isPathSeparator((unsigned char)path[j])) j++;
                    if (j < len && j != last) {
                        last = j;
                        while (j < len && !isPathSeparator((unsigned char)path[j])) j++;
                        if (j == len || j != last) {
                            if (!(firstPartLen == 1 && (firstPart[0] == '.' || firstPart[0] == '?'))) {
                                sb_append(&device, "\\\\");
                                sb_append_n(&device, firstPart, firstPartLen);
                                sb_append_ch(&device, '\\');
                                sb_append_n(&device, path + last, j - last);
                                rootEnd = j;
                            } else {
                                sb_append(&device, "\\\\");
                                sb_append_n(&device, firstPart, firstPartLen);
                                rootEnd = 4;
                            }
                        }
                    }
                }
            } else {
                rootEnd = 1;
            }
        } else if (isWindowsDeviceRoot(code) && path[1] == CHAR_COLON) {
            sb_append_n(&device, path, 2);
            rootEnd = 2;
            if (len > 2 && isPathSeparator((unsigned char)path[2])) {
                isAbsolute = 1;
                rootEnd = 3;
            }
        }

        if (device.len > 0) {
            if (resolvedDevice.len > 0) {
                if (!str_ieq(sb_cstr(&device), device.len, sb_cstr(&resolvedDevice), resolvedDevice.len)) {
                    free(device.buf);
                    continue;
                }
            } else {
                sb_append_n(&resolvedDevice, sb_cstr(&device), device.len);
            }
        }
        free(device.buf);

        if (resolvedAbsolute) {
            if (resolvedDevice.len > 0) break;
        } else {
            /* resolvedTail = `${path.slice(rootEnd)}\\${resolvedTail}` */
            sb_t nt; sb_init(&nt);
            sb_append_n(&nt, path + rootEnd, len - rootEnd);
            sb_append_ch(&nt, '\\');
            sb_append_n(&nt, sb_cstr(&resolvedTail), resolvedTail.len);
            free(resolvedTail.buf);
            resolvedTail = nt;
            resolvedAbsolute = isAbsolute;
            if (isAbsolute && resolvedDevice.len > 0) break;
        }
    }

    sb_t tail; sb_init(&tail);
    normalizeString(sb_cstr(&resolvedTail), resolvedTail.len, !resolvedAbsolute, '\\', &tail);

    sb_t out; sb_init(&out);
    sb_append_n(&out, sb_cstr(&resolvedDevice), resolvedDevice.len);
    if (resolvedAbsolute) sb_append_ch(&out, '\\');
    sb_append_n(&out, sb_cstr(&tail), tail.len);
    if (out.len == 0) sb_append_ch(&out, '.');

    free(tail.buf); free(resolvedTail.buf); free(resolvedDevice.buf); free(drivecwd);
    return sb_finish(&out);
}

/* ---- win32.dirname --------------------------------------------------------- */

char *__kml_path_win32_dirname(const char *path) {
    int64_t len = (int64_t)strlen(path);
    if (len == 0) return kml_str_dup(".");
    int64_t rootEnd = -1;
    int64_t offset = 0;
    int code = (unsigned char)path[0];

    if (len == 1) return isPathSeparator(code) ? kml_str_dup(path) : kml_str_dup(".");

    if (isPathSeparator(code)) {
        rootEnd = offset = 1;
        if (isPathSeparator((unsigned char)path[1])) {
            int64_t j = 2;
            int64_t last = j;
            while (j < len && !isPathSeparator((unsigned char)path[j])) j++;
            if (j < len && j != last) {
                last = j;
                while (j < len && isPathSeparator((unsigned char)path[j])) j++;
                if (j < len && j != last) {
                    last = j;
                    while (j < len && !isPathSeparator((unsigned char)path[j])) j++;
                    if (j == len) return kml_str_dup(path);
                    if (j != last) rootEnd = offset = j + 1;
                }
            }
        }
    } else if (isWindowsDeviceRoot(code) && path[1] == CHAR_COLON) {
        rootEnd = (len > 2 && isPathSeparator((unsigned char)path[2])) ? 3 : 2;
        offset = rootEnd;
    }

    int64_t end = -1;
    int matchedSlash = 1;
    for (int64_t i = len - 1; i >= offset; --i) {
        if (isPathSeparator((unsigned char)path[i])) {
            if (!matchedSlash) { end = i; break; }
        } else {
            /* We saw the first non-path separator */
            matchedSlash = 0;
        }
    }
    if (end == -1) {
        if (rootEnd == -1) return kml_str_dup(".");
        end = rootEnd;
    }
    return kml_str_from(path, end);
}

/* ---- win32.basename -------------------------------------------------------- */

char *__kml_path_win32_basename(const char *path, const char *suffix) {
    int64_t plen = (int64_t)strlen(path);
    int64_t start = 0;
    int64_t end = -1;
    int matchedSlash = 1;

    if (plen >= 2 && isWindowsDeviceRoot((unsigned char)path[0]) && path[1] == CHAR_COLON) start = 2;

    if (suffix != NULL) {
        int64_t slen = (int64_t)strlen(suffix);
        if (slen > 0 && slen <= plen) {
            if (strcmp(suffix, path) == 0) return kml_str_dup("");
            int64_t extIdx = slen - 1;
            int64_t firstNonSlashEnd = -1;
            for (int64_t i = plen - 1; i >= start; --i) {
                int code = (unsigned char)path[i];
                if (isPathSeparator(code)) {
                    if (!matchedSlash) { start = i + 1; break; }
                } else {
                    if (firstNonSlashEnd == -1) {
                        matchedSlash = 0;
                        firstNonSlashEnd = i + 1;
                    }
                    if (extIdx >= 0) {
                        if (code == (unsigned char)suffix[extIdx]) {
                            if (--extIdx == -1) end = i;
                        } else {
                            extIdx = -1;
                            end = firstNonSlashEnd;
                        }
                    }
                }
            }
            if (start == end) end = firstNonSlashEnd;
            else if (end == -1) end = plen;
            return kml_str_from(path + start, end - start);
        }
    }

    for (int64_t i = plen - 1; i >= start; --i) {
        if (isPathSeparator((unsigned char)path[i])) {
            if (!matchedSlash) { start = i + 1; break; }
        } else if (end == -1) {
            matchedSlash = 0;
            end = i + 1;
        }
    }
    if (end == -1) return kml_str_dup("");
    return kml_str_from(path + start, end - start);
}

/* ---- win32.extname --------------------------------------------------------- */

char *__kml_path_win32_extname(const char *path) {
    int64_t plen = (int64_t)strlen(path);
    int64_t start = 0;
    int64_t startDot = -1;
    int64_t startPart = 0;
    int64_t end = -1;
    int matchedSlash = 1;
    int preDotState = 0;

    if (plen >= 2 && path[1] == CHAR_COLON && isWindowsDeviceRoot((unsigned char)path[0])) start = startPart = 2;

    for (int64_t i = plen - 1; i >= start; --i) {
        int code = (unsigned char)path[i];
        if (isPathSeparator(code)) {
            if (!matchedSlash) { startPart = i + 1; break; }
            continue;
        }
        if (end == -1) { matchedSlash = 0; end = i + 1; }
        if (code == CHAR_DOT) {
            if (startDot == -1) startDot = i;
            else if (preDotState != 1) preDotState = 1;
        } else if (startDot != -1) {
            preDotState = -1;
        }
    }

    if (startDot == -1 || end == -1 || preDotState == 0 ||
        (preDotState == 1 && startDot == end - 1 && startDot == startPart + 1)) {
        return kml_str_dup("");
    }
    return kml_str_from(path + startDot, end - startDot);
}

/* ---- win32.parse ----------------------------------------------------------- */

void __kml_path_win32_parse(const char *path, char **root, char **dir, char **base, char **ext, char **name) {
    *root = kml_str_dup(""); *dir = kml_str_dup(""); *base = kml_str_dup(""); *ext = kml_str_dup(""); *name = kml_str_dup("");
    int64_t len = (int64_t)strlen(path);
    if (len == 0) return;
    int64_t rootEnd = 0;
    int code = (unsigned char)path[0];

    if (len == 1) {
        if (isPathSeparator(code)) { *root = kml_str_dup(path); *dir = kml_str_dup(path); return; }
        *base = kml_str_dup(path); *name = kml_str_dup(path);
        return;
    }
    if (isPathSeparator(code)) {
        rootEnd = 1;
        if (isPathSeparator((unsigned char)path[1])) {
            int64_t j = 2;
            int64_t last = j;
            while (j < len && !isPathSeparator((unsigned char)path[j])) j++;
            if (j < len && j != last) {
                last = j;
                while (j < len && isPathSeparator((unsigned char)path[j])) j++;
                if (j < len && j != last) {
                    last = j;
                    while (j < len && !isPathSeparator((unsigned char)path[j])) j++;
                    if (j == len) rootEnd = j;
                    else if (j != last) rootEnd = j + 1;
                }
            }
        }
    } else if (isWindowsDeviceRoot(code) && path[1] == CHAR_COLON) {
        if (len <= 2) { *root = kml_str_dup(path); *dir = kml_str_dup(path); return; }
        rootEnd = 2;
        if (isPathSeparator((unsigned char)path[2])) {
            if (len == 3) { *root = kml_str_dup(path); *dir = kml_str_dup(path); return; }
            rootEnd = 3;
        }
    }
    if (rootEnd > 0) *root = kml_str_from(path, rootEnd);

    int64_t startDot = -1;
    int64_t startPart = rootEnd;
    int64_t end = -1;
    int matchedSlash = 1;
    int64_t i = len - 1;
    int preDotState = 0;

    for (; i >= rootEnd; --i) {
        code = (unsigned char)path[i];
        if (isPathSeparator(code)) {
            if (!matchedSlash) { startPart = i + 1; break; }
            continue;
        }
        if (end == -1) { matchedSlash = 0; end = i + 1; }
        if (code == CHAR_DOT) {
            if (startDot == -1) startDot = i;
            else if (preDotState != 1) preDotState = 1;
        } else if (startDot != -1) {
            preDotState = -1;
        }
    }

    if (end != -1) {
        if (startDot == -1 || preDotState == 0 ||
            (preDotState == 1 && startDot == end - 1 && startDot == startPart + 1)) {
            *base = kml_str_from(path + startPart, end - startPart);
            *name = kml_str_from(path + startPart, end - startPart);
        } else {
            *name = kml_str_from(path + startPart, startDot - startPart);
            *base = kml_str_from(path + startPart, end - startPart);
            *ext = kml_str_from(path + startDot, end - startDot);
        }
    }

    if (startPart > 0 && startPart != rootEnd) *dir = kml_str_from(path, startPart - 1);
    else *dir = kml_str_dup(*root);
}

/* ---- win32.normalize (exposed) / relative / toNamespacedPath --------------- */

char *__kml_path_win32_normalize(const char *path) { return win32_normalize(path); }

static char *win32_resolve1(const char *p, const char *cwd, int host_is_windows) {
    char *args[1] = { (char *)p };
    return __kml_path_win32_resolve(1, args, cwd, host_is_windows);
}

char *__kml_path_win32_relative(const char *from_in, const char *to_in, const char *cwd, int host_is_windows) {
    if (strcmp(from_in, to_in) == 0) return kml_str_dup("");
    char *fromOrig = win32_resolve1(from_in, cwd, host_is_windows);
    char *toOrig = win32_resolve1(to_in, cwd, host_is_windows);
    if (strcmp(fromOrig, toOrig) == 0) return kml_str_dup("");
    /* from = fromOrig.toLowerCase(); to = toOrig.toLowerCase() — ASCII case
     * folding never changes the byte length, so Node's "length changed"
     * branch (Unicode special-casing) cannot arise here. */
    int64_t flen = (int64_t)strlen(fromOrig), tlen = (int64_t)strlen(toOrig);
    char *from = (char *)malloc((size_t)flen + 1), *to = (char *)malloc((size_t)tlen + 1);
    for (int64_t k = 0; k <= flen; k++) from[k] = (char)tolower((unsigned char)fromOrig[k]);
    for (int64_t k = 0; k <= tlen; k++) to[k] = (char)tolower((unsigned char)toOrig[k]);
    if (strcmp(from, to) == 0) { free(from); free(to); return kml_str_dup(""); }

    int64_t fromStart = 0;
    while (fromStart < flen && from[fromStart] == CHAR_BACKWARD_SLASH) fromStart++;
    int64_t fromEnd = flen;
    while (fromEnd - 1 > fromStart && from[fromEnd - 1] == CHAR_BACKWARD_SLASH) fromEnd--;
    int64_t fromLen = fromEnd - fromStart;

    int64_t toStart = 0;
    while (toStart < tlen && to[toStart] == CHAR_BACKWARD_SLASH) toStart++;
    int64_t toEnd = tlen;
    while (toEnd - 1 > toStart && to[toEnd - 1] == CHAR_BACKWARD_SLASH) toEnd--;
    int64_t toLen = toEnd - toStart;

    int64_t length = fromLen < toLen ? fromLen : toLen;
    int64_t lastCommonSep = -1;
    int64_t i = 0;
    for (; i < length; i++) {
        int fromCode = (unsigned char)from[fromStart + i];
        if (fromCode != (unsigned char)to[toStart + i]) break;
        else if (fromCode == CHAR_BACKWARD_SLASH) lastCommonSep = i;
    }

    if (i != length) {
        if (lastCommonSep == -1) { free(from); free(to); return toOrig; }
    } else {
        if (toLen > length) {
            if (to[toStart + i] == CHAR_BACKWARD_SLASH) {
                char *r = kml_str_dup(toOrig + toStart + i + 1);
                free(from); free(to); return r;
            }
            if (i == 2) {
                char *r = kml_str_dup(toOrig + toStart + i);
                free(from); free(to); return r;
            }
        }
        if (fromLen > length) {
            if (from[fromStart + i] == CHAR_BACKWARD_SLASH) lastCommonSep = i;
            else if (i == 2) lastCommonSep = 3;
        }
        if (lastCommonSep == -1) lastCommonSep = 0;
    }

    sb_t out; sb_init(&out);
    for (i = fromStart + lastCommonSep + 1; i <= fromEnd; ++i) {
        if (i == fromEnd || from[i] == CHAR_BACKWARD_SLASH) {
            sb_append(&out, out.len == 0 ? ".." : "\\..");
        }
    }
    toStart += lastCommonSep;
    char *r;
    if (out.len > 0) {
        sb_append_n(&out, toOrig + toStart, toEnd - toStart);
        r = sb_finish(&out);
    } else {
        free(out.buf);
        if (toOrig[toStart] == CHAR_BACKWARD_SLASH) ++toStart;
        r = kml_str_from(toOrig + toStart, toEnd - toStart);
    }
    free(from); free(to);
    return r;
}

char *__kml_path_win32_to_namespaced_path(const char *path, const char *cwd, int host_is_windows) {
    if (path[0] == 0) return kml_str_dup(path);
    char *resolvedPath = win32_resolve1(path, cwd, host_is_windows);
    int64_t rl = (int64_t)strlen(resolvedPath);
    if (rl <= 2) return kml_str_dup(path);
    if (resolvedPath[0] == CHAR_BACKWARD_SLASH) {
        if (resolvedPath[1] == CHAR_BACKWARD_SLASH) {
            int code = (unsigned char)resolvedPath[2];
            if (code != '?' && code != CHAR_DOT) {
                sb_t b; sb_init(&b);
                sb_append(&b, "\\\\?\\UNC\\");
                sb_append(&b, resolvedPath + 2);
                return sb_finish(&b);
            }
        }
    } else if (isWindowsDeviceRoot((unsigned char)resolvedPath[0]) &&
               resolvedPath[1] == CHAR_COLON && resolvedPath[2] == CHAR_BACKWARD_SLASH) {
        sb_t b; sb_init(&b);
        sb_append(&b, "\\\\?\\");
        sb_append(&b, resolvedPath);
        return sb_finish(&b);
    }
    return resolvedPath;
}

/* ---- posix.normalize / resolve / relative --------------------------------------
 * The posix flavour's join/dirname/basename/extname/parse/format/isAbsolute
 * stay in the emitted-IR runtime (runtime_path.go); these three are ported
 * here because they share normalizeString, and (unlike the IR join) keep a
 * trailing separator exactly as Node does. toNamespacedPath is the identity
 * for posix and is emitted inline. */

static int isPosixSep(int code) { return code == CHAR_FORWARD_SLASH; }

/* normalizeString with a pluggable separator test (Node passes the predicate). */
static void normalizeStringWith(const char *path, int64_t len, int allowAboveRoot, char separator,
                                int (*isSep)(int), sb_t *res) {
    int64_t lastSegmentLength = 0;
    int64_t lastSlash = -1;
    int dots = 0;
    int code = 0;
    for (int64_t i = 0; i <= len; ++i) {
        if (i < len) code = (unsigned char)path[i];
        else if (isSep(code)) break;
        else code = CHAR_FORWARD_SLASH;

        if (isSep(code)) {
            if (lastSlash == i - 1 || dots == 1) {
                /* NOOP */
            } else if (dots == 2) {
                if (res->len < 2 || lastSegmentLength != 2 ||
                    res->buf[res->len - 1] != CHAR_DOT || res->buf[res->len - 2] != CHAR_DOT) {
                    if (res->len > 2) {
                        int64_t lastSlashIndex = res->len - lastSegmentLength - 1;
                        if (lastSlashIndex == -1) {
                            sb_truncate(res, 0);
                            lastSegmentLength = 0;
                        } else {
                            sb_truncate(res, lastSlashIndex);
                            lastSegmentLength = res->len - 1 - lastIndexOf(res->buf, res->len, separator);
                        }
                        lastSlash = i;
                        dots = 0;
                        continue;
                    } else if (res->len != 0) {
                        sb_truncate(res, 0);
                        lastSegmentLength = 0;
                        lastSlash = i;
                        dots = 0;
                        continue;
                    }
                }
                if (allowAboveRoot) {
                    if (res->len > 0) sb_append_ch(res, separator);
                    sb_append(res, "..");
                    lastSegmentLength = 2;
                }
            } else {
                if (res->len > 0) {
                    sb_append_ch(res, separator);
                    sb_append_n(res, path + lastSlash + 1, i - (lastSlash + 1));
                } else {
                    sb_truncate(res, 0);
                    sb_append_n(res, path + lastSlash + 1, i - (lastSlash + 1));
                }
                lastSegmentLength = i - lastSlash - 1;
            }
            lastSlash = i;
            dots = 0;
        } else if (code == CHAR_DOT && dots != -1) {
            ++dots;
        } else {
            dots = -1;
        }
    }
}

char *__kml_path_posix_normalize(const char *path) {
    int64_t len = (int64_t)strlen(path);
    if (len == 0) return kml_str_dup(".");
    int isAbsolute = path[0] == CHAR_FORWARD_SLASH;
    int trailingSeparator = path[len - 1] == CHAR_FORWARD_SLASH;
    sb_t b; sb_init(&b);
    normalizeStringWith(path, len, !isAbsolute, '/', isPosixSep, &b);
    if (b.len == 0) {
        free(b.buf);
        if (isAbsolute) return kml_str_dup("/");
        return kml_str_dup(trailingSeparator ? "./" : ".");
    }
    if (trailingSeparator) sb_append_ch(&b, '/');
    if (!isAbsolute) return sb_finish(&b);
    sb_t out; sb_init(&out);
    sb_append_ch(&out, '/');
    sb_append_n(&out, sb_cstr(&b), b.len);
    free(b.buf);
    return sb_finish(&out);
}

char *__kml_path_posix_resolve(int64_t n, char **args, const char *cwd) {
    if (n == 0 || (n == 1 && (args[0][0] == 0 || (args[0][0] == '.' && args[0][1] == 0)))) {
        if (cwd[0] == CHAR_FORWARD_SLASH) return kml_str_dup(cwd);
    }
    sb_t resolvedPath; sb_init(&resolvedPath);
    int resolvedAbsolute = 0;
    for (int64_t i = n - 1; i >= 0 && !resolvedAbsolute; i--) {
        const char *path = args[i];
        if (path[0] == 0) continue;
        sb_t nt; sb_init(&nt);
        sb_append(&nt, path);
        sb_append_ch(&nt, '/');
        sb_append_n(&nt, sb_cstr(&resolvedPath), resolvedPath.len);
        free(resolvedPath.buf);
        resolvedPath = nt;
        resolvedAbsolute = path[0] == CHAR_FORWARD_SLASH;
    }
    if (!resolvedAbsolute) {
        sb_t nt; sb_init(&nt);
        sb_append(&nt, cwd);
        sb_append_ch(&nt, '/');
        sb_append_n(&nt, sb_cstr(&resolvedPath), resolvedPath.len);
        free(resolvedPath.buf);
        resolvedPath = nt;
        resolvedAbsolute = cwd[0] == CHAR_FORWARD_SLASH;
    }
    sb_t norm; sb_init(&norm);
    normalizeStringWith(sb_cstr(&resolvedPath), resolvedPath.len, !resolvedAbsolute, '/', isPosixSep, &norm);
    free(resolvedPath.buf);
    if (resolvedAbsolute) {
        sb_t out; sb_init(&out);
        sb_append_ch(&out, '/');
        sb_append_n(&out, sb_cstr(&norm), norm.len);
        free(norm.buf);
        return sb_finish(&out);
    }
    if (norm.len == 0) { free(norm.buf); return kml_str_dup("."); }
    return sb_finish(&norm);
}

char *__kml_path_posix_relative(const char *from_in, const char *to_in, const char *cwd) {
    if (strcmp(from_in, to_in) == 0) return kml_str_dup("");
    char *a1[1] = { (char *)from_in };
    char *a2[1] = { (char *)to_in };
    char *from = __kml_path_posix_resolve(1, a1, cwd);
    char *to = __kml_path_posix_resolve(1, a2, cwd);
    if (strcmp(from, to) == 0) return kml_str_dup("");

    int64_t fromStart = 1;
    int64_t fromEnd = (int64_t)strlen(from);
    int64_t fromLen = fromEnd - fromStart;
    int64_t toStart = 1;
    int64_t toLen = (int64_t)strlen(to) - toStart;
    int64_t length = fromLen < toLen ? fromLen : toLen;
    int64_t lastCommonSep = -1;
    int64_t i = 0;
    for (; i < length; i++) {
        int fromCode = (unsigned char)from[fromStart + i];
        if (fromCode != (unsigned char)to[toStart + i]) break;
        else if (fromCode == CHAR_FORWARD_SLASH) lastCommonSep = i;
    }
    if (i == length) {
        if (toLen > length) {
            if (to[toStart + i] == CHAR_FORWARD_SLASH) return kml_str_dup(to + toStart + i + 1);
            if (i == 0) return kml_str_dup(to + toStart + i);
        } else if (fromLen > length) {
            if (from[fromStart + i] == CHAR_FORWARD_SLASH) lastCommonSep = i;
            else if (i == 0) lastCommonSep = 0;
        }
    }
    sb_t out; sb_init(&out);
    for (i = fromStart + lastCommonSep + 1; i <= fromEnd; ++i) {
        if (i == fromEnd || from[i] == CHAR_FORWARD_SLASH) {
            sb_append(&out, out.len == 0 ? ".." : "/..");
        }
    }
    sb_append(&out, to + toStart + lastCommonSep);
    return sb_finish(&out);
}

/* ---- url.pathToFileURL / url.fileURLToPath, Windows halves ---------------- */

/* pathToFileURL on Windows (lib/internal/url.js): a UNC input (`\\server\share\p`)
 * becomes host `server` + pathname `/share/p`; anything else is
 * path.resolve()d by the caller, then `\` becomes `/` and the result is
 * rooted (`/C:/foo/bar`). Returns the pathname (not yet percent-encoded — the
 * caller's URL layer does that) and writes the host (possibly "") to *host.
 * Returns NULL for a UNC input with no share separator (Node throws
 * ERR_INVALID_ARG_VALUE there). */
char *__kml_path_win32_to_file_url(const char *filepath, const char *resolved, char **host) {
    if (filepath[0] == '\\' && filepath[1] == '\\') {
        const char *end = strchr(filepath + 2, '\\');
        if (!end) { *host = kml_str_dup(""); return NULL; }
        *host = kml_str_from(filepath + 2, (int64_t)(end - (filepath + 2)));
        char *p = kml_str_dup(end);
        for (char *q = p; *q; q++) if (*q == '\\') *q = '/';
        return p;
    }
    *host = kml_str_dup("");
    int64_t n = (int64_t)strlen(resolved);
    char *p = kml_str_alloc(n + 1);
    p[0] = '/';
    for (int64_t i = 0; i < n; i++) p[i + 1] = resolved[i] == '\\' ? '/' : resolved[i];
    return p;
}

/* libcurl rejects a `file:` URL whose host is anything but empty/localhost
 * (CURLUE_BAD_FILE_URL), but Node's win32 fileURLToPath maps such a host to a
 * UNC server. Split the host off `file://server/share/p` before curl parses
 * the URL: returns `file:///share/p` and writes the host ("" when there was
 * none) to *host. Anything not of that shape is returned unchanged. */
char *__kml_path_win32_split_file_host(const char *url, char **host) {
    const char pfx[] = "file://";
    size_t pl = sizeof pfx - 1;
    if (strncmp(url, pfx, pl) != 0 || url[pl] == '/' || url[pl] == 0) {
        *host = kml_str_dup("");
        return kml_str_dup(url);
    }
    const char *rest = strchr(url + pl, '/');
    if (!rest) rest = url + strlen(url);
    *host = kml_str_from(url + pl, (int64_t)(rest - (url + pl)));
    sb_t b; sb_init(&b);
    sb_append(&b, "file://");
    if (*rest == 0) sb_append_ch(&b, '/');
    sb_append(&b, rest);
    return sb_finish(&b);
}

static int hexval(int c) {
    if (c >= '0' && c <= '9') return c - '0';
    c |= 0x20;
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    return -1;
}

/* fileURLToPath on Windows (getPathFromURLWin32): `rawPath` is the URL's
 * still-encoded pathname, `host` its hostname. Rejects an encoded `/` or `\`
 * (*err = 1: "must not include encoded \ or / characters"), flips `/` to `\`,
 * percent-decodes, then either prefixes `\\host` or requires a drive letter
 * (*err = 2: "must be absolute") and drops the leading `\`. */
char *__kml_path_win32_from_file_url(const char *host, const char *rawPath, int *err) {
    *err = 0;
    int64_t n = (int64_t)strlen(rawPath);
    for (int64_t i = 0; i + 2 < n; i++) {
        if (rawPath[i] == '%') {
            int third = ((unsigned char)rawPath[i + 2]) | 0x20;
            if ((rawPath[i + 1] == '2' && third == 'f') || (rawPath[i + 1] == '5' && third == 'c')) {
                *err = 1;
                return NULL;
            }
        }
    }
    sb_t b; sb_init(&b);
    /* libcurl hands a Windows drive path back without its leading `/`
     * (`C:/foo` for `file:///C:/foo`); the WHATWG pathname always has it. */
    if (n > 0 && rawPath[0] != '/') sb_append_ch(&b, '\\');
    for (int64_t i = 0; i < n; i++) {
        int c = (unsigned char)rawPath[i];
        if (c == '%' && i + 2 < n && hexval((unsigned char)rawPath[i + 1]) >= 0 && hexval((unsigned char)rawPath[i + 2]) >= 0) {
            c = hexval((unsigned char)rawPath[i + 1]) * 16 + hexval((unsigned char)rawPath[i + 2]);
            i += 2;
        } else if (c == '/') {
            c = '\\';
        }
        sb_append_ch(&b, (char)c);
    }
    if (host && host[0]) {
        sb_t out; sb_init(&out);
        sb_append(&out, "\\\\");
        sb_append(&out, host);
        sb_append_n(&out, sb_cstr(&b), b.len);
        free(b.buf);
        return sb_finish(&out);
    }
    int letter = b.len > 1 ? (((unsigned char)b.buf[1]) | 0x20) : 0;
    int sep = b.len > 2 ? (unsigned char)b.buf[2] : 0;
    if (letter < 'a' || letter > 'z' || sep != ':') {
        free(b.buf);
        *err = 2;
        return NULL;
    }
    char *out = kml_str_from(b.buf + 1, b.len - 1);
    free(b.buf);
    return out;
}

/* ---- win32.format (shared _format with sep '\\') --------------------------- */

char *__kml_path_win32_format(const char *root, const char *dir, const char *base, const char *ext, const char *name) {
    const char *d = (dir && dir[0]) ? dir : root;
    sb_t b; sb_init(&b);
    if (base && base[0]) {
        sb_append(&b, base);
    } else {
        if (name) sb_append(&b, name);
        if (ext && ext[0]) {
            if (ext[0] != '.') sb_append_ch(&b, '.');
            sb_append(&b, ext);
        }
    }
    if (!d || !d[0]) return sb_finish(&b);
    sb_t out; sb_init(&out);
    sb_append(&out, d);
    if (strcmp(d, root ? root : "") != 0) sb_append_ch(&out, '\\');
    sb_append_n(&out, sb_cstr(&b), b.len);
    free(b.buf);
    return sb_finish(&out);
}
