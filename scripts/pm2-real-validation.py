"""Linux-only PM2 interoperability in disposable PID/network/mount namespaces.

Run under bounded_validation.py on a disposable native GitHub Linux runner.
The privileged shim executes only fixed system programs, drops all privileges,
then starts repository code. Nothing contacts an existing manager or host pipe.
"""
import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import stat
import subprocess
import sys
import time
try:
    import resource
except ImportError:
    resource = None  # Windows can run refusal fixtures; the launcher is Linux-only.

from validation_workspace import ROOT, guarded_output, retain_summary, tap_counts


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def require_link_free_fixture(root):
    # The storage guard deliberately refuses links. Inspect without following
    # them before executing the fixture; npm CLI shims are unnecessary here.
    pending = [root]
    while pending:
        path = pending.pop()
        info = path.lstat()
        if stat.S_ISLNK(info.st_mode) or getattr(info, 'st_file_attributes', 0) & 0x400:
            raise RuntimeError('Fixture dependencies contain an unsupported link')
        if stat.S_ISDIR(info.st_mode):
            pending.extend(path.iterdir())


def fixture_inputs(output):
    """Called before isolation; scripts are never executed by npm."""
    source = ROOT / 'scripts/fixtures/pm2-real'
    package = output / 'pm2-package'
    package.mkdir()
    for name in ('package.json', 'package-lock.json'):
        (package / name).write_bytes((source / name).read_bytes())
    for name in ('npm-user.conf', 'npm-global.conf'):
        (output / name).write_text('')
    env = {key: value for key, value in os.environ.items()
           if key.upper() not in ('NODE_OPTIONS', 'NODE_PATH') and not key.upper().startswith(('CANARY_', 'NPM_'))}
    env.update({'HOME': str(output), 'npm_config_cache': str(output / 'npm-cache'),
                'npm_config_userconfig': str(output / 'npm-user.conf'),
                'npm_config_globalconfig': str(output / 'npm-global.conf'),
                'npm_config_registry': 'https://registry.npmjs.org', 'npm_config_update_notifier': 'false'})
    subprocess.run([shutil.which('npm'), 'ci', '--ignore-scripts', '--omit=optional', '--no-audit', '--no-fund', '--no-bin-links',
                    '--prefix', str(package)], env=env, check=True, timeout=120)
    require_link_free_fixture(package)
    lock = json.loads((package / 'package-lock.json').read_text())
    if lock['packages']['node_modules/pm2']['version'] != '6.0.8':
        raise ValueError('Unexpected fixture PM2 version')
    helper = source / 'manager.mjs'
    pins = re.findall(r"'([^']+)': '([a-f0-9]{64})'", helper.read_text())
    if len(pins) != 19:
        raise ValueError('Missing PM2 source pins')
    for name, expected in pins:
        if digest(package / 'node_modules/pm2' / name) != expected:
            raise ValueError('Installed PM2 source differs: ' + name)
    return {'version': '6.0.8', 'coreFilesVerified': len(pins), 'resolvedPackages': len(lock['packages']), 'linkFree': True,
            'lockSha256': digest(source / 'package-lock.json'), 'managerSha256': digest(helper)}


def namespace_supervisor():
    # Trusted system unshare remains PID 1 with its original credentials and
    # parent-death signal. Repository code is its unprivileged direct child.
    return (sys.platform == 'linux' and os.getpid() > 1 and os.getppid() == 1 and os.geteuid() != 0
            and Path('/proc/1').stat().st_uid == 0 and Path('/proc/1/comm').read_text().strip() == 'unshare')


def subreaper():
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0) != 0:  # PR_SET_CHILD_SUBREAPER
        raise OSError(ctypes.get_errno(), 'Cannot supervise adopted fixture children')


def reap_namespace():
    """Signal only this private namespace; the caller and PID 1 are excluded."""
    if not namespace_supervisor():
        raise RuntimeError('Refusing cleanup outside the private PID namespace')
    os.kill(-1, signal.SIGKILL)
    count = 0
    until = time.monotonic() + 5
    while True:
        try:
            pid, _ = os.waitpid(-1, os.WNOHANG)
        except ChildProcessError:
            return count
        if pid:
            count += 1
        elif time.monotonic() > until:
            raise RuntimeError('Private namespace still has unreaped children')
        else:
            time.sleep(0.01)


