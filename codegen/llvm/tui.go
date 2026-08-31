package llvm

// tui.go — the `klain:tui` painter runtime (TDD-00150 Stage 1).
//
// This is the C side of the native TUI substrate: it wraps the vendored Yoga
// flexbox engine (yoga.go) with a small handle-based node API the generated IR
// calls into, attaches per-node paint attributes (text, colours, border, and
// the leaf component kinds — list/spinner/progress/input), and paints a laid-out
// tree with a double-buffered cell-grid diff so each frame emits only the
// minimal cursor-move + write sequence.
//
// The retained node tree lives here, in C, rather than as a walked KML object
// tree, because the project has no closure->C-function-pointer trampoline yet
// (TDD-00150 Stage 2): the `state -> view -> update` loop is written in userland
// TypeScript over the shipped klain:tty key reads and SIGWINCH, calling the
// builder functions (which map 1:1 onto YGNodeNew + style setters here) to build
// a fresh tree each frame and then render(root). render() frees the tree after
// painting so a long-running loop doesn't grow without bound.
//
// Text is width-aware: each grid cell holds a whole grapheme (a base code point
// plus any trailing zero-width combining marks) and its display width from a
// standard wcwidth table, so wide (CJK/emoji) glyphs occupy two columns with a
// continuation cell and combining marks fold onto their base rather than
// stealing a column. Layout, wrapping, and the diff painter all reason in
// columns, so alignment holds for such content.

// UsesTui reports whether the program used klain:tui, gating both this painter
// runtime and the vendored Yoga engine in EmbeddedCSources.
func (e *Emitter) UsesTui() bool { return e.usedTui }

