"""One-time dependency preparation on a disposable Linux runner."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import urllib.request
import concurrent.futures
import sys

ROOT = Path.cwd()
os.environ['GOFLAGS'] = '-p=1'


def run(*args):
    return subprocess.check_output(args, text=True)


if '--finish' in sys.argv:
    print(run('go', 'get', '-u', '-t', './apps/node-go/...'))
    print(run('go', 'mod', 'tidy'))
    raise SystemExit(0)

requirements = json.loads(run('go', 'mod', 'edit', '-json'))['Require']
def latest(item):
    name = item['Path']
    escaped = ''.join('!' + c.lower() if c.isupper() else c for c in name)
    with urllib.request.urlopen('https://proxy.golang.org/' + escaped + '/@latest', timeout=30) as response:
        return name, json.load(response)['Version']
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
    for name, version in pool.map(latest, requirements):
        print(run('go', 'mod', 'edit', '-require=' + name + '@' + version))

vendor = ROOT / 'apps/node-go/third_party/goja'
metadata = json.loads(run('go', 'mod', 'download', '-json', 'github.com/dop251/goja@latest'))
if metadata.get('Error'):
    raise RuntimeError(metadata['Error'])
upstream = Path(metadata['Dir'])
record = json.loads((vendor / 'CANARY_PATCH.json').read_text())
files = {p.relative_to(upstream).as_posix(): p.read_bytes()
         for p in upstream.rglob('*') if p.is_file()
         and ((p.suffix == '.go' and not p.name.endswith('_test.go')
               and 'testdata' not in p.parts)
              or p.relative_to(upstream).as_posix() in ('go.mod', 'go.sum', 'LICENSE'))}
for name in record['upstreamFiles'].keys() - files.keys():
    (vendor / name).unlink()
for name, data in files.items():
    target = vendor / name
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(data)
old_version = record['version']
old_count = len(record['upstreamFiles'])
with urllib.request.urlopen('https://api.github.com/repos/dop251/goja/commits/' + metadata['Version'].rsplit('-', 1)[1], timeout=30) as response:
    commit = json.load(response)['sha']
assert commit.startswith(metadata['Version'].rsplit('-', 1)[1])
record.update(version=metadata['Version'], commit=commit,
              moduleChecksum=metadata['Sum'],
              upstreamFiles={name: hashlib.sha256(data).hexdigest() for name, data in sorted(files.items())})
(vendor / 'CANARY_PATCH.json').write_text(json.dumps(record, indent=2) + '\n')
notice = vendor / 'NOTICE.txt'
notice.write_bytes(notice.read_bytes().replace(old_version.encode(), record['version'].encode())
                   .replace(str(old_count).encode() + b' upstream', str(len(files)).encode() + b' upstream'))
verifier = ROOT / 'scripts/verify-jsonquery-runtime.py'
verifier.write_bytes(verifier.read_bytes()
                     .replace(f']) != {old_count}'.encode(), f']) != {len(files)}'.encode())
                     .replace(f'{old_count + 1} pinned Goja'.encode(), f'{len(files) + 1} pinned Goja'.encode()))
print('Updated Goja', record['version'], len(files), 'upstream files; helper retained')
