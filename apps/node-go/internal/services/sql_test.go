package services

import (
	"bytes"
	"crypto/sha1"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPostgresDriverBaseConfiguration(t *testing.T) {
	_, err := pgconn.ParseConfig(postgresBaseDSN)
	if err != nil {
		t.Fatal(err)
	}
}

func postgresFixture(t *testing.T, encrypted bool, variant string, rows int, value []byte) (string, *TLSConfig) {
	tc, trust := testTLS(t)
	address := fixture(t, func(raw net.Conn) error {
		var c net.Conn = raw
		b := pgproto3.NewBackend(c, c)
		msg, err := b.ReceiveStartupMessage()
		if err != nil {
			return err
		}
		if encrypted {
			if _, ok := msg.(*pgproto3.SSLRequest); !ok {
				return fmt.Errorf("expected PostgreSQL SSLRequest")
			}
			if _, err := c.Write([]byte{'S'}); err != nil {
				return err
			}
			sc := tls.Server(c, tc)
			if sc.Handshake() != nil {
				return nil
			}
			c = sc
			b = pgproto3.NewBackend(c, c)
			msg, err = b.ReceiveStartupMessage()
			if err != nil {
				return err
			}
		}
		s, ok := msg.(*pgproto3.StartupMessage)
		if !ok || s.Parameters["user"] != "monitor" || s.Parameters["database"] != "monitor" || s.Parameters["default_transaction_read_only"] != "on" {
			return fmt.Errorf("incorrect PostgreSQL startup")
		}
		b.Send(&pgproto3.AuthenticationCleartextPassword{})
		if err = b.Flush(); err != nil {
			return err
		}
		_ = b.SetAuthType(pgproto3.AuthTypeCleartextPassword)
		msg, err = b.Receive()
		if err != nil {
			return err
		}
		p, ok := msg.(*pgproto3.PasswordMessage)
		if !ok || p.Password != "fixture-secret" {
			return fmt.Errorf("incorrect PostgreSQL password exchange")
		}
		if variant == "auth-refused" {
			b.Send(&pgproto3.ErrorResponse{Severity: "FATAL", Code: "28P01", Message: "fixture-secret private-value"})
			return b.Flush()
		}
		b.Send(&pgproto3.AuthenticationOk{})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if err = b.Flush(); err != nil {
			return err
		}
		msg, err = b.Receive()
		if err != nil {
			return err
		}
		q, ok := msg.(*pgproto3.Query)
		if !ok || q.String != "SELECT private_value" {
			return fmt.Errorf("incorrect PostgreSQL monitoring query")
		}
		if variant == "query-timeout" {
			_, _ = io.Copy(io.Discard, c)
			return nil
		}
		if variant == "query-error" {
			b.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "42501", Message: "fixture-secret private-value"})
			b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
			return b.Flush()
		}
		if variant == "malformed" {
			_, _ = c.Write([]byte{'D', 0, 0, 0, 3})
			return nil
		}
		b.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("private_column"), DataTypeOID: 25, DataTypeSize: -1}}})
		for i := 0; i < rows; i++ {
			b.Send(&pgproto3.DataRow{Values: [][]byte{value}})
		}
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte(fmt.Sprintf("SELECT %d", rows))})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		// Result-bound tests deliberately close before draining the whole response.
		if err = b.Flush(); err != nil && variant != "limit" {
			return err
		}
		return nil
	})
	return address, trust
}
func TestPostgresActualDriverExchange(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprint(encrypted), func(t *testing.T) {
			address, trust := postgresFixture(t, encrypted, "success", 1, []byte("42"))
			c := &SQLConfig{DSN: "postgres://monitor:fixture-secret@" + address + "/monitor", Query: "SELECT private_value", TLSMode: "plain", Condition: &Condition{Operator: "ge", Expected: []byte(`42`)}}
			if encrypted {
				c.TLSMode = "tls"
				c.TLS = trust
			}
			expectState(t, Config{Type: Postgres, Timeout: time.Second, Postgres: c}, "up")
		})
	}
}
func TestPostgresFailuresBoundsAndTrust(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rows  int
		value []byte
	}{
		{"auth-refused", 1, []byte("42")}, {"query-error", 1, []byte("42")}, {"malformed", 1, []byte("42")}, {"mismatch", 1, []byte("private-value")}, {"empty", 0, nil}, {"limit", 101, []byte("42")}, {"limit", 1, bytes.Repeat([]byte{'x'}, maxBody+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address, _ := postgresFixture(t, false, tc.name, tc.rows, tc.value)
			expectState(t, Config{Type: Postgres, Timeout: time.Second, Postgres: &SQLConfig{DSN: "postgres://monitor:fixture-secret@" + address + "/monitor", Query: "SELECT private_value", TLSMode: "plain", Condition: &Condition{Operator: "eq", Expected: []byte(`42`)}}}, "down")
		})
	}
	address, _ := postgresFixture(t, true, "untrusted", 1, []byte("42"))
	expectState(t, Config{Type: Postgres, Timeout: time.Second, Postgres: &SQLConfig{DSN: "postgres://monitor:fixture-secret@" + address + "/monitor", Query: "SELECT private_value"}}, "down")
}

