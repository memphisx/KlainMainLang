// klain:assets — a directory embedded into the executable at compile time:
//
//	const site = embedDir('./dist')
//	const html = site.get('/index.html') // its bytes, no copy
//
// embedDir's path is read when the program is compiled, so it must be a
// string literal; the files are linked into the binary.

// The files of an embedded directory.
export class EmbeddedAssets {
    private readonly blob: number;

    constructor(blob: number) {
        this.blob = blob;
    }

    // The blob's address (klain:webview's `serve`).
    _kmlBlob(): number {
        return this.blob;
    }

    // The bytes of the file at path (root-relative, `/index.html`), sharing
    // the binary's own copy; an empty buffer when there is no such file.
    get(path: string): ArrayBuffer {
        const addr = __kml_native.assetsGet(this.blob, path);
        return __kml_native.ffiView(addr, __kml_native.assetsLength(), true) as ArrayBuffer;
    }
}

/** @intrinsic klain:assets.embedDir */
declare function embedDir(path: string): EmbeddedAssets;
export { embedDir };

// The EmbeddedAssets of the blob embedDir's call site just linked.
export function _kmlEmbedded(): EmbeddedAssets {
    return new EmbeddedAssets(__kml_native.assetsTake());
}

// Serves the embedded files on an ephemeral loopback port, returned
// (klain:webview's `serve`).
export function _kmlServe(assets: EmbeddedAssets): number {
    return __kml_native.assetsServe(assets._kmlBlob());
}
