// util.parseArgs: Node's lib/internal/util/parse_args/parse_args.js and
// utils.js in TypeScript. Arguments are turned into tokens (options with
// their values, positionals, the `--` terminator), checked in strict mode,
// and stored into a null-prototype `values` object; defaults fill the rest.

import { NodeTypeError } from './internal_errors';

export interface ParseArgsOptionConfig {
    type: 'string' | 'boolean';
    multiple?: boolean | undefined;
    short?: string | undefined;
    default?: string | boolean | string[] | boolean[] | undefined;
}

export interface ParseArgsConfig {
    args?: string[] | undefined;
    options?: { [longOption: string]: ParseArgsOptionConfig } | undefined;
    strict?: boolean | undefined;
    allowPositionals?: boolean | undefined;
    allowNegative?: boolean | undefined;
    tokens?: boolean | undefined;
}

function hasOwn(obj: any, prop: string): boolean {
    return Object.prototype.hasOwnProperty.call(obj, prop);
}

function objectGetOwn(obj: any, prop: string): any {
    if (hasOwn(obj, prop)) return obj[prop];
    return undefined;
}

function optionsGetOwn(options: any, longOption: string, prop: string): any {
    if (hasOwn(options, longOption)) return objectGetOwn(options[longOption], prop);
    return undefined;
}

function received(value: any): string {
    if (value === null) return 'Received null';
    if (value === undefined) return 'Received undefined';
    if (typeof value === 'function') return 'Received function ' + (value.name || '<anonymous>');
    if (typeof value === 'object') {
        const ctor = value.constructor;
        if (ctor && typeof ctor.name === 'string' && ctor.name !== '') return 'Received an instance of ' + ctor.name;
        return 'Received ' + String(value);
    }
    let shown = String(value);
    if (typeof value === 'string') {
        if (shown.length > 28) shown = shown.slice(0, 25) + '...';
        shown = "'" + shown + "'";
    }
    return 'Received type ' + typeof value + ' (' + shown + ')';
}

function invalidArgType(name: string, expected: string, value: any): NodeTypeError {
    const kind = name.indexOf('.') >= 0 ? 'property' : 'argument';
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" ' + kind + ' must be ' + expected + '. ' + received(value));
}

function validateArray(v: any, name: string): void {
    if (!Array.isArray(v)) throw invalidArgType(name, 'an instance of Array', v);
}

function validateBoolean(v: any, name: string): void {
    if (typeof v !== 'boolean') throw invalidArgType(name, 'of type boolean', v);
}

function validateString(v: any, name: string): void {
    if (typeof v !== 'string') throw invalidArgType(name, 'of type string', v);
}

function validateObject(v: any, name: string): void {
    if (v === null || typeof v !== 'object' || Array.isArray(v)) throw invalidArgType(name, 'of type object', v);
}

function validateStringArray(v: any, name: string): void {
    validateArray(v, name);
    for (let i = 0; i < v.length; i++) validateString(v[i], name + '[' + i + ']');
}

function validateBooleanArray(v: any, name: string): void {
    validateArray(v, name);
    for (let i = 0; i < v.length; i++) validateBoolean(v[i], name + '[' + i + ']');
}

function invalidOptionValue(message: string): NodeTypeError {
    return new NodeTypeError('ERR_PARSE_ARGS_INVALID_OPTION_VALUE', message);
}

function unknownOption(option: string, allowPositionals: boolean): NodeTypeError {
    const suggestDashDash = allowPositionals ? ". To specify a positional argument starting with a '-', place it at the end of the command after '--', as in '-- " + JSON.stringify(option) : '';
    return new NodeTypeError('ERR_PARSE_ARGS_UNKNOWN_OPTION', "Unknown option '" + option + "'" + suggestDashDash);
}

function isOptionValue(value: any): boolean {
    return value != null; // greedy: an option-argument may start with a dash
}

function isOptionLikeValue(value: any): boolean {
    if (value == null) return false;
    return value.length > 1 && value.charAt(0) === '-';
}

function isLoneShortOption(arg: string): boolean {
    return arg.length === 2 && arg.charAt(0) === '-' && arg.charAt(1) !== '-';
}

function isLoneLongOption(arg: string): boolean {
    return arg.length > 2 && arg.startsWith('--') && !arg.includes('=', 3);
}

function isLongOptionAndValue(arg: string): boolean {
    return arg.length > 2 && arg.startsWith('--') && arg.includes('=', 3);
}

function findLongOptionForShort(shortOption: string, options: any): string {
    validateObject(options, 'options');
    for (const longOption of Object.keys(options)) {
        if (objectGetOwn(options[longOption], 'short') === shortOption) return longOption;
    }
    return shortOption;
}

