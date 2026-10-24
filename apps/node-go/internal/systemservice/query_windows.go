//go:build windows

package systemservice

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

type scmAPI struct {
	openManager func(*uint16, *uint16, uint32) (windows.Handle, error)
	openService func(windows.Handle, *uint16, uint32) (windows.Handle, error)
	query       func(windows.Handle, uint32, *byte, uint32, *uint32) error
	close       func(windows.Handle) error
}

func platformQuery(ctx context.Context, name string) serviceState {
	return querySCM(ctx, name, scmAPI{windows.OpenSCManager, windows.OpenService, windows.QueryServiceStatusEx, windows.CloseServiceHandle})
}

func querySCM(ctx context.Context, name string, api scmAPI) serviceState {
	if ctx.Err() != nil {
		return unknown
	}
	manager, err := api.openManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return unknown
	}
	defer api.close(manager)
	if ctx.Err() != nil {
		return unknown
	}
	encoded, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return unknown
	}
	service, err := api.openService(manager, encoded, windows.SERVICE_QUERY_STATUS)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return missing
	}
	if err != nil {
		return unknown
	}
	defer api.close(service)
	if ctx.Err() != nil {
		return unknown
	}
	var status windows.SERVICE_STATUS_PROCESS
	var needed uint32
	if api.query(service, windows.SC_STATUS_PROCESS_INFO, (*byte)(unsafe.Pointer(&status)), uint32(unsafe.Sizeof(status)), &needed) != nil {
		return unknown
	}
	switch status.CurrentState {
	case windows.SERVICE_RUNNING:
		return running
	case windows.SERVICE_STOPPED, windows.SERVICE_START_PENDING, windows.SERVICE_STOP_PENDING,
		windows.SERVICE_CONTINUE_PENDING, windows.SERVICE_PAUSE_PENDING, windows.SERVICE_PAUSED:
		return stopped
	default:
		return unknown
	}
}
