package services

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func ntpPacket(request []byte, t2, t3 time.Time) []byte {
	p := make([]byte, 48)
	p[0] = 4<<3 | 4
	p[1] = 2
	copy(p[24:32], request[40:48])
	putNTPTime(p[32:], t2)
	putNTPTime(p[40:], t3)
	return p
}
func ntpFixture(t *testing.T, change func([]byte) []byte) string {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var b [512]byte
		n, a, e := c.ReadFrom(b[:])
		if e != nil {
			return
		}
		if n != 48 || b[0] != 4<<3|3 || binary.BigEndian.Uint64(b[40:48]) == 0 {
			t.Error("invalid NTP request")
			return
		}
		now := time.Now()
		p := change(ntpPacket(b[:n], now, now))
		if p != nil {
			_, _ = c.WriteTo(p, a)
		}
	}()
	t.Cleanup(func() { c.Close(); <-done })
	return c.LocalAddr().String()
}
func TestNTPProtocol(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]byte) []byte
		state  string
	}{
		{"valid", func(p []byte) []byte { return p }, "up"},
		{"v3", func(p []byte) []byte { p[0] = 3<<3 | 4; return p }, "up"},
		{"short", func(p []byte) []byte { return p[:47] }, "down"},
		{"oversize", func(p []byte) []byte { return make([]byte, 1024) }, "down"},
		{"origin", func(p []byte) []byte { p[24] ^= 1; return p }, "down"},
		{"mode", func(p []byte) []byte { p[0] = 4<<3 | 3; return p }, "down"},
		{"version", func(p []byte) []byte { p[0] = 2<<3 | 4; return p }, "down"},
		{"leap", func(p []byte) []byte { p[0] |= 3 << 6; return p }, "down"},
		{"kiss", func(p []byte) []byte { p[1] = 0; return p }, "down"},
		{"unsynchronized", func(p []byte) []byte { p[1] = 16; return p }, "down"},
		{"stratum", func(p []byte) []byte { p[1] = 5; return p }, "down"},
		{"dispersion", func(p []byte) []byte { binary.BigEndian.PutUint32(p[8:], 32768); return p }, "down"},
		{"offset", func(p []byte) []byte {
			putNTPTime(p[32:], time.Now().Add(2*time.Second))
			copy(p[40:], p[32:40])
			return p
		}, "down"},
		{"impossible", func(p []byte) []byte { putNTPTime(p[40:], time.Now().Add(2*time.Second)); return p }, "down"},
		{"zero", func(p []byte) []byte { clear(p[32:]); return p }, "down"},
		{"time-bound", func(p []byte) []byte {
			putNTPTime(p[32:], time.Now().Add(48*time.Hour))
			copy(p[40:], p[32:40])
			return p
		}, "down"},
		{"deadline", func(p []byte) []byte { return nil }, "down"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := ntpFixture(t, tc.change)
			expectState(t, Config{Type: NTP, Timeout: 120 * time.Millisecond, NTP: &NTPConfig{Address: a}}, tc.state)
		})
	}
}
func TestNTPThresholdsEraAndCancellation(t *testing.T) {
	t1 := time.Date(2036, 2, 8, 0, 0, 0, 0, time.UTC)
	t4 := t1.Add(100 * time.Millisecond)
	var req [48]byte
	putNTPTime(req[40:], t1)
	p := ntpPacket(req[:], t1.Add(30*time.Millisecond), t1.Add(30*time.Millisecond))
	if r := evaluateNTP(p, req[40:], t1, t4, NTPConfig{}); r.State != "up" {
		t.Fatal(r)
	}
	for _, c := range []NTPConfig{{OffsetThreshold: time.Millisecond}, {LatencyThreshold: 50 * time.Millisecond}} {
		if r := evaluateNTP(p, req[40:], t1, t4, c); r.State != "down" {
			t.Fatal(r)
		}
	}
	for _, c := range []NTPConfig{{Address: "bad"}, {Address: "127.0.0.1:123", StratumThreshold: 1}, {Address: "127.0.0.1:123", OffsetThreshold: -1}, {Address: "127.0.0.1:123", LatencyThreshold: time.Hour}} {
		if c.validate() == nil {
			t.Fatal("accepted invalid NTP options")
		}
	}
	a := ntpFixture(t, func(p []byte) []byte { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	r := Check(ctx, Config{Type: NTP, Timeout: time.Second, NTP: &NTPConfig{Address: a}})
	if r.Message != "service check canceled" {
		t.Fatal(r)
	}
}
