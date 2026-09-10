// webview_sailfish.cpp — the `-webview=sailfish` backend (TDD-00146 Stage 3).
//
// Implements the same 13-function `webview_*` C ABI as the vendored
// webview/webview binding (webview.h), so the emitter and the whole klain:webview
// language surface (bind/typed-bind/eval/serve/--emit-window-dts) are unchanged —
// only the engine underneath differs. Here the engine is Sailfish's Gecko
// (embedlite / qtmozembed) via the `RawWebView` QML type shipped by
// `Sailfish.WebView`, driven from C++.
//
// Why load the QML type instead of instantiating QuickMozView directly: RawWebView
// owns the QMozWindow / GL-context / compositor wiring that a Sailfish webview
// needs. Re-doing that by hand in C++ is a large, fragile job; hosting the shipped
// component in a QQuickView lets it do that work and gives us the QMozView method
// surface (runJavaScript / sendAsyncMessage / recvAsyncMessage) to drive.
//
// The bind protocol is byte-identical to webview.h's: the same injected
// `window._rpc` glue defines `window[name]` as a Promise-returning function that
// calls `window.external.invoke(JSON.stringify({id,method,params}))`. The only
// difference is transport — webview.h routes `invoke` through a WebKit message
// handler; here `invoke` dispatches a DOM CustomEvent that a small frame script
// forwards to the chrome side as an async message, surfaced in C++ as the
// `recvAsyncMessage` signal. Native→page (eval, and promise resolution) go through
// `runJavaScript`, exactly as webview.h uses `eval`.
//
// Qt here is the frozen Sailfish Qt 5.6, so nothing newer than 5.6 is used —
// notably QMetaObject::invokeMethod's functor overload (5.10) is unavailable, so
// cross-thread dispatch posts a QEvent carrying a std::function instead.

#include <QGuiApplication>
#include <QTimer>
#include <QQuickView>
#include <QQmlComponent>
#include <QQmlEngine>
#include <QQuickItem>
#include <QVariant>
#include <QVariantMap>
#include <QString>
#include <QUrl>
#include <QEvent>
#include <QCoreApplication>
#include <QTemporaryFile>
#include <QDir>
#include <QStandardPaths>

#include <webengine.h>

// The klain runtime's GUI-thread drain entry points (ADR-00439). A native QTimer
// calls these directly at ~60 Hz instead of routing __kml_tick through the page
// (a full embedlite IPC round-trip per tick). All three are always defined for a
// webview program — real runtimes or no-op stubs (see emitLoopTaskStubs) — with
// external linkage, so the shim links against them.
extern "C" {
void __kml_timer_tick();
void __kml_task_sched_step();
void __kml_drain_microtasks();
}

#include <functional>
#include <map>
#include <string>
#include <cstring>
#include <cstdio>
#include <cstdlib>

// KLAIN_WV_DEBUG=1 traces the shim's lifecycle to stderr — for on-device
// bring-up of the embedlite bridge. Off by default.
#define KLAIN_WV_LOG(...)                                                       \
  do {                                                                          \
    if (::getenv("KLAIN_WV_DEBUG")) {                                           \
      ::fprintf(stderr, "[kml-wv] " __VA_ARGS__);                               \
      ::fprintf(stderr, "\n");                                                  \
      ::fflush(stderr);                                                         \
    }                                                                           \
  } while (0)

