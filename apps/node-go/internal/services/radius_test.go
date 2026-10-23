package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

func radiusOptions() RADIUSConfig {
	return RADIUSConfig{Username: "fixture-user", Password: "fixture-secret-more-than-one-block", Secret: "private-value", CalledStationID: "called-123", CallingStationID: "calling-456"}
}

func radiusFixtureResponse(request []byte, code byte, secret string, mode string) ([]byte, error) {
	if len(request) < 20 || request[0] != 1 || int(binary.BigEndian.Uint16(request[2:4])) != len(request) {
		return nil, errors.New("invalid RADIUS Access-Request")
	}
	attrs := map[byte][]byte{}
	for i := 20; i < len(request); {
		if i+2 > len(request) || request[i+1] < 2 || i+int(request[i+1]) > len(request) {
			return nil, errors.New("invalid request attributes")
		}
		attrs[request[i]] = request[i+2 : i+int(request[i+1])]
		i += int(request[i+1])
	}
	if string(attrs[1]) != "fixture-user" || string(attrs[30]) != "called-123" || string(attrs[31]) != "calling-456" || len(attrs[80]) != 16 || len(attrs[2])%16 != 0 || len(attrs[2]) == 0 {
		return nil, errors.New("missing request attributes")
	}
	// Verify the client MAC and independently decode all PAP blocks.
	wire := append([]byte(nil), request...)
	clear(wire[len(wire)-16:])
	mac := hmac.New(md5.New, []byte(secret))
	mac.Write(wire)
	if !hmac.Equal(mac.Sum(nil), attrs[80]) {
		return nil, nil
	} // wrong shared secret: silent discard
	password := make([]byte, len(attrs[2]))
	previous := request[4:20]
	for i := 0; i < len(password); i += 16 {
		preimage := append(append([]byte(nil), []byte(secret)...), previous...)
		digest := md5.Sum(preimage)
		for j := 0; j < 16; j++ {
			password[i+j] = attrs[2][i+j] ^ digest[j]
		}
		previous = attrs[2][i : i+16]
	}
	if string(bytes.TrimRight(password, "\x00")) != "fixture-secret-more-than-one-block" {
		code = 3
	}
	p := make([]byte, 20)
	p[0] = code
	p[1] = request[1]
	// Include a private Reply-Message and require the engine to redact it.
	p = append(p, 18, 16)
	p = append(p, []byte("fixture-secret")...)
	if mode != "missing" {
		p = append(p, 80, 18)
		p = append(p, make([]byte, 16)...)
		if mode == "duplicate" {
			p = append(p, 80, 18)
			p = append(p, make([]byte, 16)...)
		}
	}
	if mode == "malformedAttr" {
		p = append(p, 18, 1)
	}
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	copy(p[4:20], request[4:20])
	if mode != "missing" {
		mac = hmac.New(md5.New, []byte(secret))
		mac.Write(p)
		copy(p[len(p)-16:], mac.Sum(nil))
		if mode == "badMA" {
			p[len(p)-1] ^= 1
		}
	}
	h := md5.New()
	h.Write(p)
	h.Write([]byte(secret))
	copy(p[4:20], h.Sum(nil))
	if mode == "badResponseAuth" {
		p[4] ^= 1
	}
	if mode == "wrongID" {
		p[1]++
	}
	if mode == "truncated" {
		p = p[:len(p)-1]
	}
	if mode == "oversized" {
		p = make([]byte, 4097)
	}
	return p, nil
}

func TestRADIUSLocalAuthenticationAndRejection(t *testing.T) {
	for _, tc := range []struct {
		name, mode, state string
		code              byte
		unsafe            bool
	}{
		{"accept", "", "up", 2, false}, {"reject", "", "down", 3, false}, {"challenge", "", "down", 11, false}, {"unknownCode", "", "down", 5, false},
		{"missingMAC", "missing", "down", 2, false}, {"legacyExplicit", "missing", "up", 2, true}, {"invalidMACLegacy", "badMA", "down", 2, true},
		{"badMAC", "badMA", "down", 2, false}, {"duplicateMAC", "duplicate", "down", 2, false}, {"badAuth", "badResponseAuth", "down", 2, false},
		{"wrongID", "wrongID", "down", 2, false}, {"malformed", "malformedAttr", "down", 2, false}, {"truncated", "truncated", "down", 2, false}, {"oversized", "oversized", "down", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := radiusOptions()
			c.UnsafeAllowMissingMessageAuthenticator = tc.unsafe
			c.Address = udpServiceFixture(t, func(p []byte) ([]byte, error) { return radiusFixtureResponse(p, tc.code, c.Secret, tc.mode) })
			r := expectState(t, Config{Type: RADIUS, Timeout: time.Second, RADIUS: &c}, tc.state)
			if tc.code == 3 && !strings.Contains(r.Message, "Access-Reject") {
				t.Fatal("reject semantics missing")
			}
		})
	}
	for _, bad := range []string{"password", "secret"} {
		t.Run(bad, func(t *testing.T) {
			server := radiusOptions()
			c := server
			c.Address = udpServiceFixture(t, func(p []byte) ([]byte, error) { return radiusFixtureResponse(p, 2, server.Secret, "") })
			if bad == "password" {
				c.Password = "wrong-credential"
			} else {
				c.Secret = "wrong-credential"
			}
			expectState(t, Config{Type: RADIUS, Timeout: 50 * time.Millisecond, RADIUS: &c}, "down")
		})
	}
}

func TestRADIUSTimeoutCancelRandomnessAndValidation(t *testing.T) {
	c := radiusOptions()
	c.Address = udpServiceFixture(t, func([]byte) ([]byte, error) { return nil, nil })
	a, e := radiusRequest(c)
	if e != nil {
		t.Fatal(e)
	}
	b, e := radiusRequest(c)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(a[4:20], b[4:20]) || bytes.Equal(a[20:], b[20:]) || bytes.Contains(a, []byte(c.Password)) || bytes.Contains(a, []byte(c.Secret)) {
		t.Fatal("request lacked random authenticator or hid credentials incorrectly")
	}
	expectState(t, Config{Type: RADIUS, Timeout: 30 * time.Millisecond, RADIUS: &c}, "down")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	if r := Check(ctx, Config{Type: RADIUS, Timeout: time.Second, RADIUS: &c}); !strings.Contains(r.Message, "canceled") {
		t.Fatal(r)
	}
	for _, mutate := range []func(*RADIUSConfig){func(c *RADIUSConfig) { c.Username = "" }, func(c *RADIUSConfig) { c.Secret = "" }, func(c *RADIUSConfig) { c.Password = strings.Repeat("x", 129) }, func(c *RADIUSConfig) { c.CalledStationID = strings.Repeat("x", 254) }} {
		bad := c
		mutate(&bad)
		e := Validate(Config{Type: RADIUS, Timeout: time.Second, RADIUS: &bad})
		if e == nil || strings.Contains(e.Error(), "private-value") {
			t.Fatal("invalid credential validation")
		}
	}
}
