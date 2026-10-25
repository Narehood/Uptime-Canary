"""Native hosted Linux browser acceptance, inside the storage guard.

Download only the pinned headless fixture into disposable scratch. Runtime is
offline in fresh PID/network namespaces after dropping privileges. Chromium's
sandbox remains enabled. No host browser, profile or network service is used.
"""
import argparse
from contextlib import contextmanager
import hashlib
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shutil
import stat
import subprocess
import sys
import time
import uuid
from urllib.request import ProxyHandler, build_opener
import zipfile

from browser_linux_runtime import admitted_runtime, namespace
from validation_workspace import ROOT, guarded_output, retain_summary

VERSION = '156.0.8078.4'
PROVENANCE = 'https://github.com/microsoft/playwright/blob/cfe490d941f992655948eee8fd4fd94e1c4234bf/packages/playwright-core/src/server/registry/index.ts'
MAX_DOWNLOAD = 128 * 1024 * 1024
MAX_UNPACKED = 300 * 1024 * 1024


def apparmor_source(binary, name, root):
    """One literal fixture executable; never a wildcard or user-supplied rule."""
    if (not re.fullmatch(r'canary-browser-[0-9a-f]{32}', name)
            or not re.fullmatch(r'/[A-Za-z0-9/_.-]+', root)
            or str(PurePosixPath(root)) != root or '..' in PurePosixPath(root).parts or '//' in root):
        raise ValueError('Invalid private browser AppArmor identity')
    base = root + '/.canary/work/current/outputs/bri/'
    allowed = {base + folder + '/chrome-headless-shell'
               for folder in ('chrome-headless-shell-linux64', 'chrome-headless-shell-linux-arm64')}
    if binary not in allowed:
        raise ValueError('Refusing a non-fixture AppArmor attachment')
    # Chromium's documented Ubuntu userns profile, narrowed to this exact
    # executable instead of a build-directory glob. No system file is written.
    return ('abi <abi/4.0>,\nprofile ' + name + ' "' + binary + '" flags=(unconfined) {\n  userns,\n}\n').encode('ascii')


@contextmanager
def loaded_apparmor_profile(source, info):
    command = ['/usr/bin/sudo', '-n', '/usr/sbin/apparmor_parser', '-K']
    cleanup = False
    info.update(loaded=False, removed=False)
    try:
        try:
            subprocess.run([*command, '-a'], input=source, stdout=subprocess.DEVNULL,
                           stderr=subprocess.PIPE, check=True, timeout=15)
        except subprocess.TimeoutExpired:
            # A timed-out add may have committed. The generated name is unique;
            # attempt removal before reporting the ambiguous load as a failure.
            cleanup = True
            raise
        cleanup = True
        info['loaded'] = True
        yield
    finally:
        if cleanup:
            subprocess.run([*command, '-R'], input=source, stdout=subprocess.DEVNULL,
                           stderr=subprocess.PIPE, check=True, timeout=15)
            info['removed'] = True


@contextmanager
def browser_apparmor(chrome, report):
    if (sys.platform != 'linux' or os.environ.get('GITHUB_ACTIONS') != 'true' or os.geteuid() == 0
            or Path(os.environ.get('GITHUB_WORKSPACE', '')).resolve() != ROOT
            or not (ROOT / '.canary/bounded-validation.lock').is_file()
            or chrome.resolve(strict=True) != chrome):
        raise RuntimeError('Private browser AppArmor admission failed')
    name = 'canary-browser-' + uuid.uuid4().hex
    source = apparmor_source(str(chrome), name, str(ROOT))
    enabled = Path('/sys/module/apparmor/parameters/enabled')
    if not enabled.is_file() or enabled.read_text().strip() == 'N':
        report['apparmor'] = {'required': False, 'globalUsernsRestrictionsDisabled': False}
        yield
        return
    if enabled.read_text().strip() != 'Y' or not Path('/usr/sbin/apparmor_parser').is_file():
        raise RuntimeError('AppArmor fixture prerequisite unavailable')
    info = report['apparmor'] = {'required': True, 'profile': name, 'exactBinaryOnly': True,
                                'globalUsernsRestrictionsDisabled': False}
    with loaded_apparmor_profile(source, info):
        yield