function isShortOptionGroup(arg: string, options: any): boolean {
    if (arg.length <= 2 || arg.charAt(0) !== '-' || arg.charAt(1) === '-') return false;
    const longOption = findLongOptionForShort(arg.charAt(1), options);
    return optionsGetOwn(options, longOption, 'type') !== 'string';
}

function isShortOptionAndValue(arg: string, options: any): boolean {
    validateObject(options, 'options');
    if (arg.length <= 2 || arg.charAt(0) !== '-' || arg.charAt(1) === '-') return false;
    const longOption = findLongOptionForShort(arg.charAt(1), options);
    return optionsGetOwn(options, longOption, 'type') === 'string';
}

// In strict mode, throw for possible usage errors like --foo --bar.
function checkOptionLikeValue(token: any): void {
    if (!token.inlineValue && isOptionLikeValue(token.value)) {
        const example = token.rawName.startsWith('--') ? "'" + token.rawName + "=-XYZ'" : "'--" + token.name + "=-XYZ' or '" + token.rawName + "-XYZ'";
        throw invalidOptionValue("Option '" + token.rawName + "' argument is ambiguous.\nDid you forget to specify the option argument for '" +
            token.rawName + "'?\nTo specify an option argument starting with a dash use " + example + '.');
    }
}

// In strict mode, throw for usage errors.
function checkOptionUsage(config: any, token: any): void {
    let tokenName: string = token.name;
    if (!hasOwn(config.options, tokenName)) {
        if (config.allowNegative && tokenName.startsWith('no-')) {
            tokenName = tokenName.slice(3);
            if (!hasOwn(config.options, tokenName) || optionsGetOwn(config.options, tokenName, 'type') !== 'boolean') {
                throw unknownOption(token.rawName, config.allowPositionals);
            }
        } else {
            throw unknownOption(token.rawName, config.allowPositionals);
        }
    }
    const short = optionsGetOwn(config.options, tokenName, 'short');
    const shortAndLong = (short ? '-' + short + ', ' : '') + '--' + tokenName;
    const type = optionsGetOwn(config.options, tokenName, 'type');
    if (type === 'string' && typeof token.value !== 'string') {
        throw invalidOptionValue("Option '" + shortAndLong + " <value>' argument missing");
    }
    if (type === 'boolean' && token.value != null) {
        throw invalidOptionValue("Option '" + shortAndLong + "' does not take an argument");
    }
}

function storeOption(token: any, options: any, values: any, allowNegative: boolean): void {
    let longOption: string = token.name;
    let optionValue: any = token.value;
    if (longOption === '__proto__') return;
    if (allowNegative && longOption.startsWith('no-') && optionValue === undefined) {
        // Boolean option negation: --no-foo
        longOption = longOption.slice(3);
        token.name = longOption;
        optionValue = false;
    }
    const newValue = optionValue ?? true;
    if (optionsGetOwn(options, longOption, 'multiple')) {
        if (values[longOption]) values[longOption].push(newValue);
        else values[longOption] = [newValue];
    } else {
        values[longOption] = newValue;
    }
}

function argsToTokens(args: string[], options: any): any[] {
    const tokens: any[] = [];
    let index = -1;
    let groupCount = 0;
    const remainingArgs: string[] = args.slice();
    while (remainingArgs.length > 0) {
        const arg = remainingArgs.shift() as string;
        const nextArg = remainingArgs[0];
        if (groupCount > 0) groupCount--;
        else index++;

        if (arg === '--') {
            // Everything after a bare '--' is a positional argument.
            tokens.push({ kind: 'option-terminator', index });
            for (const rest of remainingArgs) tokens.push({ kind: 'positional', index: ++index, value: rest });
            break;
        }

        if (isLoneShortOption(arg)) {
            const shortOption = arg.charAt(1);
            const longOption = findLongOptionForShort(shortOption, options);
            let value: any;
            let inlineValue: any;
            if (optionsGetOwn(options, longOption, 'type') === 'string' && isOptionValue(nextArg)) {
                value = remainingArgs.shift();
                inlineValue = false;
            }
            tokens.push({ kind: 'option', name: longOption, rawName: arg, index, value, inlineValue });
            if (value != null) ++index;
            continue;
        }

        if (isShortOptionGroup(arg, options)) {
            // Expand -fXzy to -f -X -z -y.
            const expanded: string[] = [];
            for (let i = 1; i < arg.length; i++) {
                const shortOption = arg.charAt(i);
                const longOption = findLongOptionForShort(shortOption, options);
                if (optionsGetOwn(options, longOption, 'type') !== 'string' || i === arg.length - 1) {
                    expanded.push('-' + shortOption);
                } else {
                    // A string option in the middle: -abfFILE is -a -b -fFILE.
                    expanded.push('-' + arg.slice(i));
                    break;
                }
            }
            remainingArgs.unshift(...expanded);
            groupCount = expanded.length;
            continue;
        }

        if (isShortOptionAndValue(arg, options)) {
            const shortOption = arg.charAt(1);
            const longOption = findLongOptionForShort(shortOption, options);
            tokens.push({ kind: 'option', name: longOption, rawName: '-' + shortOption, index, value: arg.slice(2), inlineValue: true });
            continue;
        }

        if (isLoneLongOption(arg)) {
            const longOption = arg.slice(2);
            let value: any;
            let inlineValue: any;
            if (optionsGetOwn(options, longOption, 'type') === 'string' && isOptionValue(nextArg)) {
                value = remainingArgs.shift();
                inlineValue = false;
            }
            tokens.push({ kind: 'option', name: longOption, rawName: arg, index, value, inlineValue });
            if (value != null) ++index;
            continue;
        }

        if (isLongOptionAndValue(arg)) {
            const equalIndex = arg.indexOf('=');
            const longOption = arg.slice(2, equalIndex);
            tokens.push({ kind: 'option', name: longOption, rawName: '--' + longOption, index, value: arg.slice(equalIndex + 1), inlineValue: true });
            continue;
        }

        tokens.push({ kind: 'positional', index, value: arg });
    }
    return tokens;
}

