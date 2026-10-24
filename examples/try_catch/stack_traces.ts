// An Error's stack: the program's own frames, named and placed as Node
// prints them — functions, methods, constructors, callbacks called as a
// timer's method, and the async functions awaiting the current one.

class Parser {
    parse(text: string): number {
        const n = Number(text);
        if (Number.isNaN(n)) throw new SyntaxError(`not a number: ${text}`);
        return n;
    }
}

function load(text: string): number {
    return new Parser().parse(text);
}

try {
    load("abc");
} catch (e: any) {
    console.log(e.stack);
}

async function fetchConfig(): Promise<string> {
    await null;
    return new Error("config missing").stack!;
}

async function start(): Promise<void> {
    console.log(await fetchConfig());
}

start();

setTimeout(function onTick() {
    console.trace("tick");
}, 1);
