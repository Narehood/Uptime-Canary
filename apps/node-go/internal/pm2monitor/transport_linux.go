//go:build linux

package pm2monitor

import (
	"context"
	"net"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

func dialManager(ctx context.Context, c Config) (net.Conn, error) {
	if Validate(c) != nil || c.Options.Transport != "unix" || ctx.Err() != nil {
		return nil, errConfig
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path.Join(c.Options.Home, "rpc.sock"))
	if err != nil {
		return nil, errReply
	}
	u, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		return nil, errReply
	}
	raw, err := u.SyscallConn()
	var peer *unix.Ucred
	var queryErr error
	if err == nil {
		err = raw.Control(func(fd uintptr) {
			peer, queryErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		})
	}
	if err != nil || queryErr != nil || peer == nil || peer.Uid != uint32(os.Geteuid()) || ctx.Err() != nil {
		conn.Close()
		return nil, errReply
	}
	return conn, nil
}
