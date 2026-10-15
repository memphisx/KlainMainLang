import re, sys, collections
# usage: py undefrefs.py <go test log>
# For each symbol a link reported undefined, the runtime objects (and
# sections) that referenced it, most frequent first.
log = open(sys.argv[1], encoding='utf-8', errors='replace').read().splitlines()
ref = re.compile(r'([a-z0-9_]+)-[0-9a-f]{20}\.o:[^(]*\(\.text\$([A-Za-z0-9_.]+)')
undef = re.compile(r"undefined reference to `([^']+)'")
by = collections.defaultdict(collections.Counter)
for i, l in enumerate(log):
    m = undef.search(l)
    if not m:
        continue
    src = ref.search(l) or (ref.search(log[i - 1]) if i else None)
    by[m.group(1)][(src.group(1), src.group(2)) if src else ('?', '?')] += 1
for sym, c in sorted(by.items(), key=lambda kv: -sum(kv[1].values())):
    print(sym, sum(c.values()))
    for (obj, fn), n in c.most_common(4):
        print('   %5d  %s.o  %s' % (n, obj, fn))
