// klain:tui — a terminal UI over Yoga's flexbox layout and a double-buffered
// ANSI diff painter (tui.c). Each builder makes a node in the painter's
// retained tree; render(root) lays the tree out, paints only the cells that
// changed since the last frame and frees the tree, so a program builds a
// fresh tree per frame from its state:
//
//	enter()
//	render(Box({ border: 'round', padding: 1 }, [Text('hello', { bold: true })]))
//	leave()
//
// Input is klain:tty's readKey; resize is process's 'SIGWINCH'.

export type Color =
    | 'black' | 'red' | 'green' | 'yellow' | 'blue' | 'magenta' | 'cyan' | 'white' | 'default'
    | 'gray' | 'grey' | 'brightblack' | 'brightred' | 'brightgreen' | 'brightyellow'
    | 'brightblue' | 'brightmagenta' | 'brightcyan' | 'brightwhite';

export type Justify = 'flex-start' | 'center' | 'flex-end' | 'space-between' | 'space-around' | 'space-evenly';
export type Align = 'auto' | 'flex-start' | 'center' | 'flex-end' | 'stretch' | 'baseline';
export type BorderStyle = 'none' | 'single' | 'round' | 'double';

export interface Props {
    width?: number;
    height?: number;
    minWidth?: number;
    minHeight?: number;
    flexDirection?: 'row' | 'column';
    flexGrow?: number;
    flexShrink?: number;
    flexBasis?: number;
    justifyContent?: Justify;
    alignItems?: Align;
    alignSelf?: Align;
    flexWrap?: 'nowrap' | 'wrap';
    padding?: number;
    paddingX?: number;
    paddingY?: number;
    paddingTop?: number;
    paddingRight?: number;
    paddingBottom?: number;
    paddingLeft?: number;
    margin?: number;
    marginX?: number;
    marginY?: number;
    marginTop?: number;
    marginRight?: number;
    marginBottom?: number;
    marginLeft?: number;
    gap?: number;
    color?: Color;
    backgroundColor?: Color;
    borderColor?: Color;
    border?: boolean | BorderStyle;
    bold?: boolean;
    dim?: boolean;
    underline?: boolean;
    inverse?: boolean;
    // Text and TextInput: whether the text wraps (Text's default) or is
    // clipped to one line (TextInput's).
    wrap?: boolean;
    // Spinner: the caption after the glyph.
    label?: string;
    // List: the highlighted item.
    selected?: number;
}

// A node of the painter's tree, valid until the render that frees it.
export class Node {
    readonly handle: number;
    constructor(handle: number) {
        this.handle = handle;
    }
}

// The node kinds (tui.c's K_*).
const BOX = 0;
const TEXT = 1;
const LIST = 2;
const SPINNER = 3;
const PROGRESS = 4;
const INPUT = 5;

// The SGR foreground code of a colour; a background's is 10 more.
function colorCode(c: Color): number {
    switch (c) {
        case 'black': return 30;
        case 'red': return 31;
        case 'green': return 32;
        case 'yellow': return 33;
        case 'blue': return 34;
        case 'magenta': return 35;
        case 'cyan': return 36;
        case 'white': return 37;
        case 'gray':
        case 'grey':
        case 'brightblack': return 90;
        case 'brightred': return 91;
        case 'brightgreen': return 92;
        case 'brightyellow': return 93;
        case 'brightblue': return 94;
        case 'brightmagenta': return 95;
        case 'brightcyan': return 96;
        case 'brightwhite': return 97;
    }
    return -1;
}

function justifyCode(j: Justify): number {
    switch (j) {
        case 'flex-start': return 1;
        case 'center': return 2;
        case 'flex-end': return 3;
        case 'space-between': return 4;
        case 'space-around': return 5;
        case 'space-evenly': return 6;
    }
    return 0;
}

function alignCode(a: Align): number {
    switch (a) {
        case 'auto': return 0;
        case 'flex-start': return 1;
        case 'center': return 2;
        case 'flex-end': return 3;
        case 'stretch': return 4;
        case 'baseline': return 5;
    }
    return 0;
}

function borderCode(b: boolean | BorderStyle): number {
    switch (b) {
        case true:
        case 'single': return 1;
        case 'round': return 2;
        case 'double': return 3;
    }
    return 0;
}

// A dimension, NaN when unset (Yoga's default stays).
function dim(v: number | undefined): number {
    return v === undefined ? NaN : v;
}

// A padding or margin side: its own prop, else its axis's, else the
// group's, else unset.
function edge(side: number | undefined, axis: number | undefined, all: number | undefined): number {
    if (side !== undefined) return side;
    if (axis !== undefined) return axis;
    return dim(all);
}

function make(kind: number): number {
    return __kml_native.tuiNode(kind);
}