export function parseArgs(config?: ParseArgsConfig): { values: any; positionals: string[]; tokens?: any[] };
export function parseArgs(config: any = {}): any {
    const args = objectGetOwn(config, 'args') ?? process.argv.slice(2);
    const strict = objectGetOwn(config, 'strict') ?? true;
    const allowPositionals = objectGetOwn(config, 'allowPositionals') ?? !strict;
    const returnTokens = objectGetOwn(config, 'tokens') ?? false;
    const allowNegative = objectGetOwn(config, 'allowNegative') ?? false;
    const options = objectGetOwn(config, 'options') ?? Object.create(null);
    const parseConfig = { args, strict, options, allowPositionals, allowNegative };

    validateArray(args, 'args');
    validateBoolean(strict, 'strict');
    validateBoolean(allowPositionals, 'allowPositionals');
    validateBoolean(returnTokens, 'tokens');
    validateBoolean(allowNegative, 'allowNegative');
    validateObject(options, 'options');
    for (const longOption of Object.keys(options)) {
        const optionConfig = options[longOption];
        validateObject(optionConfig, 'options.' + longOption);
        const optionType = objectGetOwn(optionConfig, 'type');
        if (optionType !== 'string' && optionType !== 'boolean') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "options.' + longOption + ".type\" property must be ('string|boolean'). " + received(optionType));
        }
        if (hasOwn(optionConfig, 'short')) {
            const shortOption = optionConfig.short;
            validateString(shortOption, 'options.' + longOption + '.short');
            if (shortOption.length !== 1) {
                throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The property 'options." + longOption + ".short' must be a single character. Received '" + shortOption + "'");
            }
        }
        const multipleOption = objectGetOwn(optionConfig, 'multiple');
        if (hasOwn(optionConfig, 'multiple')) validateBoolean(multipleOption, 'options.' + longOption + '.multiple');
        const defaultValue = objectGetOwn(optionConfig, 'default');
        if (defaultValue !== undefined) {
            const name = 'options.' + longOption + '.default';
            if (optionType === 'string') {
                if (multipleOption) validateStringArray(defaultValue, name);
                else validateString(defaultValue, name);
            } else if (multipleOption) {
                validateBooleanArray(defaultValue, name);
            } else {
                validateBoolean(defaultValue, name);
            }
        }
    }

    // Phase 1: identify tokens.
    const tokens = argsToTokens(args, options);

    // Phase 2: process tokens into option values and positionals.
    const values: any = Object.create(null);
    const positionals: string[] = [];
    for (const token of tokens) {
        if (token.kind === 'option') {
            if (strict) {
                checkOptionUsage(parseConfig, token);
                checkOptionLikeValue(token);
            }
            storeOption(token, options, values, allowNegative);
        } else if (token.kind === 'positional') {
            if (!allowPositionals) {
                throw new NodeTypeError('ERR_PARSE_ARGS_UNEXPECTED_POSITIONAL', "Unexpected argument '" + token.value + "'. This command does not take positional arguments");
            }
            positionals.push(token.value);
        }
    }

    // Phase 3: fill in default values for missing options.
    for (const longOption of Object.keys(options)) {
        const optionConfig = options[longOption];
        const def = objectGetOwn(optionConfig, 'default');
        if (def !== undefined && values[longOption] === undefined && longOption !== '__proto__') values[longOption] = def;
    }

    const result: any = { values, positionals };
    if (returnTokens) result.tokens = tokens;
    return result;
}
