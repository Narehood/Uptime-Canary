// Wire fixture copied from internal/services/oracle_test.go, without changing the engine tests.
// It exercises the same TNS/TTC authentication and query inside the bounded child.
package probeworker

import (
	"bytes"

	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha1"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"

	"errors"
	"fmt"
	"io"
	"net"

	"strings"
	"sync/atomic"
	"testing"
)

// Independent minimal TNS/TTC peer. It negotiates TTC3, verifies the actual
// Oracle 11g AES password exchange, decodes the query and returns typed rows.
// It is not an Oracle database/version certification.
type oracleFixtureOptions struct {
	tls      *tls.Config
	fault    string
	query    string
	value    []byte
	number   bool
	rowCount int
	columns  int
	seen     *atomic.Int32
}

func oracleFixture(t *testing.T, o oracleFixtureOptions) string {
	t.Helper()
	return fixture(t, func(c net.Conn) error {
		if o.tls != nil {
			tc := tls.Server(c, o.tls)
			if tc.Handshake() != nil {
				return nil
			}
			c = tc
		}
		err := serveOracleFixture(c, o)
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		var ne net.Error
		if errors.As(err, &ne) {
			return nil
		}
		return err
	})
}
func tnsRead(c net.Conn) (byte, []byte, error) {
	var h [8]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint16(h[:]))
	if n < 8 || n > 65535 {
		return 0, nil, errors.New("invalid fixture TNS size")
	}
	b := make([]byte, n-8)
	_, err := io.ReadFull(c, b)
	return h[4], b, err
}
func tnsWrite(c net.Conn, kind byte, b []byte) error {
	packet := make([]byte, 8, len(b)+8)
	binary.BigEndian.PutUint16(packet, uint16(len(b)+8))
	packet[4] = kind
	packet = append(packet, b...)
	_, err := c.Write(packet)
	return err
}
func ttcRead(c net.Conn) ([]byte, error) {
	kind, b, err := tnsRead(c)
	if err != nil {
		return nil, err
	}
	if kind != 6 || len(b) < 2 {
		return nil, errors.New("expected TNS DATA")
	}
	return b[2:], nil
}
func ttcWrite(c net.Conn, b []byte) error {
	for len(b) > 0 {
		n := min(len(b), 32<<10)
		if err := tnsWrite(c, 6, append([]byte{0, 0}, b[:n]...)); err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}
func oraInt(n int) []byte {
	if n == 0 {
		return []byte{0}
	}
	b := binary.BigEndian.AppendUint32(nil, uint32(n))
	i := 0
	for b[i] == 0 {
		i++
	}
	return append([]byte{byte(4 - i)}, b[i:]...)
}
func oraCLR(b []byte) []byte {
	if len(b) < 0xfe {
		return append([]byte{byte(len(b))}, b...)
	}
	out := []byte{0xfe}
	for len(b) > 0 {
		n := min(len(b), 0xfd)
		out = append(out, byte(n))
		out = append(out, b[:n]...)
		b = b[n:]
	}
	return append(out, 0)
}
func oraDLC(b []byte) []byte {
	out := oraInt(len(b))
	if len(b) > 0 {
		out = append(out, oraCLR(b)...)
	}
	return out
}
func oraKV(k, v string, n int) []byte {
	out := oraDLC([]byte(k))
	out = append(out, oraDLC([]byte(v))...)
	return append(out, oraInt(n)...)
}

type oraReader struct{ *bytes.Reader }

func (r oraReader) number() (int, error) {
	n, err := r.ReadByte()
	if err != nil || n > 4 {
		return 0, errors.New("invalid compressed TTC integer")
	}
	v := 0
	for i := 0; i < int(n); i++ {
		b, e := r.ReadByte()
		if e != nil {
			return 0, e
		}
		v = v<<8 | int(b)
	}
	return v, nil
}
func (r oraReader) clr() ([]byte, error) {
	n, e := r.ReadByte()
	if e != nil {
		return nil, e
	}
	if n == 0xff || n == 0 {
		return nil, nil
	}
	if n == 0xfe {
		var out []byte
		for {
			size, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			if size == 0 {
				return out, nil
			}
			b := make([]byte, int(size))
			if _, err = io.ReadFull(r, b); err != nil {
				return nil, err
			}
			out = append(out, b...)
		}
	}
	b := make([]byte, int(n))
	_, e = io.ReadFull(r, b)
	return b, e
}
func (r oraReader) dlc() ([]byte, error) {
	n, e := r.number()
	if e != nil || n == 0 {
		return nil, e
	}
	b, e := r.clr()
	if e == nil && len(b) != n {
		return nil, errors.New("DLC length mismatch")
	}
	return b, e
}
func oraSummary(code int, rows int) []byte {
	b := []byte{4}
	b = append(b, oraInt(rows)...)
	b = append(b, oraInt(code)...)
	// array error/count, cursor ID, error position; SQL type/fatal/flags/options;
	// UPI/warning; RBA/partition/table/block/slot/OS; statement/call/pad/success.
	b = append(b, make([]byte, 24)...)
	if code != 0 {
		b = append(b, oraCLR([]byte("fixture-secret private-value ORA error"))...)
	}
	return b
}
func oraAES(key, data []byte, decrypt bool) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, errors.New("invalid AES data")
	}
	out := make([]byte, len(data))
	iv := make([]byte, aes.BlockSize)
	if decrypt {
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	} else {
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
	}
	return out, nil
}

