//go:build !linux && !windows

package pm2monitor

import (
	"context"
	"net"
)

func dialManager(context.Context, Config) (net.Conn, error) { return nil, errReply }
