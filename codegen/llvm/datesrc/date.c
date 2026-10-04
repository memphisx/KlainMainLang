#include <string.h>
/* date.c — Date and performance clocks (in C per TDD-00240): the wall and
 * monotonic reads, the UTC calendar decompose/compose, the ISO 8601 parser
 * and the weekday/month name tables. Pure integer calendar math (Hinnant's
 * civil algorithms), not gmtime(): the Windows CRT's gmtime returns NULL
 * before 1970 and gmtime's buffer is not thread-safe. */
#include <stdio.h>
#include <time.h>

typedef long long i64;
typedef unsigned long long u64;

const char *const __kml_weekday_names[7] = {"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"};
const char *const __kml_month_names[12] = {"Jan", "Feb", "Mar", "Apr", "May", "Jun",
                                           "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"};

/* Milliseconds since the Unix epoch. */
i64 __kml_date_now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return (i64)ts.tv_sec * 1000 + (i64)ts.tv_nsec / 1000000;
}

/* The monotonic clock libuv's uv_hrtime reads: Darwin's CLOCK_UPTIME_RAW
 * (ns resolution; CLOCK_MONOTONIC there counts microseconds). */
#ifdef __APPLE__
#define KML_MONO_CLOCK CLOCK_UPTIME_RAW
#else
#define KML_MONO_CLOCK CLOCK_MONOTONIC
#endif

static double ts_ms(const struct timespec *ts) {
    return (double)ts->tv_sec * 1000.0 + (double)ts->tv_nsec / 1000000.0;
}

static double perf_origin, perf_origin_wall;

double __kml_perf_raw_ms(void) {
    struct timespec ts;
    clock_gettime(KML_MONO_CLOCK, &ts);
    return ts_ms(&ts);
}

/* A constructor: the time origin is process start (ADR-00568). */
__attribute__((constructor)) void __kml_perf_init(void) {
    struct timespec ts;
    perf_origin = __kml_perf_raw_ms();
    clock_gettime(CLOCK_REALTIME, &ts);
    perf_origin_wall = ts_ms(&ts);
}

double __kml_performance_now(void) { return __kml_perf_raw_ms() - perf_origin; }
double __kml_native_perf_now(void) { return __kml_performance_now(); }
double __kml_native_perf_time_origin(void) { return perf_origin_wall; }

/* ms since epoch -> UTC fields {year, month[0-11], day, weekday[0=Sun],
 * hour, minute, second, millisecond} into out[8]. (The IR-visible
 * __kml_date_decompose returns these as an aggregate, so it stays a thin
 * generated wrapper over this.) */
void __kml_date_decompose_out(i64 ms, i64 *out) {
    i64 secs = ms / 1000, millis = ms % 1000;
    if (millis < 0) {
        millis += 1000;
        secs -= 1;
    }
    i64 days = secs / 86400, sod = secs % 86400;
    if (sod < 0) {
        sod += 86400;
        days -= 1;
    }
    i64 wd = (days + 4) % 7;
    if (wd < 0) wd += 7;
    i64 z = days + 719468;
    i64 era = (z < 0 ? z - 146096 : z) / 146097;
    i64 doe = z - era * 146097;
    i64 yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    i64 y = yoe + era * 400;
    i64 doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    i64 mp = (5 * doy + 2) / 153;
    i64 d = doy - (153 * mp + 2) / 5 + 1;
    i64 m = mp < 10 ? mp + 3 : mp - 9;
    out[0] = y + (m <= 2);
    out[1] = m - 1;
    out[2] = d;
    out[3] = wd;
    out[4] = sod / 3600;
    out[5] = sod % 3600 / 60;
    out[6] = sod % 60;
    out[7] = millis;
}

/* Days since 1970-01-01 for a proleptic-Gregorian (year, month[1-12], day).
 * Unsigned arithmetic: out-of-range inputs wrap, as the generated code did. */
