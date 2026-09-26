#!/usr/bin/env python3
"""irdiff.py <binary> <outfile> — compile every corpus file to IR (-emit-llvm),
record sha1 of (exit status, stdout, stderr) per file. Diff two outfiles to
prove a front-end change is behaviour-neutral."""
import hashlib, os, subprocess, sys, json
from concurrent.futures import ThreadPoolExecutor

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
binary, outfile = sys.argv[1], sys.argv[2]
files = []
for base in ["examples", "tools", "apps"]:
    for dp, _, fs in os.walk(os.path.join(ROOT, base)):
        if "node_modules" in dp:
            continue
        files += [os.path.join(dp, f) for f in fs if f.endswith((".ts", ".js"))]
t262 = os.path.join(ROOT, ".test262/test/language")
for dp, _, fs in os.walk(t262):
    files += [os.path.join(dp, f) for f in fs if f.endswith(".js") and "_FIXTURE" not in f]
files.sort()
# Every .js file is compiled in both emitter lanes: the js lane under its own
# path, the strict lane (the default) as "<path>#strict". The two lanes emit
# different IR, and conformance measures both.
jobs = [(f, "js" if f.endswith(".js") else None) for f in files]
jobs += [(f, "strict") for f in files if f.endswith(".js")]

def one(job):
    f, lane = job
    args = [binary, "-emit-llvm"]
    if lane:
        args.append("-compat=" + lane)
    try:
        r = subprocess.run(args + [f], capture_output=True, timeout=60, cwd=os.path.dirname(f))
        blob = b"%d\0" % r.returncode + r.stdout + b"\0" + r.stderr
    except subprocess.TimeoutExpired:
        blob = b"TIMEOUT"
    key = f[len(ROOT) + 1:] + ("#strict" if lane == "strict" else "")
    return key, hashlib.sha1(blob).hexdigest(), (blob[:400].decode("utf8", "replace") if not blob.startswith(b"0\0") else "")

with ThreadPoolExecutor(max_workers=os.cpu_count()) as ex:
    res = list(ex.map(one, jobs))
json.dump({f: [h, e] for f, h, e in res}, open(outfile, "w"))
print(len(res), "files;", sum(1 for _, _, e in res if e), "non-zero exits")
