"""Retain licenses for the optional service's complete locked dependency tree."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path, PurePosixPath
import re

spec = importlib.util.spec_from_file_location('canary_notices', Path(__file__).with_name('third-party-notices.py'))
notices = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notices)
ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    service = ROOT / 'services/game-query'
    lock = json.loads((service / 'package-lock.json').read_text(encoding='utf-8'))
    sections, packages = [], []
    for relative, entry in sorted(lock['packages'].items()):
        if not relative:
            continue
        path = PurePosixPath(relative)
        if path.is_absolute() or '..' in path.parts or path.parts[0] != 'node_modules' or entry.get('link'):
            raise ValueError('Unsupported service dependency location')
        directory = service.joinpath(*path.parts)
        if not directory.resolve().is_relative_to(service.resolve()) or directory.is_symlink():
            raise ValueError('Service dependency escapes owned tree')
        package = json.loads((directory / 'package.json').read_text(encoding='utf-8'))
        if package['version'] != entry['version']:
            raise ValueError('Installed service dependency differs from lock')
        try:
            # Path comparisons fold case on Windows but not on Linux. Use the
            # retained relative filename, with explicit case-sensitive order,
            # so identical installed bytes produce identical notices everywhere.
            texts = sorted(notices.texts(directory), key=lambda entry: entry[0])
        except RuntimeError:
            # Several historical npm packages publish their notice in README
            # or component.json rather than a standalone LICENSE. Preserve
            # those exact distributed declarations; do not invent attribution.
            evidence = [p for p in directory.iterdir() if p.is_file() and p.name.lower().startswith('readme')]
            component = directory / 'component.json'
            declaration = package.get('license')
            if component.is_file():
                evidence.append(component)
                declaration = declaration or json.loads(component.read_text(encoding='utf-8')).get('license')
            if declaration != 'MIT' or not evidence:
                raise ValueError('Service dependency has no identifiable license evidence: ' + package['name'])
            texts = [('package.json', (directory / 'package.json').read_text(encoding='utf-8').replace('\r\n', '\n'))]
            for file in sorted(evidence, key=lambda file: file.relative_to(directory).as_posix()):
                raw = file.read_bytes()
                if len(raw) > 256 * 1024 or b'\0' in raw:
                    raise ValueError('Service license evidence exceeds bounds')
                texts.append((file.name, raw.decode('utf-8-sig').replace('\r\n', '\n')))
            if not any(re.search(r'\bMIT\b', text) for _, text in texts):
                raise ValueError('Missing distributed license declaration')
        sections.append('\n' + '=' * 78 + '\n' + package['name'] + ' ' + entry['version'] + '\n' + '=' * 78 + '\n')
        files = []
        for filename, text in texts:
            sections.append('\n--- ' + filename + ' ---\n\n' + text.rstrip() + '\n')
            files.append({'file': filename, 'sha256': hashlib.sha256(text.encode()).hexdigest()})
        packages.append({'name': package['name'], 'version': entry['version'], 'integrity': entry['integrity'], 'notices': files})
    header = 'Uptime-Canary optional GameDig query service third-party notices\n\nOriginal dependency terms remain applicable. Node and Alpine base packages are separate.\n'
    notice = header + ''.join(sections)
    metadata = json.dumps({'lockSha256': hashlib.sha256((service / 'package-lock.json').read_bytes()).hexdigest(), 'packages': packages}, indent=2) + '\n'
    for name, content in [('THIRD_PARTY_NOTICES.txt', notice), ('third-party-manifest.json', metadata)]:
        path = service / name
        if args.check:
            if not path.is_file() or path.read_text(encoding='utf-8') != content:
                raise ValueError('Service notices changed; regenerate scripts/game-query-notices.py')
        else:
            path.write_text(content, encoding='utf-8', newline='\n')
    print(f'Verified {len(packages)} optional-service notice sets')


if __name__ == '__main__':
    main()
