import os, sys, subprocess, glob, time, urllib.request, argparse
# usage: py examples.py [--mm auto|manual] [--optmem] [--out DIR] [--cli PATH] [--only REGEX]
# Windows twin of `make examples`: compiles and runs every example with the same
# selection and per-file flags as the Makefile recipe, with httpbin-lite serving
# the fetch/http examples. Binaries go under --out (default $KML_SCRATCH/examples)
# instead of next to the sources; stdout of each run is kept there as <name>.out
# so two modes can be diffed. Run from the repo root.
ap = argparse.ArgumentParser()
ap.add_argument('--mm')
ap.add_argument('--optmem', action='store_true')
ap.add_argument('--out')
ap.add_argument('--cli', default=os.path.join(os.environ.get('KML_SCRATCH', '.'), 'klainmain.exe'))
ap.add_argument('--only')
ap.add_argument('--timeout', type=int, default=120)
a = ap.parse_args()
mode = (a.mm or 'default') + ('-optmem' if a.optmem else '')
out = a.out or os.path.join(os.environ.get('KML_SCRATCH', '.'), 'examples', mode)
os.makedirs(out, exist_ok=True)

def listing():
    items = []
    for f in sorted(glob.glob('examples/**/*.ts', recursive=True)):
        f = f.replace('\\', '/')
        if f.endswith('_worker.ts') or f.startswith('examples/tls/') or f.startswith('examples/webview/') \
                or os.path.basename(f) == 'standard_decorators.ts':
            continue
        items.append((f, []))
    for f in sorted(glob.glob('examples/jsmode/*.js')):
        items.append((f.replace('\\', '/'), ['-compat=js']))
    if os.path.exists('examples/decorators/standard_decorators.ts'):
        items.append(('examples/decorators/standard_decorators.ts', ['-decorators=standard']))
    return items

def modeflags(f):
    fl = []
    if a.mm and f not in ('examples/memory/memory_free.ts', 'examples/finalization/finalization_registry.ts'):
        fl.append('-mm=' + a.mm)
    if a.optmem:
        fl.append('-optimize-memory')
    return fl

fx = os.path.join(out, 'httpbin-lite.exe')
subprocess.run([a.cli, '-o', fx, 'tools/httpbin-lite/httpbin.ts'], capture_output=True)
env = dict(os.environ, HTTPBIN_LITE_PORT='8765')
fixture = subprocess.Popen([fx], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
for _ in range(50):
    try:
        urllib.request.urlopen('http://127.0.0.1:8765/get', timeout=1); break
    except Exception:
        time.sleep(0.1)
import re
only = re.compile(a.only) if a.only else None
ok = fail = 0
fails = []
try:
    for f, extra in listing():
        if only and not only.search(f):
            continue
        name = f[len('examples/'):].rsplit('.', 1)[0].replace('/', '__')
        exe = os.path.join(out, name + '.exe')
        c = subprocess.run([a.cli] + modeflags(f) + extra + ['-o', exe, f], capture_output=True,
                           text=True, encoding='utf-8', errors='replace')
        if c.returncode != 0:
            fail += 1; fails.append((f, 'COMPILE', c.stderr.strip().splitlines()[-3:])); print('FAIL compile', f, flush=True); continue
        try:
            r = subprocess.run([exe], stdin=subprocess.DEVNULL, capture_output=True, timeout=a.timeout)
        except subprocess.TimeoutExpired:
            fail += 1; fails.append((f, 'TIMEOUT', [])); print('FAIL timeout', f, flush=True); continue
        open(os.path.join(out, name + '.out'), 'wb').write(r.stdout)
        if r.returncode != 0:
            fail += 1
            fails.append((f, 'EXIT %d' % r.returncode, r.stderr.decode('utf-8', 'replace').strip().splitlines()[-3:]))
            print('FAIL exit', r.returncode, f, flush=True)
        else:
            ok += 1
finally:
    fixture.kill()
print('\n[%s] Results: %d passed, %d failed' % (mode, ok, fail))
for f, why, tail in fails:
    print('  %-60s %s' % (f, why))
    for l in tail:
        print('      ' + l)
sys.exit(1 if fail else 0)