i64 __kml_days_from_civil(i64 y0, i64 m, i64 d) {
    i64 y = (i64)((u64)y0 - (u64)(m <= 2));
    i64 era = (y < 0 ? (i64)((u64)y - 399) : y) / 400;
    i64 yoe = (i64)((u64)y - (u64)era * 400);
    i64 mp = m + (m > 2 ? -3 : 9);
    i64 doy = (i64)((u64)((i64)((u64)153 * (u64)mp + 2) / 5) + (u64)d - 1);
    u64 doe = (u64)yoe * 365 + (u64)(yoe / 4) - (u64)(yoe / 100) + (u64)doy;
    return (i64)((u64)era * 146097 + doe - 719468);
}

/* The inverse of decompose: month is 1-indexed here. */
i64 __kml_date_compose(i64 year, i64 month, i64 day, i64 hour, i64 min, i64 sec, i64 msec) {
    u64 days = (u64)__kml_days_from_civil(year, month, day);
    u64 secs = days * 86400 + (u64)hour * 3600 + (u64)min * 60 + (u64)sec;
    return (i64)(secs * 1000 + (u64)msec);
}

/* The milliseconds of a time's fraction: its digits, not their integer
 * value (".5" is 500 ms, ".05" 50; digits past the third truncate). */
static int frac_ms(const char *str) {
    const char *t = strchr(str, 'T');
    const char *dot = t ? strchr(t, '.') : 0;
    if (!dot) return 0;
    int ms = 0, n = 0;
    for (const char *c = dot + 1; *c >= '0' && *c <= '9'; c++, n++)
        if (n < 3) ms = ms * 10 + (*c - '0');
    for (; n < 3; n++) ms *= 10;
    return ms;
}

/* ISO 8601 -> ms since epoch; -1 when unparseable. Offset forms are tried
 * before the "Z" forms: sscanf counts only converted fields, so an offset
 * string fed to a "Z" pattern would still report every number matched.
 * Each offset pattern bakes its sign in (a "-00:30" offset would lose it
 * through %d). */
i64 __kml_date_parse(const char *str) {
    int year, month, day, hour = 0, min = 0, sec = 0, msec = 0, offh, offm;
    i64 off_ms = 0;
    if (sscanf(str, "%d-%d-%dT%d:%d:%d.%d+%d:%d", &year, &month, &day, &hour, &min, &sec, &msec, &offh, &offm) == 9) {
        off_ms = ((i64)offh * 60 + offm) * 60 * 1000;
    } else if (sscanf(str, "%d-%d-%dT%d:%d:%d.%d-%d:%d", &year, &month, &day, &hour, &min, &sec, &msec, &offh, &offm) == 9) {
        off_ms = -(((i64)offh * 60 + offm) * 60 * 1000);
    } else if ((msec = 0, sscanf(str, "%d-%d-%dT%d:%d:%d+%d:%d", &year, &month, &day, &hour, &min, &sec, &offh, &offm) == 8)) {
        off_ms = ((i64)offh * 60 + offm) * 60 * 1000;
    } else if ((msec = 0, sscanf(str, "%d-%d-%dT%d:%d:%d-%d:%d", &year, &month, &day, &hour, &min, &sec, &offh, &offm) == 8)) {
        off_ms = -(((i64)offh * 60 + offm) * 60 * 1000);
    } else {
        hour = min = sec = msec = 0;
        if (sscanf(str, "%d-%d-%dT%d:%d:%d.%dZ", &year, &month, &day, &hour, &min, &sec, &msec) != 7) {
            hour = min = sec = msec = 0;
            if (sscanf(str, "%d-%d-%dT%d:%d:%dZ", &year, &month, &day, &hour, &min, &sec) != 6) {
                hour = min = sec = msec = 0;
                if (sscanf(str, "%d-%d-%d", &year, &month, &day) != 3) return -1;
            }
        }
    }
    if (msec) msec = frac_ms(str);
    return __kml_date_compose(year, month, day, hour, min, sec, msec) - off_ms;
}
