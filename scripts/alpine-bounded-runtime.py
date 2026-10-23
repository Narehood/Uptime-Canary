"""One diskless Alpine query-worker check; invoke only through bounded_validation.

Everything acquired/built lives in the guard's disposable outputs. No installer
is executed, no virtual disk is created, and no host service is installed.
"""
import ctypes
import hashlib
import http.server
import json
import os
from pathlib import Path
import re
import shutil
import socket
import struct
import subprocess
import tarfile
import threading
import time
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / '.canary/work/current/outputs'
SOURCE = ROOT / '.canary/jsonata-runtime-candidate/r3-intrinsic-20261022T223939Z/source'
GO = ROOT / '.canary/tools/go/bin/go.exe'
MIB = 1024 ** 2
RESULT = ROOT / '.canary/alpine-bounded-result.json'
SERIAL_LOG = ROOT / '.canary/alpine-bounded-runtime.log'
FREE_AT_START = shutil.disk_usage(ROOT).free


def host_headroom(minimum_mib=2048):
    class Memory(ctypes.Structure):
        _fields_ = [('length', ctypes.c_ulong), ('load', ctypes.c_ulong)] + [
            (name, ctypes.c_ulonglong) for name in ('totalPhysical', 'availablePhysical', 'totalCommit',
                                                  'availableCommit', 'totalVirtual', 'availableVirtual', 'reserved')]
    state = Memory()
    state.length = ctypes.sizeof(state)
    if not ctypes.windll.kernel32.GlobalMemoryStatusEx(ctypes.byref(state)):
        raise RuntimeError('Cannot inspect host memory headroom')
    if state.availablePhysical < minimum_mib * MIB or state.availableCommit < minimum_mib * MIB:
        raise RuntimeError(f'Host lacks memory/commit reserve: physical={state.availablePhysical}, commit={state.availableCommit}')
    if FREE_AT_START - shutil.disk_usage(ROOT).free > 700 * MIB:
        raise RuntimeError('Unexpected host volume allocation; refusing further work')


def digest(path, algorithm='sha256'):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, algorithm).hexdigest()


def acquire(url, path, limit, expected=None, algorithm='sha256'):
    host_headroom()
    print('Acquire', path.name, flush=True)
    request = urllib.request.Request(url, headers={'User-Agent': 'Uptime-Canary-local-validation'})
    count = 0
    with urllib.request.urlopen(request, timeout=45) as response, path.open('xb') as stream:
        while chunk := response.read(256 * 1024):
            count += len(chunk)
            if count > limit:
                raise RuntimeError('Acquisition exceeded its reserved size')
            stream.write(chunk)
    actual = digest(path, algorithm)
    if expected and actual != expected:
        raise RuntimeError('Published artifact digest mismatch: ' + path.name)
    return {'file': path.name, 'url': url, 'bytes': count, algorithm: actual}


def command(args, cwd=OUT, timeout=180):
    print('Command', args[0], *args[1:3], flush=True)
    completed = subprocess.run(list(map(str, args)), cwd=cwd, check=True,
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               timeout=timeout, creationflags=subprocess.CREATE_NO_WINDOW)
    if len(completed.stdout) > 2 * MIB:
        raise RuntimeError('Preparation output exceeded bound')
    return completed.stdout.decode('utf-8', 'replace')