// ---------------------------------------------------------------------------
// Cross-thread dispatch (Qt 5.6): post a QEvent carrying a std::function<void()>
// to an object living on the GUI thread; its event() runs it there.
// ---------------------------------------------------------------------------
namespace {

class FnEvent : public QEvent {
public:
  static QEvent::Type kType() {
    static QEvent::Type t = static_cast<QEvent::Type>(QEvent::registerEventType());
    return t;
  }
  explicit FnEvent(std::function<void()> fn) : QEvent(kType()), mFn(std::move(fn)) {}
  std::function<void()> mFn;
};

// The page-side RPC glue, identical to webview.h's bind() injection: define
// window[name] as a function returning a Promise, registered in window._rpc,
// forwarding the call through window.external.invoke. %1 is the bound name.
static QString bindGlueJS(const QString &name) {
  return QString(
    "(function(){var name='%1';"
    "var RPC=window._rpc=(window._rpc||{nextSeq:1});"
    "window[name]=function(){"
    "var seq=RPC.nextSeq++;"
    "var promise=new Promise(function(resolve,reject){RPC[seq]={resolve:resolve,reject:reject};});"
    "window.external.invoke(JSON.stringify({id:seq,method:name,params:Array.prototype.slice.call(arguments)}));"
    "return promise;};})()").arg(name);
}

// Injected once per view: window.external.invoke routes to a DOM CustomEvent the
// frame script forwards to native. Kept separate from the per-bind glue so it is
// installed exactly once.
static const char *kExternalInvokeJS =
  "if(!window.external){window.external={};}"
  "window.external.invoke=function(s){"
  "document.dispatchEvent(new CustomEvent('kmlrpc',{detail:s,bubbles:true}));};";

// The frame script (privileged, runs in the content process): forward the page's
// 'kmlrpc' CustomEvent to the chrome side as an async message. wantsUntrusted=true
// so events dispatched by page script are delivered. Modeled on the shipped
// embedhelper.js idiom (bare addEventListener attaches to the content window).
// The page dispatches a bubbling 'kmlrpc' CustomEvent on `document`; this frame
// script (privileged, content process) catches it in the capture phase
// (useCapture=true, wantsUntrusted=true — a page-script event is untrusted) and
// forwards it to the chrome side as an async message. A non-bubbling event on
// window, or a bubble-phase listener, does NOT reach the frame script — verified
// on-device.
static const char *kFrameScriptJS =
  "addEventListener('kmlrpc',function(e){"
  "sendAsyncMessage('embed:kmlrpc',{msg:e.detail});"
  "},true,true);";

// Minimal JSON string-field extractor mirroring webview.h's detail::json_parse
// usage (id/method/params). The message is the object built by the bind glue, so
// this only needs to pull top-level "id" (number, as string), "method" (string),
// and "params" (raw array text). Values arrive already JSON; we return raw slices.
static std::string jsonField(const std::string &s, const std::string &key) {
  std::string pat = "\"" + key + "\"";
  size_t k = s.find(pat);
  if (k == std::string::npos) return "";
  size_t c = s.find(':', k + pat.size());
  if (c == std::string::npos) return "";
  size_t i = c + 1;
  while (i < s.size() && (s[i] == ' ' || s[i] == '\t')) i++;
  if (i >= s.size()) return "";
  if (s[i] == '"') { // string: return unquoted contents (no escape handling — ids/methods are simple)
    size_t j = i + 1;
    std::string out;
    while (j < s.size() && s[j] != '"') { if (s[j] == '\\' && j + 1 < s.size()) j++; out += s[j++]; }
    return out;
  }
  if (s[i] == '[' || s[i] == '{') { // nested: return balanced slice verbatim
    char open = s[i], close = open == '[' ? ']' : '}';
    int depth = 0; size_t j = i; bool inStr = false;
    for (; j < s.size(); j++) {
      char ch = s[j];
      if (inStr) { if (ch == '\\') j++; else if (ch == '"') inStr = false; continue; }
      if (ch == '"') inStr = true;
      else if (ch == open) depth++;
      else if (ch == close) { depth--; if (depth == 0) { j++; break; } }
    }
    return s.substr(i, j - i);
  }
  // number/bool/null: read to the next , } ]
  size_t j = i;
  while (j < s.size() && s[j] != ',' && s[j] != '}' && s[j] != ']') j++;
  std::string out = s.substr(i, j - i);
  while (!out.empty() && (out.back() == ' ' || out.back() == '\t')) out.pop_back();
  return out;
}

} // namespace

// ---------------------------------------------------------------------------
// The engine object: owns the QGuiApplication, the QQuickView window, the
// RawWebView item, and the bindings map. One per process (V1, like the other
// backends).
// ---------------------------------------------------------------------------
class SailfishWebview : public QObject {
  Q_OBJECT
public:
  using binding_fn = void (*)(const char *seq, const char *req, void *arg);

