import os, sys, shutil, subprocess, tempfile, argparse
from concurrent.futures import ThreadPoolExecutor
# Compile every .ts under a source tree to LLVM IR with two compilers (or one compiler under two
# environments) and report the files whose IR differs — the check for a change that must not alter codegen.
# usage (repo root): py tools/testsel/irdiff.py --a <klainmain A> [--b <klainmain B>] [--env-b NAME=VAL]
#                       [--src examples] [--jobs 8]
# Work happens in $KML_SCRATCH/irdiff (or the temp dir); the source tree is copied there twice.
ap = argparse.ArgumentParser()
ap.add_argument('--a', required=True)
ap.add_argument('--b')
ap.add_argument('--env-b', action='append', default=[])
ap.add_argument('--src', default='examples')
ap.add_argument('--jobs', type=int, default=8)
ap.add_argument('--timeout', type=int, default=180)
args = ap.parse_args()
comp = {'a': os.path.abspath(args.a), 'b': os.path.abspath(args.b or args.a)}
envs = {'a': dict(os.environ), 'b': dict(os.environ)}
for kv in args.env_b:
    k, _, v = kv.partition('=')
    envs['b'][k] = v
root = os.path.join(os.environ.get('KML_SCRATCH') or tempfile.gettempdir(), 'irdiff')
shutil.rmtree(root, ignore_errors=True)
for side in 'ab':
    shutil.copytree(args.src, os.path.join(root, side), ignore=shutil.ignore_patterns('node_modules', '*.exe', '*.ll'))
files = []
for d, _, fs in os.walk(os.path.join(root, 'a')):
    for f in fs:
        if f.endswith('.ts') and not f.endswith('.d.ts'):
            files.append(os.path.relpath(os.path.join(d, f), os.path.join(root, 'a')))
files.sort()

def run(side, rel):
    path = os.path.join(root, side, rel)
    try:
        p = subprocess.run([comp[side], '-emit-llvm', os.path.basename(path)], cwd=os.path.dirname(path),
                           env=envs[side], capture_output=True, text=True, encoding='utf-8', errors='replace',
                           timeout=args.timeout)
        msg = 'rc=%d %s' % (p.returncode, (p.stderr or '').strip()[:300])
        ir = p.stdout if p.returncode == 0 and p.stdout else None  # -emit-llvm prints the IR on stdout
        if ir:  # a source path baked into the IR (import.meta.url, __filename) names the a/ or b/ copy
            for sep in ('\\5C', '/', '\\'):
                ir = ir.replace('irdiff' + sep + side + sep, 'irdiff' + sep + '_' + sep)
    except subprocess.TimeoutExpired:
        msg, ir = 'TIMEOUT', None
    return msg, ir

def one(rel):
    (ma, ia), (mb, ib) = run('a', rel), run('b', rel)
    if ia is None and ib is None:
        # both failed to compile: the diagnostics must agree too
        return rel, 'same-error' if ma.replace(os.sep + 'a' + os.sep, '') == mb.replace(os.sep + 'b' + os.sep, '') else 'ERR-DIFF\n   a: %s\n   b: %s' % (ma, mb)
    if ia is None or ib is None:
        return rel, 'ONE-SIDED\n   a: %s\n   b: %s' % (ma, mb)
    return rel, 'same' if ia == ib else 'IR-DIFF'

counts = {}
with ThreadPoolExecutor(args.jobs) as ex:
    for rel, res in ex.map(one, files):
        key = res.split('\n')[0]
        counts[key] = counts.get(key, 0) + 1
        if key not in ('same', 'same-error'):
            print(rel, res, flush=True)
print('files', len(files), counts)
