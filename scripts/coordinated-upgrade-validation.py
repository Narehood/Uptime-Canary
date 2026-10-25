"""Exercise a real published protocol-18 executable against the new package.

Linux amd64 only: the pinned baseline image was published for that platform.
The old container is never started. Only its executable is copied into guarded
scratch, and both executable processes use private loopback fixture databases.
No historical source checkout, compiler cache or binary archive is created.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import uuid

from validation_workspace import ROOT, guarded_output, retain_summary, tap_counts

BASE_COMMIT = '103f5c45f64d788f02df0a6393ac2b3fdbd42f0b'
BASE_IMAGE = 'ghcr.io/narehood/uptime-canary-node@sha256:506358f91ba3f95ead59d5b848791c15e1f26a8a67fd919059388da7ef6e9dfe'


def admit_baseline(value):
    if not isinstance(value, list) or len(value) != 1:
        raise ValueError('Unexpected baseline image inventory')
    image = value[0]
    labels = image.get('Config', {}).get('Labels', {})
    if (image.get('Os') != 'linux' or image.get('Architecture') != 'amd64'
            or BASE_IMAGE not in image.get('RepoDigests', [])
            or labels.get('org.opencontainers.image.revision') != BASE_COMMIT
            or labels.get('org.opencontainers.image.source') != 'https://github.com/Narehood/Uptime-Canary'):
        raise ValueError('Baseline image digest, source or platform mismatch')
    return image['Id']


def admitted_host():
    return platform.system() == 'Linux' and platform.machine().lower() in ('x86_64', 'amd64')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    args = parser.parse_args()
    if not admitted_host():
        raise ValueError('Published-baseline upgrade validation requires native Linux amd64')
    binary = args.binary.resolve(strict=True)
    scratch = (ROOT / '.canary/work/current/outputs').resolve()
    if not binary.is_relative_to(scratch) or not binary.is_file():
        raise ValueError('The current executable must belong to the active storage guard')
    if not shutil.which('docker') or not shutil.which('node'):
        raise ValueError('Existing Docker and Node are required')
    output = guarded_output('upgrade')
    baseline = output / 'canary-protocol18'
    name = 'canary-upgrade-extract-' + uuid.uuid4().hex
    report = {'passed': False, 'baselineCommit': BASE_COMMIT, 'baselineImage': BASE_IMAGE,
              'platform': 'linux/amd64', 'binarySha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
              'scope': 'Published protocol18 executable to current package, private loopback state; no release signoff'}
    # Use an empty, owned Docker client configuration. No user registry login,
    # context, credential helper or certificate setting enters this pull.
    config = output / 'docker-config'
    config.mkdir(mode=0o700)
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(('CANARY_', 'DOCKER_')) and key != 'NODE_OPTIONS'}
    env.update({'DOCKER_CONFIG': str(config), 'TSX_DISABLE_CACHE': '1'})
    created = False

    def docker(*arguments, timeout=60):
        result = subprocess.run(['docker', *arguments], env=env, capture_output=True, timeout=timeout)
        if result.returncode or len(result.stdout) + len(result.stderr) > 1 << 20:
            raise ValueError('Owned baseline image extraction failed')
        return result.stdout

    try:
        docker('pull', '--platform', 'linux/amd64', BASE_IMAGE, timeout=120)
        report['baselineImageId'] = admit_baseline(json.loads(docker('image', 'inspect', BASE_IMAGE)))
        created = True
        docker('create', '--name', name, '--network', 'none', '--read-only', '--entrypoint', '/usr/local/bin/canary', BASE_IMAGE, 'version')
        docker('cp', name + ':/usr/local/bin/canary', str(baseline))
        if baseline.is_symlink() or not baseline.is_file() or not 1 << 20 <= baseline.stat().st_size <= 64 << 20:
            raise ValueError('Unexpected published executable')
        baseline.chmod(0o700)
        report['baselineBinarySha256'] = hashlib.sha256(baseline.read_bytes()).hexdigest()
        identity = subprocess.run([str(baseline), 'version'], env=env, capture_output=True, timeout=10, check=True)
        version = json.loads(identity.stdout)
        if version.get('peerProtocol') != 18 or version.get('os') != 'linux' or version.get('arch') != 'amd64':
            raise ValueError('Published executable does not implement the declared baseline')
        report['baselineRuntimeIdentity'] = version
        env.update({'CANARY_BIN': str(binary), 'CANARY_LEGACY_BIN': str(baseline)})
        command = ['node', '--import', 'tsx', '--test', '--test-concurrency=1', '--test-reporter=tap',
                   'apps/proof/test/coordinated-upgrade.compat.ts']
        result = subprocess.run(command, cwd=ROOT, env=env, capture_output=True, timeout=260)
        if len(result.stdout) + len(result.stderr) > 128 << 10:
            raise ValueError('Upgrade proof output exceeds its bound')
        print(result.stdout.decode('utf-8', errors='replace'), flush=True)
        report['counts'] = tap_counts(result.stdout.decode('utf-8', errors='replace'))
        report['proofExitCode'] = result.returncode
        if result.returncode:
            raise ValueError('Published-baseline upgrade proof failed')
        if hashlib.sha256(binary.read_bytes()).hexdigest() != report['binarySha256']:
            raise ValueError('Current executable changed during upgrade validation')
        if hashlib.sha256(baseline.read_bytes()).hexdigest() != report['baselineBinarySha256']:
            raise ValueError('Published baseline executable changed during upgrade validation')
        report['passed'] = True
    finally:
        if created:
            try:
                docker('rm', '-v', name)
                report['extractContainerRemoved'] = True
            except (OSError, ValueError, subprocess.SubprocessError):
                report['passed'] = False
                report['extractContainerRemoved'] = False
        # The pinned image remains in the daemon's ordinary container cache;
        # never delete an image that another concurrent CI job could be using.
        retain_summary('coordinated-upgrade', report)
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
