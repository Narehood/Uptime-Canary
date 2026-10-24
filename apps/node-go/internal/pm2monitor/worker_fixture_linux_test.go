//go:build linux

package pm2monitor

import (
	"context"
	"net"
	"os"
	"path"
	"testing"
	"time"
)

func workerFixtureListener(t *testing.T) (Config, string, func() (net.Conn, error)) {
	t.Helper()
	home, err := os.MkdirTemp("", "cpm2-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(home); err != nil {
			t.Error(err)
		}
	})
	endpoint := path.Join(home, "rpc.sock")
	if len(endpoint) > 107 {
		t.Fatal("owned TMPDIR is too long for a private PM2 socket")
	}
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return Config{Name: "fixture-app", Timeout: 2 * time.Second, Options: Options{Transport: "unix", Home: home}}, endpoint, listener.Accept
}

func workerFixtureDial(ctx context.Context, c Config, endpoint string) (net.Conn, error) {
	if endpoint != path.Join(c.Options.Home, "rpc.sock") {
		return nil, errConfig
	}
	return dialManager(ctx, c)
}