func serveOracleFixture(c net.Conn, o oracleFixtureOptions) error {
	kind, b, err := tnsRead(c)
	if err != nil {
		return err
	}
	if kind != 1 {
		return errors.New("expected TNS CONNECT")
	}
	if len(b) == 66 {
		b, err = ttcRead(c)
		if err != nil {
			return err
		}
	}
	if !bytes.Contains(b, []byte("SERVICE_NAME=fixture-service")) {
		return errors.New("missing real TNS connect descriptor")
	}
	if o.seen != nil {
		o.seen.Add(1)
	}
	if o.fault == "connect-timeout" {
		_, _ = io.Copy(io.Discard, c)
		return nil
	}
	if o.fault == "refused" {
		return tnsWrite(c, 4, append([]byte{0, 0, 0, 0}, []byte("(DESCRIPTION=(ERR=12514))")...))
	}
	if o.fault == "malformed" {
		return tnsWrite(c, 2, []byte{1})
	}
	accept := make([]byte, 37)
	binary.BigEndian.PutUint16(accept, 310)
	binary.BigEndian.PutUint16(accept[4:], 8192)
	binary.BigEndian.PutUint16(accept[6:], 8192)
	binary.BigEndian.PutUint16(accept[12:], 45)
	if err = tnsWrite(c, 2, accept); err != nil {
		return err
	}
	b, err = ttcRead(c)
	if err != nil {
		return err
	}
	if len(b) < 3 || b[0] != 1 {
		return errors.New("missing protocol negotiation")
	}
	caps := make([]byte, 40)
	caps[4] = 2
	caps[7] = 3
	national := make([]byte, 11)
	binary.BigEndian.PutUint16(national[9:], 2000)
	protocol := append([]byte{1, 6, 0}, []byte("Oracle wire fixture\x00")...)
	protocol = append(protocol, 0x69, 0x03, 3, 0, 0, 0, 11)
	protocol = append(protocol, national...)
	protocol = append(protocol, byte(len(caps)))
	protocol = append(protocol, caps...)
	protocol = append(protocol, 2, 0, 0)
	if err = ttcWrite(c, protocol); err != nil {
		return err
	}
	b, err = ttcRead(c)
	if err != nil {
		return err
	}
	if len(b) == 0 || b[0] != 2 {
		return errors.New("missing type negotiation")
	}
	if err = ttcWrite(c, []byte{2, 0}); err != nil {
		return err
	}
	b, err = ttcRead(c)
	if err != nil {
		return err
	}
	if len(b) < 3 || b[0] != 3 || b[1] != 0x76 || !bytes.Contains(b, []byte("fixture-user")) {
		return errors.New("missing first authentication request")
	}
	salt := []byte("0123456789")
	password := "fixture-secret"
	hash := sha1.Sum(append([]byte(password), salt...))
	key := append(hash[:], 0, 0, 0, 0)
	serverKey := bytes.Repeat([]byte{0x25, 0x58, 0x81, 0x19}, 12)
	encrypted, err := oraAES(key, serverKey, false)
	if err != nil {
		return err
	}
	auth := append([]byte{8}, oraInt(2)...)
	auth = append(auth, oraKV("AUTH_SESSKEY", strings.ToUpper(hex.EncodeToString(encrypted)), 0)...)
	auth = append(auth, oraKV("AUTH_VFR_DATA", strings.ToUpper(hex.EncodeToString(salt)), 6949)...)
	auth = append(auth, oraSummary(0, 0)...)
	if err = ttcWrite(c, auth); err != nil {
		return err
	}
	b, err = ttcRead(c)
	if err != nil {
		return err
	}
	if len(b) < 3 || b[0] != 3 || b[1] != 0x73 {
		return errors.New("missing password authentication")
	}
	r := oraReader{bytes.NewReader(b[3:])}
	if _, err = r.ReadByte(); err != nil {
		return err
	}
	if _, err = r.number(); err != nil {
		return err
	}
	if _, err = r.number(); err != nil {
		return err
	}
	if _, err = r.ReadByte(); err != nil {
		return err
	}
	count, err := r.number()
	if err != nil {
		return err
	}
	if _, err = r.ReadByte(); err != nil {
		return err
	}
	if _, err = r.ReadByte(); err != nil {
		return err
	}
	user, err := r.clr()
	if err != nil {
		return err
	}
	if string(user) != "fixture-user" {
		return errors.New("wrong auth user")
	}
	values := map[string]string{}
	for i := 0; i < count; i++ {
		k, e := r.dlc()
		if e != nil {
			return e
		}
		v, e := r.dlc()
		if e != nil {
			return e
		}
		if _, e = r.number(); e != nil {
			return e
		}
		values[string(k)] = string(v)
	}
	clientEncrypted, err := hex.DecodeString(values["AUTH_SESSKEY"])
	if err != nil {
		return err
	}
	clientKey, err := oraAES(key, clientEncrypted, true)
	if err != nil {
		return err
	}
	if len(clientKey) != 48 {
		return errors.New("wrong session key size")
	}
	xor := make([]byte, 24)
	for i := range xor {
		xor[i] = serverKey[i+16] ^ clientKey[i+16]
	}
	a := md5.Sum(xor[:16])
	z := md5.Sum(xor[16:])
	passwordKey := append(a[:], z[:]...)[:24]
	cipherPassword, err := hex.DecodeString(values["AUTH_PASSWORD"])
	if err != nil {
		return err
	}
	clear, err := oraAES(passwordKey, cipherPassword, true)
	if err != nil {
		return err
	}
	valid := false
	if len(clear) > 16 {
		pad := int(clear[len(clear)-1])
		if pad > 0 && pad <= 16 && len(clear) >= 16+pad {
			valid = string(clear[16:len(clear)-pad]) == password
		}
	}
	if !valid || o.fault == "bad-auth" {
		return ttcWrite(c, oraSummary(1017, 0))
	}
	// Populate NLS language through real TTC server-network-information, avoiding
	// the driver's fallback NLS bootstrap SQL, then return session properties.
	logged := []byte{23, 4, 0, 0, 1, 1}
	logged = append(logged, oraKV("", "AMERICAN", 16)...)
	logged = append(logged, 0, 0, 0)
	logged = append(logged, 8, 1, 2)
	logged = append(logged, oraKV("AUTH_SESSION_ID", "1", 0)...)
	logged = append(logged, oraKV("AUTH_SERIAL_NUM", "1", 0)...)
	logged = append(logged, oraSummary(0, 0)...)
	if err = ttcWrite(c, logged); err != nil {
		return err
	}
	b, err = ttcRead(c)
	if err != nil {
		return err
	}
	if len(b) < 2 || b[0] != 3 || b[1] != 0x3b {
		return errors.New("missing DB version request")
	}
	version := append([]byte{8}, oraInt(len("Oracle Fixture"))...)
	version = append(version, oraCLR([]byte("Oracle Fixture"))...)
	version = append(version, oraInt(19<<24)...)
	if err = ttcWrite(c, version); err != nil {
		return err
	}
	b, err = ttcRead(c)
	if err != nil {
		return err
	}
	query := o.query
	if query == "" {
		query = "SELECT 1 FROM DUAL"
	}
	if !bytes.Contains(b, []byte(query)) {
		return fmt.Errorf("expected monitoring SELECT in TTC query, got %x", b)
	}
	if o.seen != nil {
		o.seen.Add(1)
	}
	if o.fault == "query-timeout" {
		_, _ = io.Copy(io.Discard, c)
		return nil
	}
	if o.fault == "query-error" {
		return ttcWrite(c, oraSummary(942, 0))
	}
	if o.fault == "query-malformed" {
		return ttcWrite(c, []byte{0x7f})
	}
	columns := o.columns
	if columns == 0 {
		columns = 1
	}
	response := []byte{16, 0}
	response = append(response, oraInt(128)...)
	response = append(response, oraInt(columns)...)
	response = append(response, 0)
	for i := 0; i < columns; i++ {
		dataType := byte(1)
		precision := byte(0)
		maxLen := 128
		if o.number {
			dataType = 2
			precision = 38
			maxLen = 22
		}
		column := []byte{dataType, 0, precision, 0}
		column = append(column, oraInt(maxLen)...)
		column = append(column, 0, 0, 0, 0)
		column = append(column, oraInt(873)...)
		column = append(column, 1)
		column = append(column, oraInt(128)...)
		column = append(column, 1, 1)
		column = append(column, oraDLC([]byte("RESULT"))...)
		column = append(column, 0, 0, 0)
		response = append(response, column...)
	}
	response = append(response, 0, 0, 0)
	rowCount := o.rowCount
	if rowCount == 0 && o.fault != "no-rows" {
		rowCount = 1
	}
	for i := 0; i < rowCount; i++ {
		response = append(response, 7)
		for j := 0; j < columns; j++ {
			response = append(response, oraCLR(o.value)...)
		}
	}
	response = append(response, oraSummary(1403, rowCount)...)
	if err = ttcWrite(c, response); err != nil {
		return err
	}
	// Close cursor and logoff are real requests too. The engine closes its socket
	// before connection cleanup; no fixture response is needed after EOF.
	for {
		b, err = ttcRead(c)
		if err != nil {
			return err
		}
		if len(b) >= 2 && b[0] == 3 && b[1] == 9 {
			if err = ttcWrite(c, oraSummary(0, 0)); err != nil {
				return err
			}
		}
	}
}
