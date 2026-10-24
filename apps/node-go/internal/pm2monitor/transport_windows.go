//go:build windows

package pm2monitor

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsEndpoint = `\\.\pipe\rpc.sock`

type pipeAddress struct{}

func (pipeAddress) Network() string { return "pipe" }
func (pipeAddress) String() string  { return "local PM2 daemon" }

type windowsPipeConn struct{ *os.File }

func (*windowsPipeConn) LocalAddr() net.Addr  { return pipeAddress{} }
func (*windowsPipeConn) RemoteAddr() net.Addr { return pipeAddress{} }

// Consume the handle on every outcome. On the pinned Go toolchain NewFile
// detects overlapped pipes and installs IOCP; deadline support is mandatory.
// Never call File.Fd after this transfer: that can disassociate the poller.
func newWindowsPipeConn(handle windows.Handle) (net.Conn, error) {
	file := os.NewFile(uintptr(handle), "PM2 connection")
	if file == nil {
		windows.CloseHandle(handle)
		return nil, errReply
	}
	if err := file.SetDeadline(time.Time{}); err != nil {
		file.Close()
		return nil, errReply
	}
	return &windowsPipeConn{file}, nil
}

// Deployment prerequisite: private pipe authority must have been established
// at first creation and retained for the entire lifetime of all instances.
// A current descriptor cannot attest to previously granted server handles.
func privatePipeDescriptor(sd *windows.SECURITY_DESCRIPTOR, expected *windows.SID) error {
	if sd == nil || !sd.IsValid() || expected == nil || !expected.IsValid() {
		return errReply
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(expected) {
		return errReply
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return errReply
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return errReply
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return errReply
	}
	const dangerous = windows.FILE_APPEND_DATA | windows.WRITE_DAC | windows.WRITE_OWNER |
		windows.GENERIC_WRITE | windows.GENERIC_ALL | windows.MAXIMUM_ALLOWED
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, index, &ace) != nil || ace == nil {
			return errReply
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue // Ignoring denies only makes this policy more conservative.
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			return errReply
		}
		const offset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
		if uintptr(ace.Header.AceSize) < offset+8 {
			return errReply
		}
		sidBytes := unsafe.Slice((*byte)(unsafe.Pointer(&ace.SidStart)), int(ace.Header.AceSize)-int(offset))
		if int(sidBytes[1]) > 15 || 8+4*int(sidBytes[1]) > len(sidBytes) {
			return errReply
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() {
			return errReply
		}
		// Only the supported file/generic access masks are interpretable here.
		const fileAllAccess = 0x001f01ff // FILE_ALL_ACCESS from the file access-rights mapping.
		const supported = fileAllAccess | windows.GENERIC_READ | windows.GENERIC_WRITE |
			windows.GENERIC_EXECUTE | windows.GENERIC_ALL
		if uint32(ace.Mask) & ^uint32(supported) != 0 {
			return errReply
		}
		if uint32(ace.Mask)&uint32(dangerous) != 0 && !sid.Equals(expected) && !sid.Equals(system) && !sid.Equals(administrators) {
			return errReply
		}
	}
	return nil
}

func admitWindowsPipe(ctx context.Context, handle windows.Handle, expected *windows.SID) error {
	typ, err := windows.GetFileType(handle)
	var flags uint32
	if err != nil || typ != windows.FILE_TYPE_PIPE || windows.GetNamedPipeInfo(handle, &flags, nil, nil, nil) != nil ||
		flags&windows.PIPE_SERVER_END != 0 || flags&windows.PIPE_TYPE_MESSAGE != 0 || ctx.Err() != nil {
		return errReply
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || privatePipeDescriptor(sd, expected) != nil || ctx.Err() != nil {
		return errReply
	}
	var serverPID uint32
	if windows.GetNamedPipeServerProcessId(handle, &serverPID) != nil || serverPID == 0 {
		return errReply
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, serverPID)
	if err != nil {
		return errReply
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token) != nil {
		return errReply
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	var again uint32
	if err != nil || user.User.Sid == nil || !user.User.Sid.Equals(expected) ||
		windows.GetNamedPipeServerProcessId(handle, &again) != nil || again != serverPID || ctx.Err() != nil {
		return errReply
	}
	return nil
}

func dialManager(ctx context.Context, c Config) (net.Conn, error) {
	return dialWindowsPipe(ctx, c, windowsEndpoint)
}

// The endpoint parameter is private to the transport and native test fixtures;
// no management, peer or worker configuration can select a different pipe.
func dialWindowsPipe(ctx context.Context, c Config, endpoint string) (net.Conn, error) {
	if Validate(c) != nil || c.Options.Transport != "windows-pipe" || ctx.Err() != nil {
		return nil, errConfig
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var threadToken windows.Token
	err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &threadToken)
	if err == nil {
		threadToken.Close()
		return nil, errReply
	}
	if !errors.Is(err, windows.ERROR_NO_TOKEN) {
		return nil, errReply
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user.User.Sid == nil || ctx.Err() != nil {
		return nil, errReply
	}
	name, err := windows.UTF16PtrFromString(endpoint)
	if err != nil {
		return nil, errReply
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
	if err != nil {
		return nil, errReply
	}
	if admitWindowsPipe(ctx, handle, user.User.Sid) != nil {
		windows.CloseHandle(handle)
		return nil, errReply
	}
	return newWindowsPipeConn(handle)
}