// TuiSource is the embedded C painter runtime. It is plain C (compiled as a .c
// TU); it calls Yoga purely through Yoga.h's extern "C" ABI, so it links against
// the C++ Yoga objects without itself being C++.
func TuiSource() string {
	return `#include <yoga/Yoga.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <unistd.h>
#include <math.h>
#include <stdarg.h>
#include <sys/ioctl.h>

/* ---- node model ------------------------------------------------------- */

enum { K_BOX=0, K_TEXT=1, K_LIST=2, K_SPINNER=3, K_PROGRESS=4, K_INPUT=5 };

typedef struct TuiNode {
  YGNodeRef yg;
  int kind;
  char *text;            /* Text content / Spinner label / Input value */
  int fg, bg;            /* ANSI SGR colour number, or -1 for default */
  int attr;             /* bit0 bold, bit1 dim, bit2 underline, bit3 reverse */
  int border;           /* 0 none, 1 single, 2 round, 3 double */
  int borderColor;      /* -1 default */
  int wrap;             /* Text: 1 = wrap on width, 0 = clip */
  char **items; int nitems; int icap;  /* List */
  int selected;         /* List selected index, -1 none */
  double progress;      /* Progress 0..1 */
  int spinnerFrame;     /* Spinner frame index */
  struct TuiNode **kids; int nkids; int kcap;
} TuiNode;

static char *kmltui_dup(const char *s) {
  if (!s) return NULL;
  size_t n = strlen(s);
  char *o = (char *)malloc(n + 1);
  memcpy(o, s, n + 1);
  return o;
}

/* ---- Unicode: decode, wcwidth, encode --------------------------------- */

/* Decode s into an array of Unicode code points. Returns the code-point count;
   out must hold at least strlen(s) entries. Malformed bytes pass through as
   Latin-1. Full 4-byte (astral/emoji) sequences decode to a single cp. */
static int kmltui_decode(const char *s, unsigned int *out, int max) {
  int n = 0; const unsigned char *p = (const unsigned char *)s;
  while (*p && n < max) {
    unsigned int cp; int len;
    if (*p < 0x80) { cp = *p; len = 1; }
    else if ((*p >> 5) == 0x6) { cp = *p & 0x1F; len = 2; }
    else if ((*p >> 4) == 0xE) { cp = *p & 0x0F; len = 3; }
    else if ((*p >> 3) == 0x1E) { cp = *p & 0x07; len = 4; }
    else { cp = *p; len = 1; }
    for (int i = 1; i < len; i++) {
      if ((p[i] & 0xC0) == 0x80) cp = (cp << 6) | (p[i] & 0x3F);
      else { len = i; break; }
    }
    out[n++] = cp; p += len;
  }
  return n;
}

/* Encode one code point as UTF-8 into b (up to 4 bytes); returns byte count. */
static int kmltui_enc(unsigned int cp, char *b) {
  if (cp < 0x80) { b[0]=(char)cp; return 1; }
  if (cp < 0x800) { b[0]=(char)(0xC0|(cp>>6)); b[1]=(char)(0x80|(cp&0x3F)); return 2; }
  if (cp < 0x10000) { b[0]=(char)(0xE0|(cp>>12)); b[1]=(char)(0x80|((cp>>6)&0x3F)); b[2]=(char)(0x80|(cp&0x3F)); return 3; }
  b[0]=(char)(0xF0|(cp>>18)); b[1]=(char)(0x80|((cp>>12)&0x3F)); b[2]=(char)(0x80|((cp>>6)&0x3F)); b[3]=(char)(0x80|(cp&0x3F)); return 4;
}

/* Standard wcwidth (Markus Kuhn's table): 0 for combining/zero-width marks and
   control chars, 2 for East-Asian-wide and emoji, 1 otherwise. */
struct kmltui_iv { unsigned int first, last; };
static int kmltui_intable(unsigned int c, const struct kmltui_iv *t, int max) {
  int lo=0, hi=max-1;
  if (c < t[0].first || c > t[max-1].last) return 0;
  while (lo <= hi) {
    int mid = (lo+hi)/2;
    if (c > t[mid].last) lo = mid+1;
    else if (c < t[mid].first) hi = mid-1;
    else return 1;
  }
  return 0;
}
/* Zero-width: combining marks, joiners, variation selectors, skin-tone
   modifiers — enough to cover the common scripts and emoji sequences. */
static const struct kmltui_iv KMLTUI_ZERO[] = {
  {0x0300,0x036F},{0x0483,0x0489},{0x0591,0x05BD},{0x05BF,0x05BF},
  {0x05C1,0x05C2},{0x05C4,0x05C5},{0x05C7,0x05C7},{0x0610,0x061A},
  {0x064B,0x065F},{0x0670,0x0670},{0x06D6,0x06DC},{0x06DF,0x06E4},
  {0x06E7,0x06E8},{0x06EA,0x06ED},{0x0711,0x0711},{0x0730,0x074A},
  {0x07A6,0x07B0},{0x07EB,0x07F3},{0x0816,0x0819},{0x081B,0x0823},
  {0x0825,0x0827},{0x0829,0x082D},{0x0859,0x085B},{0x08E3,0x0902},
  {0x093A,0x093A},{0x093C,0x093C},{0x0941,0x0948},{0x094D,0x094D},
  {0x0951,0x0957},{0x0962,0x0963},{0x0981,0x0981},{0x09BC,0x09BC},
  {0x09C1,0x09C4},{0x09CD,0x09CD},{0x09E2,0x09E3},{0x0A01,0x0A02},
  {0x0A3C,0x0A3C},{0x0A41,0x0A42},{0x0A47,0x0A48},{0x0A4B,0x0A4D},
  {0x0A70,0x0A71},{0x0A81,0x0A82},{0x0ABC,0x0ABC},{0x0AC1,0x0AC5},
  {0x0AC7,0x0AC8},{0x0ACD,0x0ACD},{0x0B01,0x0B01},{0x0B3C,0x0B3C},
  {0x0B3F,0x0B3F},{0x0B41,0x0B44},{0x0B4D,0x0B4D},{0x0B56,0x0B56},
  {0x0BC0,0x0BC0},{0x0BCD,0x0BCD},{0x0C3E,0x0C40},{0x0C46,0x0C48},
  {0x0C4A,0x0C4D},{0x0CBC,0x0CBC},{0x0CBF,0x0CBF},{0x0CCC,0x0CCD},
  {0x0D41,0x0D44},{0x0D4D,0x0D4D},{0x0DCA,0x0DCA},{0x0E31,0x0E31},
  {0x0E34,0x0E3A},{0x0E47,0x0E4E},{0x0EB1,0x0EB1},{0x0EB4,0x0EB9},
  {0x0F18,0x0F19},{0x0F35,0x0F35},{0x0F37,0x0F37},{0x0F39,0x0F39},
  {0x0F71,0x0F7E},{0x0F80,0x0F84},{0x0F86,0x0F87},{0x0FC6,0x0FC6},
  {0x1712,0x1714},{0x1732,0x1734},{0x1752,0x1753},{0x1772,0x1773},
  {0x17B4,0x17B5},{0x17B7,0x17BD},{0x17C6,0x17C6},{0x17C9,0x17D3},
  {0x180B,0x180D},{0x18A9,0x18A9},{0x1920,0x1922},{0x1927,0x1928},
  {0x1932,0x1932},{0x1939,0x193B},{0x1A17,0x1A18},{0x1B00,0x1B03},
  {0x1B34,0x1B34},{0x1B36,0x1B3A},{0x1B3C,0x1B3C},{0x1B42,0x1B42},
  {0x1DC0,0x1DFF},{0x200B,0x200F},{0x202A,0x202E},{0x2060,0x2064},
  {0x20D0,0x20F0},{0xFE00,0xFE0F},{0xFE20,0xFE2F},{0xFEFF,0xFEFF},
  {0x1F3FB,0x1F3FF},{0xE0100,0xE01EF},
};
static int kmltui_wcwidth(unsigned int c) {
  if (c == 0) return 0;
  if (c < 32 || (c >= 0x7F && c < 0xA0)) return 0; /* control */
  if (kmltui_intable(c, KMLTUI_ZERO, (int)(sizeof KMLTUI_ZERO/sizeof KMLTUI_ZERO[0]))) return 0;
  if (c >= 0x1100 &&
      (c <= 0x115F ||                                   /* Hangul Jamo */
       c == 0x2329 || c == 0x232A ||
       (c >= 0x2E80 && c <= 0xA4CF && c != 0x303F) ||   /* CJK .. Yi */
       (c >= 0xAC00 && c <= 0xD7A3) ||                  /* Hangul syllables */
       (c >= 0xF900 && c <= 0xFAFF) ||                  /* CJK compat */
       (c >= 0xFE10 && c <= 0xFE19) ||
       (c >= 0xFE30 && c <= 0xFE6F) ||
       (c >= 0xFF00 && c <= 0xFF60) ||                  /* fullwidth forms */
       (c >= 0xFFE0 && c <= 0xFFE6) ||
       (c >= 0x1F300 && c <= 0x1FAFF) ||                /* emoji + symbols */
       (c >= 0x20000 && c <= 0x3FFFD)))                 /* CJK ext */
    return 2;
  return 1;
}
/* Column width of a code-point span (combining marks contribute 0). */
static int kmltui_colwidth(const unsigned int *cp, int start, int len) {
  int w = 0;
  for (int i = 0; i < len; i++) w += kmltui_wcwidth(cp[start+i]);
  return w;
}

/* ---- text wrapping (shared by measure + paint) ------------------------ */

/* Fill starts[]/lens[] with up to maxlines wrapped line spans (in code-point
   indices) of the decoded string cp[0..n); returns line count. Greedy
   word-wrap in *columns* (wide glyphs count 2), hard breaks for words longer
   than width. width<=0 => one line. */
static int kmltui_wrap(const unsigned int *cp, int n, int width, int *starts, int *lens, int maxlines) {
  int i = 0, lc = 0;
  if (width <= 0) { if (maxlines>0){starts[0]=0;lens[0]=n;} return 1; }
  while (i < n && lc < maxlines) {
    int lineStart = i, lastSpace = -1, col = 0;
    while (i < n && cp[i] != '\n') {
      int cw = kmltui_wcwidth(cp[i]);
      if (cw > 0 && col + cw > width) break;
      if (cp[i] == ' ') lastSpace = i;
      i++; col += cw;
    }
    int lineEnd;
    if (i < n && cp[i] == '\n') { lineEnd = i; i++; }
    else if (i >= n) { lineEnd = i; }
    else if (lastSpace > lineStart) { lineEnd = lastSpace; i = lastSpace + 1; }
    else { lineEnd = i; } /* hard break mid-word */
    starts[lc] = lineStart; lens[lc] = lineEnd - lineStart; lc++;
  }
  if (lc == 0) { starts[0]=0; lens[0]=0; lc=1; }
  return lc;
}

static YGSize kmltui_measure(YGNodeConstRef node, float w, YGMeasureMode wm,
                             float h, YGMeasureMode hm) {
  TuiNode *t = (TuiNode *)YGNodeGetContext(node);
  YGSize sz; sz.width = 0; sz.height = 1;
  (void)h; (void)hm;
  if (!t) return sz;
  if (t->kind == K_TEXT) {
    const char *s = t->text ? t->text : "";
    int slen = (int)strlen(s) + 1;
    unsigned int *cp = (unsigned int*)malloc(sizeof(unsigned int)*slen);
    int n = kmltui_decode(s, cp, slen);
    if (!t->wrap) { int cw = kmltui_colwidth(cp,0,n); free(cp); sz.width = (float)cw; sz.height = 1; return sz; }
    int avail = (wm == YGMeasureModeUndefined) ? kmltui_colwidth(cp,0,n) : (int)w;
    int *st = (int*)malloc(sizeof(int)*(n+1)), *ln = (int*)malloc(sizeof(int)*(n+1));
    int lc = kmltui_wrap(cp, n, avail, st, ln, n+1);
    int maxw = 0; for (int k=0;k<lc;k++){ int cw=kmltui_colwidth(cp,st[k],ln[k]); if (cw>maxw) maxw=cw; }
    free(st); free(ln); free(cp);
    sz.width = (float)maxw; sz.height = (float)lc; return sz;
  }
  if (t->kind == K_LIST) {
    int maxw = 0;
    for (int k=0;k<t->nitems;k++){
      int sl=(int)strlen(t->items[k])+1;
      unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*sl);
      int l=kmltui_decode(t->items[k],cp,sl); int cw=kmltui_colwidth(cp,0,l); free(cp);
      if(cw>maxw)maxw=cw;
    }
    sz.width = (float)maxw; sz.height = (float)(t->nitems>0?t->nitems:1); return sz;
  }
  if (t->kind == K_PROGRESS) {
    sz.width = (wm == YGMeasureModeUndefined) ? 20.f : w; sz.height = 1; return sz;
  }
  if (t->kind == K_SPINNER) {
    int lbl = 0;
    if (t->text) { int sl=(int)strlen(t->text)+1; unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*sl); int l=kmltui_decode(t->text,cp,sl); lbl=kmltui_colwidth(cp,0,l); free(cp); }
    sz.width = (float)(1 + (lbl ? lbl + 1 : 0)); sz.height = 1; return sz;
  }
  if (t->kind == K_INPUT) {
    int len = 0;
    if (t->text) { int sl=(int)strlen(t->text)+1; unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*sl); int l=kmltui_decode(t->text,cp,sl); len=kmltui_colwidth(cp,0,l); free(cp); }
    sz.width = (wm == YGMeasureModeUndefined) ? (float)(len+1) : w; sz.height = 1; return sz;
  }
  return sz;
}

/* ---- builders (called from generated IR) ------------------------------ */

TuiNode *__kml_tui_node(int kind) {
  TuiNode *t = (TuiNode *)calloc(1, sizeof(TuiNode));
  t->yg = YGNodeNew();
  t->kind = kind;
  t->fg = -1; t->bg = -1; t->borderColor = -1; t->selected = -1; t->wrap = 1;
  YGNodeSetContext(t->yg, t);
  if (kind != K_BOX) YGNodeSetMeasureFunc(t->yg, kmltui_measure);
  /* A List yields to a bounded container (default Yoga flexShrink is 0), so a
     tall list inside a fixed-height box shrinks to that box and scrolls rather
     than overflowing it. An explicit flexShrink prop still overrides this. */
  if (kind == K_LIST) YGNodeStyleSetFlexShrink(t->yg, 1);
  return t;
}

void __kml_tui_set_size(TuiNode *t, double w, double h) {
  if (!isnan(w)) YGNodeStyleSetWidth(t->yg, (float)w);
  if (!isnan(h)) YGNodeStyleSetHeight(t->yg, (float)h);
}
void __kml_tui_set_min(TuiNode *t, double w, double h) {
  if (!isnan(w)) YGNodeStyleSetMinWidth(t->yg, (float)w);
  if (!isnan(h)) YGNodeStyleSetMinHeight(t->yg, (float)h);
}
void __kml_tui_set_flex(TuiNode *t, int dir, double grow, double shrink, double basis) {
  if (dir == 1) YGNodeStyleSetFlexDirection(t->yg, YGFlexDirectionColumn);
  else if (dir == 0) YGNodeStyleSetFlexDirection(t->yg, YGFlexDirectionRow);
  if (!isnan(grow)) YGNodeStyleSetFlexGrow(t->yg, (float)grow);
  if (!isnan(shrink)) YGNodeStyleSetFlexShrink(t->yg, (float)shrink);
  if (!isnan(basis)) YGNodeStyleSetFlexBasis(t->yg, (float)basis);
}
void __kml_tui_set_justify(TuiNode *t, int j) {
  YGNodeStyleSetJustifyContent(t->yg, (YGJustify)j);
}
void __kml_tui_set_align(TuiNode *t, int a) {
  YGNodeStyleSetAlignItems(t->yg, (YGAlign)a);
}
void __kml_tui_set_self(TuiNode *t, int a) {
  YGNodeStyleSetAlignSelf(t->yg, (YGAlign)a);
}
void __kml_tui_set_wrap(TuiNode *t, int wrap) {
  YGNodeStyleSetFlexWrap(t->yg, wrap ? YGWrapWrap : YGWrapNoWrap);
}
void __kml_tui_set_padding(TuiNode *t, double top, double right, double bot, double left) {
  if(!isnan(top))YGNodeStyleSetPadding(t->yg,YGEdgeTop,(float)top);
  if(!isnan(right))YGNodeStyleSetPadding(t->yg,YGEdgeRight,(float)right);
  if(!isnan(bot))YGNodeStyleSetPadding(t->yg,YGEdgeBottom,(float)bot);
  if(!isnan(left))YGNodeStyleSetPadding(t->yg,YGEdgeLeft,(float)left);
}
void __kml_tui_set_margin(TuiNode *t, double top, double right, double bot, double left) {
  if(!isnan(top))YGNodeStyleSetMargin(t->yg,YGEdgeTop,(float)top);
  if(!isnan(right))YGNodeStyleSetMargin(t->yg,YGEdgeRight,(float)right);
  if(!isnan(bot))YGNodeStyleSetMargin(t->yg,YGEdgeBottom,(float)bot);
  if(!isnan(left))YGNodeStyleSetMargin(t->yg,YGEdgeLeft,(float)left);
}
void __kml_tui_set_gap(TuiNode *t, double g) {
  if(!isnan(g)) YGNodeStyleSetGap(t->yg, YGGutterAll, (float)g);
}
void __kml_tui_set_colors(TuiNode *t, int fg, int bg) { t->fg = fg; t->bg = bg; }
void __kml_tui_set_attr(TuiNode *t, int attr) { t->attr = attr; }
void __kml_tui_set_border(TuiNode *t, int style, int color) {
  t->border = style; t->borderColor = color;
  if (style != 0) YGNodeStyleSetBorder(t->yg, YGEdgeAll, 1);
}
void __kml_tui_set_text(TuiNode *t, const char *s, int wrap) {
  free(t->text); t->text = kmltui_dup(s); t->wrap = wrap;
}
void __kml_tui_add_item(TuiNode *t, const char *s) {
  if (t->nitems >= t->icap) { t->icap = t->icap ? t->icap*2 : 4; t->items = (char**)realloc(t->items, sizeof(char*)*t->icap); }
  t->items[t->nitems++] = kmltui_dup(s);
}
void __kml_tui_set_selected(TuiNode *t, int idx) { t->selected = idx; }
void __kml_tui_set_progress(TuiNode *t, double v) { t->progress = v<0?0:(v>1?1:v); }
void __kml_tui_set_spinner(TuiNode *t, int frame) { t->spinnerFrame = frame; }

void __kml_tui_insert(TuiNode *parent, TuiNode *child) {
  if (parent->nkids >= parent->kcap) { parent->kcap = parent->kcap ? parent->kcap*2 : 4; parent->kids = (TuiNode**)realloc(parent->kids, sizeof(TuiNode*)*parent->kcap); }
  YGNodeInsertChild(parent->yg, child->yg, parent->nkids);
  parent->kids[parent->nkids++] = child;
}

static void kmltui_free(TuiNode *t) {
  if (!t) return;
  for (int i=0;i<t->nkids;i++) kmltui_free(t->kids[i]);
  for (int i=0;i<t->nitems;i++) free(t->items[i]);
  free(t->items); free(t->kids); free(t->text);
  YGNodeFree(t->yg);
  free(t);
}

/* ---- cell grid + diff painter ----------------------------------------- */

/* One grid cell holds a whole grapheme (base + trailing combining marks) as
   UTF-8 bytes plus its display width: w==1 normal, w==2 wide (the next column
   is a w==0 continuation), w==0 continuation (skipped by the painter). */
typedef struct { char g[16]; unsigned char nb; unsigned char w; short fg; short bg; short attr; } Cell;

static Cell *g_front = NULL;    /* last painted grid, persistent */
static Cell *g_back = NULL;
static int g_cols = 0, g_rows = 0;
static char *g_out = NULL; static size_t g_outlen = 0, g_outcap = 0;

static void kmltui_emit(const char *s, size_t n) {
  if (g_outlen + n + 1 > g_outcap) { g_outcap = (g_outlen+n+1)*2; g_out = (char*)realloc(g_out, g_outcap); }
  memcpy(g_out + g_outlen, s, n); g_outlen += n;
}
static void kmltui_emitf(const char *fmt, ...) {
  char buf[64]; va_list ap; __builtin_va_start(ap, fmt);
  int n = vsnprintf(buf, sizeof buf, fmt, ap); __builtin_va_end(ap);
  if (n>0) kmltui_emit(buf, (size_t)n);
}

/* border glyph tables: [style][0..5] = TL TR BL BR H V */
static const unsigned int BORDER[4][6] = {
  {0,0,0,0,0,0},
  {0x250C,0x2510,0x2514,0x2518,0x2500,0x2502}, /* single */
  {0x256D,0x256E,0x2570,0x256F,0x2500,0x2502}, /* round  */
  {0x2554,0x2557,0x255A,0x255D,0x2550,0x2551}, /* double */
};

/* Write a laid-out grapheme (UTF-8 bytes + display width) into the back grid;
   a w==2 glyph also stamps a w==0 continuation into the next column. */
static void setglyph(int x, int y, const char *g, int nb, int w, int fg, int bg, int attr) {
  if (x<0||y<0||x>=g_cols||y>=g_rows) return;
  Cell *c = &g_back[y*g_cols + x];
  if (nb > (int)sizeof c->g) nb = (int)sizeof c->g;
  memcpy(c->g, g, nb); c->nb=(unsigned char)nb; c->w=(unsigned char)w;
  c->fg=(short)fg; c->bg=(short)bg; c->attr=(short)attr;
  if (w == 2 && x+1 < g_cols) {
    Cell *k = &g_back[y*g_cols + x+1];
    k->nb=0; k->w=0; k->fg=(short)fg; k->bg=(short)bg; k->attr=(short)attr;
  }
}
/* Convenience: place a single code point (border glyphs, cursor block, …). */
static void setcp(int x, int y, unsigned int cp, int fg, int bg, int attr) {
  char b[4]; int nb = kmltui_enc(cp, b);
  int w = kmltui_wcwidth(cp); if (w < 1) w = 1;
  setglyph(x, y, b, nb, w, fg, bg, attr);
}
/* Paint a code-point span left-to-right from (x,y), grouping trailing
   combining marks onto their base and advancing by column width. Returns the
   number of columns consumed; stops at maxcol. */
static int paint_cps(int x, int y, const unsigned int *cp, int start, int cnt, int maxcol, int fg, int bg, int attr) {
  int col = 0, i = 0;
  while (i < cnt && col < maxcol) {
    unsigned int c = cp[start+i];
    int w = kmltui_wcwidth(c);
    if (w == 0) { i++; continue; }         /* stray combining mark: drop */
    if (col + w > maxcol) break;            /* wide glyph won't fit the edge */
    char g[16]; int nb = kmltui_enc(c, g); i++;
    while (i < cnt && nb + 4 <= (int)sizeof g && kmltui_wcwidth(cp[start+i]) == 0)
      nb += kmltui_enc(cp[start+i++], g + nb);
    setglyph(x+col, y, g, nb, w, fg, bg, attr);
    col += w;
  }
  return col;
}

static void fillbg(int x,int y,int w,int h,int bg){
  for(int j=0;j<h;j++)for(int i=0;i<w;i++){ if(x+i<0||y+j<0||x+i>=g_cols||y+j>=g_rows)continue; Cell*c=&g_back[(y+j)*g_cols+x+i]; c->g[0]=' '; c->nb=1; c->w=1; c->bg=(short)bg; }
}

static void paint_node(TuiNode *t, int ox, int oy) {
  float fl = YGNodeLayoutGetLeft(t->yg), ft = YGNodeLayoutGetTop(t->yg);
  int x = ox + (int)(fl+0.5f), y = oy + (int)(ft+0.5f);
  int w = (int)(YGNodeLayoutGetWidth(t->yg)+0.5f), h = (int)(YGNodeLayoutGetHeight(t->yg)+0.5f);
  if (t->bg >= 0) fillbg(x,y,w,h,t->bg);
  if (t->border && w>=2 && h>=2) {
    const unsigned int *b = BORDER[t->border];
    int bc = t->borderColor, bg = t->bg;
    setcp(x,y,b[0],bc,bg,0); setcp(x+w-1,y,b[1],bc,bg,0);
    setcp(x,y+h-1,b[2],bc,bg,0); setcp(x+w-1,y+h-1,b[3],bc,bg,0);
    for(int i=1;i<w-1;i++){ setcp(x+i,y,b[4],bc,bg,0); setcp(x+i,y+h-1,b[4],bc,bg,0); }
    for(int j=1;j<h-1;j++){ setcp(x,y+j,b[5],bc,bg,0); setcp(x+w-1,y+j,b[5],bc,bg,0); }
  }
  switch (t->kind) {
    case K_TEXT: {
      const char *s = t->text ? t->text : "";
      int slen = (int)strlen(s)+1;
      unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*slen);
      int n=kmltui_decode(s,cp,slen);
      int *st=(int*)malloc(sizeof(int)*(n+1)), *ln=(int*)malloc(sizeof(int)*(n+1));
      int lc = t->wrap ? kmltui_wrap(cp, n, w>0?w:n, st, ln, n+1) : (st[0]=0,ln[0]=n,1);
      for(int r=0;r<lc && r<h;r++) paint_cps(x, y+r, cp, st[r], ln[r], w>0?w:n, t->fg, t->bg, t->attr);
      free(st); free(ln); free(cp);
      break;
    }
    case K_LIST: {
      int n = t->nitems;
      /* Scroll so the selected item stays visible when the list is taller than
         its laid-out height; a 1-column scrollbar tracks position on the right.
         The offset is derived from the selected index each frame — no retained
         state, matching the immediate-mode model. */
      int bar = (n > h && w >= 2);
      int cw = bar ? w-1 : w;           /* content width, leaving room for the bar */
      int off = 0;
      if (n > h) {
        int sel = t->selected < 0 ? 0 : t->selected;
        off = sel - h/2;
        if (off > n - h) off = n - h;
        if (off < 0) off = 0;
      }
      for(int r=0; r<h && off+r<n; r++){
        int idx = off + r;
        const char *s = t->items[idx];
        int slen=(int)strlen(s)+1;
        unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*slen);
        int cn=kmltui_decode(s,cp,slen);
        int sel = (idx==t->selected);
        int attr = t->attr | (sel?8:0);
        if (sel) for(int i=0;i<cw;i++) setglyph(x+i,y+r," ",1,1,t->fg,t->bg,attr);
        paint_cps(x, y+r, cp, 0, cn, cw, t->fg, t->bg, attr);
        free(cp);
      }
      if (bar) {
        int th = h*h/n; if (th < 1) th = 1;
        int maxoff = n - h;
        int ty = maxoff>0 ? off*(h-th)/maxoff : 0;
        for(int r=0;r<h;r++)
          setcp(x+w-1, y+r, (r>=ty && r<ty+th)?0x2588:0x2591, t->fg, t->bg, t->attr);
      }
      break;
    }
    case K_PROGRESS: {
      int filled = (int)(t->progress * w + 0.5);
      for(int i=0;i<w;i++) setcp(x+i,y, i<filled?0x2588:0x2591, t->fg, t->bg, t->attr);
      break;
    }
    case K_SPINNER: {
      static const unsigned int FR[] = {0x280B,0x2819,0x2839,0x2838,0x283C,0x2834,0x2826,0x2827,0x2807,0x280F};
      setcp(x,y,FR[t->spinnerFrame % 10],t->fg,t->bg,t->attr);
      if (t->text && *t->text) {
        int slen=(int)strlen(t->text)+1;
        unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*slen);
        int n=kmltui_decode(t->text,cp,slen);
        paint_cps(x+2, y, cp, 0, n, w>2?w-2:0, t->fg, t->bg, t->attr);
        free(cp);
      }
      break;
    }
    case K_INPUT: {
      const char *s = t->text ? t->text : "";
      int slen=(int)strlen(s)+1;
      unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*slen);
      int n=kmltui_decode(s,cp,slen);
      int used = paint_cps(x, y, cp, 0, n, w, t->fg, t->bg, t->attr);
      if (used < w) setglyph(x+used,y," ",1,1,t->fg,t->bg,t->attr|8); /* cursor block */
      free(cp);
      break;
    }
  }
  for (int i=0;i<t->nkids;i++) paint_node(t->kids[i], x, y);
}

static int termcols(void){ struct winsize ws; if(ioctl(1,TIOCGWINSZ,&ws)==0&&ws.ws_col>0)return ws.ws_col; return 80; }
static int termrows(void){ struct winsize ws; if(ioctl(1,TIOCGWINSZ,&ws)==0&&ws.ws_row>0)return ws.ws_row; return 24; }

void __kml_tui_enter(void) { const char *s = "\x1b[?1049h\x1b[?25l\x1b[2J"; if(write(1,s,strlen(s))<0){} free(g_front); g_front=NULL; }
void __kml_tui_leave(void) { const char *s = "\x1b[?25h\x1b[?1049l"; if(write(1,s,strlen(s))<0){} free(g_front); g_front=NULL; g_cols=g_rows=0; }

/* emit an SGR sequence for a cell's attributes, tracking current state. */
static void sgr_for(Cell *c, short *cfg, short *cbg, short *cattr) {
  if (c->fg==*cfg && c->bg==*cbg && c->attr==*cattr) return;
  kmltui_emit("\x1b[0m", 4);
  if (c->attr & 1) kmltui_emit("\x1b[1m",4);
  if (c->attr & 2) kmltui_emit("\x1b[2m",4);
  if (c->attr & 4) kmltui_emit("\x1b[4m",4);
  if (c->attr & 8) kmltui_emit("\x1b[7m",4);
  if (c->fg>=0) kmltui_emitf("\x1b[%dm", c->fg);
  if (c->bg>=0) kmltui_emitf("\x1b[%dm", c->bg);
  *cfg=c->fg; *cbg=c->bg; *cattr=c->attr;
}

static void cell_default(Cell *c) { c->g[0]=' '; c->nb=1; c->w=1; c->fg=-1; c->bg=-1; c->attr=0; }
static int cell_eq(const Cell *a, const Cell *b) {
  return a->nb==b->nb && a->w==b->w && a->fg==b->fg && a->bg==b->bg && a->attr==b->attr && memcmp(a->g,b->g,a->nb)==0;
}

void __kml_tui_render(TuiNode *root) {
  int cols = termcols(), rows = termrows();
  int resized = (cols != g_cols || rows != g_rows);
  g_back = (Cell*)malloc(sizeof(Cell)*cols*rows);
  for (int i=0;i<cols*rows;i++) cell_default(&g_back[i]);
  int oldc=g_cols, oldr=g_rows; g_cols=cols; g_rows=rows;

  /* Fill the screen only where the root left its size unset — an explicit
     width/height on the root is honoured. */
  if (YGNodeStyleGetWidth(root->yg).unit == YGUnitAuto || YGNodeStyleGetWidth(root->yg).unit == YGUnitUndefined)
    YGNodeStyleSetWidth(root->yg, (float)cols);
  if (YGNodeStyleGetHeight(root->yg).unit == YGUnitAuto || YGNodeStyleGetHeight(root->yg).unit == YGUnitUndefined)
    YGNodeStyleSetHeight(root->yg, (float)rows);
  YGNodeCalculateLayout(root->yg, (float)cols, (float)rows, YGDirectionLTR);
  paint_node(root, 0, 0);

  g_outlen = 0;
  if (resized) { kmltui_emit("\x1b[2J", 4); free(g_front); g_front=NULL; }
  short cfg=-2,cbg=-2,cattr=-2; int lastRow=-1,lastCol=-1;
  for (int y=0;y<rows;y++){
    for (int x=0;x<cols;x++){
      Cell *bc = &g_back[y*cols+x];
      if (bc->w == 0) continue;   /* continuation of a wide glyph to the left */
      Cell *fc = (g_front && !resized && y<oldr && x<oldc) ? &g_front[y*oldc+x] : NULL;
      if (fc && cell_eq(fc, bc)) continue;
      if (y!=lastRow || x!=lastCol) kmltui_emitf("\x1b[%d;%dH", y+1, x+1);
      sgr_for(bc, &cfg, &cbg, &cattr);
      kmltui_emit(bc->g, bc->nb);
      lastRow=y; lastCol=x + (bc->w?bc->w:1);
    }
  }
  kmltui_emit("\x1b[0m", 4);
  if (g_outlen) { if(write(1, g_out, g_outlen)<0){} }
  free(g_front); g_front = g_back; g_back = NULL;
  kmltui_free(root);
}
`
}
