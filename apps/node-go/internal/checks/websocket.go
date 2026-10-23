package checks

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

var errWSProtocol = errors.New("WebSocket protocol or resource limit violation")

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

func checkWebSocket(ctx context.Context, c Config) Result {
	o := wsOptions(c)
	tlsOpts := tlsOptions(c)
	u, _ := parseTargetURL(c.Target, true)
	if u.Scheme == "wss" {
		u.Scheme = "https"
	} else {
		u.Scheme = "http"
	}
	transport, err := makeTransport(tlsOpts, o.ProxyURL, o.UseEnvironmentProxy)
	if err != nil {
		return down("WebSocket transport configuration failed")
	}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return down("WebSocket request could not be constructed")
	}
	applyHeaders(req, o.Headers, o.BasicAuth, o.BearerToken)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return down("WebSocket random challenge generation failed")
	}
	key := base64.StdEncoding.EncodeToString(nonce)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return down("WebSocket connection or TLS verification failed")
	}
	defer resp.Body.Close()
	r := Result{State: Down, StatusCode: resp.StatusCode}
	if resp.TLS != nil {
		r.Certificate = certificateSummary(*resp.TLS, tlsOpts)
	}
	digest := sha1.Sum([]byte(key + websocketGUID)) // mandated by RFC 6455, not used as a security hash
	if resp.StatusCode != 101 || !headerToken(resp.Header, "Connection", "upgrade") || !headerToken(resp.Header, "Upgrade", "websocket") || len(resp.Header.Values("Sec-WebSocket-Accept")) != 1 || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(digest[:]) || resp.Header.Get("Sec-WebSocket-Extensions") != "" || resp.Header.Get("Sec-WebSocket-Protocol") != "" {
		r.Message = "WebSocket upgrade validation failed"
		return r
	}
	rw, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		r.Message = "WebSocket upgrade transport is unavailable"
		return r
	}
	stop := context.AfterFunc(ctx, func() { _ = rw.Close() })
	defer stop()
	if _, err := rand.Read(nonce); err != nil {
		r.Message = "WebSocket ping generation failed"
		return r
	}
	if err := writeWSControl(rw, 9, nonce); err != nil {
		r.Message = "WebSocket ping write failed"
		return r
	}
	remaining := maxWSBytes
	fragmented := false
	for frame := 0; frame < maxWSFrames; frame++ {
		opcode, fin, payload, err := readWSFrame(rw, &remaining)
		if err != nil {
			r.Message = "WebSocket frame read or protocol validation failed"
			return r
		}
		switch opcode {
		case 8:
			if len(payload) == 1 {
				r.Message = "WebSocket close frame is invalid"
			} else {
				r.Message = "WebSocket closed before matching pong"
			}
			return r
		case 9:
			if err := writeWSControl(rw, 10, payload); err != nil {
				r.Message = "WebSocket pong write failed"
				return r
			}
		case 10:
			if bytes.Equal(payload, nonce) {
				_ = writeWSControl(rw, 8, []byte{3, 232})
				r.State = Up
				r.Message = "WebSocket upgrade and ping/pong passed"
				return r
			}
		case 0:
			if !fragmented {
				r.Message = "WebSocket unexpected continuation frame"
				return r
			}
			fragmented = !fin
		case 1, 2:
			if fragmented {
				r.Message = "WebSocket fragmented message is invalid"
				return r
			}
			fragmented = !fin
		}
	}
	r.Message = "WebSocket frame count exceeds limit"
	return r
}

// writeWSControl always masks client frames with an independent random key.
func writeWSControl(w io.Writer, opcode byte, payload []byte) error {
	if len(payload) > 125 {
		return errWSProtocol
	}
	frame := make([]byte, 6+len(payload))
	frame[0] = 0x80 | opcode
	frame[1] = 0x80 | byte(len(payload))
	if _, err := rand.Read(frame[2:6]); err != nil {
		return err
	}
	for i, b := range payload {
		frame[6+i] = b ^ frame[2+i%4]
	}
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func readWSFrame(r io.Reader, remaining *int) (opcode byte, fin bool, payload []byte, err error) {
	var header [2]byte
	if _, err = io.ReadFull(r, header[:]); err != nil {
		return
	}
	opcode = header[0] & 15
	fin = header[0]&0x80 != 0
	if header[0]&0x70 != 0 || header[1]&0x80 != 0 || opcode != 0 && opcode != 1 && opcode != 2 && opcode != 8 && opcode != 9 && opcode != 10 {
		err = errWSProtocol
		return
	}
	length := uint64(header[1] & 127)
	if opcode >= 8 && (!fin || length > 125) {
		err = errWSProtocol
		return
	}
	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(r, ext[:]); err != nil {
			return
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
		if length < 126 {
			err = errWSProtocol
			return
		}
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(r, ext[:]); err != nil {
			return
		}
		length = binary.BigEndian.Uint64(ext[:])
		if length < 65536 || length>>63 != 0 {
			err = errWSProtocol
			return
		}
	}
	if length > uint64(*remaining) {
		err = errWSProtocol
		return
	}
	*remaining -= int(length)
	payload = make([]byte, int(length))
	_, err = io.ReadFull(r, payload)
	return
}
