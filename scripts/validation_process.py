"""Own validation descendants until cleanup, even after their parent exits."""
import os
from pathlib import Path
import signal
import subprocess
import sys
import uuid


def _windows_api():
    import ctypes
    from ctypes import wintypes

    kernel = ctypes.WinDLL('kernel32', use_last_error=True)
    kernel.CreateJobObjectW.argtypes = [ctypes.c_void_p, wintypes.LPCWSTR]
    kernel.CreateJobObjectW.restype = wintypes.HANDLE
    kernel.OpenJobObjectW.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.LPCWSTR]
    kernel.OpenJobObjectW.restype = wintypes.HANDLE
    kernel.SetInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD]
    kernel.SetInformationJobObject.restype = wintypes.BOOL
    kernel.AssignProcessToJobObject.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
    kernel.AssignProcessToJobObject.restype = wintypes.BOOL
    kernel.GetCurrentProcess.restype = wintypes.HANDLE
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel.CloseHandle.restype = wintypes.BOOL
    return kernel


class _WindowsJob:
    def __init__(self):
        import ctypes
        from ctypes import wintypes

        class BasicLimits(ctypes.Structure):
            _fields_ = [('process_time', ctypes.c_int64), ('job_time', ctypes.c_int64),
                        ('flags', wintypes.DWORD), ('minimum_working_set', ctypes.c_size_t),
                        ('maximum_working_set', ctypes.c_size_t), ('active_processes', wintypes.DWORD),
                        ('affinity', ctypes.c_size_t), ('priority', wintypes.DWORD),
                        ('scheduling', wintypes.DWORD)]

        class ExtendedLimits(ctypes.Structure):
            _fields_ = [('basic', BasicLimits), ('io_counters', ctypes.c_uint64 * 6),
                        ('process_memory', ctypes.c_size_t), ('job_memory', ctypes.c_size_t),
                        ('peak_process_memory', ctypes.c_size_t), ('peak_job_memory', ctypes.c_size_t)]

        self.kernel = _windows_api()
        self.name = 'Local\\UptimeCanaryValidation-' + uuid.uuid4().hex
        self.handle = self.kernel.CreateJobObjectW(None, self.name)
        if not self.handle:
            raise ctypes.WinError(ctypes.get_last_error())
        limits = ExtendedLimits()
        limits.basic.flags = 0x2000  # JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        if not self.kernel.SetInformationJobObject(self.handle, 9, ctypes.byref(limits), ctypes.sizeof(limits)):
            error = ctypes.WinError(ctypes.get_last_error())
            self.close()
            raise error

    def close(self):
        if self.handle:
            import ctypes
            if not self.kernel.CloseHandle(self.handle):
                raise ctypes.WinError(ctypes.get_last_error())
            self.handle = None


def _attach_job(name):
    import ctypes
    kernel = _windows_api()
    handle = kernel.OpenJobObjectW(1, False, name)  # JOB_OBJECT_ASSIGN_PROCESS
    if not handle:
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        if not kernel.AssignProcessToJobObject(handle, kernel.GetCurrentProcess()):
            raise ctypes.WinError(ctypes.get_last_error())
    finally:
        kernel.CloseHandle(handle)


def start_command(command, **options):
    if os.name != 'nt':
        return subprocess.Popen(command, start_new_session=True, **options)
    job = _WindowsJob()
    try:
        # The bootstrap joins before starting the command. Assigning an already
        # running command would let a fast parent escape with an unowned child.
        process = subprocess.Popen(
            [sys.executable, '-B', str(Path(__file__).resolve()), '--job', job.name, '--', *command],
            creationflags=subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.CREATE_NO_WINDOW,
            **options)
    except BaseException:
        job.close()
        raise
    process._validation_job = job
    return process


def stop_tree(process):
    job = getattr(process, '_validation_job', None)
    if job is not None:
        job.close()
    elif os.name != 'nt':
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    elif process.poll() is None:
        subprocess.run(['taskkill', '/PID', str(process.pid), '/T', '/F'],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False,
                       creationflags=subprocess.CREATE_NO_WINDOW)
    process.wait()


if __name__ == '__main__':
    if os.name != 'nt' or len(sys.argv) < 5 or sys.argv[1] != '--job' or sys.argv[3] != '--':
        raise SystemExit('This bootstrap is owned by bounded_validation.py')
    _attach_job(sys.argv[2])
    raise SystemExit(subprocess.call(sys.argv[4:], stdin=sys.stdin, stdout=sys.stdout,
                                    stderr=sys.stderr, creationflags=subprocess.CREATE_NO_WINDOW))
