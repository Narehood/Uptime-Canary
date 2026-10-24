//go:build !linux && !windows

package pm2monitor

import (
	"context"
	"net"
	"testing"
)

func workerFixtureListener(t *testing.T) (Config, string, func() (net.Conn, error)) {
	t.Skip("native PM2 transport unavailable")
	return Config{}, "", nil
}
func workerFixtureDial(context.Context, Config, string) (net.Conn, error) { return nil, errReply }