def extract_fixture(archive, output, folder):
    """Reject links, traversal, duplicate members and decompression bombs."""
    with zipfile.ZipFile(archive) as package:
        members = package.infolist()
        if not members or len(members) > 512 or sum(m.file_size for m in members) > MAX_UNPACKED:
            raise ValueError('Browser archive exceeds extraction bounds')
        seen = set()
        for member in members:
            path = PurePosixPath(member.filename)
            mode = member.external_attr >> 16
            if (path.is_absolute() or not path.parts or path.parts[0] != folder
                    or any(part in ('', '.', '..') for part in path.parts)
                    or member.orig_filename != member.filename or '\\' in member.orig_filename or ':' in member.filename
                    or member.filename in seen or member.flag_bits & 1
                    or stat.S_ISLNK(mode) or stat.S_IFMT(mode) not in (0, stat.S_IFREG, stat.S_IFDIR)):
                raise ValueError('Unsupported browser archive member')
            seen.add(member.filename)
        for member in members:
            target = output.joinpath(*PurePosixPath(member.filename).parts)
            if not target.resolve().is_relative_to(output.resolve()):
                raise ValueError('Browser archive escapes owned output')
            if member.is_dir():
                target.mkdir(parents=True, exist_ok=True, mode=0o700)
            else:
                target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                with package.open(member) as source, target.open('xb') as destination:
                    shutil.copyfileobj(source, destination, 1024 * 1024)
                target.chmod(0o700 if member.external_attr >> 16 & 0o111 else 0o600)
    binary = output / folder / 'chrome-headless-shell'
    if not binary.is_file():
        raise ValueError('Browser executable missing from fixture')
    binary.chmod(0o700)
    return binary


def fixture(output):
    architecture = {'x86_64': 'linux64', 'aarch64': 'linux-arm64'}.get(platform.machine())
    if not architecture:
        raise RuntimeError('Actual browser requires native Linux amd64 or arm64')
    folder = 'chrome-headless-shell-' + architecture
    url = f'https://cdn.playwright.dev/builds/cft/{VERSION}/{architecture}/{folder}.zip'
    archive = output / 'browser.zip'
    digest, size = hashlib.sha256(), 0
    opener = build_opener(ProxyHandler({}))
    with opener.open(url, timeout=60) as response, archive.open('xb') as destination:
        while chunk := response.read(1024 * 1024):
            size += len(chunk)
            if size > MAX_DOWNLOAD:
                raise ValueError('Browser download exceeds bound')
            digest.update(chunk)
            destination.write(chunk)
    browser = extract_fixture(archive, output, folder)
    archive.unlink()  # Discard the one declared archive before runtime starts.
    return browser, {'version': VERSION, 'url': url, 'upstreamSource': PROVENANCE,
                     'archiveSha256': digest.hexdigest(), 'downloadBytes': size,
                     'binarySha256': hashlib.sha256(browser.read_bytes()).hexdigest()}


