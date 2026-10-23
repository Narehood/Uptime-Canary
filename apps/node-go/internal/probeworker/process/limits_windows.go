//go:build windows

package process

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsGuard struct{ job windows.Handle }

func prepareProcess(cmd *exec.Cmd) (processGuard, error) {
	job, err := windows.CreateJobObject(nil, nil) // unnamed; handle not inherited
	if err != nil {
		return nil, err
	}
	g := &windowsGuard{job: job}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	var pin runtime.Pinner
	pin.Pin(&info) // x/sys's job APIs accept an opaque uintptr buffer
	defer pin.Unpin()
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
	info.BasicLimitInformation.ActiveProcessLimit = 1
	info.ProcessMemoryLimit = hardMemoryBytes
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		g.close()
		return nil, err
	}
	if err = verifyJob(job); err != nil {
		g.close()
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return g, nil
}

func (g *windowsGuard) attach(p *os.Process) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(g.job, h)
}
func (g *windowsGuard) close() { _ = windows.CloseHandle(g.job) }

func verifyJob(job windows.Handle) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	var pin runtime.Pinner
	pin.Pin(&info)
	defer pin.Unpin()
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return err
	}
	flags := uint32(windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
	if info.BasicLimitInformation.LimitFlags&flags != flags || info.ProcessMemoryLimit == 0 || info.ProcessMemoryLimit > hardMemoryBytes {
		return errProtocol
	}
	return nil
}

func applyChildLimits() error {
	// The parent has assigned the fresh job before it writes any config. A null
	// job handle queries the calling process's immediate job (nested jobs work).
	// The child can be scheduled before AssignProcessToJobObject completes.
	// Wait without reading stdin; the parent's total deadline still kills us.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := verifyJob(0); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errProtocol
		}
		time.Sleep(time.Millisecond)
	}
}