  SailfishWebview(int debug) : mDebug(debug) {
    // A minimal argc/argv for QGuiApplication (it keeps references to them).
    static int argc = 1;
    static char arg0[] = "klain-webview";
    static char *argv[] = {arg0, nullptr};
    mApp = new QGuiApplication(argc, argv);

    QString profile = QStandardPaths::writableLocation(QStandardPaths::CacheLocation)
                      + "/klain-webview";
    SailfishOS::WebEngine::initialize(profile);

    mView = new QQuickView();
    mView->setResizeMode(QQuickView::SizeRootObjectToView);

    // Inline QML: a sized root Item (grown to the window by SizeRootObjectToView)
    // with RawWebView filling it. RawWebView cannot be the root itself — as root
    // its `anchors.fill: parent` has no parent, leaving it 0×0 (a blank window).
    const char *qml =
      "import QtQuick 2.0\n"
      "import Sailfish.WebView 1.0\n"
      // active: true is required — a RawWebView does not paint while inactive
      // (WebView.qml sets `active` too). Without it the window stays blank.
      "Item { RawWebView { objectName: \"kmlRawWebView\"; anchors.fill: parent; active: true } }\n";
    QQmlComponent comp(mView->engine());
    comp.setData(QByteArray(qml), QUrl("qrc:/kml_inline.qml"));
    QObject *root = comp.create();
    QQuickItem *rootItem = qobject_cast<QQuickItem *>(root);
    mWebView = root ? root->findChild<QQuickItem *>("kmlRawWebView") : nullptr;
    if (rootItem && mWebView) {
      mView->setContent(QUrl("qrc:/kml_inline.qml"), &comp, rootItem);

      // The QMozView is not ready until it emits viewInitialized(): load* and
      // runJavaScript are rejected before that ("run javascript can be called
      // only after view is initialized"). So all view operations queue until
      // then, and the message-channel/frame-script setup happens in the handler.
      connect(mWebView, SIGNAL(viewInitialized()),
              this, SLOT(onViewInitialized()));
      // Route the page→native async message onto on_message.
      connect(mWebView, SIGNAL(recvAsyncMessage(QString, QVariant)),
              this, SLOT(onRecvAsyncMessage(QString, QVariant)));
      // Re-inject the RPC glue on each page load.
      connect(mWebView, SIGNAL(domContentLoadedChanged()),
              this, SLOT(onDomContentLoaded()));
    }
    KLAIN_WV_LOG("ctor: mWebView=%p", (void *)mWebView);
  }

  ~SailfishWebview() {
    delete mView;
    delete mApp;
  }

  QQuickItem *item() const { return mWebView; }

  void run() {
    if (mView) mView->show();
    mApp->exec();
  }
  void terminate() { mApp->quit(); }

  void dispatch(std::function<void()> fn) {
    QCoreApplication::postEvent(this, new FnEvent(std::move(fn)));
  }

  void setTitle(const QString &t) { if (mView) mView->setTitle(t); }
  void setSize(int w, int h) { if (mView) mView->resize(w, h); }

  void navigate(const QString &url) {
    whenReady([this, url]() { QMetaObject::invokeMethod(mWebView, "load", Q_ARG(QString, url)); });
  }
  void setHtml(const QString &html) {
    whenReady([this, html]() { QMetaObject::invokeMethod(mWebView, "loadHtml", Q_ARG(QString, html), Q_ARG(QUrl, QUrl())); });
  }
  void eval(const QString &js) {
    whenReady([this, js]() { QMetaObject::invokeMethod(mWebView, "runJavaScript", Q_ARG(QString, js)); });
  }

  // init()/bind(): only *store* the scripts — they are injected on each page load
  // by onDomContentLoaded (which runs after the view is initialized and the page
  // is parsed, when runJavaScript is valid). Injecting here would race the view's
  // initialization.
  void init(const QString &js) { mInitScripts.push_back(js); }

