import re, sys, glob
# usage: py sel.py <needle-regex> [max]  -> prints a -run regex of E2E tests whose body matches
needle = re.compile(sys.argv[1])
names = []
for f in glob.glob('tests/*_test.go'):
    s = open(f, encoding='utf-8').read()
    parts = re.split(r'(?m)^func (Test\w+)\(', s)
    for i in range(1, len(parts), 2):
        if needle.search(parts[i + 1]):
            names.append(parts[i])
if len(sys.argv) > 2 and sys.argv[2] == 'count':
    print(len(names))
else:
    print('^(' + '|'.join(names) + ')$')
