import re, sys, glob, subprocess, os, tempfile
# run from the repo root; per-batch logs go to $KML_SCRATCH (or the temp dir)
# usage: py netbatch.py [batch-size] [batches]; runs the network-named E2E tests (batch.py's skip list, inverted)
size = int(sys.argv[1]) if len(sys.argv) > 1 else 60
only = [int(x) for x in sys.argv[2].split(',')] if len(sys.argv) > 2 else None
net = re.compile(r'HTTP|Http|Fetch|Net[A-Z]|WebSocket|Server|TLS|Tls|Dgram|Cluster|EventSource|SSE|Socket|XHR|XMLHttp|Dns|DNS')
names = []
for f in sorted(glob.glob('tests/*_test.go')):
    s = open(f, encoding='utf-8').read()
    for m in re.finditer(r'(?m)^func (Test\w+)\(', s):
        if net.search(m.group(1)):
            names.append(m.group(1))
print('selected', len(names), flush=True)
if only == [-1]:
    print('\n'.join(names))
    sys.exit(0)
bad = 0
for i in range(0, len(names), size):
    if only is not None and i // size not in only:
        continue
    chunk = names[i:i + size]
    rx = '^(' + '|'.join(chunk) + ')$'
    p = subprocess.run(['go', 'test', './tests/', '-run', rx, '-count=1', '-timeout', '30m', '-v'],
                       capture_output=True, text=True, encoding='utf-8', errors='replace')
    out = p.stdout + p.stderr
    logdir = os.environ.get('KML_SCRATCH') or tempfile.gettempdir()
    open(os.path.join(logdir, 'netbatch-%d.log' % (i // size)), 'w', encoding='utf-8').write(out)
    lines = out.splitlines()
    fails = [l for l in lines if l.lstrip().startswith('--- FAIL') or l.startswith('panic') or 'build failed' in l]
    skips = [l for l in lines if l.lstrip().startswith('--- SKIP')]
    passes = [l for l in lines if l.lstrip().startswith('--- PASS')]
    last = [l for l in lines if l.startswith('ok') or l.startswith('FAIL')]
    print('batch', i // size, 'rc', p.returncode, 'pass', len(passes), 'skip', len(skips), 'fail', len(fails),
          last[-1] if last else out[-300:], flush=True)
    for l in fails + skips:
        print('  ', l, flush=True)
    bad += len(fails)
print('TOTAL FAILS', bad, flush=True)