  void bind(const std::string &name, binding_fn fn, void *arg) {
    if (mBindings.count(name)) return;
    mBindings[name] = Binding{fn, arg};
    mInitScripts.push_back(bindGlueJS(QString::fromStdString(name)));
  }
  void unbind(const std::string &name) {
    if (mBindings.erase(name)) eval(QString("delete window['%1'];").arg(QString::fromStdString(name)));
  }

  // resolve(): settle the page-side promise, on the GUI thread, exactly as
  // webview.h does — eval a resolve/reject + delete of the _rpc slot.
  void resolve(const std::string &seq, int status, const std::string &result) {
    std::string s = seq, r = result;
    dispatch([this, s, status, r]() {
      QString js = status == 0
        ? QString("window._rpc[%1].resolve(%2); delete window._rpc[%1]")
            .arg(QString::fromStdString(s)).arg(QString::fromStdString(r))
        : QString("window._rpc[%1].reject(%2); delete window._rpc[%1]")
            .arg(QString::fromStdString(s)).arg(QString::fromStdString(r));
      eval(js);
    });
  }

protected:
  bool event(QEvent *e) override {
    if (e->type() == FnEvent::kType()) { static_cast<FnEvent *>(e)->mFn(); return true; }
    return QObject::event(e);
  }

private Q_SLOTS:
  void onViewInitialized() {
    mReady = true;
    KLAIN_WV_LOG("viewInitialized: flushing %zu pending", mPending.size());
    // The view can take messages now: subscribe to our channel and install the
    // bridge frame script, then flush everything queued before init.
    QMetaObject::invokeMethod(mWebView, "addMessageListener",
                              Q_ARG(QString, QString("embed:kmlrpc")));
    QMetaObject::invokeMethod(mWebView, "forceViewActiveFocus");
    installFrameScript();
    std::vector<std::function<void()>> pending;
    pending.swap(mPending);
    for (auto &fn : pending) fn();
    // Drive the loop-fusion pump natively (ADR-00439): a 16 ms QTimer drains the
    // runtime's timers/tasks/microtasks on the GUI thread directly, so no
    // __kml_tick IPC round-trip through the page is needed. The page-side pump is
    // suppressed in onDomContentLoaded.
    mPump = new QTimer(this);
    connect(mPump, SIGNAL(timeout()), this, SLOT(onPumpTick()));
    mPump->start(16);
  }

  void onPumpTick() {
    __kml_timer_tick();
    __kml_task_sched_step();
    __kml_drain_microtasks();
  }

  void onDomContentLoaded() {
    if (!mWebView) return;
    // domContentLoadedChanged fires on both the false (new load starting) and
    // true (parsed) transitions; only inject once the document is actually there.
    if (!mWebView->property("domContentLoaded").toBool()) return;
    KLAIN_WV_LOG("domContentLoaded: injecting %zu init scripts", mInitScripts.size());
    // Suppress the runtime's page-side pump: its installer is guarded by
    // `if(!window.__kml_pump)`, so pre-setting it truthy stops the setInterval →
    // __kml_tick IPC. The native QTimer pump drives the runtime instead.
    eval(QString::fromUtf8("window.__kml_pump=1;"));
    // Re-inject external.invoke + every stored init/bind script on each load
    // (webview.h's init-before-every-load contract).
    eval(QString::fromUtf8(kExternalInvokeJS));
    for (const QString &s : mInitScripts) eval(s);
  }

  void onRecvAsyncMessage(const QString &name, const QVariant &data) {
    if (name != "embed:kmlrpc") return;
    QVariantMap m = data.toMap();
    std::string msg = m.value("msg").toString().toStdString();
    KLAIN_WV_LOG("recv kmlrpc msg=%s", msg.c_str());
    on_message(msg);
  }

private:
  struct Binding { binding_fn fn; void *arg; };

  // Run fn now if the view is initialized, else queue it until viewInitialized().
  void whenReady(std::function<void()> fn) {
    if (!mWebView) return;
    if (mReady) fn();
    else mPending.push_back(std::move(fn));
  }

