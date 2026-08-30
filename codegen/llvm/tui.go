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

/* ---- UTF-8 decoding + text wrapping (shared by measure + paint) -------- */

/* Decode s into an array of Unicode code points (one grid cell each — so a
   multibyte glyph is one column, not one-per-byte). Returns the code-point
   count; out must hold at least strlen(s) entries. Malformed bytes pass through
   as Latin-1. Wide (CJK/emoji) cells are still counted as one column — a
   documented Stage 1 limitation. */
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

/* Fill starts[]/lens[] with up to maxlines wrapped line spans (in code-point
   indices) of the decoded string cp[0..n); returns line count. Greedy
   word-wrap, hard breaks for words longer than width. width<=0 => one line. */
static int kmltui_wrap(const unsigned int *cp, int n, int width, int *starts, int *lens, int maxlines) {
  int i = 0, lc = 0;
  if (width <= 0) { if (maxlines>0){starts[0]=0;lens[0]=n;} return 1; }
  while (i < n && lc < maxlines) {
    int lineStart = i, lastSpace = -1, col = 0;
    while (i < n && cp[i] != '\n' && col < width) {
      if (cp[i] == ' ') lastSpace = i;
      i++; col++;
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
    if (!t->wrap) { free(cp); sz.width = (float)n; sz.height = 1; return sz; }
    int avail = (wm == YGMeasureModeUndefined) ? n : (int)w;
    int *st = (int*)malloc(sizeof(int)*(n+1)), *ln = (int*)malloc(sizeof(int)*(n+1));
    int lc = kmltui_wrap(cp, n, avail, st, ln, n+1);
    int maxw = 0; for (int k=0;k<lc;k++) if (ln[k]>maxw) maxw=ln[k];
    free(st); free(ln); free(cp);
    sz.width = (float)maxw; sz.height = (float)lc; return sz;
  }
  if (t->kind == K_LIST) {
    int maxw = 0;
    for (int k=0;k<t->nitems;k++){
      int sl=(int)strlen(t->items[k])+1;
      unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*sl);
      int l=kmltui_decode(t->items[k],cp,sl); free(cp);
      if(l>maxw)maxw=l;
    }
    sz.width = (float)maxw; sz.height = (float)(t->nitems>0?t->nitems:1); return sz;
  }
  if (t->kind == K_PROGRESS) {
    sz.width = (wm == YGMeasureModeUndefined) ? 20.f : w; sz.height = 1; return sz;
  }
  if (t->kind == K_SPINNER) {
    int lbl = t->text ? (int)strlen(t->text) : 0;
    sz.width = (float)(1 + (lbl ? lbl + 1 : 0)); sz.height = 1; return sz;
  }
  if (t->kind == K_INPUT) {
    int len = t->text ? (int)strlen(t->text) : 0;
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

typedef struct { unsigned int ch; short fg; short bg; short attr; } Cell;

static Cell *g_front = NULL;    /* last painted grid, persistent */
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
static void kmltui_putcp(unsigned int cp) {
  char b[4];
  if (cp < 0x80) { b[0]=(char)cp; kmltui_emit(b,1); }
  else if (cp < 0x800) { b[0]=(char)(0xC0|(cp>>6)); b[1]=(char)(0x80|(cp&0x3F)); kmltui_emit(b,2); }
  else { b[0]=(char)(0xE0|(cp>>12)); b[1]=(char)(0x80|((cp>>6)&0x3F)); b[2]=(char)(0x80|(cp&0x3F)); kmltui_emit(b,3); }
}

/* border glyph tables: [style][0..5] = TL TR BL BR H V */
static const unsigned int BORDER[4][6] = {
  {0,0,0,0,0,0},
  {0x250C,0x2510,0x2514,0x2518,0x2500,0x2502}, /* single */
  {0x256D,0x256E,0x2570,0x256F,0x2500,0x2502}, /* round  */
  {0x2554,0x2557,0x255A,0x255D,0x2550,0x2551}, /* double */
};

static Cell *g_back = NULL;
static void setcell(int x, int y, unsigned int ch, int fg, int bg, int attr) {
  if (x<0||y<0||x>=g_cols||y>=g_rows) return;
  Cell *c = &g_back[y*g_cols + x];
  c->ch = ch; c->fg = (short)fg; c->bg = (short)bg; c->attr = (short)attr;
}
static void fillbg(int x,int y,int w,int h,int bg){
  for(int j=0;j<h;j++)for(int i=0;i<w;i++){ if(x+i<0||y+j<0||x+i>=g_cols||y+j>=g_rows)continue; Cell*c=&g_back[(y+j)*g_cols+x+i]; c->ch=' '; c->bg=(short)bg; }
}

static void paint_node(TuiNode *t, int ox, int oy) {
  float fl = YGNodeLayoutGetLeft(t->yg), ft = YGNodeLayoutGetTop(t->yg);
  int x = ox + (int)(fl+0.5f), y = oy + (int)(ft+0.5f);
  int w = (int)(YGNodeLayoutGetWidth(t->yg)+0.5f), h = (int)(YGNodeLayoutGetHeight(t->yg)+0.5f);
  if (t->bg >= 0) fillbg(x,y,w,h,t->bg);
  if (t->border && w>=2 && h>=2) {
    const unsigned int *b = BORDER[t->border];
    int bc = t->borderColor, bg = t->bg;
    setcell(x,y,b[0],bc,bg,0); setcell(x+w-1,y,b[1],bc,bg,0);
    setcell(x,y+h-1,b[2],bc,bg,0); setcell(x+w-1,y+h-1,b[3],bc,bg,0);
    for(int i=1;i<w-1;i++){ setcell(x+i,y,b[4],bc,bg,0); setcell(x+i,y+h-1,b[4],bc,bg,0); }
    for(int j=1;j<h-1;j++){ setcell(x,y+j,b[5],bc,bg,0); setcell(x+w-1,y+j,b[5],bc,bg,0); }
  }
  switch (t->kind) {
    case K_TEXT: {
      const char *s = t->text ? t->text : "";
      int slen = (int)strlen(s)+1;
      unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*slen);
      int n=kmltui_decode(s,cp,slen);
      int *st=(int*)malloc(sizeof(int)*(n+1)), *ln=(int*)malloc(sizeof(int)*(n+1));
      int lc = t->wrap ? kmltui_wrap(cp, n, w>0?w:n, st, ln, n+1) : (st[0]=0,ln[0]=n,1);
      for(int r=0;r<lc && r<h;r++){
        for(int i=0;i<ln[r] && i<w;i++) setcell(x+i,y+r,cp[st[r]+i],t->fg,t->bg,t->attr);
      }
      free(st); free(ln); free(cp);
      break;
    }
    case K_LIST: {
      for(int r=0;r<t->nitems && r<h;r++){
        const char *s = t->items[r];
        int slen=(int)strlen(s)+1;
        unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*slen);
        int n=kmltui_decode(s,cp,slen);
        int sel = (r==t->selected);
        int attr = t->attr | (sel?8:0);
        for(int i=0;i<w;i++){
          unsigned int ch = (i < n) ? cp[i] : ' ';
          if (i<n || sel) setcell(x+i,y+r,ch,t->fg,t->bg,attr);
        }
        free(cp);
      }
      break;
    }
    case K_PROGRESS: {
      int filled = (int)(t->progress * w + 0.5);
      for(int i=0;i<w;i++) setcell(x+i,y, i<filled?0x2588:0x2591, t->fg, t->bg, t->attr);
      break;
    }
    case K_SPINNER: {
      static const unsigned int FR[] = {0x280B,0x2819,0x2839,0x2838,0x283C,0x2834,0x2826,0x2827,0x2807,0x280F};
      setcell(x,y,FR[t->spinnerFrame % 10],t->fg,t->bg,t->attr);
      if (t->text && *t->text) { const char*s=t->text; for(int i=0;s[i]&&2+i<w;i++) setcell(x+2+i,y,(unsigned char)s[i],t->fg,t->bg,t->attr); }
      break;
    }
    case K_INPUT: {
      const char *s = t->text ? t->text : "";
      int slen=(int)strlen(s)+1;
      unsigned int *cp=(unsigned int*)malloc(sizeof(unsigned int)*slen);
      int n=kmltui_decode(s,cp,slen);
      for(int i=0;i<n && i<w;i++) setcell(x+i,y,cp[i],t->fg,t->bg,t->attr);
      if (n<w) setcell(x+n,y,' ',t->fg,t->bg,t->attr|8); /* cursor block */
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

void __kml_tui_render(TuiNode *root) {
  int cols = termcols(), rows = termrows();
  int resized = (cols != g_cols || rows != g_rows);
  g_back = (Cell*)malloc(sizeof(Cell)*cols*rows);
  for (int i=0;i<cols*rows;i++){ g_back[i].ch=' '; g_back[i].fg=-1; g_back[i].bg=-1; g_back[i].attr=0; }
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
      Cell *fc = (g_front && !resized && y<oldr && x<oldc) ? &g_front[y*oldc+x] : NULL;
      if (fc && fc->ch==bc->ch && fc->fg==bc->fg && fc->bg==bc->bg && fc->attr==bc->attr) continue;
      if (y!=lastRow || x!=lastCol) kmltui_emitf("\x1b[%d;%dH", y+1, x+1);
      sgr_for(bc, &cfg, &cbg, &cattr);
      kmltui_putcp(bc->ch);
      lastRow=y; lastCol=x+1;
    }
  }
  kmltui_emit("\x1b[0m", 4);
  if (g_outlen) { if(write(1, g_out, g_outlen)<0){} }
  free(g_front); g_front = g_back; g_back = NULL;
  kmltui_free(root);
}
`
}
