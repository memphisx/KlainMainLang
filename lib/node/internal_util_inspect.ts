// Node's lib/internal/util/inspect.js, as far as `inspect` itself: the
// options, inspect.custom, inspect.colors and inspect.styles. The rendering
// is the compiler's inspector (__kml_native.inspectWith), which takes the
// layout options. Kept apart from `util` so the modules that inspect
// (events, readline) do not import `util`'s whole surface.

export interface InspectOptions {
    showHidden?: boolean | undefined;
    depth?: number | null | undefined;
    colors?: boolean | undefined;
    customInspect?: boolean | undefined;
    showProxy?: boolean | undefined;
    maxArrayLength?: number | null | undefined;
    maxStringLength?: number | null | undefined;
    breakLength?: number | undefined;
    compact?: boolean | number | undefined;
    sorted?: boolean | ((a: string, b: string) => number) | undefined;
    getters?: 'get' | 'set' | boolean | undefined;
    numericSeparator?: boolean | undefined;
}

class InspectDefaults {
    showHidden = false;
    depth: number | null = 2;
    colors = false;
    customInspect = true;
    showProxy = false;
    maxArrayLength: number | null = 100;
    maxStringLength: number | null = 10000;
    breakLength = 80;
    compact: boolean | number = 3;
    sorted = false;
    getters = false;
    numericSeparator = false;
}

const inspectDefaultOptions = new InspectDefaults();

// An option's value, the default when absent.
export function inspectOption(options: any, key: string): any {
    const v = options === undefined || options === null ? undefined : options[key];
    const defaults: any = inspectDefaultOptions;
    return v === undefined ? defaults[key] : v;
}

// A count option: null and Infinity are unlimited.
function countOpt(v: any): number {
    return v === null || v === Infinity ? Infinity : v;
}

/**
 * Returns a string representation of object, as console.log prints it.
 */
export function inspect(object: any, showHidden?: boolean, depth?: number | null, color?: boolean): string;
export function inspect(object: any, options?: InspectOptions): string;
export function inspect(object: any, ...rest: any[]): string {
    let options: any = undefined;
    const first = rest[0];
    if (typeof first === 'boolean') {
        // The legacy (object, showHidden, depth, colors) form.
        options = { showHidden: first, depth: rest.length > 1 ? rest[1] : undefined };
    } else if (first !== undefined && first !== null && typeof first === 'object') {
        options = first;
    }
    const depth = inspectOption(options, 'depth');
    const compact = inspectOption(options, 'compact');
    return __kml_native.inspectWith(
        object,
        depth === null ? Infinity : depth,
        compact === false ? 0 : compact === true ? 3 : compact,
        inspectOption(options, 'sorted') ? 1 : 0,
        inspectOption(options, 'breakLength'),
        countOpt(inspectOption(options, 'maxArrayLength')),
    );
}

inspect.custom = Symbol.for('nodejs.util.inspect.custom');
inspect.defaultOptions = inspectDefaultOptions;
inspect.colors = {
    reset: [0, 0],
    bold: [1, 22],
    dim: [2, 22],
    italic: [3, 23],
    underline: [4, 24],
    blink: [5, 25],
    inverse: [7, 27],
    hidden: [8, 28],
    strikethrough: [9, 29],
    doubleunderline: [21, 24],
    black: [30, 39],
    red: [31, 39],
    green: [32, 39],
    yellow: [33, 39],
    blue: [34, 39],
    magenta: [35, 39],
    cyan: [36, 39],
    white: [37, 39],
    bgBlack: [40, 49],
    bgRed: [41, 49],
    bgGreen: [42, 49],
    bgYellow: [43, 49],
    bgBlue: [44, 49],
    bgMagenta: [45, 49],
    bgCyan: [46, 49],
    bgWhite: [47, 49],
    framed: [51, 54],
    overlined: [53, 55],
    gray: [90, 39],
    redBright: [91, 39],
    greenBright: [92, 39],
    yellowBright: [93, 39],
    blueBright: [94, 39],
    magentaBright: [95, 39],
    cyanBright: [96, 39],
    whiteBright: [97, 39],
    bgGray: [100, 49],
    bgRedBright: [101, 49],
    bgGreenBright: [102, 49],
    bgYellowBright: [103, 49],
    bgBlueBright: [104, 49],
    bgMagentaBright: [105, 49],
    bgCyanBright: [106, 49],
    bgWhiteBright: [107, 49],
} as { [name: string]: number[] };
inspect.styles = {
    special: 'cyan',
    number: 'yellow',
    bigint: 'yellow',
    boolean: 'yellow',
    undefined: 'grey',
    null: 'bold',
    string: 'green',
    symbol: 'green',
    date: 'magenta',
    regexp: 'red',
    module: 'underline',
} as { [name: string]: string };

// The aliases Node defines on inspect.colors.
const colorAliases: { [alias: string]: string } = {
    grey: 'gray',
    blackBright: 'gray',
    bgGrey: 'bgGray',
    bgBlackBright: 'bgGray',
    faint: 'dim',
    crossedout: 'strikethrough',
    strikeThrough: 'strikethrough',
    crossedOut: 'strikethrough',
    conceal: 'hidden',
    swapColors: 'inverse',
    swapcolors: 'inverse',
    doubleUnderline: 'doubleunderline',
};

export function colorCodes(name: string): number[] | undefined {
    const direct = inspect.colors[name];
    if (direct !== undefined) return direct;
    const alias = colorAliases[name];
    return alias === undefined ? undefined : inspect.colors[alias];
}