function applyProps(h: number, p: Props): void {
    if (p.width !== undefined || p.height !== undefined) {
        __kml_native.tuiSize(h, dim(p.width), dim(p.height));
    }
    if (p.minWidth !== undefined || p.minHeight !== undefined) {
        __kml_native.tuiMin(h, dim(p.minWidth), dim(p.minHeight));
    }
    if (p.flexDirection !== undefined || p.flexGrow !== undefined || p.flexShrink !== undefined || p.flexBasis !== undefined) {
        const dir = p.flexDirection === undefined ? -1 : p.flexDirection === 'row' ? 0 : 1;
        __kml_native.tuiFlex(h, dir, dim(p.flexGrow), dim(p.flexShrink), dim(p.flexBasis));
    }
    if (p.justifyContent !== undefined) __kml_native.tuiJustify(h, justifyCode(p.justifyContent));
    if (p.alignItems !== undefined) __kml_native.tuiAlign(h, alignCode(p.alignItems));
    if (p.alignSelf !== undefined) __kml_native.tuiSelf(h, alignCode(p.alignSelf));
    if (p.flexWrap !== undefined) __kml_native.tuiWrap(h, p.flexWrap === 'wrap' ? 1 : 0);
    if (p.padding !== undefined || p.paddingX !== undefined || p.paddingY !== undefined ||
        p.paddingTop !== undefined || p.paddingRight !== undefined || p.paddingBottom !== undefined || p.paddingLeft !== undefined) {
        __kml_native.tuiPadding(h,
            edge(p.paddingTop, p.paddingY, p.padding),
            edge(p.paddingRight, p.paddingX, p.padding),
            edge(p.paddingBottom, p.paddingY, p.padding),
            edge(p.paddingLeft, p.paddingX, p.padding));
    }
    if (p.margin !== undefined || p.marginX !== undefined || p.marginY !== undefined ||
        p.marginTop !== undefined || p.marginRight !== undefined || p.marginBottom !== undefined || p.marginLeft !== undefined) {
        __kml_native.tuiMargin(h,
            edge(p.marginTop, p.marginY, p.margin),
            edge(p.marginRight, p.marginX, p.margin),
            edge(p.marginBottom, p.marginY, p.margin),
            edge(p.marginLeft, p.marginX, p.margin));
    }
    if (p.gap !== undefined) __kml_native.tuiGap(h, p.gap);
    if (p.color !== undefined || p.backgroundColor !== undefined) {
        const fg = p.color === undefined ? -1 : colorCode(p.color);
        const bg = p.backgroundColor === undefined ? -1 : colorCode(p.backgroundColor);
        __kml_native.tuiColors(h, fg, bg < 0 ? -1 : bg + 10);
    }
    if (p.border !== undefined) {
        __kml_native.tuiBorder(h, borderCode(p.border), p.borderColor === undefined ? -1 : colorCode(p.borderColor));
    }
    let attr = 0;
    if (p.bold === true) attr |= 1;
    if (p.dim === true) attr |= 2;
    if (p.underline === true) attr |= 4;
    if (p.inverse === true) attr |= 8;
    if (attr !== 0) __kml_native.tuiAttr(h, attr);
}

// A flex container holding children in order.
export function Box(props?: Props, children?: Node[]): Node {
    const h = make(BOX);
    if (props !== undefined) applyProps(h, props);
    if (children !== undefined) {
        for (const c of children) __kml_native.tuiInsert(h, c.handle);
    }
    return new Node(h);
}

function textNode(kind: number, text: string, wrapByDefault: boolean, props?: Props): Node {
    const h = make(kind);
    let wrap = wrapByDefault;
    if (props !== undefined && props.wrap !== undefined) wrap = props.wrap;
    __kml_native.tuiText(h, text, wrap ? 1 : 0);
    if (props !== undefined) applyProps(h, props);
    return new Node(h);
}

// Text, wrapped to its box unless props.wrap is false.
export function Text(text: string, props?: Props): Node {
    return textNode(TEXT, text, true, props);
}

// A one-line input field showing text, clipped unless props.wrap is true.
export function TextInput(text: string, props?: Props): Node {
    return textNode(INPUT, text, false, props);
}

// A scrolling list keeping props.selected in view.
export function List(items: string[], props?: Props): Node {
    const h = make(LIST);
    for (const item of items) __kml_native.tuiAddItem(h, item);
    if (props !== undefined) {
        if (props.selected !== undefined) __kml_native.tuiSelected(h, props.selected);
        applyProps(h, props);
    }
    return new Node(h);
}

// A spinner showing its frame-th glyph, then props.label.
export function Spinner(frame: number, props?: Props): Node {
    const h = make(SPINNER);
    __kml_native.tuiSpinner(h, frame);
    if (props !== undefined) {
        if (props.label !== undefined) __kml_native.tuiText(h, props.label, 0);
        applyProps(h, props);
    }
    return new Node(h);
}

// A progress bar filled to value (0 to 1).
export function Progress(value: number, props?: Props): Node {
    const h = make(PROGRESS);
    __kml_native.tuiProgress(h, value);
    if (props !== undefined) applyProps(h, props);
    return new Node(h);
}

// Lays out and paints root, then frees its tree.
export function render(root: Node): void {
    __kml_native.tuiRender(root.handle);
}

// Switches to the alternate screen and hides the cursor.
export function enter(): void {
    __kml_native.tuiEnter();
}

// Restores the main screen and the cursor.
export function leave(): void {
    __kml_native.tuiLeave();
}
