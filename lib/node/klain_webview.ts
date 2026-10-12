// klain:webview — a desktop window over the system browser engine (WKWebView,
// WebKitGTK, WebView2) through webview/webview. Native functions are exposed
// to the page as `window.<name>(...)`, each returning a Promise there:
//
//	const w = new Webview({ title: 'App', width: 800, height: 600, bindings: {
//	  add: (a: number, b: number): number => a + b,
//	} })
//	w.html('<script>window.add(2, 3).then(console.log)</script>')
//	w.run()
import { EmbeddedAssets, _kmlServe } from './klain_assets';

// A native function the page calls with its arguments decoded from JSON and
// whose result (or the value its Promise settles to) is sent back as JSON.
export type Binding = (...args: any[]) => any;

export interface WebviewOptions {
    title?: string;
    // Given together.
    width?: number;
    height?: number;
    // The browser engine's developer tools.
    debug?: boolean;
    // Each key a `window.*` function: the whole surface the page can call.
    bindings?: { [name: string]: Binding };
    // Files embedded with klain:assets' embedDir, served on a loopback port
    // and navigated to: a single-binary SPA.
    serve?: EmbeddedAssets;
}

// One window per process: webview/webview runs a single GUI loop.
let created = false;

export class Webview {
    private readonly w: number;

    constructor(options?: WebviewOptions) {
        if (created) {
            throw new Error('only one Webview window per process is supported');
        }
        const o: WebviewOptions = options === undefined ? {} : options;
        if ((o.width === undefined) !== (o.height === undefined)) {
            throw new TypeError('Webview width and height must be given together');
        }
        created = true;
        this.w = __kml_native.wvCreate(o.debug === true);
        if (o.title !== undefined) __kml_native.wvSetTitle(this.w, o.title);
        if (o.width !== undefined && o.height !== undefined) {
            __kml_native.wvSetSize(this.w, o.width, o.height);
        }
        const bindings = o.bindings;
        if (bindings !== undefined) {
            for (const name of Object.keys(bindings)) this.register(name, bindings[name], true);
        }
        if (o.serve !== undefined) {
            __kml_native.wvNavigate(this.w, 'http://127.0.0.1:' + _kmlServe(o.serve) + '/');
        }
    }

    navigate(url: string): void {
        __kml_native.wvNavigate(this.w, url);
    }

    // Replaces the page with html.
    html(html: string): void {
        __kml_native.wvSetHtml(this.w, html);
    }

    setTitle(title: string): void {
        __kml_native.wvSetTitle(this.w, title);
    }

    setSize(width: number, height: number): void {
        __kml_native.wvSetSize(this.w, width, height);
    }

    // Script run at the start of every page load.
    init(js: string): void {
        __kml_native.wvInit(this.w, js);
    }

    // Runs js in the page; safe from any thread.
    eval(js: string): void {
        __kml_native.wvEval(this.w, js);
    }

    // `window.<name>(...)` calls fn with the page's arguments as a JSON
    // array string; fn's string result (or what its Promise settles to) is
    // the JSON the page's Promise resolves with, anything else null.
    bind(name: string, fn: (args: string) => any): void {
        this.register(name, fn, false);
    }

    // `window.<name>(...)` calls fn with the page's arguments decoded from
    // JSON and resolves with its result encoded as JSON.
    bindTyped(name: string, fn: Binding): void {
        this.register(name, fn, true);
    }

    unbind(name: string): void {
        __kml_native.wvUnbind(this.w, name);
    }

    // Shows the window and runs the GUI loop, the runtime's own loop inside
    // it, until terminate().
    run(): void {
        __kml_native.wvRun(this.w);
    }

    terminate(): void {
        __kml_native.wvTerminate(this.w);
    }

    destroy(): void {
        __kml_native.wvDestroy(this.w);
    }

    private register(name: string, fn: Binding, typed: boolean): void {
        __kml_native.wvBind(this.w, name, (id: string, req: string): void => {
            this.dispatch(id, req, fn, typed);
        });
    }

    private dispatch(id: string, req: string, fn: Binding, typed: boolean): void {
        let r: any;
        try {
            r = typed ? fn(...JSON.parse(req)) : fn(req);
        } catch (e) {
            __kml_native.wvReturn(this.w, id, 1, '"native handler threw"');
            return;
        }
        if (r instanceof Promise) {
            r.then(
                (v: any) => __kml_native.wvReturn(this.w, id, 0, encode(v, typed)),
                (_e: any) => __kml_native.wvReturn(this.w, id, 1, '"native handler rejected"'),
            );
            return;
        }
        __kml_native.wvReturn(this.w, id, 0, encode(r, typed));
    }
}

// A result as the JSON the page's Promise resolves with.
function encode(v: any, typed: boolean): string {
    if (!typed) return typeof v === 'string' ? v : 'null';
    const s = JSON.stringify(v);
    return s === undefined ? 'null' : s;
}
