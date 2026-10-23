"""Stage a private Goja source extension inside bounded validation scratch.

This is a declared local module replacement, not a module-cache overlay. It
extracts only 81 pinned production/license/module files (about 1.4 MiB), adds
one source file, and leaves the cache, root modules and frozen R3 untouched.
Nothing in production imports or enables this experiment.
"""
import hashlib
import base64
import json
import os
from pathlib import Path
import zipfile

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / '.canary/jsonata-runtime-candidate/r3-intrinsic-20261022T223939Z/source'
OUT = ROOT / '.canary/work/current/outputs'
VERSION = 'v0.0.0-20261022125820-07c4b9d6265b'


def stage(numeric=False):
    if not (ROOT / '.canary/bounded-validation.lock').is_file():
        raise RuntimeError('Use bounded_validation.py')
    if Path(os.environ.get('GOTMPDIR', '')).parent != OUT.parent:
        raise RuntimeError('Missing guarded scratch')
    manifest = json.loads((SOURCE.parent / 'source-manifest-final.json').read_text())
    if any(hashlib.sha256((SOURCE / name).read_bytes()).hexdigest() != digest
           for name, digest in manifest.items()):
        raise RuntimeError('Frozen source drift')
    study = json.loads((ROOT / '.canary/jsonata-descriptor-study.json').read_text())
    for name, digest in [('nativeGuardSource', 'nativeGuardSha256'),
                         ('gojaExtensionSource', 'gojaExtensionSha256')]:
        if hashlib.sha256(study[name].encode()).hexdigest() != study[digest]:
            raise RuntimeError('Candidate source drift')
    archive = Path(os.environ['GOMODCACHE']) / 'cache/download/github.com/dop251/goja/@v' / (VERSION + '.zip')
    module = OUT / 'goja-descriptor-source'
    module.mkdir()
    prefix = 'github.com/dop251/goja@' + VERSION + '/'
    source_hashes = {}
    total = 0
    with zipfile.ZipFile(archive) as entries:
        content_hash = hashlib.sha256()
        for name in sorted(entries.namelist()):
            if not name.endswith('/'):
                content_hash.update((hashlib.sha256(entries.read(name)).hexdigest()
                                     + '  ' + name + '\n').encode())
        checksum = 'h1:' + base64.b64encode(content_hash.digest()).decode()
        expected = 'github.com/dop251/goja ' + VERSION + ' ' + checksum
        if expected not in (SOURCE / 'go.sum').read_text().splitlines():
            raise RuntimeError('Pinned Goja content does not match frozen go.sum')
        for item in entries.infolist():
            if not item.filename.startswith(prefix) or item.is_dir():
                continue
            name = item.filename[len(prefix):]
            path = Path(name)
            if path.is_absolute() or '..' in path.parts or ':' in name:
                raise RuntimeError('Invalid pinned archive path')
            if not ((name.endswith('.go') and not name.endswith('_test.go'))
                    or path.name in ('go.mod', 'go.sum', 'LICENSE')):
                continue
            total += item.file_size
            if total > 2 * 1024 * 1024:
                raise RuntimeError('Pinned production source exceeds reservation')
            data = entries.read(item)
            target = module / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
            source_hashes[name] = hashlib.sha256(data).hexdigest()
    if len(source_hashes) != 81 or total != 1466627:
        raise RuntimeError('Unexpected pinned Goja production inventory')
    (module / 'canary_own_data.go').write_text(study['gojaExtensionSource'], encoding='utf-8', newline='\n')
    guard = OUT / 'native_guard_candidate.go'
    guard.write_text(study['nativeGuardSource'], encoding='utf-8', newline='\n')
    overlay = OUT / 'descriptor-overlay.json'
    replacements = {
        str(SOURCE / 'apps/node-go/internal/jsonquery/jsvm/native_guard.go'): str(guard)
    }
    if numeric:
        numeric_replacements = stage_numeric(SOURCE, OUT)
        replacements.update(numeric_replacements)
    overlay.write_text(json.dumps({'Replace': replacements}), encoding='utf-8')
    modfile = OUT / 'descriptor.mod'
    modfile.write_text((SOURCE / 'go.mod').read_text() + '\nreplace github.com/dop251/goja => '
                       + json.dumps(module.as_posix()) + '\n', encoding='utf-8')
    modfile.with_suffix('.sum').write_bytes((SOURCE / 'go.sum').read_bytes())
    record = {'private': True, 'adopted': False, 'frozenOriginalFiles': len(manifest),
              'moduleReplacement': True, 'moduleCacheChanged': False,
              'gojaVersion': VERSION, 'gojaZipSHA256': hashlib.sha256(archive.read_bytes()).hexdigest(),
              'upstreamFiles': len(source_hashes), 'upstreamBytes': total,
              'upstreamSourceHashes': source_hashes,
              'extensionSHA256': study['gojaExtensionSha256'],
              'guardSHA256': study['nativeGuardSha256'], 'limitsChanged': False}
    record['numericScanner'] = numeric
    if numeric:
        record['numericSources'] = {str(Path(name).relative_to(SOURCE)):
                                   hashlib.sha256(Path(target).read_bytes()).hexdigest()
                                   for name, target in numeric_replacements.items()}
    return {'source': SOURCE, 'out': OUT, 'module': module, 'guard': guard,
            'modfile': modfile, 'overlay': overlay, 'record': record}


