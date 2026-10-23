package services

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // Explicit SNMP USM SHA-1 compatibility option.
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"math"
	"net"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
	"uptime-canary/apps/node-go/internal/jsonquery"
)

type SNMPConfig struct {
	Address           string            `json:"address"`
	Version           string            `json:"version"`
	OID               string            `json:"oid"`
	Community         string            `json:"community,omitempty"`
	Username          string            `json:"username,omitempty"`
	SecurityLevel     string            `json:"securityLevel,omitempty"`
	AuthProtocol      string            `json:"authProtocol,omitempty"`
	AuthPassphrase    string            `json:"authPassphrase,omitempty"`
	PrivacyProtocol   string            `json:"privacyProtocol,omitempty"`
	PrivacyPassphrase string            `json:"privacyPassphrase,omitempty"`
	ContextName       string            `json:"contextName,omitempty"`
	Condition         *Condition        `json:"condition,omitempty"`
	JSONata           *jsonquery.Config `json:"jsonata,omitempty"`
}

func validSNMPOID(s string) bool {
	if len(s) > 1024 {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(s, "."), ".")
	if len(parts) < 2 || len(parts) > 128 {
		return false
	}
	var first uint64
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
		n, e := strconv.ParseUint(p, 10, 32)
		if e != nil || (i == 0 && n > 2) || (i == 1 && first < 2 && n > 39) {
			return false
		}
		if i == 0 {
			first = n
		}
	}
	return true
}

func (c SNMPConfig) validate() error {
	if err := validAddress(c.Address); err != nil {
		return err
	}
	if !validSNMPOID(c.OID) {
		return errors.New("invalid SNMP OID")
	}
	if c.Version != "1" && c.Version != "2c" && c.Version != "3" {
		return errors.New("SNMP version must be 1, 2c or 3")
	}
	if c.Version != "3" {
		// gosnmp's v1/v2c encoder uses a one-octet BER community length.
		if len(c.Community) == 0 || len(c.Community) > 127 {
			return errors.New("invalid SNMP community")
		}
		if c.Username != "" || c.SecurityLevel != "" || c.AuthProtocol != "" || c.AuthPassphrase != "" || c.PrivacyProtocol != "" || c.PrivacyPassphrase != "" || c.ContextName != "" {
			return errors.New("SNMP USM options require version 3")
		}
	} else {
		if c.Community != "" || len(c.Username) == 0 || len(c.Username) > 32 || len(c.ContextName) > 32 {
			return errors.New("invalid SNMPv3 username, context or community")
		}
		switch c.SecurityLevel {
		case "noAuthNoPriv":
			if c.AuthProtocol != "" || c.AuthPassphrase != "" || c.PrivacyProtocol != "" || c.PrivacyPassphrase != "" {
				return errors.New("SNMP security options disagree with security level")
			}
		case "authNoPriv", "authPriv":
			if (c.AuthProtocol != "sha" && c.AuthProtocol != "sha256") || len(c.AuthPassphrase) < 8 || len(c.AuthPassphrase) > 1024 {
				return errors.New("invalid SNMPv3 authentication options")
			}
			if c.SecurityLevel == "authPriv" {
				if c.PrivacyProtocol != "aes" || len(c.PrivacyPassphrase) < 8 || len(c.PrivacyPassphrase) > 1024 {
					return errors.New("invalid SNMPv3 privacy options")
				}
			} else if c.PrivacyProtocol != "" || c.PrivacyPassphrase != "" {
				return errors.New("SNMP privacy options require authPriv")
			}
		default:
			return errors.New("SNMPv3 securityLevel is required")
		}
	}
	if c.Condition != nil {
		if err := c.Condition.validate(); err != nil {
			return err
		}
	}
	if c.JSONata != nil {
		return c.JSONata.Validate()
	}
	return nil
}

const snmpMaxPacket = 16 << 10

// The small BER envelope reader bounds lengths before any dependency decoding.
// Offsets point into the pristine datagram, including USM authentication bytes.
type snmpBER struct {
	p         []byte
	base, pos int
}
type snmpField struct {
	tag    byte
	p      []byte
	offset int
}

