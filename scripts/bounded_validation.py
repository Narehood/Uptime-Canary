"""Run one validation command with a shared cache and bounded disposable storage.

No source snapshots, VM images, per-task caches, or retained binary collections.
The command must write disposable outputs below .canary/work/current/outputs.
Only the cache owned by this runner and its own scratch folders are reclaimed.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import stat
import subprocess
import sys
import threading
import time

from validation_paths import filesystem_path

MIB = 1024 ** 2
ROOT = Path(__file__).resolve().parent.parent
GENERATED = ROOT / '.canary'
WORK = GENERATED / 'work' / 'current'
CACHE = GENERATED / 'cache' / 'go-build'
LOCK = GENERATED / 'bounded-validation.lock'
PROJECT_LIMIT = 3 * 1024 * MIB
IDLE_LIMIT = 1280 * MIB
CACHE_TARGET = 256 * MIB
SCRATCH_LIMIT = 512 * MIB
MODULE_GROWTH_LIMIT = 256 * MIB
LOG_LIMIT = 2 * MIB
SOURCE_SUFFIXES = ('.go', '.js', '.mjs', '.cjs', '.ts', '.tsx', '.css', '.html',
                   '.py', '.json', '.mod', '.sum', '.yaml', '.yml', '.toml',
                   '.conf', '.sh', '.ps1', '.cmd', '.bat')


class StorageError(RuntimeError):
    pass


def files_below(path):
    """Do not follow links/reparse points, including Windows junctions."""
    pending = [filesystem_path(path)]
    while pending:
        directory = pending.pop()
        try:
            with os.scandir(directory) as entries:
                for entry in entries:
                    try:
                        info = entry.stat(follow_symlinks=False)
                        if entry.is_symlink() or getattr(info, 'st_file_attributes', 0) & 0x400:
                            continue
                        if stat.S_ISDIR(info.st_mode):
                            pending.append(entry.path)
                        elif stat.S_ISREG(info.st_mode):
                            yield Path(entry.path), info
                    except FileNotFoundError:
                        continue
        except FileNotFoundError:
            continue


def size(path):
    return sum(info.st_size for _, info in files_below(path))


def source_digest(path):
    digest = hashlib.sha256()
    for folder, directories, names in os.walk(path):
        directories[:] = sorted(d for d in directories if d not in
            ('.canary', '.git', 'node_modules', 'dist', '__pycache__'))
        for name in sorted(names):
            source = Path(folder) / name
            if name.startswith('.env') or not (source.suffix in SOURCE_SUFFIXES or name.startswith('Dockerfile')):
                continue
            if source.is_symlink():
                continue
            digest.update(source.relative_to(path).as_posix().encode())
            digest.update(hashlib.sha256(filesystem_path(source).read_bytes()).digest())
    return digest.hexdigest()


def owned_path(path, parent):
    def normal(value):
        spelling = str(Path(value).resolve())
        if os.name == 'nt' and spelling.startswith('\\\\?\\UNC\\'):
            spelling = '\\\\' + spelling[8:]
        elif os.name == 'nt' and spelling.startswith('\\\\?\\'):
            spelling = spelling[4:]
        return Path(spelling)
    full, allowed = normal(path), normal(parent)
    if full == allowed or not full.is_relative_to(allowed):
        raise StorageError('Refusing cleanup outside its disposable directory')
    return full


def remove_scratch(path):
    full = owned_path(path, WORK)
    if not full.exists():
        return
    # Refuse unexpected links instead of risking a target outside scratch.
    for folder, directories, names in os.walk(filesystem_path(full), followlinks=False):
        for name in directories + names:
            info = os.lstat(os.path.join(folder, name))
            if stat.S_ISLNK(info.st_mode) or getattr(info, 'st_file_attributes', 0) & 0x400:
                raise StorageError('Scratch contains an unexpected link; manual assistance required')
    shutil.rmtree(filesystem_path(full))


def trim_cache():
    entries = sorted(files_below(CACHE), key=lambda item: item[1].st_mtime_ns)
    total = sum(info.st_size for _, info in entries)
    for path, info in entries:
        if total <= CACHE_TARGET:
            break
        # Go's cache metadata at the root is tiny and stays in place.
        if path.parent == filesystem_path(CACHE):
            continue
        owned_path(path, CACHE)
        try:
            path.unlink()
            total -= info.st_size
        except FileNotFoundError:
            pass


def usage(module_path, external_cache):
    return {'projectBytes': size(ROOT), 'scratchBytes': size(WORK / 'tmp') + size(WORK / 'outputs'),
            'cacheBytes': size(CACHE), 'moduleBytes': size(module_path),
            'externalBuildCacheBytes': size(external_cache)}


def breach(current, baseline):
    if current['projectBytes'] > PROJECT_LIMIT:
        return 'Repository exceeded the 3 GiB hard limit'
    if current['scratchBytes'] > SCRATCH_LIMIT:
        return f'Disposable command scratch exceeded {SCRATCH_LIMIT // MIB} MiB'
    if current['moduleBytes'] > baseline['moduleBytes'] + MODULE_GROWTH_LIMIT:
        return 'Shared module-cache growth exceeded 256 MiB'
    if current['externalBuildCacheBytes'] > 16 * MIB:
        return 'A build is writing outside the single owned build cache'
    return None


def stop_tree(process):
    if process.poll() is not None:
        return
    if os.name == 'nt':
        subprocess.run(['taskkill', '/PID', str(process.pid), '/T', '/F'],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
    else:
        os.killpg(process.pid, signal.SIGKILL)
    process.wait()


def main():
    global SCRATCH_LIMIT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cwd', type=Path, default=ROOT)
    parser.add_argument('--timeout', type=int, default=180)
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--scratch-limit-mib', type=int, default=512,
                        help='May lower the scratch budget, never increase it')
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if not 1 <= args.scratch_limit_mib <= 512:
        parser.error('Scratch limit must be between 1 and 512 MiB')
    SCRATCH_LIMIT = args.scratch_limit_mib * MIB
    command = args.command[1:] if args.command[:1] == ['--'] else args.command
    if not args.check and not command:
        parser.error('Supply a command after --')
    cwd = args.cwd.resolve()
    if not cwd.is_relative_to(ROOT):
        parser.error('Command cwd must be inside this repository')
    go = GENERATED / 'tools' / 'go' / 'bin' / ('go.exe' if os.name == 'nt' else 'go')
    env = {k: v for k, v in os.environ.items() if not k.startswith('CANARY_')}
    env.update({'CGO_ENABLED': '0', 'GOTOOLCHAIN': 'local', 'PYTHONUTF8': '1'})
    paths = subprocess.check_output([str(go), 'env', 'GOCACHE', 'GOMODCACHE'], env=env, text=True).splitlines()
    external_cache, module_path = map(Path, paths)
    baseline_path = GENERATED / 'storage-policy-baseline.json'
    current = usage(module_path, external_cache)
    baseline = json.loads(baseline_path.read_text()) if baseline_path.exists() else current
    if not baseline_path.exists():
        baseline_path.write_text(json.dumps(baseline, indent=2))
    if args.check:
        print(json.dumps({'usage': current, 'error': breach(current, baseline),
                          'projectLimitBytes': PROJECT_LIMIT, 'idleTargetBytes': IDLE_LIMIT}, indent=2))
        return 2 if breach(current, baseline) else 0
    GENERATED.mkdir(exist_ok=True)
    try:
        lock_fd = os.open(LOCK, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        raise StorageError('Another validation owns the cache; do not start a parallel build')
    os.write(lock_fd, str(os.getpid()).encode())
    os.close(lock_fd)
    result = {'command': command, 'cwd': str(cwd), 'before': current,
              'sourceBefore': source_digest(cwd), 'peakObserved': dict(current)}
    process = None
    reader = None
    start = time.monotonic()
    try:
        remove_scratch(WORK / 'tmp')
        remove_scratch(WORK / 'outputs')
        trim_cache()
        problem = breach(usage(module_path, external_cache), baseline)
        if problem:
            raise StorageError(problem)
        for directory in (CACHE, WORK / 'tmp', WORK / 'outputs'):
            directory.mkdir(parents=True, exist_ok=True)
        env.update({'GOCACHE': str(CACHE), 'GOMODCACHE': str(module_path),
                    'GOTMPDIR': str(WORK / 'tmp'), 'TMP': str(WORK / 'tmp'),
                    'TEMP': str(WORK / 'tmp'), 'GOFLAGS': '-p=1 -mod=readonly'})
        options = {'creationflags': subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.CREATE_NO_WINDOW} if os.name == 'nt' else {'start_new_session': True}
        process = subprocess.Popen(command, cwd=str(cwd), env=env, stdout=subprocess.PIPE,
                                   stderr=subprocess.STDOUT, **options)
        log_path = WORK / 'latest.log'
        result['outputBytes'] = 0

        def collect():
            with log_path.open('wb') as log:
                while chunk := process.stdout.read1(16384):
                    available = max(0, LOG_LIMIT - result['outputBytes'])
                    log.write(chunk[:available])
                    log.flush()
                    result['outputBytes'] += len(chunk)
        reader = threading.Thread(target=collect, daemon=True)
        reader.start()
        while process.poll() is None:
            current = usage(module_path, external_cache)
            for field, count in current.items():
                result['peakObserved'][field] = max(count, result['peakObserved'][field])
            problem = breach(current, baseline)
            if problem or time.monotonic() - start > args.timeout:
                result['stopped'] = problem or 'Command timeout'
                stop_tree(process)
                break
            time.sleep(2)
        process.wait()
        reader.join(timeout=5)
        if reader.is_alive():
            raise StorageError('Command left an open output pipe; do not start another build')
        result['exitCode'] = process.returncode
        result['logTruncated'] = result['outputBytes'] > LOG_LIMIT
    except (StorageError, OSError) as error:
        result['error'] = str(error)
    finally:
        if process is not None:
            stop_tree(process)
        try:
            remove_scratch(WORK / 'tmp')
            remove_scratch(WORK / 'outputs')
            trim_cache()
        except (StorageError, OSError) as error:
            result['cleanupError'] = str(error)
        result['after'] = usage(module_path, external_cache)
        result['sourceFingerprintVersion'] = 2
        result['sourceSuffixes'] = SOURCE_SUFFIXES
        result['sourceNamedFiles'] = 'Dockerfile*; .env* excluded; generated directories skipped'
        result['sourceAfter'] = source_digest(cwd)
        result['sourceUnchanged'] = result['sourceBefore'] == result['sourceAfter']
        result['seconds'] = round(time.monotonic() - start, 3)
        result['pauseRequired'] = bool(result.get('cleanupError') or
            breach(result['after'], baseline) or result['after']['projectBytes'] > IDLE_LIMIT)
        WORK.mkdir(parents=True, exist_ok=True)
        (WORK / 'latest.json').write_text(json.dumps(result, indent=2))
        # Keep twelve small results, never a collection of source/binary copies.
        history_path = GENERATED / 'bounded-checks.json'
        history = json.loads(history_path.read_text()) if history_path.exists() else []
        history_path.write_text(json.dumps((history + [result])[-12:], indent=2))
        LOCK.unlink()
    print(json.dumps(result, indent=2))
    return 2 if result.get('error') or result.get('stopped') or result['pauseRequired'] or not result['sourceUnchanged'] else result.get('exitCode', 1)


if __name__ == '__main__':
    sys.exit(main())
