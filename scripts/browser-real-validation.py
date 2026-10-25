"""Exercise the production binary against one owned private Chromium instance.

Use bounded_validation.py. No user browser profile, application DB, installed
binary replacement, browser download or artifact collection is involved.
Linux calls enter only through browser-linux-validation.py after private
namespace admission. Neither platform disables Chromium's own sandbox.
"""
import argparse
import ctypes
from ctypes import wintypes
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import threading
import time
from urllib.request import ProxyHandler, build_opener

from bounded_validation import source_digest
from validation_process import start_command, stop_tree
from validation_workspace import ROOT, guarded_output, retain_summary, tap_counts


def stop_private_browser(process):
    """Terminate only this fixture's job and witness zero active members."""
    if os.name != 'nt':
        # The admitted namespace supervisor additionally kills and reaps every
        # descendant (including a renderer that changes its process group).
        stop_tree(process)
        return {'namespaceReapingRequired': True}
    job = getattr(process, '_validation_job', None)
    if job is None:
        raise RuntimeError('An owned Windows browser job is required')
    kernel = job.kernel
    kernel.TerminateJobObject.argtypes = [wintypes.HANDLE, wintypes.UINT]
    kernel.TerminateJobObject.restype = wintypes.BOOL
    kernel.QueryInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD, ctypes.c_void_p]
    kernel.QueryInformationJobObject.restype = wintypes.BOOL

    class Accounting(ctypes.Structure):
        _fields_ = [('user', ctypes.c_int64), ('kernel', ctypes.c_int64),
                    ('period_user', ctypes.c_int64), ('period_kernel', ctypes.c_int64),
                    ('faults', wintypes.DWORD), ('total', wintypes.DWORD),
                    ('active', wintypes.DWORD), ('terminated', wintypes.DWORD)]

    try:
        if not kernel.TerminateJobObject(job.handle, 1):
            raise ctypes.WinError(ctypes.get_last_error())
        deadline = time.monotonic() + 5
        while True:
            accounting = Accounting()
            if not kernel.QueryInformationJobObject(job.handle, 1, ctypes.byref(accounting), ctypes.sizeof(accounting), None):
                raise ctypes.WinError(ctypes.get_last_error())
            if accounting.active == 0:
                return {'remainingProcesses': 0, 'totalOwnedProcesses': accounting.total}
            if time.monotonic() > deadline:
                raise RuntimeError('Private Chromium processes survived termination')
            time.sleep(0.02)
    finally:
        stop_tree(process)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--chrome', type=Path, default=Path('C:/Program Files/Google/Chrome/Application/chrome.exe'))
    parser.add_argument('--linux-runtime', action='store_true', help=argparse.SUPPRESS)
    parser.add_argument('--control-witness', type=Path, help=argparse.SUPPRESS)
    args = parser.parse_args()
    if os.name != 'nt':
        from browser_linux_runtime import admitted_runtime
        if not args.linux_runtime or not admitted_runtime():
            raise SystemExit('Linux real-browser execution requires the private PID/network-namespace launcher')
    elif args.linux_runtime or args.control_witness:
        raise SystemExit('Linux namespace controls are unavailable on Windows')
    if not args.chrome.is_absolute() or not args.chrome.is_file():
        raise SystemExit('Existing Chromium executable required')
    subprocess.run([shutil.which('node'), '--check', 'scripts/browser-real-proof.mjs'], cwd=ROOT, check=True, timeout=10)
    output = guarded_output('browser-real')
    source = source_digest(ROOT)
    report = {'sourceBefore': source, 'platform': 'windows-amd64' if os.name == 'nt' else 'linux-' + os.uname().machine, 'passed': False,
              'liveMonitoredServices': False, 'browserSandboxDisabled': False}
    binary = args.binary.resolve() if args.binary else output / 'canary.exe'
    if not args.binary:
        subprocess.run([str(ROOT / '.canary/tools/go/bin/go.exe'), 'build', '-p=1', '-mod=readonly', '-trimpath',
                        '-o', str(binary), './apps/node-go/cmd/canary'], cwd=ROOT, check=True, timeout=180)
    if not binary.is_file():
        raise SystemExit('Production executable required')
    report['binarySha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
    profile = output / 'chrome-profile'
    profile.mkdir(mode=0o700)
    chrome = start_command([str(args.chrome), '--headless=new', '--remote-debugging-address=127.0.0.1',
        '--remote-debugging-port=0', '--user-data-dir=' + str(profile), '--no-first-run', '--no-default-browser-check',
        '--disable-background-networking', '--disable-component-update', '--disable-sync', '--disable-extensions',
        '--no-proxy-server', '--password-store=basic', 'about:blank'],
        stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, cwd=output,
        env={**{k: os.environ[k] for k in ('SystemRoot', 'WINDIR', 'PATH') if k in os.environ}, 'HOME': str(output), 'TMPDIR': str(output), 'TMP': str(output), 'TEMP': str(output), 'LOCALAPPDATA': str(output / 'local'), 'APPDATA': str(output / 'roaming')})
    diagnostics = bytearray()
    overflow = threading.Event()
    def drain():
        while chunk := chrome.stderr.read(4096):
            remaining = max(0, 65536 - len(diagnostics))
            diagnostics.extend(chunk[:remaining])
            if len(chunk) > remaining:
                overflow.set()
    reader = threading.Thread(target=drain, daemon=True)
    reader.start()
    try:
        deadline = time.monotonic() + 30
        while not (profile / 'DevToolsActivePort').is_file():
            if chrome.poll() is not None or overflow.is_set() or time.monotonic() > deadline:
                raise RuntimeError('Private Chromium did not become ready')
            time.sleep(0.025)
        lines = (profile / 'DevToolsActivePort').read_text().splitlines()
        if len(lines) != 2 or not lines[0].isdecimal() or not 1 <= int(lines[0]) <= 65535 or not lines[1].startswith('/devtools/browser/'):
            raise RuntimeError('Invalid private debugging endpoint')
        origin = 'http://127.0.0.1:' + lines[0]
        opener = build_opener(ProxyHandler({}))
        with opener.open(origin + '/json/version', timeout=2) as response:
            raw = response.read(8193)
        if len(raw) > 8192:
            raise RuntimeError('Browser version reply exceeds limit')
        version = json.loads(raw)
        report['browserVersion'] = version['Browser']
        if args.control_witness:
            if (os.name == 'nt' or args.control_witness.resolve() != ROOT / '.canary/work/current/outputs/bri/control-ready.json'):
                raise RuntimeError('Refusing an unowned control witness')
            args.control_witness.write_text(json.dumps({'ready': True, 'namespace': os.readlink('/proc/self/ns/pid'), 'browserVersion': version['Browser']}) + '\n')
            # The outer GNU timeout kills only the system unshare supervisor.
            # It must kill PID 1 and all private browser descendants too.
            time.sleep(90)
            raise RuntimeError('Supervisor SIGKILL control did not terminate the runtime')
        env = {k: v for k, v in os.environ.items() if not k.startswith('CANARY_') and k != 'NODE_OPTIONS'}
        env.update(CANARY_BIN=str(binary), BROWSER_FIXTURE_ENDPOINT='ws://127.0.0.1:' + lines[0] + lines[1],
                   TMPDIR=str(output), TMP=str(output), TEMP=str(output))
        command = [shutil.which('node'), '--import', 'tsx', '--test', '--test-reporter=tap', '--test-concurrency=1', 'scripts/browser-real-proof.mjs']
        result = subprocess.run(command, cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=140)
        text = result.stdout.decode('utf-8', 'replace')
        if len(result.stdout) > 128 * 1024:
            raise RuntimeError('Browser proof output exceeds limit')
        report['proofOutput'] = text
        print(text, flush=True)
        if result.returncode:
            raise RuntimeError('Actual browser process proof failed')
        report['proofs'] = tap_counts(text)
        if overflow.is_set():
            raise RuntimeError('Private Chromium diagnostics exceed limit')
        report['passed'] = True
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        report['error'] = str(error)
        report['browserDiagnostic'] = bytes(diagnostics[:8192]).decode('utf-8', 'replace')
        raise
    finally:
        report['cleanup'] = stop_private_browser(chrome)
        reader.join(timeout=2)
        report['sourceAfter'] = source_digest(ROOT)
        report['sourceUnchanged'] = report['sourceBefore'] == report['sourceAfter']
        retain_summary('browser-real', report)
        print(json.dumps(report, indent=2), flush=True)
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