def stage_numeric(source, out):
    """Replace primitive scans and a redundant decimal prefix regex."""
    import subprocess
    package = source / 'apps/node-go/internal/jsonquery/jsvm'
    number_source = (ROOT / 'scripts/jsonata_numeric_candidate.go.txt').read_text()
    numeric_go = out / 'numeric_string_candidate.go'
    numeric_go.write_text(number_source, encoding='utf-8', newline='\n')
    canonical = (package / 'jsonata-source.js').read_text()
    start = canonical.index("        if (typeof arg === 'string') {", canonical.index('    function number(arg) {'))
    end = canonical.index('\n\n        // undefined inputs', start)
    old = canonical[start:end]
    new = '''        var privateKind;
        if (typeof arg === 'string' && typeof canaryPrivateNumericString === 'function') {
            privateKind = canaryPrivateNumericString(arg);
        } else {
''' + old + '''
        }'''
    canonical = canonical[:start] + new + canonical[end:]
    decimal = '/^-?[0-9]+(\\.[0-9]+)?([Ee][-+]?[0-9]+)?$/.test(arg)'
    radix = '/^(0[xX][0-9A-Fa-f]+)|(0[oO][0-7]+)|(0[bB][0-1]+)$/.test(arg)'
    if canonical.count(decimal) != 1 or canonical.count(radix) != 1:
        raise RuntimeError('Pinned numeric branch drift')
    decimal_branch = "} else if (typeof arg === 'string' && " + decimal
    if canonical.count(decimal_branch) != 1:
        raise RuntimeError('Pinned decimal branch drift')
    canonical = canonical.replace(decimal_branch, """} else if (typeof privateKind === 'number' && Number.isFinite(privateKind)) {
            result = privateKind;
        } else if (typeof arg === 'string' && typeof privateKind === 'undefined' && """ + decimal)
    canonical = canonical.replace(radix, "(typeof privateKind === 'undefined' ? " + radix + ' : privateKind === true)')
    canonical_path = out / 'numeric-jsonata-source.js'
    canonical_path.write_text(canonical, encoding='utf-8', newline='\n')
    runtime_path = out / 'numeric-jsonata.js'
    esbuild = ROOT / 'node_modules/@esbuild/win32-x64/esbuild.exe'
    subprocess.run([str(esbuild), str(canonical_path), '--minify-whitespace',
                    '--charset=utf8', '--outfile=' + str(runtime_path)], check=True,
                   stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    evaluate = (package / 'evaluate.go').read_text()
    if evaluate.count('(function(canaryPrivateValidator){') != 1:
        raise RuntimeError('Pinned factory drift')
    evaluate = evaluate.replace('(function(canaryPrivateValidator){',
                                '(function(canaryPrivateValidator,canaryPrivateNumericString){')
    original = 'initialize(goja.Undefined(), vm.ToValue(validator))'
    if evaluate.count(original) != 1:
        raise RuntimeError('Pinned initializer drift')
    evaluate = evaluate.replace(original, 'initialize(goja.Undefined(), vm.ToValue(validator), vm.ToValue(privateNumericString(vm, ctx, &interrupted)))')
    evaluate_path = out / 'numeric-evaluate.go'
    evaluate_path.write_text(evaluate, encoding='utf-8', newline='\n')
    return {str(package / 'numeric_string_candidate.go'): str(numeric_go),
            str(package / 'jsonata-source.js'): str(canonical_path),
            str(package / 'jsonata.js'): str(runtime_path),
            str(package / 'evaluate.go'): str(evaluate_path)}
