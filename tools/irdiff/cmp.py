#!/usr/bin/env python3
"""cmp.py base.json new.json — list files whose compile outcome changed, grouped."""
import json, sys, collections, re
a, b = json.load(open(sys.argv[1])), json.load(open(sys.argv[2]))
def msg(e):
    if not e:
        return "OK"
    parts = e.split("\x00")
    txt = parts[2] if len(parts) > 2 else e
    txt = re.sub(r"/Users/\S+?: ", "", txt)
    return re.sub(r"\d+:\d+", "L:C", txt.strip().splitlines()[0] if txt.strip() else txt)[:110]
groups = collections.Counter(); ex = {}
changed = 0
for f in sorted(a):
    if f not in b or a[f][0] == b[f][0]:
        continue
    changed += 1
    k = (msg(a[f][1]), msg(b[f][1]))
    groups[k] += 1
    ex.setdefault(k, f)
print(changed, "changed of", len(a))
for (x, y), n in groups.most_common(40):
    print(f"{n:5}  {x}\n       -> {y}\n       e.g. {ex[(x, y)]}")
