package services

import (
	"context"
	"crypto/hmac"
	"crypto/md5" // RADIUS wire protocol requires MD5 (RFC 2865/3579).
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
)

type RADIUSConfig struct {
	Address                                string `json:"address"`
	Username                               string `json:"username"`
	Password                               string `json:"password"`
	Secret                                 string `json:"secret"`
	CalledStationID                        string `json:"calledStationId,omitempty"`
	CallingStationID                       string `json:"callingStationId,omitempty"`
	UnsafeAllowMissingMessageAuthenticator bool   `json:"unsafeAllowMissingMessageAuthenticator,omitempty"`
}

func (c RADIUSConfig) validate() error {
	if err := validAddress(c.Address); err != nil {
		return err
	}
	if len(c.Username) == 0 || len(c.Username) > 253 || len(c.Password) == 0 || len(c.Password) > 128 || len(c.Secret) == 0 || len(c.Secret) > 1024 {
		return errors.New("invalid RADIUS credentials")
	}
	if len(c.CalledStationID) > 253 || len(c.CallingStationID) > 253 {
		return errors.New("RADIUS station ID exceeds limit")
	}
	return nil
}

func radiusAttribute(p []byte, typ byte, value []byte) []byte {
	p = append(p, typ, byte(len(value)+2))
	return append(p, value...)
}

// RFC 2865 section 5.2 hides the PAP password; it is not modern encryption.
func radiusPassword(password, secret string, authenticator []byte) []byte {
	n := (len(password) + 15) / 16 * 16
	padded := make([]byte, n)
	copy(padded, password)
	previous := authenticator
	for i := 0; i < n; i += 16 {
		h := md5.New()
		h.Write([]byte(secret))
		h.Write(previous)
		digest := h.Sum(nil)
		for j := 0; j < 16; j++ {
			padded[i+j] ^= digest[j]
		}
		previous = padded[i : i+16]
	}
	return padded
}

func radiusRequest(c RADIUSConfig) ([]byte, error) {
	p := make([]byte, 20)
	p[0] = 1 // Access-Request
	if _, err := rand.Read(p[1:2]); err != nil {
		return nil, err
	}
	if _, err := rand.Read(p[4:20]); err != nil {
		return nil, err
	}
	p = radiusAttribute(p, 1, []byte(c.Username))
	p = radiusAttribute(p, 2, radiusPassword(c.Password, c.Secret, p[4:20]))
	if c.CalledStationID != "" {
		p = radiusAttribute(p, 30, []byte(c.CalledStationID))
	}
	if c.CallingStationID != "" {
		p = radiusAttribute(p, 31, []byte(c.CallingStationID))
	}
	p = radiusAttribute(p, 80, make([]byte, 16))
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	h := hmac.New(md5.New, []byte(c.Secret))
	h.Write(p)
	copy(p[len(p)-16:], h.Sum(nil))
	return p, nil
}

func checkRADIUS(ctx context.Context, c RADIUSConfig) Result {
	p, err := radiusRequest(c)
	if err != nil {
		return down("RADIUS request creation failed")
	}
	conn, err := dial(ctx, "udp", c.Address)
	if err != nil {
		return down("RADIUS connection failed")
	}
	defer conn.Close()
	if n, err := conn.Write(p); err != nil || n != len(p) {
		return down("RADIUS request failed")
	}
	var b [4097]byte
	n, err := conn.Read(b[:])
	if err != nil {
		return down("RADIUS response failed")
	}
	return radiusResponse(b[:n], p, c)
}

func radiusResponse(p, request []byte, c RADIUSConfig) Result {
	if len(p) < 20 || len(p) > 4096 || int(binary.BigEndian.Uint16(p[2:4])) != len(p) || p[1] != request[1] || (p[0] != 2 && p[0] != 3 && p[0] != 11) {
		return down("invalid RADIUS response")
	}
	// First verify the Response Authenticator with all attributes intact.
	h := md5.New()
	h.Write(p[:4])
	h.Write(request[4:20])
	h.Write(p[20:])
	h.Write([]byte(c.Secret))
	if subtle.ConstantTimeCompare(p[4:20], h.Sum(nil)) != 1 {
		return down("RADIUS response authentication failed")
	}
	ma := -1
	for i := 20; i < len(p); {
		if i+2 > len(p) || p[i] == 0 || int(p[i+1]) < 2 || i+int(p[i+1]) > len(p) {
			return down("invalid RADIUS attributes")
		}
		if p[i] == 80 {
			if p[i+1] != 18 || ma != -1 {
				return down("invalid RADIUS message authenticator")
			}
			ma = i + 2
		}
		i += int(p[i+1])
	}
	if ma < 0 && !c.UnsafeAllowMissingMessageAuthenticator {
		return down("RADIUS message authenticator required")
	}
	if ma >= 0 {
		// RFC 3579: use the original request authenticator and zero MA while hashing.
		wire := append([]byte(nil), p...)
		copy(wire[4:20], request[4:20])
		clear(wire[ma : ma+16])
		mac := hmac.New(md5.New, []byte(c.Secret))
		mac.Write(wire)
		if !hmac.Equal(p[ma:ma+16], mac.Sum(nil)) {
			return down("RADIUS message authentication failed")
		}
	}
	switch p[0] {
	case 2:
		return up("RADIUS Access-Accept authenticated")
	case 3:
		return down("RADIUS Access-Reject authenticated")
	default:
		return down("RADIUS Access-Challenge requires unsupported authentication flow")
	}
}