func mysqlPacket(w io.Writer, seq byte, b []byte) error {
	h := []byte{byte(len(b)), byte(len(b) >> 8), byte(len(b) >> 16), seq}
	if _, e := w.Write(append(h, b...)); e != nil {
		return e
	}
	return nil
}
func readMySQLPacket(r io.Reader) (byte, []byte, error) {
	var h [4]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return 0, nil, e
	}
	n := int(h[0]) | int(h[1])<<8 | int(h[2])<<16
	if n > 2*maxBody {
		return 0, nil, fmt.Errorf("fixture packet too big")
	}
	b := make([]byte, n)
	_, e := io.ReadFull(r, b)
	return h[3], b, e
}
func mysqlLenenc(b []byte) []byte {
	n := len(b)
	var out []byte
	if n < 251 {
		out = []byte{byte(n)}
	} else if n <= 65535 {
		out = []byte{0xfc, byte(n), byte(n >> 8)}
	} else {
		out = []byte{0xfd, byte(n), byte(n >> 8), byte(n >> 16)}
	}
	return append(out, b...)
}
func mysqlFixture(t *testing.T, encrypted bool, variant string, rows int, value []byte) (string, *TLSConfig) {
	tc, trust := testTLS(t)
	address := fixture(t, func(raw net.Conn) error {
		var c net.Conn = raw
		caps := uint32(1 | 8 | 512 | 8192 | 32768 | 131072 | 524288)
		if encrypted {
			caps |= 2048
		}
		salt := []byte("0123456789abcdefghij")
		b := append([]byte{10}, []byte("8.0.40-fixture\x00")...)
		b = append(b, 1, 0, 0, 0)
		b = append(b, salt[:8]...)
		b = append(b, 0, byte(caps), byte(caps>>8), 45, 2, 0, byte(caps>>16), byte(caps>>24), 21)
		b = append(b, make([]byte, 10)...)
		b = append(b, salt[8:]...)
		b = append(b, 0)
		b = append(b, []byte("mysql_native_password\x00")...)
		if err := mysqlPacket(c, 0, b); err != nil {
			return err
		}
		seq, b, err := readMySQLPacket(c)
		if err != nil {
			return err
		}
		if encrypted {
			if len(b) != 32 || binary.LittleEndian.Uint32(b)&2048 == 0 {
				return fmt.Errorf("expected MySQL SSLRequest")
			}
			sc := tls.Server(c, tc)
			if sc.Handshake() != nil {
				return nil
			}
			c = sc
			seq, b, err = readMySQLPacket(c)
			if err != nil {
				return err
			}
		}
		if len(b) < 33 {
			return fmt.Errorf("short MySQL handshake")
		}
		rest := b[32:]
		i := bytes.IndexByte(rest, 0)
		if i < 0 || string(rest[:i]) != "monitor" {
			return fmt.Errorf("incorrect MySQL user")
		}
		rest = rest[i+1:]
		if len(rest) < 1 || int(rest[0])+1 > len(rest) {
			return fmt.Errorf("incorrect MySQL authentication length")
		}
		token := rest[1 : 1+int(rest[0])]
		stage1 := sha1.Sum([]byte("fixture-secret"))
		stage2 := sha1.Sum(stage1[:])
		hash := sha1.New()
		hash.Write(salt)
		hash.Write(stage2[:])
		expected := hash.Sum(nil)
		for i := range expected {
			expected[i] ^= stage1[i]
		}
		if subtle.ConstantTimeCompare(token, expected) != 1 {
			return fmt.Errorf("incorrect MySQL native password exchange")
		}
		if variant == "auth-refused" {
			return mysqlPacket(c, seq+1, append([]byte{0xff, 0x15, 4, '#'}, []byte("28000fixture-secret private-value")...))
		}
		if err := mysqlPacket(c, seq+1, []byte{0, 0, 0, 2, 0, 0, 0}); err != nil {
			return err
		}
		_, b, err = readMySQLPacket(c)
		if err != nil {
			return err
		}
		if len(b) < 1 || b[0] != 3 || string(b[1:]) != "SELECT private_value" {
			return fmt.Errorf("incorrect MySQL monitoring query")
		}
		if variant == "query-timeout" {
			_, _ = io.Copy(io.Discard, c)
			return nil
		}
		if variant == "query-error" {
			return mysqlPacket(c, 1, append([]byte{0xff, 0x15, 4, '#'}, []byte("42000fixture-secret private-value")...))
		}
		if variant == "malformed" {
			_, _ = c.Write([]byte{1, 0, 0, 55, 0xff})
			return nil
		}
		if err := mysqlPacket(c, 1, []byte{1}); err != nil {
			return err
		}
		var column []byte
		for _, s := range []string{"def", "monitor", "", "", "private_column", ""} {
			column = append(column, mysqlLenenc([]byte(s))...)
		}
		column = append(column, 0x0c, 33, 0, 0, 4, 0, 0, 0xfd, 0, 0, 0, 0, 0)
		if err := mysqlPacket(c, 2, column); err != nil {
			return err
		}
		if err := mysqlPacket(c, 3, []byte{0xfe, 0, 0, 2, 0}); err != nil {
			return err
		}
		for i := 0; i < rows; i++ {
			var v []byte
			if value == nil {
				v = []byte{0xfb}
			} else {
				v = mysqlLenenc(value)
			}
			if err := mysqlPacket(c, byte(i+4), v); err != nil {
				if variant == "limit" {
					return nil
				}
				return err
			}
		}
		_ = mysqlPacket(c, byte(rows+4), []byte{0xfe, 0, 0, 2, 0})
		return nil
	})
	return address, trust
}
func TestMySQLActualDriverExchange(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprint(encrypted), func(t *testing.T) {
			address, trust := mysqlFixture(t, encrypted, "success", 1, []byte("42"))
			c := &SQLConfig{DSN: "monitor:fixture-secret@tcp(" + address + ")/monitor", Query: "SELECT private_value", TLSMode: "plain", Condition: &Condition{Operator: "eq", Expected: []byte(`42`)}}
			if encrypted {
				c.TLSMode = "tls"
				c.TLS = trust
			}
			expectState(t, Config{Type: MySQL, Timeout: time.Second, MySQL: c}, "up")
		})
	}
}
func TestMySQLFailuresBoundsAndTrust(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rows  int
		value []byte
	}{
		{"auth-refused", 1, []byte("42")}, {"query-error", 1, []byte("42")}, {"malformed", 1, []byte("42")}, {"mismatch", 1, []byte("private-value")}, {"empty", 0, nil}, {"limit", 101, []byte("42")}, {"limit", 1, bytes.Repeat([]byte{'x'}, maxBody+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address, _ := mysqlFixture(t, false, tc.name, tc.rows, tc.value)
			expectState(t, Config{Type: MySQL, Timeout: time.Second, MySQL: &SQLConfig{DSN: "monitor:fixture-secret@tcp(" + address + ")/monitor", Query: "SELECT private_value", TLSMode: "plain", Condition: &Condition{Operator: "eq", Expected: []byte(`42`)}}}, "down")
		})
	}
	address, _ := mysqlFixture(t, true, "untrusted", 1, []byte("42"))
	expectState(t, Config{Type: MySQL, Timeout: time.Second, MySQL: &SQLConfig{DSN: "monitor:fixture-secret@tcp(" + address + ")/monitor", Query: "SELECT private_value"}}, "down")
}
func TestDSNRestrictionsAndEnvironmentIsolation(t *testing.T) {
	for _, dsn := range []string{"postgres://monitor:fixture-secret@localhost/db?sslmode=disable", "postgres://monitor@localhost/db?servicefile=/private", "postgres://localhost/db", "host=localhost user=monitor"} {
		if _, e := postgresConfig(SQLConfig{DSN: dsn}); e == nil {
			t.Fatal("accepted forbidden PostgreSQL DSN")
		}
	}
	for _, dsn := range []string{"monitor:fixture-secret@tcp(localhost:3306)/db?multiStatements=true", "monitor@unix(/tmp/mysql.sock)/db", "monitor@tcp(localhost:3306)/db?allowAllFiles=true"} {
		if _, e := mysqlConfig(SQLConfig{DSN: dsn}); e == nil {
			t.Fatal("accepted forbidden MySQL DSN")
		}
	}
	t.Setenv("PGPASSWORD", "environment-secret")
	t.Setenv("PGHOST", "private-host")
	t.Setenv("PGSSLROOTCERT", "private-file")
	t.Setenv("PGOPTIONS", "private-options")
	c, e := postgresConfig(SQLConfig{DSN: "postgres://monitor@localhost:5432/db"})
	if e != nil {
		t.Fatal(e)
	}
	if c.Password != "" || c.Host != "localhost" || c.TLSConfig == nil || c.TLSConfig.InsecureSkipVerify || c.Fallbacks != nil || strings.Contains(fmt.Sprint(c.RuntimeParams), "private") {
		t.Fatal("environment leaked into monitor config")
	}
	t.Setenv("PGSERVICE", "private-service")
	if _, e := postgresConfig(SQLConfig{DSN: "postgres://monitor@localhost/db"}); e == nil {
		t.Fatal("accepted environment service file")
	}
}

