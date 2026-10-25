//go:build windows

package process

import (
	"os/exec"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestTypedWindowsJobsKeepBoundedProcessAndMemoryAuthority(t *testing.T) {
	for _, engine := range []string{"redis", "pm2", "system-service", "tailscale-ping", "Tailscale-ping", ""} {
		t.Run(engine, func(t *testing.T) {
			guard, err := prepareTypedProcess(&exec.Cmd{}, engine)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.close()
			job := guard.(*windowsGuard).job
			var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
			var pin runtime.Pinner
			pin.Pin(&info)
			defer pin.Unpin()
			if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
				t.Fatal(err)
			}
			want := uint32(1)
			if engine == "tailscale-ping" {
				want = 2
			}
			flags := uint32(windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
			if info.BasicLimitInformation.ActiveProcessLimit != want || info.BasicLimitInformation.LimitFlags&flags != flags || info.ProcessMemoryLimit != hardMemoryBytes || info.BasicLimitInformation.LimitFlags&(windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK|windows.JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK) != 0 {
				t.Fatal("worker job authority widened unexpectedly")
			}
			if want == 2 && (info.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_JOB_MEMORY == 0 || info.JobMemoryLimit != hardMemoryBytes) {
				t.Fatal("worker and client lack aggregate memory ceiling")
			}
		})
	}
}
