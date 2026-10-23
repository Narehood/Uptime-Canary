package services

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRedisActualRESPAuthenticationTLS(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprint(encrypted), func(t *testing.T) {
			tc, trust := testTLS(t)
			address := fixture(t, func(raw net.Conn) error {
				var c net.Conn = raw
				if encrypted {
					c = tls.Server(raw, tc)
				}
				r := bufio.NewReader(c)
				for _, s := range []struct{ request, reply string }{
					{"*3\r\n$4\r\nAUTH\r\n$7\r\nmonitor\r\n$14\r\nfixture-secret\r\n", "+OK\r\n"},
					{"*2\r\n$6\r\nSELECT\r\n$1\r\n2\r\n", "+OK\r\n"}, {"*1\r\n$4\r\nPING\r\n", "+PONG\r\n"},
				} {
					b := make([]byte, len(s.request))
					if _, err := io.ReadFull(r, b); err != nil {
						return err
					}
					if string(b) != s.request {
						return fmt.Errorf("unexpected RESP command")
					}
					if _, err := io.WriteString(c, s.reply); err != nil {
						return err
					}
				}
				return nil
			})
			c := &RedisConfig{Address: address, Username: "monitor", Password: "fixture-secret", Database: 2, TLSMode: "plain"}
			if encrypted {
				c.TLSMode = "tls"
				c.TLS = trust
			}
			expectState(t, Config{Type: Redis, Timeout: time.Second, Redis: c}, "up")
		})
	}
}
func TestRedisRepliesAndTrust(t *testing.T) {
	for _, reply := range []string{"-NOAUTH fixture-secret\r\n", "+NOTPONG\r\n", "$4\r\nPONG\r\n", "+PONG\n", strings.Repeat("x", 5000)} {
		t.Run(fmt.Sprint(len(reply), reply[:1]), func(t *testing.T) {
			address := fixture(t, func(c net.Conn) error {
				b := make([]byte, 14)
				if _, e := io.ReadFull(c, b); e != nil {
					return e
				}
				_, _ = io.WriteString(c, reply)
				return nil
			})
			expectState(t, Config{Type: Redis, Timeout: time.Second, Redis: &RedisConfig{Address: address, TLSMode: "plain"}}, "down")
		})
	}
	tc, _ := testTLS(t)
	address := fixture(t, func(c net.Conn) error { _ = tls.Server(c, tc).Handshake(); return nil })
	expectState(t, Config{Type: Redis, Timeout: time.Second, Redis: &RedisConfig{Address: address}}, "down")
}