func TestSQLQueryDeadlineAndNullScalar(t *testing.T) {
	for _, typ := range []Type{Postgres, MySQL} {
		t.Run(string(typ), func(t *testing.T) {
			for _, variant := range []string{"query-timeout", "null"} {
				t.Run(variant, func(t *testing.T) {
					var address string
					if typ == Postgres {
						address, _ = postgresFixture(t, false, variant, 1, nil)
					} else {
						address, _ = mysqlFixture(t, false, variant, 1, nil)
					}
					sqlc := &SQLConfig{Query: "SELECT private_value", TLSMode: "plain", Condition: &Condition{Operator: "eq", Expected: []byte(`null`)}}
					c := Config{Type: typ, Timeout: 80 * time.Millisecond}
					if variant == "null" {
						c.Timeout = time.Second
					}
					if typ == Postgres {
						sqlc.DSN = "postgres://monitor:fixture-secret@" + address + "/monitor"
						c.Postgres = sqlc
					} else {
						sqlc.DSN = "monitor:fixture-secret@tcp(" + address + ")/monitor"
						c.MySQL = sqlc
					}
					state := "up"
					if variant == "query-timeout" {
						state = "down"
					}
					r := expectState(t, c, state)
					if r.LatencyMs > 1000 {
						t.Fatal("SQL query deadline did not close connection")
					}
				})
			}
		})
	}
}
