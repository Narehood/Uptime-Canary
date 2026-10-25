package browsermonitor

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

type memorySocket struct {
	input  *bytes.Reader
	output bytes.Buffer
}

func (m *memorySocket) Read(b []byte) (int, error)  { return m.input.Read(b) }
func (m *memorySocket) Write(b []byte) (int, error) { return m.output.Write(b) }
func (m *memorySocket) Close() error                { return nil }

func serverFrame(op byte, fin bool, body []byte) []byte {
	if fin {
		op |= 0x80
	}
	out := []byte{op}
	if len(body) < 126 {
		out = append(out, byte(len(body)))
	} else if len(body) < 65536 {
		out = append(out, 126, byte(len(body)>>8), byte(len(body)))
	} else {
		out = append(out, 127)
		out = binary.BigEndian.AppendUint64(out, uint64(len(body)))
	}
	return append(out, body...)
}

func TestBoundedFragmentedBrowserSocket(t *testing.T) {
	input := serverFrame(1, false, []byte(`{"id":`))
	input = append(input, serverFrame(9, true, []byte("private-ping"))...)
	input = append(input, serverFrame(0, true, []byte(`1,"result":{}}`))...)
	m := &memorySocket{input: bytes.NewReader(input)}
	s := &socket{rw: m, remaining: 8 << 20}
	raw, err := s.receive()
	if err != nil || string(raw) != `{"id":1,"result":{}}` {
		t.Fatal("fragmented CDP message lost", err)
	}
	if m.output.Len() == 0 || m.output.Bytes()[0] != 0x8a || m.output.Bytes()[1]&0x80 == 0 {
		t.Fatal("server ping not answered with masked pong")
	}
	for _, input := range [][]byte{
		serverFrame(0, true, []byte("orphan")), serverFrame(2, true, []byte("binary")), {0x81, 0x80},
		{0x89, 126, 0, 126}, {0x81, 126, 0, 1},
		append(serverFrame(1, false, []byte("first")), serverFrame(1, true, []byte("new"))...),
		append(serverFrame(1, false, []byte(strings.Repeat("x", 384<<10))), serverFrame(0, true, []byte("x"))...),
		{0x81, 127, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	} {
		m := &memorySocket{input: bytes.NewReader(input)}
		s := &socket{rw: m, remaining: 8 << 20}
		if _, err := s.receive(); err == nil {
			t.Fatal("invalid/unbounded browser frame admitted")
		}
	}
	frames := bytes.Repeat(serverFrame(10, true, nil), 16385)
	if _, err := (&socket{rw: &memorySocket{input: bytes.NewReader(frames)}, remaining: 8 << 20}).receive(); err == nil {
		t.Fatal("control-frame flood unbounded")
	}
}

func TestBrowserClientFramesMaskAndBoundRequests(t *testing.T) {
	m := &memorySocket{input: bytes.NewReader(nil)}
	s := &socket{rw: m}
	if err := s.sendJSON(map[string]any{"method": "Page.enable"}); err != nil {
		t.Fatal(err)
	}
	raw := m.output.Bytes()
	if raw[0] != 0x81 || raw[1]&0x80 == 0 {
		t.Fatal("client frame not masked")
	}
	n := int(raw[1] & 127)
	body := make([]byte, n)
	for i := range body {
		body[i] = raw[6+i] ^ raw[2+i%4]
	}
	if !bytes.Contains(body, []byte("Page.enable")) {
		t.Fatal("masked request not recoverable")
	}
	if s.send(1, make([]byte, 8193)) == nil || s.send(9, make([]byte, 126)) == nil {
		t.Fatal("request/control output limit bypassed")
	}
	var _ io.ReadWriteCloser = m
}