func (r *snmpBER) next() (snmpField, error) {
	bad := errors.New("invalid SNMP BER")
	if len(r.p)-r.pos < 2 {
		return snmpField{}, bad
	}
	tag := r.p[r.pos]
	r.pos++
	if tag&31 == 31 {
		return snmpField{}, bad
	}
	n := int(r.p[r.pos])
	r.pos++
	if n&128 != 0 {
		count := n & 127
		if count == 0 || count > 3 || len(r.p)-r.pos < count || r.p[r.pos] == 0 {
			return snmpField{}, bad
		}
		n = 0
		for range count {
			n = n<<8 | int(r.p[r.pos])
			r.pos++
		}
		if n < 128 {
			return snmpField{}, bad
		}
	}
	if n > len(r.p)-r.pos {
		return snmpField{}, bad
	}
	f := snmpField{tag: tag, p: r.p[r.pos : r.pos+n], offset: r.base + r.pos}
	r.pos += n
	return f, nil
}
func (f snmpField) reader() snmpBER { return snmpBER{p: f.p, base: f.offset} }
func (r *snmpBER) field(tag byte) (snmpField, error) {
	f, e := r.next()
	if e == nil && f.tag != tag {
		e = errors.New("invalid SNMP field type")
	}
	return f, e
}
func (r *snmpBER) integer() (uint32, error) {
	f, e := r.field(2)
	if e != nil || len(f.p) == 0 || len(f.p) > 5 || f.p[0]&128 != 0 {
		return 0, errors.New("invalid SNMP integer")
	}
	var n uint64
	for _, b := range f.p {
		n = n<<8 | uint64(b)
	}
	if n > math.MaxInt32 {
		return 0, errors.New("invalid SNMP integer range")
	}
	return uint32(n), nil
}
func (r *snmpBER) done() bool { return r.pos == len(r.p) }

type snmpEnvelope struct {
	version          uint32
	community        string
	msgID            uint32
	flags            gosnmp.SnmpV3MsgFlags
	engine, username string
	boots, clock     uint32
	auth             []byte
	authOffset       int
}

func snmpHeader(p []byte) (snmpEnvelope, error) {
	var h snmpEnvelope
	bad := errors.New("invalid SNMP envelope")
	if len(p) > snmpMaxPacket {
		return h, bad
	}
	r := snmpBER{p: p}
	outer, e := r.field(0x30)
	if e != nil || !r.done() {
		return h, bad
	}
	r = outer.reader()
	h.version, e = r.integer()
	if e != nil {
		return h, bad
	}
	if h.version == 0 || h.version == 1 {
		community, e := r.field(4)
		if e != nil || len(community.p) > 128 {
			return h, bad
		}
		h.community = string(community.p)
		pdu, e := r.next()
		if e != nil || !r.done() || (pdu.tag != 0xa0 && pdu.tag != 0xa2) {
			return h, bad
		}
		return h, nil
	}
	if h.version != 3 {
		return h, bad
	}
	head, e := r.field(0x30)
	if e != nil {
		return h, bad
	}
	hr := head.reader()
	h.msgID, e = hr.integer()
	if e != nil {
		return h, bad
	}
	size, e := hr.integer()
	if e != nil || size < 484 {
		return h, bad
	}
	flags, e := hr.field(4)
	if e != nil || len(flags.p) != 1 || flags.p[0]&^byte(7) != 0 || flags.p[0]&3 == 2 {
		return h, bad
	}
	h.flags = gosnmp.SnmpV3MsgFlags(flags.p[0])
	model, e := hr.integer()
	if e != nil || model != 3 || !hr.done() {
		return h, bad
	}
	security, e := r.field(4)
	if e != nil {
		return h, bad
	}
	sr := security.reader()
	seq, e := sr.field(0x30)
	if e != nil || !sr.done() {
		return h, bad
	}
	sr = seq.reader()
	engine, e := sr.field(4)
	if e != nil || len(engine.p) > 32 {
		return h, bad
	}
	h.engine = string(engine.p)
	h.boots, e = sr.integer()
	if e != nil {
		return h, bad
	}
	h.clock, e = sr.integer()
	if e != nil {
		return h, bad
	}
	user, e := sr.field(4)
	if e != nil || len(user.p) > 32 {
		return h, bad
	}
	h.username = string(user.p)
	auth, e := sr.field(4)
	if e != nil {
		return h, bad
	}
	h.auth = auth.p
	h.authOffset = auth.offset
	priv, e := sr.field(4)
	if e != nil || !sr.done() {
		return h, bad
	}
	if h.flags&gosnmp.AuthNoPriv == 0 && len(auth.p) != 0 {
		return h, bad
	}
	if h.flags&3 == gosnmp.AuthPriv {
		if len(priv.p) != 8 {
			return h, bad
		}
		if _, e = r.field(4); e != nil {
			return h, bad
		}
	} else {
		if len(priv.p) != 0 {
			return h, bad
		}
		if _, e = r.field(0x30); e != nil {
			return h, bad
		}
	}
	if !r.done() {
		return h, bad
	}
	return h, nil
}

func snmpHash(protocol string) func() hash.Hash {
	if protocol == "sha256" {
		return sha256.New
	}
	return sha1.New
}

