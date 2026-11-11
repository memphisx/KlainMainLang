// kml:global
// navigator — a global — as Node has it (lib/internal/navigator.js): the
// process's Navigator, its values read from the host on first use. Navigator
// itself cannot be constructed. A program that names navigator or Navigator
// without declaring it imports this module.

import { availableParallelism, machine, type } from 'os';

let creating = false;

// The default locale as ICU picks it: LC_ALL, else LC_MESSAGES, else LANG,
// as a BCP 47 tag (`el_GR.UTF-8` → `el-GR`, `sr_RS@latin` → `sr-RS-latin`);
// none, or the C/POSIX locale, is en-US.
function defaultLanguage(): string {
    const env = process.env;
    let v = env.LC_ALL || env.LC_MESSAGES || env.LANG || '';
    const dot = v.indexOf('.');
    const at = v.indexOf('@');
    const modifier = at >= 0 ? v.slice(at + 1) : '';
    if (dot >= 0) v = v.slice(0, dot);
    else if (at >= 0) v = v.slice(0, at);
    if (v === '' || v === 'C' || v === 'POSIX') return 'en-US';
    v = v.replace(/_/g, '-');
    return modifier !== '' ? `${v}-${modifier}` : v;
}

// Node's getNavigatorPlatform.
function platformName(): string {
    switch (process.platform) {
        case 'darwin':
            return 'MacIntel';
        case 'win32':
            return 'Win32';
        case 'linux':
            // From process.arch, not the kernel's machine name (aarch64).
            if (process.arch === 'ia32') return 'Linux i686';
            if (process.arch === 'x64') return 'Linux x86_64';
            return `Linux ${process.arch}`;
    }
    return `${type()} ${machine()}`;
}

export class Navigator {
    #concurrency: number | undefined;
    #language: string | undefined;
    #languages: readonly string[] | undefined;
    #userAgent: string | undefined;
    #platform: string | undefined;

    constructor() {
        if (!creating) {
            const e: any = new TypeError('Illegal constructor');
            e.code = 'ERR_ILLEGAL_CONSTRUCTOR';
            throw e;
        }
    }

    get hardwareConcurrency(): number {
        if (this.#concurrency === undefined) this.#concurrency = availableParallelism();
        return this.#concurrency;
    }

    get language(): string {
        if (this.#language === undefined) this.#language = defaultLanguage();
        return this.#language;
    }

    get languages(): readonly string[] {
        if (this.#languages === undefined) this.#languages = Object.freeze([this.language]);
        return this.#languages;
    }

    get userAgent(): string {
        if (this.#userAgent === undefined) this.#userAgent = `Node.js/${process.version.slice(1).split('.')[0]}`;
        return this.#userAgent;
    }

    get platform(): string {
        if (this.#platform === undefined) this.#platform = platformName();
        return this.#platform;
    }
}

function makeNavigator(): Navigator {
    creating = true;
    try {
        return new Navigator();
    } finally {
        creating = false;
    }
}

export const navigator: Navigator = makeNavigator(); // kml:global