def offline(args):
    output = args.offline.resolve()
    if (output != ROOT / '.canary/work/current/outputs/bri' or not admitted_runtime()
            or os.geteuid() != args.uid or os.getegid() != args.gid
            or output.stat().st_uid != args.uid or output.stat().st_mode & 0o777 != 0o700
            or os.readlink('/proc/self/ns/net') == args.parent_network
            or not args.binary.resolve().is_relative_to(ROOT / '.canary/work/current/outputs')
            or not args.chrome.resolve().is_relative_to(output)):
        raise RuntimeError('Private browser namespace/credential admission failed')
    import resource
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    namespace.subreaper()
    if not args.node or not Path(args.node).is_file():
        raise RuntimeError('Pinned Node executable required')
    os.environ.clear()
    os.environ.update(PATH=str(Path(args.node).parent) + ':/usr/bin:/bin', HOME=str(output), LANG='C', LC_ALL='C', TZ='UTC',
                      GOCACHE=str(ROOT / '.canary/cache/go-build'), GOTMPDIR=str(ROOT / '.canary/work/current/tmp'),
                      TSX_DISABLE_CACHE='1', TMPDIR=str(output), TMP=str(output), TEMP=str(output))
    spec = importlib.util.spec_from_file_location('browser_runtime', Path(__file__).with_name('browser-real-validation.py'))
    runtime = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(runtime)
    sys.argv = [str(spec.origin), '--binary', str(args.binary), '--chrome', str(args.chrome), '--linux-runtime']
    if args.control:
        sys.argv.extend(['--control-witness', str(output / 'control-ready.json')])
    try:
        return runtime.main()
    finally:
        reaped = namespace.reap_namespace()
        remaining = [p.name for p in Path('/proc').iterdir() if p.name.isdecimal() and p.name not in ('1', str(os.getpid()))]
        (output / 'reaped.json').write_text(json.dumps({'adoptedChildrenReaped': reaped, 'remainingProcesses': len(remaining)}) + '\n')
        if remaining:
            raise RuntimeError('Private browser namespace retained processes')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--offline', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--chrome', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--node', help=argparse.SUPPRESS)
    parser.add_argument('--uid', type=int, help=argparse.SUPPRESS)
    parser.add_argument('--gid', type=int, help=argparse.SUPPRESS)
    parser.add_argument('--parent-network', help=argparse.SUPPRESS)
    parser.add_argument('--control', action='store_true', help=argparse.SUPPRESS)
    args = parser.parse_args()
    if sys.platform != 'linux':
        raise RuntimeError('Actual browser acceptance requires native Linux')
    if args.offline:
        return offline(args)
    if (os.environ.get('GITHUB_ACTIONS') != 'true' or os.geteuid() == 0
            or Path(os.environ.get('GITHUB_WORKSPACE', '')).resolve() != ROOT
            or not args.binary.resolve().is_relative_to(ROOT / '.canary/work/current/outputs')
            or not args.binary.is_file()):
        raise RuntimeError('Only an owned executable on a disposable hosted Linux runner is admitted')
    os.umask(0o077)
    output = guarded_output('bri')
    namespace.subreaper()
    report = {'passed': False, 'releaseSignoff': False, 'browserSandboxDisabled': False,
              'binarySha256': hashlib.sha256(args.binary.read_bytes()).hexdigest()}
    try:
        chrome, report['fixture'] = fixture(output)
        with browser_apparmor(chrome, report):
            # Fixed system code runs privileged. Both system supervisors retain
            # credentials/PDEATHSIG; repository code starts only after setpriv.
            shim = '"/usr/sbin/ip" link set lo up && exec "/usr/bin/setpriv" "$@"'
            command = ['/usr/bin/sudo', '-n', '/usr/bin/unshare', '--net', '--pid', '--fork', '--kill-child', '--mount-proc',
                       '--', '/usr/bin/unshare', '--fork', '--kill-child', '--', '/bin/sh', '-c', shim, 'canary-browser-namespace',
                       '--reuid=' + str(os.getuid()), '--regid=' + str(os.getgid()), '--clear-groups',
                       '--inh-caps=-all', '--ambient-caps=-all', '--bounding-set=-all', '--no-new-privs',
                       '/usr/bin/python3', '-B', str(Path(__file__).resolve()), '--offline', str(output),
                       '--uid', str(os.getuid()), '--gid', str(os.getgid()), '--parent-network', os.readlink('/proc/self/ns/net'),
                       '--binary', str(args.binary.resolve()), '--chrome', str(chrome), '--node', shutil.which('node')]
            subprocess.run(command, cwd=ROOT, stdin=subprocess.DEVNULL, check=True, timeout=200)
            report['runtime'] = json.loads((ROOT / '.canary/browser-real-current.json').read_text())
            report['cleanup'] = json.loads((output / 'reaped.json').read_text())
            if not report['runtime']['passed'] or report['cleanup']['remainingProcesses']:
                raise RuntimeError('Actual browser runtime failed')
            profile = ROOT / '.canary/work/current/outputs/browser-real'
            if profile.resolve() != profile or not profile.is_relative_to(output.parent):
                raise RuntimeError('Refusing an unowned profile cleanup target')
            shutil.rmtree(profile)
            control = command[:2] + ['/usr/bin/timeout', '--foreground', '--signal=KILL', '30s'] + command[2:] + ['--control']
            start = time.monotonic()
            killed = subprocess.run(control, cwd=ROOT, stdin=subprocess.DEVNULL, timeout=45)
            witness = json.loads((output / 'control-ready.json').read_text())
            if killed.returncode != 137 or time.monotonic() - start < 29 or witness.get('ready') is not True:
                raise RuntimeError('Supervisor SIGKILL did not reach a ready private browser')
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
                    raise RuntimeError('Browser supervisor left unreaped children')
                else:
                    time.sleep(0.01)
            for entry in Path('/proc').iterdir():
                if not entry.name.isdecimal():
                    continue
                try:
                    if os.readlink(entry / 'ns/pid') == witness['namespace']:
                        raise RuntimeError('Supervisor death left a private browser process')
                except (FileNotFoundError, PermissionError):
                    continue
            report['supervisorSIGKILL'] = {'passed': True, 'actualBrowserReady': True,
                                          'adoptedChildrenReaped': reaped, 'remainingProcesses': 0}
        # Profile removal must succeed before the overall gate can pass.
        report['passed'] = True
    finally:
        retain_summary('browser-linux', report)
        print(json.dumps(report, indent=2), flush=True)
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