// RFC 3414 password-to-key and localization, per check with no global cache.
// Work is fixed at 1 MiB; caller cancellation is checked every 4 KiB.
func snmpLocalizedKey(ctx context.Context, password, engine, protocol string) ([]byte, error) {
	h := snmpHash(protocol)()
	var chunk [4096]byte
	pos := 0
	for i := 0; i < 1<<20; i += len(chunk) {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		for j := range chunk {
			chunk[j] = password[pos%len(password)]
			pos++
		}
		h.Write(chunk[:])
	}
	key := h.Sum(nil)
	h.Reset()
	h.Write(key)
	h.Write([]byte(engine))
	h.Write(key)
	return h.Sum(nil), nil
}

func snmpVerifyMAC(p []byte, h snmpEnvelope, key []byte, protocol string) bool {
	size := 12
	if protocol == "sha256" {
		size = 24
	}
	if len(h.auth) != size {
		return false
	}
	wire := append([]byte(nil), p...)
	clear(wire[h.authOffset : h.authOffset+size])
	mac := hmac.New(snmpHash(protocol), key)
	mac.Write(wire)
	return hmac.Equal(h.auth, mac.Sum(nil)[:size])
}

func snmpID() (uint32, error) {
	var b [4]byte
	_, e := rand.Read(b[:])
	return (binary.BigEndian.Uint32(b[:]) & 0x3fffffff) + 1, e
}

func decodeSNMP(decoder *gosnmp.GoSNMP, p []byte) (packet *gosnmp.SnmpPacket, err error) {
	defer func() {
		if recover() != nil {
			packet = nil
			err = errors.New("invalid SNMP payload")
		}
	}()
	return decoder.SnmpDecodePacket(p)
}

func snmpExchange(conn net.Conn, request *gosnmp.SnmpPacket, decoder *gosnmp.GoSNMP, verify func([]byte, snmpEnvelope) bool) (*gosnmp.SnmpPacket, error) {
	p, e := request.MarshalMsg()
	if e != nil {
		return nil, e
	}
	if n, e := conn.Write(p); e != nil || n != len(p) {
		return nil, errors.New("SNMP write failed")
	}
	var b [snmpMaxPacket + 1]byte
	n, e := conn.Read(b[:])
	if e != nil {
		return nil, e
	}
	h, e := snmpHeader(b[:n])
	if e != nil || !verify(b[:n], h) {
		return nil, errors.New("SNMP response envelope rejected")
	}
	// SnmpDecodePacket decrypts but does not authenticate; verify above MUST
	// authenticate the pristine wire bytes before this method is called.
	return decodeSNMP(decoder, b[:n])
}

