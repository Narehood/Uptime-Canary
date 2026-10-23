package services

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"time"
)

// NTPConfig uses strict upper thresholds, matching Kuma 2.5.6. Zero selects
// stratum <5, absolute offset <1s, root dispersion <500ms; latency is optional.
type NTPConfig struct {
	Address                 string        `json:"address"`
	StratumThreshold        uint8         `json:"stratumThreshold,omitempty"`
	OffsetThreshold         time.Duration `json:"offsetThreshold,omitempty"`
	RootDispersionThreshold time.Duration `json:"rootDispersionThreshold,omitempty"`
	LatencyThreshold        time.Duration `json:"latencyThreshold,omitempty"`
}

func (c NTPConfig) validate() error {
	if err := validAddress(c.Address); err != nil {
		return err
	}
	if c.StratumThreshold != 0 && (c.StratumThreshold < 2 || c.StratumThreshold > 16) {
		return errors.New("NTP stratum threshold must be 2 through 16")
	}
	if c.OffsetThreshold < 0 || c.OffsetThreshold > 24*time.Hour || c.RootDispersionThreshold < 0 || c.RootDispersionThreshold > 24*time.Hour || c.LatencyThreshold < 0 || c.LatencyThreshold > 5*time.Minute {
		return errors.New("NTP threshold exceeds limit")
	}
	return nil
}

func checkNTP(ctx context.Context, c NTPConfig) Result {
	conn, err := dial(ctx, "udp", c.Address)
	if err != nil {
		return down("NTP connection failed")
	}
	defer conn.Close()
	var request [48]byte
	request[0] = 4<<3 | 3 // v4, client
	t1 := time.Now()
	putNTPTime(request[40:], t1)
	if n, err := conn.Write(request[:]); err != nil || n != len(request) {
		return down("NTP request failed")
	}
	var response [513]byte
	n, err := conn.Read(response[:])
	t4 := time.Now()
	if err != nil {
		return down("NTP response failed")
	}
	return evaluateNTP(response[:n], request[40:], t1, t4, c)
}

func evaluateNTP(p, origin []byte, t1, t4 time.Time, c NTPConfig) Result {
	if len(p) < 48 || len(p) > 512 || len(p)%4 != 0 {
		return down("NTP response size is invalid")
	}
	version, mode, leap := (p[0]>>3)&7, p[0]&7, p[0]>>6
	if (version != 3 && version != 4) || mode != 4 || !bytes.Equal(p[24:32], origin) {
		return down("NTP response does not match request")
	}
	if leap == 3 || p[1] == 0 || p[1] > 15 {
		return down("NTP server is unsynchronized or refused request")
	}
	if binary.BigEndian.Uint64(p[32:40]) == 0 || binary.BigEndian.Uint64(p[40:48]) == 0 {
		return down("NTP response timestamps are invalid")
	}
	t2, t3 := readNTPTime(p[32:40], t1), readNTPTime(p[40:48], t1)
	elapsed := t4.Sub(t1)
	// Bound era interpretation, impossible server processing, and local clock jumps.
	if elapsed < 0 || elapsed > 5*time.Minute || t2.Before(t1.Add(-24*time.Hour)) || t2.After(t1.Add(24*time.Hour)) || t3.Before(t2) || t3.After(t1.Add(24*time.Hour)) || t3.Sub(t2) > elapsed+time.Millisecond {
		return down("NTP response timestamps are invalid")
	}
	offset := (t2.Sub(t1) + t3.Sub(t4)) / 2
	if offset < 0 {
		offset = -offset
	}
	delay := elapsed - t3.Sub(t2)
	if delay < 0 {
		delay = 0
	} // <=1ms quantization tolerance above
	dispersion := time.Duration(uint64(binary.BigEndian.Uint32(p[8:12])) * uint64(time.Second) / 65536)
	stratum, maxOffset, maxDispersion := c.StratumThreshold, c.OffsetThreshold, c.RootDispersionThreshold
	if stratum == 0 {
		stratum = 5
	}
	if maxOffset == 0 {
		maxOffset = time.Second
	}
	if maxDispersion == 0 {
		maxDispersion = 500 * time.Millisecond
	}
	if p[1] >= stratum {
		return down("NTP stratum threshold exceeded")
	}
	if offset >= maxOffset {
		return down("NTP offset threshold exceeded")
	}
	if dispersion >= maxDispersion {
		return down("NTP root dispersion threshold exceeded")
	}
	if c.LatencyThreshold > 0 && delay >= c.LatencyThreshold {
		return down("NTP latency threshold exceeded")
	}
	return up("NTP synchronized response passed thresholds")
}

const ntpEpoch = 2208988800

func putNTPTime(p []byte, t time.Time) {
	binary.BigEndian.PutUint32(p, uint32(t.Unix()+ntpEpoch))
	binary.BigEndian.PutUint32(p[4:], uint32((uint64(t.Nanosecond())<<32)/uint64(time.Second)))
}
func readNTPTime(p []byte, near time.Time) time.Time {
	// Select the nearest NTP era, including rollover in February 2036.
	delta := int64(int32(binary.BigEndian.Uint32(p) - uint32(near.Unix()+ntpEpoch)))
	nanos := int64(uint64(binary.BigEndian.Uint32(p[4:])) * uint64(time.Second) >> 32)
	return time.Unix(near.Unix()+delta, nanos)
}
