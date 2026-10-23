package services

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func tdsRead(c net.Conn) (byte, []byte, error) {
	var out []byte
	var typ byte
	for {
		var h [8]byte
		if _, err := io.ReadFull(c, h[:]); err != nil {
			return 0, nil, err
		}
		n := int(binary.BigEndian.Uint16(h[2:]))
		if n < 8 || len(out)+n-8 > maxBody*3 {
			return 0, nil, fmt.Errorf("fixture TDS size")
		}
		typ = h[0]
		b := make([]byte, n-8)
		if _, err := io.ReadFull(c, b); err != nil {
			return 0, nil, err
		}
		out = append(out, b...)
		if h[1]&1 != 0 {
			return typ, out, nil
		}
	}
}
func tdsWrite(c net.Conn, typ byte, p []byte) error {
	for len(p) > 0 {
		n := len(p)
		if n > 4000 {
			n = 4000
		}
		h := []byte{typ, 0, 0, 0, 0, 0, 1, 0}
		if n == len(p) {
			h[1] = 1
		}
		binary.BigEndian.PutUint16(h[2:], uint16(n+8))
		if _, err := c.Write(append(h, p[:n]...)); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

type tdsHandshakeServer struct {
	net.Conn
	pending     []byte
	passthrough bool
}

func (c *tdsHandshakeServer) Read(p []byte) (int, error) {
	if c.passthrough {
		return c.Conn.Read(p)
	}
	if len(c.pending) == 0 {
		typ, b, err := tdsRead(c.Conn)
		if err != nil {
			return 0, err
		}
		if typ != 18 {
			return 0, fmt.Errorf("expected TDS TLS handshake")
		}
		c.pending = b
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}
func (c *tdsHandshakeServer) Write(p []byte) (int, error) {
	if c.passthrough {
		return c.Conn.Write(p)
	}
	if err := tdsWrite(c.Conn, 4, p); err != nil {
		return 0, err
	}
	return len(p), nil
}
func ucs(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return b
}
func fromUCS(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}
func tdsDone(status uint16) []byte {
	b := make([]byte, 13)
	b[0] = 0xfd
	binary.LittleEndian.PutUint16(b[1:], status)
	return b
}
func tdsError() []byte {
	msg := ucs("fixture-secret private-value")
	b := make([]byte, 4+1+1+2)
	binary.LittleEndian.PutUint32(b, 18456)
	b[4] = 1
	b[5] = 14
	binary.LittleEndian.PutUint16(b[6:], uint16(len(msg)/2))
	b = append(b, msg...)
	b = append(b, 0, 0, 1, 0, 0, 0)
	h := []byte{0xaa, 0, 0}
	binary.LittleEndian.PutUint16(h[1:], uint16(len(b)))
	return append(append(h, b...), tdsDone(2)...)
}

func sqlServerFixture(t *testing.T, encrypted bool, variant string) (string, *TLSConfig) {
	tc, trust := testTLS(t)
	tc.MaxVersion = tls.VersionTLS12
	a := fixture(t, func(raw net.Conn) error {
		var c net.Conn = raw
		typ, p, err := tdsRead(c)
		if err != nil {
			return nil
		}
		if typ != 18 {
			return fmt.Errorf("expected SQL Server PRELOGIN")
		}
		requested := byte(255)
		for i := 0; i+5 <= len(p) && p[i] != 255; i += 5 {
			if p[i] == 1 {
				offset := int(binary.BigEndian.Uint16(p[i+1:]))
				if offset < len(p) {
					requested = p[offset]
				}
			}
		}
		if encrypted && requested != 1 {
			return fmt.Errorf("expected required encryption")
		}
		if !encrypted && requested != 2 {
			return fmt.Errorf("expected explicit plain encryption")
		}
		encrypt := byte(2)
		if encrypted {
			encrypt = 1
		}
		if err = tdsWrite(c, 4, []byte{1, 0, 6, 0, 1, 255, encrypt}); err != nil {
			return nil
		}
		if encrypted {
			hs := &tdsHandshakeServer{Conn: c}
			sc := tls.Server(hs, tc)
			if sc.Handshake() != nil {
				return nil
			}
			hs.passthrough = true
			c = sc
		}
		typ, p, err = tdsRead(c)
		if err != nil {
			return nil
		}
		if typ != 16 || len(p) < 94 {
			return fmt.Errorf("expected SQL Server LOGIN7")
		}
		get := func(i int) []byte {
			off := int(binary.LittleEndian.Uint16(p[i:]))
			n := int(binary.LittleEndian.Uint16(p[i+2:])) * 2
			if off+n > len(p) {
				return nil
			}
			return append([]byte(nil), p[off:off+n]...)
		}
		pass := get(44)
		for i, v := range pass {
			v ^= 0xa5
			pass[i] = v<<4 | v>>4
		}
		if fromUCS(get(40)) != "monitor" || fromUCS(pass) != "fixture-secret" || fromUCS(get(68)) != "monitor" || p[26]&32 == 0 {
			return fmt.Errorf("incorrect SQL login credentials/database/read-only intent")
		}
		if variant == "auth-refused" {
			_ = tdsWrite(c, 4, tdsError())
			return nil
		}
		ack := []byte{0xad, 10, 0, 1, 0x74, 0, 0, 4, 0, 0, 0, 0, 0}
		if err = tdsWrite(c, 4, append(ack, tdsDone(0)...)); err != nil {
			return nil
		}
		typ, p, err = tdsRead(c)
		if err != nil {
			return nil
		}
		if typ != 1 || len(p) < 22 {
			return fmt.Errorf("expected SQL Server batch")
		}
		h := int(binary.LittleEndian.Uint32(p))
		if h > len(p) || fromUCS(p[h:]) != "SELECT private_value" {
			return fmt.Errorf("incorrect SQL Server query")
		}
		if variant == "deadline" {
			_, _ = io.Copy(io.Discard, c)
			return nil
		}
		if variant == "query-error" {
			_ = tdsWrite(c, 4, tdsError())
			return nil
		}
		if variant == "malformed" {
			_ = tdsWrite(c, 4, []byte{0x81, 1, 0, 0})
			return nil
		}
		cols := 1
		if variant == "columns" {
			cols = 65
		}
		body := []byte{0x81, byte(cols), 0}
		for i := 0; i < cols; i++ {
			body = append(body, 0, 0, 0, 0, 1, 0)
			if variant == "values" {
				body = append(body, 0xe7, 0xfe, 0xff, 0, 0, 0, 0, 0)
			} else {
				body = append(body, 0x26, 8)
			}
			body = append(body, 1, 'v', 0)
		}
		n := 1
		if variant == "no-rows" {
			n = 0
		}
		if variant == "rows" {
			n = 101
		}
		if variant == "values" {
			n = 100
		}
		for i := 0; i < n; i++ {
			body = append(body, 0xd1)
			for j := 0; j < cols; j++ {
				if variant == "null" {
					body = append(body, 0)
				} else if variant == "values" {
					v := ucs(strings.Repeat("x", 12000))
					body = append(body, byte(len(v)), byte(len(v)>>8))
					body = append(body, v...)
				} else {
					body = append(body, 8)
					b := make([]byte, 8)
					binary.LittleEndian.PutUint64(b, 9007199254740993)
					body = append(body, b...)
				}
			}
		}
		body = append(body, tdsDone(0)...)
		if variant == "multiple" {
			body[len(body)-12] = 1
			body = append(body, []byte{0x81, 1, 0, 0, 0, 0, 0, 1, 0, 0x26, 8, 1, 'v', 0, 0xd1, 8, 1, 0, 0, 0, 0, 0, 0, 0}...)
			body = append(body, tdsDone(0)...)
		}
		_ = tdsWrite(c, 4, body)
		return nil
	})
	return a, trust
}
func TestSQLServerProtocol(t *testing.T) {
	for _, tc := range []struct {
		variant string
		tls     bool
		want    string
	}{{"ok", false, "up"}, {"ok", true, "up"}, {"auth-refused", false, "down"}, {"query-error", false, "down"}, {"no-rows", false, "down"}, {"null", false, "up"}, {"mismatch", false, "down"}, {"rows", false, "down"}, {"columns", false, "down"}, {"values", false, "down"}, {"multiple", false, "down"}, {"malformed", false, "down"}, {"deadline", false, "down"}} {
		t.Run(fmt.Sprintf("%s/tls=%v", tc.variant, tc.tls), func(t *testing.T) {
			a, trust := sqlServerFixture(t, tc.tls, tc.variant)
			c := SQLConfig{DSN: "sqlserver://monitor:fixture-secret@" + a + "/monitor", Query: "SELECT private_value", TLSMode: "plain", Condition: &Condition{Operator: "eq", Expected: []byte(`9007199254740993`)}}
			if tc.tls {
				c.TLSMode = "tls"
				c.TLS = trust
			}
			if tc.variant == "null" {
				c.Condition.Expected = []byte(`null`)
			}
			if tc.variant == "mismatch" {
				c.Condition.Expected = []byte(`1`)
			}
			expectState(t, Config{Type: SQLServer, Timeout: 500 * time.Millisecond, SQLServer: &c}, tc.want)
		})
	}
}
func TestSQLServerTLSValidationCancellation(t *testing.T) {
	a, _ := sqlServerFixture(t, true, "ok")
	expectState(t, Config{Type: SQLServer, Timeout: 300 * time.Millisecond, SQLServer: &SQLConfig{DSN: "sqlserver://monitor:fixture-secret@" + a + "/monitor", Query: "SELECT private_value"}}, "down")
	for _, dsn := range []string{"server=host;user id=u;password=p", "sqlserver://host/db", "sqlserver://u:p@host/db?certificate=secret", "sqlserver://DOMAIN%5Cu:p@host/db", "sqlserver://u:p@host/instance/db", "sqlserver://u:p@host/db?change+password=x"} {
		if _, err := sqlServerConfig(SQLConfig{DSN: dsn}); err == nil {
			t.Fatal("accepted unsafe SQL Server DSN")
		}
	}
	if validateSQLServer(SQLConfig{DSN: "sqlserver://u:p@host/db", Query: "WITH x AS (SELECT 1) SELECT * FROM x"}) == nil {
		t.Fatal("accepted non-SELECT query")
	}
	a = fixture(t, func(c net.Conn) error { _, _ = io.Copy(io.Discard, c); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	r := Check(ctx, Config{Type: SQLServer, Timeout: time.Second, SQLServer: &SQLConfig{DSN: "sqlserver://monitor:fixture-secret@" + a + "/monitor"}})
	if r.Message != "service check canceled" {
		t.Fatal(r)
	}
}