func checkSNMP(ctx context.Context, c SNMPConfig) Result {
	conn, e := dial(ctx, "udp", c.Address)
	if e != nil {
		return down("SNMP connection failed")
	}
	defer conn.Close()
	id, e := snmpID()
	if e != nil {
		return down("SNMP request creation failed")
	}
	oid := "." + strings.TrimPrefix(c.OID, ".")
	req := &gosnmp.SnmpPacket{RequestID: id, Version: gosnmp.Version2c, Community: c.Community, PDUType: gosnmp.GetRequest, Variables: []gosnmp.SnmpPDU{{Name: oid, Type: gosnmp.Null}}}
	decoder := &gosnmp.GoSNMP{Version: gosnmp.Version2c}
	verify := func(_ []byte, h snmpEnvelope) bool {
		return h.version == uint32(req.Version) && h.community == c.Community
	}
	if c.Version == "1" {
		req.Version = gosnmp.Version1
		decoder.Version = gosnmp.Version1
	}
	if c.Version == "3" {
		msgID, e := snmpID()
		if e != nil {
			return down("SNMP request creation failed")
		}
		probe := &gosnmp.SnmpPacket{Version: gosnmp.Version3, MsgFlags: gosnmp.Reportable, SecurityModel: gosnmp.UserSecurityModel, SecurityParameters: &gosnmp.UsmSecurityParameters{}, PDUType: gosnmp.GetRequest, MsgID: msgID, RequestID: id, MsgMaxSize: snmpMaxPacket}
		// A nonempty local name satisfies the decoder's configuration validation;
		// discovery itself deliberately has an empty wire username (RFC 3414).
		discoveryDecoder := &gosnmp.GoSNMP{Version: gosnmp.Version3, SecurityModel: gosnmp.UserSecurityModel, SecurityParameters: &gosnmp.UsmSecurityParameters{UserName: "discovery", AuthenticationProtocol: gosnmp.NoAuth, PrivacyProtocol: gosnmp.NoPriv}}
		report, e := snmpExchange(conn, probe, discoveryDecoder, func(_ []byte, h snmpEnvelope) bool {
			return h.version == 3 && h.msgID == msgID && h.flags&3 == 0 && len(h.engine) >= 5
		})
		if e != nil || report.PDUType != gosnmp.Report || report.Error != gosnmp.NoError || (report.RequestID != id && report.RequestID != 0) || len(report.Variables) != 1 || report.Variables[0].Name != ".1.3.6.1.6.3.15.1.1.4.0" || report.Variables[0].Type != gosnmp.Counter32 {
			return down("SNMPv3 engine discovery failed")
		}
		sp, ok := report.SecurityParameters.(*gosnmp.UsmSecurityParameters)
		if !ok || sp.AuthoritativeEngineBoots == math.MaxInt32 {
			return down("SNMPv3 engine parameters rejected")
		}
		sp.UserName = c.Username
		sp.AuthenticationProtocol = gosnmp.NoAuth
		sp.PrivacyProtocol = gosnmp.NoPriv
		flags := gosnmp.NoAuthNoPriv
		if c.SecurityLevel != "noAuthNoPriv" {
			flags = gosnmp.AuthNoPriv
			sp.AuthenticationProtocol = gosnmp.SHA
			if c.AuthProtocol == "sha256" {
				sp.AuthenticationProtocol = gosnmp.SHA256
			}
			sp.SecretKey, e = snmpLocalizedKey(ctx, c.AuthPassphrase, sp.AuthoritativeEngineID, c.AuthProtocol)
			if e != nil {
				return down("SNMPv3 key derivation failed")
			}
		}
		if c.SecurityLevel == "authPriv" {
			flags = gosnmp.AuthPriv
			sp.PrivacyProtocol = gosnmp.AES
			key, e := snmpLocalizedKey(ctx, c.PrivacyPassphrase, sp.AuthoritativeEngineID, c.AuthProtocol)
			if e != nil {
				return down("SNMPv3 key derivation failed")
			}
			sp.PrivacyKey = key[:16]
			sp.PrivacyParameters = make([]byte, 8)
			if _, e = rand.Read(sp.PrivacyParameters); e != nil {
				return down("SNMP request creation failed")
			}
		}
		req.Version = gosnmp.Version3
		req.Community = ""
		req.MsgID = msgID + 1
		req.RequestID = id + 1
		req.MsgMaxSize = snmpMaxPacket
		req.MsgFlags = flags | gosnmp.Reportable
		req.SecurityModel = gosnmp.UserSecurityModel
		req.SecurityParameters = sp
		req.ContextEngineID = sp.AuthoritativeEngineID
		req.ContextName = c.ContextName
		decoder = &gosnmp.GoSNMP{Version: gosnmp.Version3, MsgFlags: flags, SecurityModel: gosnmp.UserSecurityModel, SecurityParameters: sp}
		verify = func(p []byte, h snmpEnvelope) bool {
			if h.version != 3 || h.msgID != req.MsgID || h.flags&3 != flags || h.engine != sp.AuthoritativeEngineID || h.username != c.Username || h.boots != sp.AuthoritativeEngineBoots || h.clock < sp.AuthoritativeEngineTime || uint64(h.clock)-uint64(sp.AuthoritativeEngineTime) > 150 {
				return false
			}
			return flags == 0 || snmpVerifyMAC(p, h, sp.SecretKey, c.AuthProtocol)
		}
	}
	response, e := snmpExchange(conn, req, decoder, verify)
	if e != nil || response.PDUType != gosnmp.GetResponse || response.RequestID != req.RequestID || response.Error != gosnmp.NoError || response.ErrorIndex != 0 || len(response.Variables) != 1 || response.Variables[0].Name != oid {
		return down("SNMP GET response rejected")
	}
	if c.Version == "3" && (response.ContextName != c.ContextName || response.ContextEngineID != req.ContextEngineID) {
		return down("SNMP context does not match request")
	}
	value, ok := snmpScalar(response.Variables[0])
	if !ok {
		return down("SNMP response value is missing or unsupported")
	}
	if c.Condition != nil && !c.Condition.matches(value, false) {
		return down("SNMP value condition did not match")
	}
	if c.JSONata != nil {
		return evaluateJSONQuery(ctx, *c.JSONata, value, "SNMP")
	}
	return up("SNMP GET response passed")
}

func snmpScalar(p gosnmp.SnmpPDU) ([]byte, bool) {
	switch p.Type {
	case gosnmp.OctetString:
		b, ok := p.Value.([]byte)
		return b, ok && len(b) <= 8192
	case gosnmp.ObjectIdentifier, gosnmp.IPAddress:
		s, ok := p.Value.(string)
		return []byte(s), ok && len(s) <= 1024
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Counter64, gosnmp.Uinteger32:
		n := gosnmp.ToBigInt(p.Value)
		return []byte(n.String()), true
	}
	return nil, false
}
