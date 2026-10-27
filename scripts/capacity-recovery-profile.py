"""Collect a bounded Linux recovery CPU profile; never claim capacity acceptance.

Run through bounded_validation.py. The existing benchmark uses private loopback
fixtures and databases. The guard removes its executable and CPU profile; only
one overwritten, size-limited JSON report is retained.
"""
import os
import platform
import subprocess

from bounded_validation import source_digest
from validation_workspace import ROOT, guarded_output, retain_summary


def main():
    """Return profile-collection status on Linux using only guard-owned outputs."""
    # Refuse before creating outputs or invoking Go: workstation test binaries
    # must never cause Windows Firewall prompts during Docker-image work.
    if platform.system() != 'Linux':
        raise SystemExit('Recovery profiling requires native Linux; no executable was launched')
    output = guarded_output('capacity-recovery-profile')
    go = ROOT / '.canary/tools/go/bin/go'
    if not go.is_file():
        raise SystemExit('The existing pinned Go toolchain is required')
    profile = output / 'cpu.pprof'
    env = {k: v for k, v in os.environ.items() if not k.startswith('CANARY_')}
    env.update(CGO_ENABLED='0', GOTOOLCHAIN='local', GOENV='off', GOWORK='off',
               GOPROXY='off', GOSUMDB='off')
    before = source_digest(ROOT)
    report = {'sourceBefore': before, 'platform': platform.platform(), 'commands': [],
              'profileCollected': False, 'capacityAcceptance': False, 'releaseSignoff': False}

    def run(label, command):
        """Retain bounded command evidence and stop collection on any failure."""
        print(label, flush=True)
        result = subprocess.run(command, cwd=ROOT, env=env, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, timeout=300)
        text = result.stdout.decode('utf-8', errors='replace')[-24000:]
        report['commands'].append({'label': label, 'command': command,
                                   'exitCode': result.returncode, 'output': text})
        print(text, flush=True)
        if result.returncode:
            raise RuntimeError(label + ' failed')

    try:
        run('fixed-private-recovery-profile', [str(go), 'test', '-mod=readonly', '-p=1',
            '-gcflags=all=-dwarf=false', '-ldflags=-s -w', '-run=^$',
            '-bench=^BenchmarkCapacityRecoveryProfile$', '-benchtime=1x', '-timeout=3m',
            '-cpuprofile=' + str(profile), '-o', str(output / 'capacity.test'),
            './apps/node-go/internal/canary'])
        run('cpu-flat', [str(go), 'tool', 'pprof', '-top', '-nodecount=50', str(profile)])
        run('runtime-cumulative', [str(go), 'tool', 'pprof', '-top', '-cum',
            '-show=uptime-canary/apps/node-go/internal/canary', '-nodecount=60', str(profile)])
        report['profileCollected'] = True
    except (OSError, subprocess.TimeoutExpired, RuntimeError) as error:
        report['error'] = str(error)
    finally:
        report['sourceUnchanged'] = source_digest(ROOT) == before
        retain_summary('capacity-recovery-profile', report)
    return 0 if report['profileCollected'] and report['sourceUnchanged'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
