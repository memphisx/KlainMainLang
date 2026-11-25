// globalThis.structuredClone as a value: its implementation reaches every
// class it can clone, so it is in only the programs that can read it.

import { NodeTypeError } from './internal_errors';
import { structuredCloneOptions } from './internal_structured_clone';

function globalThisClone(value: any): any {
    return structuredClone(value);
}

export function _kmlGlobalStructuredClone(...args: any[]): any {
    if (args.length === 0) {
        // Node's own (doubled) wording.
        throw NodeTypeError('ERR_MISSING_ARGS', 'The "The value argument must be specified" argument must be specified');
    }
    structuredCloneOptions(args[1]);
    return globalThisClone(args[0]);
}