def offline(output, uid, gid, parent_network, node, binary, control=False):
    # PID 1 is an unchanged-credential system unshare supervisor. Its child
    # has already dropped UID/GID, groups, capabilities and escalation.
    if (not namespace_supervisor() or os.geteuid() != uid or os.getegid() != gid or uid == 0
            or output.stat().st_uid != uid or output.stat().st_mode & 0o777 != 0o700
            or os.readlink('/proc/self/ns/net') == parent_network
            or {name for _, name in socket.if_nameindex()} != {'lo'}):
        raise RuntimeError('Private namespace/credential admission failed')
    status = dict(line.split(':', 1) for line in Path('/proc/self/status').read_text().splitlines() if ':' in line)
    if (status.get('NoNewPrivs', '').strip() != '1' or os.getgroups()
            or any(int(status[key].strip(), 16) for key in ('CapEff', 'CapPrm', 'CapInh', 'CapBnd', 'CapAmb'))):
        raise RuntimeError('Runtime retains privileged authority')
    routes = Path('/proc/net/route').read_text().splitlines()
    if len(routes) != 1:
        raise RuntimeError('Offline namespace has a route')
    if subprocess.check_output([node, '--version'], text=True).strip() != 'v24.13.0':
        raise RuntimeError('Unreviewed Node cluster implementation')
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    subreaper()
    env = {'PATH': str(Path(node).parent) + ':/usr/bin:/bin', 'HOME': str(output),
           'TMPDIR': str(output / 'tmp'), 'LANG': 'C', 'LC_ALL': 'C', 'TZ': 'UTC',
           'TSX_DISABLE_CACHE': '1', 'CANARY_BIN': str(binary), 'PM2_GUARD': str(output)}
    (output / 'tmp').mkdir(exist_ok=True)
    report = {'passed': False, 'isolatedPIDNamespace': True, 'loopbackOnly': True,
              'uid': uid, 'capabilities': 'none', 'node': 'v24.13.0', 'releaseSignoff': False}
    try:
        command = [node, '--max-old-space-size=96', '--import', 'tsx']
        if not control:
            command.extend(['--test', '--test-reporter=tap'])
        command.append(str(ROOT / 'scripts/pm2-real-proof.mjs'))
        if control:
            command.append('--containment-control')
        result = subprocess.run(command,
                                env=env, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=150)
        text = result.stdout.decode('utf-8', errors='replace')
        print(text[-32768:], flush=True)
        report['counts'] = tap_counts(text)
        report['passed'] = result.returncode == 0
    finally:
        # Covers manager SIGKILL, startup interruption and auxiliary pidusage
        # processes too. This subreaper adopts the test's descendants.
        # No host PID is visible, and the root system PID 1 is excluded.
        report['adoptedChildrenReaped'] = reap_namespace()
        remaining = [p.name for p in Path('/proc').iterdir() if p.name.isdecimal() and p.name not in ('1', str(os.getpid()))]
        report['remainingProcesses'] = len(remaining)
        report['passed'] = report['passed'] and not remaining
        (output / 'offline-result.json').write_text(json.dumps(report, indent=2) + '\n')
    return 0 if report['passed'] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--offline', type=Path)
    parser.add_argument('--uid', type=int)
    parser.add_argument('--gid', type=int)
    parser.add_argument('--parent-network')
    parser.add_argument('--node')
    parser.add_argument('--binary', type=Path, help='Reuse the owned extracted native package executable')
    parser.add_argument('--containment-control', action='store_true')
    args = parser.parse_args()
    if sys.platform != 'linux':
        raise RuntimeError('Actual PM2 acceptance requires native Linux')
    if args.offline:
        output = args.offline.resolve()
        if output != ROOT / '.canary/work/current/outputs/pmi' or not (ROOT / '.canary/bounded-validation.lock').is_file():
            raise RuntimeError('Refusing an unowned runtime directory')
        binary = args.binary.resolve()
        if not binary.is_relative_to(ROOT / '.canary/work/current/outputs') or not binary.is_file():
            raise RuntimeError('Only an owned validation executable is allowed')
        return offline(output, args.uid, args.gid, args.parent_network, args.node, binary, args.containment_control)
    # The privilege wrapper is allowed only on a fresh disposable hosted runner.
    if (os.environ.get('GITHUB_ACTIONS') != 'true' or os.geteuid() == 0
            or Path(os.environ.get('GITHUB_WORKSPACE', '')).resolve() != ROOT):
        raise RuntimeError('Run only on a disposable native GitHub Linux runner')
    os.umask(0o077)
    output = guarded_output('pmi')
    socket_path = output / 'pm2-real/pm2/rpc.sock'
    if len(str(socket_path).encode()) > 100 or output.resolve() != output:
        raise RuntimeError('Canonical owned socket path is too long')
    node = shutil.which('node')
    subreaper()
    report = {'passed': False, 'releaseSignoff': False}
    try:
        binary = args.binary.resolve() if args.binary else output / 'canary'
        if args.binary:
            if not binary.is_relative_to(ROOT / '.canary/work/current/outputs') or not binary.is_file():
                raise RuntimeError('Only an owned validation executable is allowed')
        else:
            go = ROOT / '.canary/tools/go/bin/go'
            subprocess.run([str(go), 'build', '-mod=readonly', '-p=1', '-trimpath', '-o', str(binary),
                            './apps/node-go/cmd/canary'], cwd=ROOT, check=True, timeout=180)
        report['binarySha256'] = digest(binary)
        report['fixture'] = fixture_inputs(output)
        # The second SYSTEM unshare is PID 1 and never changes credentials.
        # Credential changes clear PDEATHSIG: keeping them in its child avoids
        # losing the outer supervisor -> PID 1 kill relationship or rearm races.
        # Both unshare instances are trusted binaries, never repository code.
        shim = '"/usr/sbin/ip" link set lo up && exec "/usr/bin/setpriv" "$@"'
        command = ['/usr/bin/sudo', '-n', '/usr/bin/unshare', '--net', '--pid', '--fork', '--kill-child', '--mount-proc',
                   '--', '/usr/bin/unshare', '--fork', '--kill-child', '--', '/bin/sh', '-c', shim, 'canary-pm2-namespace',
                   '--reuid=' + str(os.getuid()), '--regid=' + str(os.getgid()), '--clear-groups',
                   '--inh-caps=-all', '--ambient-caps=-all', '--bounding-set=-all', '--no-new-privs',
                   '/usr/bin/python3', '-B', str(Path(__file__).resolve()), '--offline', str(output),
                   '--uid', str(os.getuid()), '--gid', str(os.getgid()),
                   '--parent-network', os.readlink('/proc/self/ns/net'), '--node', node, '--binary', str(binary)]
        result = subprocess.run(command, stdin=subprocess.DEVNULL, cwd=ROOT, timeout=180)
        report['runtime'] = json.loads((output / 'offline-result.json').read_text())
        if result.returncode or not report['runtime']['passed']:
            raise RuntimeError('Actual PM2 acceptance failed')
        # Primary PID namespace has exited with no remaining processes. This
        # exact disposable run directory can now be reused for the kill control.
        run = output / 'pm2-real'
        if run.resolve() != run or not run.is_relative_to(output):
            raise RuntimeError('Refusing an unowned cleanup target')
        shutil.rmtree(run)
        # GNU timeout --foreground signals ONLY its direct command. Killing the
        # outer unshare alone must kill the unchanged-credential system PID 1,
        # and hence the actual PM2 manager/app in its private PID namespace.
        control = command[:2] + ['/usr/bin/timeout', '--foreground', '--signal=KILL', '20s'] + command[2:] + ['--containment-control']
        start = time.monotonic()
        killed = subprocess.run(control, stdin=subprocess.DEVNULL, cwd=ROOT, timeout=35)
        elapsed = time.monotonic() - start
        witness = json.loads((output / 'control-ready.json').read_text())
        if killed.returncode != 137 or elapsed < 19 or witness['status'] != 'online':
            raise RuntimeError('Supervisor SIGKILL control did not reach actual PM2')
        reaped = 0
        until = time.monotonic() + 5
        while True:
            try:
                pid, _ = os.waitpid(-1, os.WNOHANG)
            except ChildProcessError:
                break
            if pid:
                reaped += 1
            elif time.monotonic() > until:
                raise RuntimeError('Supervisor control left unreaped children')
            else:
                time.sleep(0.01)
        remaining = []
        for entry in Path('/proc').iterdir():
            if not entry.name.isdecimal():
                continue
            try:
                if os.readlink(entry / 'ns/pid') == witness['namespace']:
                    remaining.append(entry.name)
            except (FileNotFoundError, PermissionError):
                continue
        if remaining:
            raise RuntimeError('Supervisor death left private PM2 processes running')
        report['supervisorSIGKILL'] = {'passed': True, 'actualPM2Ready': True, 'seconds': round(elapsed, 3),
                                      'adoptedChildrenReaped': reaped, 'remainingProcesses': 0}
        report['passed'] = True
    finally:
        retain_summary('pm2-real', report)
        print(json.dumps(report, indent=2), flush=True)
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