def imports(path):
    """Read only PE import names; do not load DLLs to inspect them."""
    data = path.read_bytes()
    pe = struct.unpack_from('<I', data, 0x3c)[0]
    if data[pe:pe + 4] != b'PE\0\0':
        raise RuntimeError('Expected PE executable')
    sections, optional_size = struct.unpack_from('<H', data, pe + 6)[0], struct.unpack_from('<H', data, pe + 20)[0]
    optional = pe + 24
    magic = struct.unpack_from('<H', data, optional)[0]
    directories = optional + (112 if magic == 0x20b else 96)
    ranges = []
    for index in range(sections):
        entry = optional + optional_size + index * 40
        virtual_size, virtual, raw_size, raw = struct.unpack_from('<IIII', data, entry + 8)
        ranges.append((virtual, virtual + max(virtual_size, raw_size), raw))

    def offset(rva):
        for start, end, raw in ranges:
            if start <= rva < end:
                return raw + rva - start
        raise RuntimeError('Invalid PE address')

    def name(rva):
        start = offset(rva)
        end = data.index(b'\0', start, start + 512)
        value = data[start:end].decode('ascii').lower()
        if '/' in value or '\\' in value or not re.fullmatch(r'[a-z0-9_.+-]+\.(dll|drv)', value):
            raise RuntimeError('Invalid import name: ' + repr(value))
        return value

    names = set()
    for slot, stride, name_slot in ((1, 20, 12), (13, 32, 4)):
        rva, length = struct.unpack_from('<II', data, directories + slot * 8)
        if not rva:
            continue
        start = offset(rva)
        for index in range(min(4096, length // stride + 1)):
            entry = start + index * stride
            row = data[entry:entry + stride]
            if row == bytes(stride):
                break
            if slot == 13 and struct.unpack_from('<I', row)[0] != 1:
                raise RuntimeError('Unsupported delay import representation')
            names.add(name(struct.unpack_from('<I', row, name_slot)[0]))
        else:
            raise RuntimeError('Unterminated import table')
    return names


def archive_entries(seven, archive):
    listing = command([seven, 'l', '-slt', archive])
    entries = {}
    for block in listing.replace('\r\n', '\n').split('\n\n'):
        fields = dict(line.split(' = ', 1) for line in block.splitlines() if ' = ' in line)
        value = fields.get('Path', '').replace('\\', '/')
        if not fields.get('Size', '').isdigit() or fields.get('Folder') == '+':
            continue
        if value.startswith('/') or '..' in value.split('/') or ':' in value:
            raise RuntimeError('Unsafe archive entry')
        entries[value] = int(fields['Size'])
    return entries


def extract(seven, archive, destination, names):
    destination.mkdir(exist_ok=True)
    command([seven, 'x', '-y', '-bd', '-bso0', '-bsp0', '-o' + str(destination), archive, *names])


def prepare(result):
    assets = result['assets']
    integrity = {item['name']: item for item in json.loads((ROOT / '.canary/alpine-validation/integrity.json').read_text())}
    for name, url in (
        ('7zr.exe', 'https://github.com/ip7z/7zip/releases/download/26.05/7zr.exe'),
        ('7z2605-x64.exe', 'https://github.com/ip7z/7zip/releases/download/26.05/7z2605-x64.exe'),
        ('qemu-w64-setup-20260811.exe', 'https://qemu.weilnetz.de/w64/qemu-w64-setup-20260811.exe'),
    ):
        item = integrity[name]
        assets.append(acquire(url, OUT / name, 200 * MIB if name.startswith('qemu') else 3 * MIB,
                              item['digest'], item['algorithm']))
    command([OUT / '7zr.exe', 'x', '-y', '-o' + str(OUT / '7zip'), OUT / '7z2605-x64.exe', '7z.exe', '7z.dll'])
    seven, installer = OUT / '7zip/7z.exe', OUT / 'qemu-w64-setup-20260811.exe'
    entries = archive_entries(seven, installer)
    by_name = {Path(name).name.lower(): name for name in entries if name.lower().endswith('.dll')}
    runtime = OUT / 'qemu'
    queue = ['qemu-system-x86_64.exe']
    selected = set()
    while queue:
        names = sorted(set(queue) - selected)
        queue = []
        if not names:
            break
        missing = set(names) - entries.keys()
        if missing:
            raise RuntimeError('Missing archive member: ' + ', '.join(sorted(missing)))
        if sum(entries[name] for name in selected | set(names)) > 128 * MIB:
            raise RuntimeError('Minimal QEMU dependency closure exceeds 128 MiB')
        extract(seven, installer, runtime, names)
        selected.update(names)
        for name in names:
            for dependency in imports(runtime / name):
                if dependency in by_name:
                    queue.append(by_name[dependency])
                elif dependency.startswith(('api-ms-', 'ext-ms-')) or (Path(os.environ['SystemRoot']) / 'System32' / dependency).is_file():
                    continue
                else:
                    raise RuntimeError('Missing QEMU dependency: ' + dependency)
    firmware = [name for name in entries if Path(name).name in ('bios-256k.bin', 'linuxboot_dma.bin', 'linuxboot.bin', 'kvmvapic.bin')]
    result['firmwareInventory'] = {name: count for name, count in entries.items() if re.search(r'(bios|linuxboot|kvmvapic)', name)}
    print('Firmware subset', firmware, flush=True)
    if len([name for name in firmware if name.endswith('bios-256k.bin')]) != 1:
        raise RuntimeError('Expected one BIOS image; inventory: ' + str(result['firmwareInventory']))
    extract(seven, installer, runtime, firmware)
    selected.update(firmware)
    result['qemuFiles'] = [{'file': name, 'bytes': (runtime / name).stat().st_size, 'sha256': digest(runtime / name)} for name in sorted(selected)]
    print('Minimal QEMU runtime bytes', sum(item['bytes'] for item in result['qemuFiles']), flush=True)
    if sum(item['bytes'] for item in result['qemuFiles']) > 128 * MIB:
        raise RuntimeError('QEMU runtime plus firmware exceeded reservation')
    installer.unlink()  # no overlap with the ISO acquisition
    iso = OUT / 'alpine-virt-3.24.3-x86_64.iso'
    item = integrity[iso.name]
    assets.append(acquire('https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/x86_64/' + iso.name,
                          iso, 96 * MIB, item['digest']))
    extract(seven, iso, OUT / 'alpine', ['boot/vmlinuz-virt', 'boot/initramfs-virt'])
    if sum(path.stat().st_size for path in (OUT / 'alpine/boot').iterdir()) > 32 * MIB:
        raise RuntimeError('Kernel/initramfs exceeded reservation')
    apks = []
    for name in ('setpriv-2.42.3-r1.apk', 'libcap-ng-0.8.5-r2.apk', 'libeconf-0.8.3-r0.apk'):
        path = OUT / name
        assets.append(acquire('https://dl-cdn.alpinelinux.org/alpine/v3.24/main/x86_64/' + name, path, 700 * 1024))
        apks.append(path)
    bios = next(runtime / name for name in firmware if name.endswith('bios-256k.bin'))
    return runtime, bios, iso, apks


class Serial:
    def __init__(self, port, process):
        self.process = process
        deadline = time.monotonic() + 30
        while True:
            try:
                self.socket = socket.create_connection(('127.0.0.1', port), timeout=1)
                self.socket.settimeout(1)
                break
            except OSError:
                if time.monotonic() >= deadline or process.poll() is not None:
                    raise RuntimeError('QEMU serial endpoint unavailable')
        self.total = 0
        self.evidence = bytearray()

    def until(self, marker, seconds):
        output = bytearray()
        deadline = time.monotonic() + seconds
        while marker not in output:
            host_headroom()
            if time.monotonic() > deadline or self.process.poll() is not None:
                raise RuntimeError('Guest stopped or serial wait expired: ' + marker.decode())
            try:
                chunk = self.socket.recv(16384)
            except socket.timeout:
                continue
            if not chunk:
                raise RuntimeError('Serial connection closed')
            self.total += len(chunk)
            if self.total > 1500 * 1024:
                raise RuntimeError('Serial evidence exceeded bound')
            self.evidence.extend(chunk)
            output.extend(chunk)
            print(chunk.decode('utf-8', 'replace'), end='', flush=True)
        text = output.decode('utf-8', 'replace')
        return text

    def run(self, text, seconds=60):
        # Split completion marker so terminal echo cannot satisfy it.
        token = 'UC_END_' + os.urandom(6).hex()
        self.socket.sendall(('( ' + text + " ); uc_status=$?; printf '\\nUC_END_%s:%s\\n' '" + token[7:] + "' \"$uc_status\"\n").encode())
        output = self.until((token + ':').encode(), seconds)
        match = re.search(re.escape(token) + r':(\d+)', output)
        if not match:
            output += self.until(b'\n', 5)
            match = re.search(re.escape(token) + r':(\d+)', output)
        if not match or match.group(1) != '0':
            raise RuntimeError('Guest command failed')
        return output


def save_serial_log(serial, result):
    """Persist before guard cleanup or a following command truncates latest.log."""
    if serial is not None:
        SERIAL_LOG.write_bytes(serial.evidence)
        result['serialLogBytes'] = len(serial.evidence)
        result['serialLogSHA256'] = digest(SERIAL_LOG)
        result['serialLogTruncated'] = serial.total > len(serial.evidence)


def main(first_selector=0):
    if not isinstance(first_selector, int) or not 0 <= first_selector < 7:
        raise ValueError('Select a nonempty suffix of the seven fixed checks')
    if not (ROOT / '.canary/bounded-validation.lock').is_file() or Path(os.environ.get('GOTMPDIR', '')).parent != OUT.parent:
        raise RuntimeError('Run only inside bounded_validation.py')
    result = {'passed': False, 'assets': [], 'scope': 'diskless Alpine native runtime; no Docker claim', 'tests': [],
              'firstSelector': first_selector}
    process = server = serial = None
    try:
        host_headroom()
        env = dict(os.environ, GOOS='linux', GOARCH='amd64')
        print('Build one static query executable', flush=True)
        subprocess.run([str(GO), 'test', '-mod=readonly', '-c', '-o', str(OUT / 'query.test'), './apps/node-go/internal/jsonquery'],
                       cwd=SOURCE, env=env, check=True, timeout=300, creationflags=subprocess.CREATE_NO_WINDOW)
        result['querySha256'] = digest(OUT / 'query.test')
        runtime, bios, iso, apks = prepare(result)
        # A one-request stream serves only explicitly named disposable inputs.
        files = [(OUT / 'query.test', 'query.test'), *[(p, p.name) for p in apks]]
        fixture = SOURCE / 'apps/node-go/internal/jsonquery/testdata'
        files.extend((p, 'testdata/' + p.relative_to(fixture).as_posix()) for p in fixture.rglob('*') if p.is_file())
        result['payload'] = [{'file': name, 'bytes': p.stat().st_size, 'sha256': digest(p)} for p, name in files]

        class Payload(http.server.BaseHTTPRequestHandler):
            served = False
            def do_GET(self):
                if self.path != '/payload' or Payload.served:
                    self.send_error(404)
                    return
                Payload.served = True
                self.send_response(200)
                self.send_header('Connection', 'close')
                self.end_headers()
                with tarfile.open(fileobj=self.wfile, mode='w|') as archive:
                    for path, name in files:
                        archive.add(path, arcname=name, recursive=False)
            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(('127.0.0.1', 0), Payload)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            serial_port = probe.getsockname()[1]
        args = [runtime / 'qemu-system-x86_64.exe', '-machine', 'pc', '-accel', 'tcg,thread=multi',
                '-cpu', 'max', '-smp', '2', '-m', '1024', '-nodefaults', '-display', 'none', '-vga', 'none',
                '-monitor', 'none', '-no-reboot', '-L', bios.parent, '-bios', bios,
                '-serial', f'tcp:127.0.0.1:{serial_port},server=on,wait=off',
                '-netdev', f'user,id=n0,restrict=on,ipv6=off,guestfwd=tcp:10.0.2.100:8080-tcp:127.0.0.1:{server.server_port}',
                '-device', 'virtio-net-pci,netdev=n0,romfile=',
                '-drive', f'file={iso},format=raw,media=cdrom,readonly=on',
                '-kernel', OUT / 'alpine/boot/vmlinuz-virt', '-initrd', OUT / 'alpine/boot/initramfs-virt',
                '-append', 'console=ttyS0,115200 modules=loop,squashfs,sd-mod,usb-storage']
        result['qemuArgv'] = list(map(str, args))
        host_headroom(3072)  # one guest GiB plus two GiB retained for the host
        print('Boot diskless Alpine (no virtual disk)', flush=True)
        # Windows QEMU rejects dump-guest-core=off. Suppress Windows Error
        # Reporting for this child through inherited process state instead;
        # no registry, system policy or persistent machine setting is changed.
        prior_error_mode = ctypes.windll.kernel32.SetErrorMode(0x8003)
        try:
            process = subprocess.Popen(list(map(str, args)), cwd=runtime, stdout=subprocess.DEVNULL,
                                       stderr=subprocess.PIPE, creationflags=subprocess.CREATE_NO_WINDOW)
        finally:
            ctypes.windll.kernel32.SetErrorMode(prior_error_mode)
        serial = Serial(serial_port, process)
        serial.until(b'login:', 180)
        serial.socket.sendall(b'root\n')
        serial.until(b'#', 30)
        serial.run('stty -echo; ulimit -c 0', 15)
        serial.run('set -e; ip link set lo up; ip link set eth0 up; udhcpc -i eth0 -q -n -t 3 -T 2; ip addr show eth0', 20)
        serial.run('set -e; mkdir /payload; cd /payload; wget -T 20 -qO- http://10.0.2.100:8080/payload | tar -xf -', 45)
        checks = '; '.join(f"echo '{item['sha256']}  /payload/{item['file']}' | sha256sum -c -" for item in result['payload'])
        serial.run('set -e; ' + checks, 60)
        serial.run('set -e; apk --no-network --no-progress --force-non-repository add /payload/*.apk; chmod 555 /payload/query.test; uname -a; cat /etc/alpine-release; umask; /bin/setpriv --version', 60)
        # A private mount namespace makes the root and input tree read-only.
        guest = '''set -eu
mount --make-rprivate /
mkdir /sandbox
mount -t tmpfs -o size=8m tmpfs /sandbox
mkdir -p /sandbox/bin /sandbox/usr /sandbox/lib /sandbox/etc /sandbox/payload /sandbox/proc /sandbox/dev /sandbox/tmp
for d in bin usr lib etc payload; do mount --bind /$d /sandbox/$d; mount -o remount,bind,ro /sandbox/$d; done
mount -t proc proc /sandbox/proc
mount -t tmpfs -o size=1m tmpfs /sandbox/dev
for d in null zero random urandom; do touch /sandbox/dev/$d; mount --bind /dev/$d /sandbox/dev/$d; done
mount -t tmpfs -o size=512m,mode=1777,nosuid,nodev tmpfs /sandbox/tmp
mount -o remount,ro /sandbox
chroot /sandbox /bin/setpriv --reuid=65534 --regid=65534 --clear-groups --no-new-privs --bounding-set=-all --inh-caps=-all --ambient-caps=-all /bin/sh /payload/run.sh
'''
        # Finite boundary/safety cases that failed C1/R2, plus independent R3
        # worker controls. No expected legacy fixture mismatches are selected.
        selectors = [
            '^TestMatcherProjectionReviewReference$/^finite_controls$/^exact_visit_budget_document$',
            '^TestNumericConversionBoundsAndPrivacy$', '^TestResourceBoundsAndRecovery$', '^TestMaximumDocumentThroughWorker$',
            '^TestJSCandidateReviewContainerBounds$', '^TestJSCandidateReviewWorker$/^range_exact$',
            '^TestNativeRuntimeReview(Procedures|NumericStrings|BoundaryIsolation)$',
        ]
        selectors = selectors[first_selector:]
        result['plannedTests'] = selectors
        run_lines = ["set -eu", "cd /payload", "id", "umask", "grep -E '^(Cap|NoNewPrivs)' /proc/self/status",
                     "test $(id -u) = 65534", "test $(awk '/CapBnd/{print $2}' /proc/self/status) = 0000000000000000",
                     "test $(awk '/NoNewPrivs/{print $2}' /proc/self/status) = 1", "test ! -w /payload/query.test", "test ! -w /etc"]
        for index, selector in enumerate(selectors, start=first_selector):
            run_lines.append(f"printf '\\nSELECTOR {index}\\n'")
            run_lines.append(f"GOMAXPROCS=2 ./query.test -test.run '{selector}' -test.v -test.count=1 -test.timeout=90s")
        run_lines.append("printf '\\nALL_SELECTED_PASSED\\n'")
        for name, text in (('isolate.sh', guest), ('run.sh', '\n'.join(run_lines) + '\n')):
            # Fixed script text has no single quotes needing shell expansion.
            import base64
            encoded = base64.b64encode(text.encode()).decode()
            serial.run(f"echo {encoded} | base64 -d > /payload/{name}; chmod 444 /payload/{name}")
        output = serial.run('unshare -m /bin/sh /payload/isolate.sh', 420)
        result['tests'] = selectors
        result['passingEvents'] = len(re.findall(r'^\s*--- PASS:', output, re.M))
        if 'ALL_SELECTED_PASSED' not in output or result['passingEvents'] == 0:
            raise RuntimeError('Missing completion evidence')
        result['passed'] = True
    except Exception as error:
        result['error'] = str(error)
        if isinstance(error, subprocess.CalledProcessError):
            result['commandOutput'] = (error.stdout or b'')[-8000:].decode('utf-8', 'replace')
        raise
    finally:
        if serial:
            try:
                serial.socket.sendall(b'poweroff -f\n')
                process.wait(timeout=8)
            except (OSError, subprocess.TimeoutExpired):
                pass
            serial.socket.close()
        if process and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        if process and process.stderr:
            result['qemuStderr'] = process.stderr.read(8192).decode('utf-8', 'replace')
            if result['qemuStderr']:
                print(result['qemuStderr'], flush=True)
        if server:
            server.shutdown()
            server.server_close()
        result['vmStopped'] = process is None or process.poll() is not None
        save_serial_log(serial, result)
        RESULT.write_text(json.dumps(result, indent=2))
        print(json.dumps({'passed': result['passed'], 'error': result.get('error'), 'vmStopped': result['vmStopped']}), flush=True)


if __name__ == '__main__':
    main()
