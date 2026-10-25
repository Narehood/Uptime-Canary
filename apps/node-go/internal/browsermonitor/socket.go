package browsermonitor

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// The debugging transport has explicit header, message, frame and lifetime
// byte bounds. It never follows redirects, uses an environment proxy or sends
// a browser user's cookies. Requests are fixed CDP commands constructed here.
type socket struct {
	rw        io.ReadWriteCloser
	transport *http.Transport
	write     sync.Mutex
	remaining int
	frames    int
}

func dialSocket(ctx context.Context, address, token string) (*socket, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, errProtocol
	}
	if u.Scheme == "wss" {
		u.Scheme = "https"
	} else {
		u.Scheme = "http"
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, errProtocol
	}
	key := base64.StdEncoding.EncodeToString(nonce)
	r, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, errProtocol
	}
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", key)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	t := &http.Transport{MaxResponseHeaderBytes: 16 << 10, DisableKeepAlives: true}
	h := &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := h.Do(r)
	if err != nil {
		t.CloseIdleConnections()
		return nil, errProtocol
	}
	// SHA-1 is required by RFC 6455 for this challenge, not used to authenticate
	// the browser service. HTTPS certificate verification authenticates wss.
	digest := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if response.StatusCode != 101 || !headerToken(response.Header, "Connection", "upgrade") || !headerToken(response.Header, "Upgrade", "websocket") || len(response.Header.Values("Sec-WebSocket-Accept")) != 1 || response.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(digest[:]) || response.Header.Get("Sec-WebSocket-Extensions") != "" || response.Header.Get("Sec-WebSocket-Protocol") != "" {
		response.Body.Close()
		t.CloseIdleConnections()
		return nil, errProtocol
	}
	rw, ok := response.Body.(io.ReadWriteCloser)
	if !ok {
		response.Body.Close()
		t.CloseIdleConnections()
		return nil, errProtocol
	}
	return &socket{rw: rw, transport: t, remaining: 8 << 20}, nil
}

func headerToken(h http.Header, name, want string) bool {
	for _, value := range h.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), want) {
				return true
			}
		}
	}
	return false
}

func (s *socket) send(opcode byte, payload []byte) error {
	s.write.Lock()
	defer s.write.Unlock()
	if len(payload) > 8192 || opcode >= 8 && len(payload) > 125 {
		return errProtocol
	}
	header := make([]byte, 0, 14)
	header = append(header, 0x80|opcode)
	if len(payload) < 126 {
		header = append(header, 0x80|byte(len(payload)))
	} else {
		header = append(header, 0x80|126, byte(len(payload)>>8), byte(len(payload)))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return errProtocol
	}
	header = append(header, mask...)
	frame := make([]byte, len(header)+len(payload))
	copy(frame, header)
	for i, b := range payload {
		frame[len(header)+i] = b ^ mask[i%4]
	}
	for len(frame) > 0 {
		n, err := s.rw.Write(frame)
		if err != nil || n == 0 {
			return errProtocol
		}
		frame = frame[n:]
	}
	return nil
}

func (s *socket) sendJSON(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return errProtocol
	}
	return s.send(1, raw)
}

func (s *socket) receive() ([]byte, error) {
	var message []byte
	fragmented := false
	for {
		s.frames++
		if s.frames > 16384 || s.remaining <= 0 {
			return nil, errProtocol
		}
		var header [2]byte
		if _, err := io.ReadFull(s.rw, header[:]); err != nil {
			return nil, errProtocol
		}
		fin, op := header[0]&0x80 != 0, header[0]&15
		if header[0]&0x70 != 0 || header[1]&0x80 != 0 || op != 0 && op != 1 && op != 8 && op != 9 && op != 10 {
			return nil, errProtocol
		}
		length := uint64(header[1] & 127)
		if op >= 8 && (!fin || length > 125) {
			return nil, errProtocol
		}
		switch length {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(s.rw, ext[:]); err != nil {
				return nil, errProtocol
			}
			length = uint64(binary.BigEndian.Uint16(ext[:]))
			if length < 126 {
				return nil, errProtocol
			}
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(s.rw, ext[:]); err != nil {
				return nil, errProtocol
			}
			length = binary.BigEndian.Uint64(ext[:])
			if length < 65536 || length>>63 != 0 {
				return nil, errProtocol
			}
		}
		if length > uint64(s.remaining) || op < 8 && length > uint64((384<<10)-len(message)) {
			return nil, errProtocol
		}
		s.remaining -= int(length) + 2
		payload := make([]byte, int(length))
		if _, err := io.ReadFull(s.rw, payload); err != nil {
			return nil, errProtocol
		}
		switch op {
		case 8:
			return nil, errProtocol
		case 9:
			if s.send(10, payload) != nil {
				return nil, errProtocol
			}
			continue
		case 10:
			continue
		case 0:
			if !fragmented {
				return nil, errProtocol
			}
		case 1:
			if fragmented {
				return nil, errProtocol
			}
			fragmented = true
		}
		message = append(message, payload...)
		if fin {
			return message, nil
		}
	}
}

func (s *socket) close() { _ = s.rw.Close(); s.transport.CloseIdleConnections() }
