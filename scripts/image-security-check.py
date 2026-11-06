"""Scan the actual built image, with disposable Trivy data and bounded evidence."""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import subprocess

from validation_workspace import guarded_output, retain_summary

SEVERITIES = ('UNKNOWN', 'LOW', 'MEDIUM', 'HIGH', 'CRITICAL')


def summarize(data):
    if not isinstance(data, dict) or data.get('SchemaVersion') != 2:
        raise ValueError('Unsupported or absent scan report')
    meta = data.get('Metadata')
    if not isinstance(meta, dict) or not isinstance(meta.get('OS'), dict):
        raise ValueError('Missing image metadata')
    image = meta.get('ImageID', '')
    system = meta.get('OS', {})
    version = system.get('Name', '')
    if (not re.fullmatch(r'sha256:[0-9a-f]{64}', image)
            or system.get('Family') != 'alpine'
            or not re.fullmatch(r'3\.24\.\d+', version)
            or int(version.split('.')[2]) < 3 or system.get('EOSL', False)):
        raise ValueError('Expected a supported Alpine 3.24.3+ image')
    results = data.get('Results')
    if not isinstance(results, list) or not results:
        raise ValueError('Missing package scan results')
    counts = dict.fromkeys(SEVERITIES, 0)
    findings, inventories = [], []
    for result in results:
        if not isinstance(result, dict) or not isinstance(result.get('Packages'), list):
            raise ValueError('Incomplete package inventory')
        inventories.append({'target': result.get('Target'), 'type': result.get('Type'),
                            'class': result.get('Class'), 'packages': len(result['Packages'])})
        vulnerabilities = result.get('Vulnerabilities', [])
        if vulnerabilities is None:
            vulnerabilities = []
        if not isinstance(vulnerabilities, list):
            raise ValueError('Invalid vulnerability list')
        for item in vulnerabilities:
            if (not isinstance(item, dict) or item.get('Severity') not in counts
                    or not all(isinstance(item.get(k), str) and 0 < len(item[k]) < 512
                               for k in ('VulnerabilityID', 'PkgName', 'InstalledVersion'))):
                raise ValueError('Invalid vulnerability finding')
            counts[item['Severity']] += 1
            findings.append({k: item[k] for k in ('VulnerabilityID', 'PkgName', 'InstalledVersion',
                                                  'FixedVersion', 'Severity', 'PrimaryURL') if k in item})
    if not any(i['class'] == 'os-pkgs' and i['type'] == 'alpine' and i['packages'] > 0 for i in inventories):
        raise ValueError('Alpine packages were not inventoried')
    return {'imageID': image, 'alpine': version, 'inventories': inventories,
            'vulnerabilities': counts, 'findings': findings,
            'policy': 'Fail on HIGH or CRITICAL, including unfixed; retain all severities',
            'passed': counts['HIGH'] == counts['CRITICAL'] == 0}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True)
    parser.add_argument('--source', choices=('docker', 'remote'), required=True)
    args = parser.parse_args()
    output = guarded_output('image-security')
    temporary = output / 'tmp'
    temporary.mkdir()
    env = dict(os.environ)
    env.update(TRIVY_CACHE_DIR=str(output / 'cache'), TMPDIR=str(temporary),
               TMP=str(temporary), TEMP=str(temporary))
    report = {'startedAt': datetime.now(timezone.utc).isoformat(), 'image': args.image,
              'source': args.source, 'passed': False}
    try:
        command = ['trivy', 'image', '--image-src', args.source, '--scanners', 'vuln',
                   '--skip-java-db-update', '--list-all-pkgs', '--format', 'json',
                   '--output', str(output / 'scan.json'), '--timeout', '3m', args.image]
        subprocess.run(command, env=env, check=True, timeout=210)
        raw = (output / 'scan.json').read_bytes()
        if len(raw) > 8 * 1024 * 1024:
            raise ValueError('Scan evidence exceeds bounds')
        report.update(summarize(json.loads(raw)))
        version = json.loads(subprocess.check_output(['trivy', 'version', '--format', 'json'], env=env, timeout=15))
        if version.get('Version') != '0.76.2' or not isinstance(version.get('VulnerabilityDB'), dict):
            raise ValueError('Pinned scanner or vulnerability database evidence missing')
        report['scanner'] = version
    except (OSError, ValueError, TypeError, subprocess.SubprocessError) as error:
        report.update(passed=False, error=str(error)[:1000])
    report['finishedAt'] = datetime.now(timezone.utc).isoformat()
    retain_summary('image-security', report)
    print(json.dumps(report, indent=2))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
