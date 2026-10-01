// Node's lib/internal/tty.js: the colour depth of the environment's
// terminal.

import { release } from 'os';

const COLORS_2 = 1;
const COLORS_16 = 4;
const COLORS_256 = 8;
const COLORS_16m = 24;

const TERM_ENVS: { [term: string]: number } = {
    'eterm': COLORS_16,
    'cons25': COLORS_16,
    'console': COLORS_16,
    'cygwin': COLORS_16,
    'dtterm': COLORS_16,
    'gnome': COLORS_16,
    'hurd': COLORS_16,
    'jfbterm': COLORS_16,
    'konsole': COLORS_16,
    'kterm': COLORS_16,
    'mlterm': COLORS_16,
    'mosh': COLORS_16m,
    'putty': COLORS_16,
    'st': COLORS_16,
    'rxvt-unicode-24bit': COLORS_16m,
    'terminator': COLORS_16m,
    'xterm-kitty': COLORS_16m,
};

const CI_ENVS: [string, number][] = [
    ['APPVEYOR', COLORS_256],
    ['BUILDKITE', COLORS_256],
    ['CIRCLECI', COLORS_16m],
    ['DRONE', COLORS_256],
    ['GITEA_ACTIONS', COLORS_16m],
    ['GITHUB_ACTIONS', COLORS_16m],
    ['GITLAB_CI', COLORS_256],
    ['TRAVIS', COLORS_256],
];

const TERM_ENVS_REG_EXP: RegExp[] = [
    /ansi/,
    /color/,
    /linux/,
    /direct/,
    /^con[0-9]*x[0-9]/,
    /^rxvt/,
    /^screen/,
    /^xterm/,
    /^vt100/,
    /^vt220/,
];

let warned = false;
function warnOnDeactivatedColors(env: any): void {
    if (warned) return;
    let name = '';
    if (env.NODE_DISABLE_COLORS !== undefined) name = 'NODE_DISABLE_COLORS';
    if (env.NO_COLOR !== undefined) {
        if (name !== '') {
            name += "' and '";
        }
        name += 'NO_COLOR';
    }
    if (name !== '') {
        process.emitWarning("The '" + name + "' env is ignored due to the 'FORCE_COLOR' env being set.", 'Warning');
        warned = true;
    }
}

let osRelease: string[] | undefined = undefined;

function has(env: any, name: string): boolean {
    return env[name] !== undefined;
}

// The colour depth the environment's terminal supports, in bits.
export function getColorDepth(envArg?: object): number {
    const env: any = envArg === undefined ? process.env : envArg;
    // Use level 0-3 to support the same levels as `chalk` does.
    if (env.FORCE_COLOR !== undefined) {
        switch (env.FORCE_COLOR) {
            case '':
            case '1':
            case 'true':
                warnOnDeactivatedColors(env);
                return COLORS_16;
            case '2':
                warnOnDeactivatedColors(env);
                return COLORS_256;
            case '3':
                warnOnDeactivatedColors(env);
                return COLORS_16m;
            default:
                return COLORS_2;
        }
    }
    if (env.NODE_DISABLE_COLORS !== undefined || env.NO_COLOR !== undefined || env.TERM === 'dumb') {
        return COLORS_2;
    }
    if (process.platform === 'win32') {
        if (osRelease === undefined) osRelease = release().split('.');
        // Windows 10 build 10586 is the first release that supports 256
        // colors, build 14931 the first that supports 16m/TrueColor.
        if (+osRelease[0] >= 10) {
            const build = +osRelease[2];
            if (build >= 14931) return COLORS_16m;
            if (build >= 10586) return COLORS_256;
        }
        return COLORS_16;
    }
    if (env.TMUX) return COLORS_16m;
    // Azure DevOps
    if (has(env, 'TF_BUILD') && has(env, 'AGENT_NAME')) return COLORS_16;
    if (has(env, 'CI')) {
        for (const [envName, colors] of CI_ENVS) {
            if (has(env, envName)) return colors;
        }
        if (env.CI_NAME === 'codeship') return COLORS_256;
        return COLORS_2;
    }
    if (has(env, 'TEAMCITY_VERSION')) {
        return /^(9\.(0*[1-9]\d*)\.|\d{2,}\.)/.exec(env.TEAMCITY_VERSION) !== null ? COLORS_16 : COLORS_2;
    }
    switch (env.TERM_PROGRAM) {
        case 'iTerm.app':
            if (!env.TERM_PROGRAM_VERSION || /^[0-2]\./.exec(env.TERM_PROGRAM_VERSION) !== null) return COLORS_256;
            return COLORS_16m;
        case 'HyperTerm':
        case 'MacTerm':
            return COLORS_16m;
        case 'Apple_Terminal':
            return COLORS_256;
    }
    if (env.COLORTERM === 'truecolor' || env.COLORTERM === '24bit') return COLORS_16m;
    if (env.TERM) {
        const term: string = env.TERM;
        if (/truecolor/.exec(term) !== null) return COLORS_16m;
        if (/^xterm-256/.exec(term) !== null) return COLORS_256;
        const termEnv = term.toLowerCase();
        const known = TERM_ENVS[termEnv];
        if (known !== undefined) return known;
        for (const re of TERM_ENVS_REG_EXP) {
            if (re.exec(termEnv) !== null) return COLORS_16;
        }
    }
    // Move 16 color COLORTERM below 16m and 256
    if (env.COLORTERM) return COLORS_16;
    return COLORS_2;
}
