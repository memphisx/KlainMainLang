import re, sys, glob, subprocess, os
# usage: py batch.py <selector> [batch-size] [only-batches]
#   selector = a regex matched against each test's BODY (source text), or
#              "file:<a>,<b>" to take every test in tests/<a>_test.go, tests/<b>_test.go
#              (the test files ARE the area map — one file per feature area)
# Runs the selected E2E tests in batches, prints failures; network-named tests are
# skipped here (see netbatch.py). Run from the repo root.
sel = sys.argv[1]
size = int(sys.argv[2]) if len(sys.argv) > 2 else 300
only = [int(x) for x in sys.argv[3].split(',')] if len(sys.argv) > 3 else None
names = []
if sel.startswith('file:'):
    files = ['tests/%s_test.go' % a.strip() for a in sel[5:].split(',')]
    missing = [f for f in files if not os.path.exists(f)]
    if missing:
        sys.exit('no such test file(s): ' + ', '.join(missing))
    needle = None
else:
    files = sorted(glob.glob('tests/*_test.go'))
    needle = re.compile(sel)
for f in files:
    s = open(f, encoding='utf-8').read()
    parts = re.split(r'(?m)^func (Test\w+)\(', s)
    for i in range(1, len(parts), 2):
        if needle is None or needle.search(parts[i + 1]):
            names.append(parts[i])
skip = re.compile(r'HTTP|Http|Fetch|Net[A-Z]|WebSocket|Server|TLS|Tls|Dgram|Cluster|EventSource|SSE|Socket|XHR|XMLHttp|Dns|DNS')
skipped = [n for n in names if skip.search(n)]
names = [n for n in names if not skip.search(n)]
print('selected', len(names), 'skipped-network', len(skipped), flush=True)
bad = 0
for i in range(0, len(names), size):
    if only is not None and i // size not in only:
        continue
    chunk = names[i:i + size]
    rx = '^(' + '|'.join(chunk) + ')$'
    p = subprocess.run(['go', 'test', './tests/', '-run', rx, '-count=1', '-timeout', '20m'],
                       capture_output=True, text=True, encoding='utf-8', errors='replace')
    out = p.stdout + p.stderr
    fails = [l for l in out.splitlines() if l.startswith('--- FAIL') or l.startswith('panic') or 'build failed' in l]
    last = [l for l in out.splitlines() if l.startswith('ok') or l.startswith('FAIL')]
    print('batch', i // size, 'rc', p.returncode, last[-1] if last else out[-300:], flush=True)
    for l in fails:
        print('  ', l, flush=True)
    bad += len(fails)
print('TOTAL FAILS', bad, flush=True)
