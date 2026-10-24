// kml:global
// V8's Error.captureStackTrace: targetObject's `stack`, the header
// Error.prototype.toString gives it over the frames where it is called —
// those below constructorOpt's topmost frame when one is given.

export function __kml_Error_captureStackTrace(targetObject: object, constructorOpt?: Function): void { // kml:lower Error.captureStackTrace
    const t: any = targetObject;
    const frames = __kml_native.captureFrames(constructorOpt === undefined ? '' : String((constructorOpt as any).name));
    const name = t.name === undefined ? 'Error' : String(t.name);
    const message = t.message === undefined ? '' : String(t.message);
    let header = name;
    if (name === '') header = message;
    else if (message !== '') header = name + ': ' + message;
    t.stack = header + frames;
}