  void installFrameScript() {
    // loadFrameScript takes a URL; write the bridge to a temp file and load it.
    // (chrome:// registration would avoid the temp file but needs a manifest;
    // file:// keeps the shim self-contained. Confirmed loadable on-device is the
    // remaining runtime-verification step.)
    QString path = QDir::tempPath() + "/kml_webview_framescript.js";
    QFile f(path);
    if (f.open(QIODevice::WriteOnly | QIODevice::Truncate)) {
      f.write(kFrameScriptJS);
      f.close();
      KLAIN_WV_LOG("loadFrameScript file://%s", path.toUtf8().constData());
      QMetaObject::invokeMethod(mWebView, "loadFrameScript",
                                Q_ARG(QString, QString("file://") + path));
    } else {
      KLAIN_WV_LOG("frame script: cannot write %s", path.toUtf8().constData());
    }
  }

  void on_message(const std::string &msg) {
    std::string seq = jsonField(msg, "id");
    std::string name = jsonField(msg, "method");
    std::string args = jsonField(msg, "params");
    auto it = mBindings.find(name);
    if (it == mBindings.end()) return;
    it->second.fn(seq.c_str(), args.c_str(), it->second.arg);
  }

  int mDebug;
  QGuiApplication *mApp = nullptr;
  QQuickView *mView = nullptr;
  QQuickItem *mWebView = nullptr;
  bool mReady = false;
  QTimer *mPump = nullptr;
  std::vector<std::function<void()>> mPending;
  std::vector<QString> mInitScripts;
  std::map<std::string, Binding> mBindings;
};

// This class uses Qt signals/slots (Q_OBJECT), so the build runs Qt's moc over
// this translation unit and appends the generated meta-object code here. moc is
// the Sailfish target's own aarch64 moc 5.6.3 (matching the frozen Qt), run in
// the target environment — see LocateWebviewSailfish's build note. The generated
// file is #included at the end, the standard single-file-amalgamation idiom.
#include "webview_sailfish.moc"

// ---------------------------------------------------------------------------
// The 13-function C ABI (matches webview.h exactly).
// ---------------------------------------------------------------------------
extern "C" {

typedef void *webview_t;

webview_t webview_create(int debug, void *wnd) {
  (void)wnd;
  SailfishWebview *w = new SailfishWebview(debug);
  if (!w->item()) { delete w; return nullptr; }
  return w;
}
void webview_destroy(webview_t w) { delete static_cast<SailfishWebview *>(w); }
void webview_run(webview_t w) { static_cast<SailfishWebview *>(w)->run(); }
void webview_terminate(webview_t w) { static_cast<SailfishWebview *>(w)->terminate(); }
void webview_dispatch(webview_t w, void (*fn)(webview_t, void *), void *arg) {
  SailfishWebview *sw = static_cast<SailfishWebview *>(w);
  sw->dispatch([=]() { fn(w, arg); });
}
void webview_set_title(webview_t w, const char *title) {
  static_cast<SailfishWebview *>(w)->setTitle(QString::fromUtf8(title));
}
void webview_set_size(webview_t w, int width, int height, int hints) {
  (void)hints;
  static_cast<SailfishWebview *>(w)->setSize(width, height);
}
void webview_navigate(webview_t w, const char *url) {
  static_cast<SailfishWebview *>(w)->navigate(QString::fromUtf8(url));
}
void webview_set_html(webview_t w, const char *html) {
  static_cast<SailfishWebview *>(w)->setHtml(QString::fromUtf8(html));
}
void webview_init(webview_t w, const char *js) {
  static_cast<SailfishWebview *>(w)->init(QString::fromUtf8(js));
}
void webview_eval(webview_t w, const char *js) {
  static_cast<SailfishWebview *>(w)->eval(QString::fromUtf8(js));
}
void webview_bind(webview_t w, const char *name,
                  void (*fn)(const char *seq, const char *req, void *arg), void *arg) {
  static_cast<SailfishWebview *>(w)->bind(name, fn, arg);
}
void webview_unbind(webview_t w, const char *name) {
  static_cast<SailfishWebview *>(w)->unbind(name);
}
void webview_return(webview_t w, const char *seq, int status, const char *result) {
  static_cast<SailfishWebview *>(w)->resolve(seq, status, result);
}

} // extern "C"
