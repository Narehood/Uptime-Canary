//go:build windows

package systemservice

import (
	"context"
	"reflect"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestSystemServiceSCMUsesOnlyLocalQueryRightsAndClosesHandles(t *testing.T) {
	for _, state := range []uint32{windows.SERVICE_RUNNING, windows.SERVICE_STOPPED, windows.SERVICE_START_PENDING,
		windows.SERVICE_STOP_PENDING, windows.SERVICE_CONTINUE_PENDING, windows.SERVICE_PAUSE_PENDING, windows.SERVICE_PAUSED, 0, 99} {
		var closed []windows.Handle
		api := scmAPI{
			openManager: func(machine, database *uint16, access uint32) (windows.Handle, error) {
				if machine != nil || database != nil || access != windows.SC_MANAGER_CONNECT {
					t.Fatal("unexpected manager access")
				}
				return 1, nil
			},
			openService: func(manager windows.Handle, name *uint16, access uint32) (windows.Handle, error) {
				if manager != 1 || windows.UTF16PtrToString(name) != "fixture-service" || access != windows.SERVICE_QUERY_STATUS {
					t.Fatal("unexpected service access")
				}
				return 2, nil
			},
			query: func(service windows.Handle, level uint32, buffer *byte, size uint32, needed *uint32) error {
				if service != 2 || level != windows.SC_STATUS_PROCESS_INFO || size != uint32(unsafe.Sizeof(windows.SERVICE_STATUS_PROCESS{})) {
					t.Fatal("unexpected query")
				}
				(*windows.SERVICE_STATUS_PROCESS)(unsafe.Pointer(buffer)).CurrentState = state
				return nil
			},
			close: func(handle windows.Handle) error { closed = append(closed, handle); return nil },
		}
		expected := stopped
		if state == windows.SERVICE_RUNNING {
			expected = running
		}
		if state == 0 || state == 99 {
			expected = unknown
		}
		if result := querySCM(context.Background(), "fixture-service", api); result != expected || !reflect.DeepEqual(closed, []windows.Handle{2, 1}) {
			t.Fatal(state, result, closed)
		}
	}
}

func TestSystemServiceSCMPermissionMissingAndInterruptedQueries(t *testing.T) {
	for _, stage := range []string{"manager", "service-denied", "service-missing", "query"} {
		var closed []windows.Handle
		api := scmAPI{
			openManager: func(*uint16, *uint16, uint32) (windows.Handle, error) {
				if stage == "manager" {
					return 0, windows.ERROR_ACCESS_DENIED
				}
				return 1, nil
			},
			openService: func(windows.Handle, *uint16, uint32) (windows.Handle, error) {
				if stage == "service-denied" {
					return 0, windows.ERROR_ACCESS_DENIED
				}
				if stage == "service-missing" {
					return 0, windows.ERROR_SERVICE_DOES_NOT_EXIST
				}
				return 2, nil
			},
			query: func(windows.Handle, uint32, *byte, uint32, *uint32) error { return windows.ERROR_ACCESS_DENIED },
			close: func(handle windows.Handle) error { closed = append(closed, handle); return nil },
		}
		expected := unknown
		if stage == "service-missing" {
			expected = missing
		}
		if result := querySCM(context.Background(), "fixture", api); result != expected {
			t.Fatal(stage, result)
		}
		count := 1
		if stage == "manager" {
			count = 0
		}
		if stage == "query" {
			count = 2
		}
		if len(closed) != count {
			t.Fatal(stage, closed)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if querySCM(ctx, "fixture", scmAPI{}) != unknown {
		t.Fatal("canceled manager query")
	}
}
